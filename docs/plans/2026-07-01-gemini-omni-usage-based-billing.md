# Gemini Omni Flash 按 Token 用量计费改造

## 背景与目标

当前 Gemini Omni Flash (`gemini-omni-flash-preview`) 视频生成采用「创建时固定扣 $0.20」模式，费用与实际用量无关，且 $0.20 是占位估值，与真实成本偏差约 5 倍。

上游 Interactions API 完成响应已返回标准 `usage`（按模态拆分的 token 计数）。**目标**：改为按真实 token 用量计费，与 Gemini 官方定价对齐：

| 维度 | 单价（美元 / 100 万 token） |
|------|------|
| 输入（文本/图片/视频/音频，统一） | $1.50 |
| 输出文本（含思考 token） | $9.00 |
| 输出视频 | $17.50 |

并改为「创建任务不扣费不记 log，任务成功完成后按真实 usage 扣费并记 log」，在后台 poller 与用户主动查询两个并发触发点保证只扣一次。

## 真实样本验证

样本 usage：`total_input=9`，`total_output=58701`，`output video=57920`（thought 409 含在 total_output，算文本）。

```
输入:        9  × $1.50/1M  = $0.000014
输出文本:   781  × $9.00/1M  = $0.007029   (781 = 58701 - 57920)
输出视频: 57920  × $17.50/1M = $1.013600
总计                            ≈ $1.021
```

## 关键决策（已与用户确认）

| 决策点 | 选择 |
|--------|------|
| 计费方式 | 按 token 用量，输入/输出文本/输出视频三档价 |
| 计费时机 | 创建不扣费不记 log + 完成成功后扣费并记 log |
| usage 来源 | 上游 `usage` 字段（真实 token 计数） |
| Log 记录 | Log 表写真实 PromptTokens/CompletionTokens + Content 存 usage 明细 |
| Video 记录 | Quota 字段记总费用，Result 字段记完整上游 JSON（已有逻辑） |

> 说明：原需求中的 duration/mode/resolution 维度不再用于计费——上游响应不含这些字段，且 token 计费已覆盖成本。`videos` 表这三个字段对 Gemini Omni 保持空，不专门回填。

## 方案设计

### 1. 新增 token 计费函数 — `common/video-pricing.go`

Gemini Omni 按 token 计费与现有 `VideoPricingRule`（fixed/per_second）维度不兼容，新增专用函数，不污染定价规则结构：

```go
// Gemini Omni token 单价（美元 / 100 万 token）
const (
    GeminiOmniInputPricePerMTok      = 1.50
    GeminiOmniOutputTextPricePerMTok = 9.00
    GeminiOmniOutputVideoPricePerMTok = 17.50
)

// CalculateGeminiOmniQuota 按真实 token 用量计算 quota。
// inputTokens: total_input_tokens
// outputTextTokens: total_output_tokens - video_output_tokens（含 thought）
// outputVideoTokens: output_tokens_by_modality 中 video 的 tokens
func CalculateGeminiOmniQuota(inputTokens, outputTextTokens, outputVideoTokens int64) int64 {
    cost := float64(inputTokens)*GeminiOmniInputPricePerMTok/1e6 +
            float64(outputTextTokens)*GeminiOmniOutputTextPricePerMTok/1e6 +
            float64(outputVideoTokens)*GeminiOmniOutputVideoPricePerMTok/1e6
    return int64(cost * config.QuotaPerUnit)
}
```

同时**移除** `DefaultVideoPricingRules` 中 gemini-omni-flash-preview 的旧 fixed $0.20 规则，避免误用。

### 2. Gemini adaptor — `relay/channel/gemini/video_adaptor.go`

**a) 预扣费覆盖（创建时不预扣）**：
```go
func (a *VideoAdaptor) GetPrePaymentQuota() int64 { return 0 }
```

**b) `HandleVideoRequest`**：移除硬编码计费，`Quota=0`：
```go
return &relaychannel.VideoTaskResult{
    TaskId:      interResp.ID,
    TaskStatus:  "succeed",       // 仅表示任务受理
    Credentials: meta.ActualAPIKey,
    Quota:       0,               // 创建不扣费
    Prompt:      req.Prompt,
}, nil
```
`handleSuccessfulResponseWithQuota` 在 quota=0 时跳过扣费与日志（已有 `if quota != 0` 保护），创建时不记 log 自然成立。

**c) 扩展响应结构解析 `usage`**：
```go
type interactionUsageModality struct {
    Modality string `json:"modality"`
    Tokens   int64  `json:"tokens"`
}
type interactionUsage struct {
    TotalInputTokens  int64                    `json:"total_input_tokens"`
    TotalOutputTokens int64                    `json:"total_output_tokens"`
    OutputByModality  []interactionUsageModality `json:"output_tokens_by_modality"`
}
```
在 `interactionResponse` 增加 `Usage *interactionUsage \`json:"usage,omitempty"\``。

**d) 新增解析函数**：
```go
// ParseGeminiOmniUsage 从上游响应 JSON 解析计费用的 token 计数。
type GeminiOmniUsage struct {
    InputTokens       int64
    OutputTextTokens  int64
    OutputVideoTokens int64
}
func ParseGeminiOmniUsage(rawJSON string) (GeminiOmniUsage, error)
```
逻辑：`InputTokens = total_input_tokens`；遍历 `output_tokens_by_modality` 取 video；`OutputTextTokens = total_output_tokens - video_tokens`（含 thought，符合"输出文本包括思考 token"）。

**e) `FetchAndStoreVideoResult`**：返回值不变（仍返回 status/videoURL/failReason/rawJSON/err），调用方用 `ParseGeminiOmniUsage(rawJSON)` 解析。

**f) `HandleVideoResult`**：succeed 时调 `ParseGeminiOmniUsage` 解析 usage，通过 `GeneralFinalVideoResponse` 新增字段传给 controller（见下）。**不**在此更新 DB status，保持 processing，由 controller 原子扣费函数统一转换。store_url / result JSON 仍按原逻辑落库。

### 3. 响应结构 — `relay/model/general.go`

`GeneralFinalVideoResponse` 新增内部计费传递字段（不返回客户端）：
```go
InputTokens       int64 `json:"-"`
OutputTextTokens  int64 `json:"-"`
OutputVideoTokens int64 `json:"-"`
```

### 4. 原子状态转换 — `model/video.go`

新增函数，将「processing→succeed + 落 quota/store_url/result」原子化，靠 `WHERE status='processing'` 保证只扣一次：
```go
// TransitionVideoToSucceeded 原子地将任务从 processing 转为 succeed 并落计费字段。
// 返回 charged=true 表示调用方赢得竞争，需执行扣费与记 log。
func TransitionVideoToSucceeded(taskId string, quota int64, storeUrl, resultJSON string) (charged bool, task *Video, err error)
```

### 5. controller 扣费 helper — `relay/controller/video.go`

新增：
```go
func chargeGeminiOmniOnSuccess(videoTask *dbmodel.Video, usage gemini.GeminiOmniUsage, storeUrl, resultJSON string) bool {
    quota := common.CalculateGeminiOmniQuota(usage.InputTokens, usage.OutputTextTokens, usage.OutputVideoTokens)
    charged, t, err := dbmodel.TransitionVideoToSucceeded(videoTask.TaskId, quota, storeUrl, resultJSON)
    if !charged || err != nil || t == nil { return false }

    // 扣费（后台任务无 token 上下文，直接扣用户余额）
    _ = dbmodel.DecreaseUserQuota(t.UserId, quota)
    _ = dbmodel.CacheUpdateUserQuota(context.Background(), t.UserId)

    // 记 log：真实 tokens + usage 明细
    totalOutput := usage.OutputTextTokens + usage.OutputVideoTokens
    logContent := fmt.Sprintf("Gemini Omni Video model=%s, input=%d, output_text=%d, output_video=%d, cost=$%.6f",
        t.Model, usage.InputTokens, usage.OutputTextTokens, usage.OutputVideoTokens, float64(quota)/config.QuotaPerUnit)
    dbmodel.RecordVideoConsumeLog(context.Background(), t.UserId, t.ChannelId,
        int(usage.InputTokens), int(totalOutput), t.Model, "", quota, logContent,
        0, "", "", t.TaskId)
    dbmodel.UpdateUserUsedQuotaAndRequestCount(t.UserId, quota)
    dbmodel.UpdateChannelUsedQuota(t.ChannelId, quota)
    return true
}
```
> 用 `DecreaseUserQuota` 而非 `PostConsumeTokenQuota`，因后台 poller 无 TokenId 上下文。`PostConsumeTokenQuota(0,...)` 对 token_id=0 行为不确定，避免误扣 token 记录。

### 6. 用户查询路径 — `relay/controller/video.go` `invokeVideoAdaptorResult`

对 `videoTask.Provider == "gemini-omni"` 增加分支：
- `result.TaskStatus == "succeed"`：从 result 取 usage 字段，调 `chargeGeminiOmniOnSuccess`，**跳过**原 `UpdateVideoTaskStatus`（避免抢先改 status 导致 poller 漏扣）
- `result.TaskStatus == "failed"`：走原 `UpdateVideoTaskStatus` 退款路径（创建时 quota=0，`CompensateVideoTask` 退 0 无害）
- 其他状态：原逻辑

### 7. 后台 poller — `controller/gemini_video_poller.go`

`succeed` 分支改用新流程：
```go
case "succeed":
    usage, parseErr := gemini.ParseGeminiOmniUsage(rawJSON)
    if parseErr != nil { logger.Error(...); return }
    chargeGeminiOmniOnSuccess(task, usage, videoURL, rawJSON)
```
`failed` 分支：保持现有退款逻辑，`if task.Quota > 0` 保护已足够（创建时 quota=0，不会误退）。

## 影响范围

- **现有功能**：仅影响 gemini-omni-flash-preview。其他 provider 走原 `invokeVideoAdaptorResult` 路径不变。
- **数据库 schema**：无变更（Log 表 PromptTokens/CompletionTokens/Quota/Content/VideoTaskId 已存在；Video 表 Quota/Result 已存在）。
- **定价规则迁移**：移除代码内置的 gemini-omni-flash-preview fixed 规则。**注意**：已部署实例 DB 中的旧规则不会自动删除（`AddNewMissingVideoPricingRules` 只增不删），但因新流程不再调 `CalculateVideoQuota`，DB 旧规则不再生效，无影响。建议用户在管理后台删除该旧规则保持整洁。
- **并发安全**：poller（5 分钟）与用户查询可能同时触发，靠 `TransitionVideoToSucceeded` 的 `WHERE status='processing'` 原子保证只扣一次。
- **失败处理**：创建时未扣费，失败时无需退款；现有 `if task.Quota > 0` 保护正确。

## 待确认 / 兜底

- `service_tier`：样本为 `standard`（付费层），按上约定价计费。若未来出现其他 tier（如 free），单价可能不同——当前不区分，后续按需扩展。
- usage 解析兜底：若上游响应缺 `usage` 字段，`ParseGeminiOmniUsage` 返回 0，quota=0 不扣费，并在 log 记警告，避免误扣。

## 验证方式

1. `go build ./... && go vet ./...` 通过
2. 创建任务：DB 落 quota=0、status=processing，用户余额不变，**无 Log 记录**
3. 任务成功后 poller 触发：DB status=succeed、quota=按 token 计算；Log 表有记录且 PromptTokens=9、CompletionTokens=58701、Quota=约 $1.02 对应积分；用户余额扣减正确
4. 用户查询抢先于 poller 触发 succeed：扣费只发生一次，poller 后续 `WHERE status='processing'` 不匹配跳过
5. 任务失败：用户余额不变，无扣无退
6. 涉及文件：`common/video-pricing.go`、`relay/channel/gemini/video_adaptor.go`、`relay/model/general.go`、`model/video.go`、`relay/controller/video.go`、`controller/gemini_video_poller.go`
