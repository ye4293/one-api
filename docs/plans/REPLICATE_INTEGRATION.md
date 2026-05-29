# Flux 渠道接入 Replicate 改造计划

## 背景

现有 Flux 渠道对接 Black Forest Labs (BFL) 官方 API（`api.bfl.ai`）。
本次改造在**不新增渠道类型**的前提下，通过 BaseURL 区分，兼容 Replicate API（`api.replicate.com`）。

用户配置渠道时只需将 BaseURL 填写为对应平台地址，系统自动切换行为。

**核心设计原则**：统一用户感知——无论底层是 BFL 异步回调还是 Replicate 同步响应，客户端始终收到 `{id, polling_url}` 格式，无需感知底层差异。

---

## 一、模型支持差异

以 one-api 现有 BFL 模型列表为准（canonical name），映射到 Replicate 对应模型。

| one-api 模型名（BFL canonical） | BFL 路径 | Replicate 模型 ID | Replicate 支持 |
|---|---|---|---|
| `flux-2-max` | `/v1/flux-2-max` | `black-forest-labs/flux-2-max` | ✅ |
| `flux-2-pro-preview` | `/v1/flux-2-pro-preview` | `black-forest-labs/flux-2-pro` | ⚠️ 降级到 flux-2-pro |
| `flux-2-pro` | `/v1/flux-2-pro` | `black-forest-labs/flux-2-pro` | ✅ |
| `flux-2-flex` | `/v1/flux-2-flex` | `black-forest-labs/flux-2-flex` | ✅ |
| `flux-2-klein-4b` | `/v1/flux-2-klein-4b` | `black-forest-labs/flux-2-klein-4b` | ✅ |
| `flux-2-klein-9b-preview` | `/v1/flux-2-klein-9b-preview` | `black-forest-labs/flux-2-klein-9b` | ⚠️ 降级到 flux-2-klein-9b |
| `flux-2-klein-9b` | `/v1/flux-2-klein-9b` | `black-forest-labs/flux-2-klein-9b` | ✅ |
| `flux-kontext-pro` | `/v1/flux-kontext-pro` | `black-forest-labs/flux-kontext-pro` | ✅ |
| `flux-kontext-max` | `/v1/flux-kontext-max` | `black-forest-labs/flux-kontext-max` | ✅ |
| `flux-pro-1.1-ultra` | `/v1/flux-pro-1.1-ultra` | `black-forest-labs/flux-1.1-pro-ultra` | ✅（名称顺序不同）|
| `flux-pro-1.1` | `/v1/flux-pro-1.1` | `black-forest-labs/flux-1.1-pro` | ✅（名称顺序不同）|
| `flux-pro` | `/v1/flux-pro` | `black-forest-labs/flux-pro` | ✅ |
| `flux-dev` | `/v1/flux-dev` | `black-forest-labs/flux-dev` | ✅ |
| `flux-pro-1.0-fill` | `/v1/flux-pro-1.0-fill` | ❌ 暂不支持 | ❌ 不支持（见注①）|
| `flux-pro-1.0-expand` | `/v1/flux-pro-1.0-expand` | ❌ 无独立模型 | ❌ 不支持（见注②）|

> **注①** `flux-pro-1.0-fill` 虽然 Replicate 存在对应模型 `flux-fill-pro`，但请求参数结构与 BFL 存在差异，
> 暂不接入，避免引入额外适配复杂度。

> **注②** `flux-pro-1.0-expand` 在 Replicate **无独立对应模型**，不支持。

### 不支持模型的处理策略

当渠道为 Replicate，用户请求了不支持的模型时：
- 在 `ConvertFluxRequest` 中检测，**提前返回 400 错误**
- 错误信息：`"model {name} is not supported on Replicate channel"`
- **不发起实际 HTTP 请求**，不创建 pending 记录，不消耗配额

当前不支持列表：`flux-pro-1.0-fill`、`flux-pro-1.0-expand`

---

## 二、API 差异对比

### 2.1 认证方式

| | BFL | Replicate |
|---|---|---|
| Header | `x-key: <api_key>` | `Authorization: Bearer <api_key>` |

### 2.2 请求 URL 格式

| | BFL | Replicate |
|---|---|---|
| 创建任务 | `POST https://api.bfl.ai/v1/flux-2-pro` | `POST https://api.replicate.com/v1/models/black-forest-labs/flux-2-pro/predictions` |
| 查询结果 | `GET https://api.bfl.ai/v1/get_result?id={id}` | `GET https://api.replicate.com/v1/predictions/{id}` |
| 取消任务 | 无 | `POST https://api.replicate.com/v1/predictions/{id}/cancel` |

### 2.3 请求体差异

**BFL：**
```json
{
  "prompt": "a cute cat",
  "width": 1024,
  "height": 1024,
  "webhook_url": "https://your-server/flux/internal/callback"
}
```

**Replicate：**
```json
{
  "input": {
    "prompt": "a cute cat",
    "width": 1024,
    "height": 1024
  },
  "webhook": "https://your-server/flux/internal/callback",
  "webhook_events_filter": ["completed"]
}
```

差异点：
- 请求参数需包装到 `input` 字段
- webhook 字段名：`webhook_url` → `webhook`
- 可指定 `webhook_events_filter: ["completed"]` 减少不必要回调
- 使用 `Prefer: wait=60` header 让请求同步等待（最长 60s）

### 2.4 创建任务响应差异

**BFL（立即返回 task ID，无输出）：**
```json
{
  "id": "abc123",
  "polling_url": "https://api.bfl.ai/v1/get_result?id=abc123",
  "cost": 0.05,
  "input_mp": 0,
  "output_mp": 1.0
}
```

**Replicate（使用 `Prefer: wait=60` 同步等待时直接返回结果）：**
```json
{
  "id": "4h9ajx4vrhrmy0cy415935qz7m",
  "model": "black-forest-labs/flux-2-pro",
  "version": "hidden",
  "input": { "prompt": "...", "width": 1024, "height": 1024 },
  "output": "https://replicate.delivery/xezq/xxx.webp",
  "status": "succeeded",
  "created_at": "2026-05-13T07:43:40.996Z",
  "started_at": "2026-05-13T07:43:41.024Z",
  "completed_at": "2026-05-13T07:43:49.762Z",
  "urls": {
    "get": "https://api.replicate.com/v1/predictions/4h9ajx4vrhrmy0cy415935qz7m",
    "cancel": "https://api.replicate.com/v1/predictions/4h9ajx4vrhrmy0cy415935qz7m/cancel"
  },
  "metrics": {
    "image_input_megapixel_count": 0,
    "image_output_count": 1,
    "image_output_megapixel_count": 1,
    "predict_time": 8.74,
    "total_time": 8.77
  },
  "error": null,
  "logs": "Using seed: 50736\nGenerated image in 8.5sec"
}
```

关键差异：
- `output`：BFL 无此字段（异步）；Replicate 直接返回**图片 URL 字符串**（非数组）
- `cost`：BFL 返回费用（美分）；Replicate **不返回 cost**，按固定价/张计费
- `status`：Replicate 直接返回 `succeeded`（同步模式）；BFL 返回 task ID 等待回调
- `metrics.predict_time`：Replicate 返回 GPU 用时（秒），仅供参考，不用于计费

### 2.5 查询结果响应差异

**BFL 查询响应：**
```json
{
  "id": "abc123",
  "status": "Ready",
  "result": { "sample": "https://cdn.bfl.ai/xxx.jpg" }
}
```

**Replicate 查询响应：**（结构同创建响应）
```json
{
  "id": "4h9ajx4vrhrmy0cy415935qz7m",
  "status": "succeeded",
  "output": "https://replicate.delivery/xezq/xxx.webp",
  "metrics": { "predict_time": 8.74, ... }
}
```

### 2.6 回调（Webhook）格式差异

**BFL 回调：**
```json
{
  "task_id": "abc123",
  "status": "SUCCESS",
  "progress": 100,
  "result": { "sample": "https://cdn.bfl.ai/xxx.jpg" },
  "cost": 0.05,
  "input_mp": 0,
  "output_mp": 1.0
}
```

**Replicate 回调：**（与查询响应格式相同）
```json
{
  "id": "4h9ajx4vrhrmy0cy415935qz7m",
  "status": "succeeded",
  "output": "https://replicate.delivery/xezq/xxx.webp",
  "metrics": { "predict_time": 8.74 }
}
```

差异：
- task id 字段：`task_id` → `id`
- status 值：`SUCCESS/FAILED` → `succeeded/failed`
- 图片 URL：`result.sample` → `output`（字符串）
- cost：BFL 有，Replicate 无

---

## 三、统一用户感知策略

### 3.1 问题

BFL 是异步模式（提交任务 → 回调通知），Replicate 是同步模式（`Prefer: wait=60` 等待结果）。
若直接暴露差异，客户端需感知两种交互协议，体验不一致。

### 3.2 解决方案：Replicate 响应包装为 BFL 格式

**核心做法**：Replicate 的 `DoResponse` 在收到同步响应后：
1. 从 `output` 字段取出图片 URL
2. **立即将 DB 记录更新为 `status=success`，`store_url` = 图片 URL**（含扣费）
3. 向客户端返回与 BFL 格式一致的 `{id, polling_url}`

客户端拿到 `polling_url` 后调用 `GET /flux/v1/get_result?id=xxx`，
由于 DB 已是 `success` 状态，**一次查询即返回结果**，无需真正轮询。

```
BFL 渠道                          Replicate 渠道
─────────────────                ─────────────────────────────
POST /flux/v1/xxx                POST /flux/v1/xxx
      ↓                                ↓
DoResponse 返回                  DoResponse:
{id, polling_url}                  1. 解析 ReplicateResponse
      ↓                             2. DB: status=success + store_url (含扣费)
等待 BFL 回调                       3. 返回 {id, polling_url}  ← 与 BFL 格式相同
      ↓                                ↓
回调触发扣费                     客户端 GET polling_url
                                       ↓
                                 DB 已 success → 立即返回图片 URL
```

### 3.3 60s 超时处理

若 Replicate 在 60s 内未完成（返回 `status: "starting"` 或 `"processing"`）：
- DoResponse 仍正常返回 `{id, polling_url}`
- DB 记录保留 `submitted` 状态
- Replicate 异步完成后会回调 webhook（`webhook_events_filter: ["completed"]`）
- 回调处理路径与 BFL 类似，但需适配字段差异（见回调处理）

---

## 四、计费差异

| | BFL | Replicate |
|---|---|---|
| 计费来源 | 响应/回调中的 `cost` 字段（美分） | 按模型固定价格/张（Klein 系列按 GPU 时间）|
| 扣费时机 | 回调 SUCCESS 后 | DoResponse 中直接扣（同步完成时）|
| 失败是否计费 | 不计费 | 不计费（公共模型）|
| 取消是否计费 | 按进度 | 按已运行时间 |

### Replicate 各模型定价（已验证，2026-05-14）

> 数据来源：`scripts/verify_replicate_pricing.py --dry-run` 抓取 replicate.com 页面定价。

**固定价模型（flat-rate per run）：**

| one-api 模型名 | Replicate ID | 单价/张 |
|---|---|---|
| `flux-dev` | `black-forest-labs/flux-dev` | **$0.0250** |
| `flux-pro` | `black-forest-labs/flux-pro` | **$0.0550** |
| `flux-pro-1.1` | `black-forest-labs/flux-1.1-pro` | **$0.0400** |
| `flux-pro-1.1-ultra` | `black-forest-labs/flux-1.1-pro-ultra` | **$0.0600** |
| `flux-pro-1.0-fill` | `black-forest-labs/flux-fill-pro` | 暂不支持 |
| `flux-2-pro` / `flux-2-pro-preview` | `black-forest-labs/flux-2-pro` | **$0.0150** |
| `flux-2-max` | `black-forest-labs/flux-2-max` | **$0.0400** |
| `flux-2-flex` | `black-forest-labs/flux-2-flex` | **$0.0600** |
| `flux-kontext-pro` | `black-forest-labs/flux-kontext-pro` | **$0.0400** |
| `flux-kontext-max` | `black-forest-labs/flux-kontext-max` | **$0.0800** |

**GPU 时间计费模型（variable-rate）：**

Klein 系列按 `$0.001525/GPU秒` + `$2/千兆像素` 收费，无固定单价。

| one-api 模型名 | Replicate ID | 计费方式 | p50 参考值 |
|---|---|---|---|
| `flux-2-klein-4b` / `flux-2-klein-4b-preview` | `black-forest-labs/flux-2-klein-4b` | GPU 时间 | ≈$0.020/张 |
| `flux-2-klein-9b` / `flux-2-klein-9b-preview` | `black-forest-labs/flux-2-klein-9b` | GPU 时间 | ≈$0.005/张 |

> p50 为 Replicate 页面标注的中位数成本，实际因分辨率和推理步数浮动。
> Klein-4b 的 p50 高于 9b，因 4b 的推理步数通常更多（架构差异）。

**附加费用（所有模型）：**
- 输入/输出图片超大分辨率时收取额外像素费：约 $1~$2 / 千兆像素（1024×1024 ≈ 1MP，实际附加 ≤$0.002，可忽略）

---

## 五、改造步骤

### Step 1：`constant.go`

新增：
- `ReplicateModelMap`：one-api 模型名 → Replicate 模型 ID 的映射（含 preview 降级）
- `ReplicateUnsupportedModels`：Replicate 不支持的模型列表（`flux-pro-1.0-expand`）
- `ReplicatePriceMap`：Replicate 各模型固定价格（USD）

```go
var ReplicateModelMap = map[string]string{
    "flux-2-max":              "black-forest-labs/flux-2-max",
    "flux-2-pro-preview":      "black-forest-labs/flux-2-pro",   // 降级
    "flux-2-pro":              "black-forest-labs/flux-2-pro",
    "flux-2-flex":             "black-forest-labs/flux-2-flex",
    "flux-2-klein-4b":         "black-forest-labs/flux-2-klein-4b",
    "flux-2-klein-9b-preview": "black-forest-labs/flux-2-klein-9b", // 降级
    "flux-2-klein-9b":         "black-forest-labs/flux-2-klein-9b",
    "flux-kontext-pro":        "black-forest-labs/flux-kontext-pro",
    "flux-kontext-max":        "black-forest-labs/flux-kontext-max",
    "flux-pro-1.1-ultra":      "black-forest-labs/flux-1.1-pro-ultra",
    "flux-pro-1.1":            "black-forest-labs/flux-1.1-pro",
    "flux-pro":                "black-forest-labs/flux-pro",
    "flux-dev":                "black-forest-labs/flux-dev",
    // flux-pro-1.0-fill 暂不支持
    // flux-pro-1.0-expand 不支持
}

var ReplicateUnsupportedModels = map[string]bool{
    "flux-pro-1.0-fill":   true, // 暂不支持，参数结构差异
    "flux-pro-1.0-expand": true, // Replicate 无对应独立模型
}

var ReplicatePriceMap = map[string]float64{
    // 固定价模型（已验证，来源: replicate.com 页面定价，2026-05-14）
    "flux-dev":              0.025,
    "flux-pro":              0.055,
    "flux-pro-1.1":          0.040,
    "flux-pro-1.1-ultra":    0.060,
    // flux-pro-1.0-fill 暂不支持
    "flux-2-pro":            0.015,
    "flux-2-pro-preview":    0.015, // 降级到 flux-2-pro
    "flux-2-max":            0.040,
    "flux-2-flex":           0.060,
    "flux-kontext-pro":      0.040,
    "flux-kontext-max":      0.080,
    // GPU 时间计费模型（按 p50 中位数估算）
    "flux-2-klein-4b":         0.020,
    "flux-2-klein-9b":         0.005,
    "flux-2-klein-9b-preview": 0.005, // 降级到 flux-2-klein-9b
}
```

### Step 2：`adaptor.go`

新增辅助函数：
```go
func isReplicate(baseURL string) bool {
    return strings.Contains(baseURL, "replicate.com")
}
```

修改各方法：

| 方法 | 改动 |
|---|---|
| `SetupRequestHeader` | Replicate 用 `Authorization: Bearer`，BFL 用 `x-key` |
| `DoRequest` | Replicate 构造 `/v1/models/{owner}/{model}/predictions`，并加 `Prefer: wait=60` |
| `ConvertFluxRequest` | ① 检查不支持模型提前返回 400；② Replicate 将参数包装到 `input`，webhook 字段改名 |
| `DoResponse` | Replicate 直接从响应取 `output` URL，写 DB success + 扣费，然后返回 BFL 格式 `{id, polling_url}` |
| `QueryResult` | Replicate 用 `/v1/predictions/{id}` + Bearer 认证 |

**`ConvertFluxRequest` Replicate 分支伪代码：**
```go
if isReplicate(meta.BaseURL) {
    // 不支持的模型提前拦截
    if ReplicateUnsupportedModels[meta.OriginModelName] {
        return nil, fmt.Errorf("model %s is not supported on Replicate channel", meta.OriginModelName)
    }
    // 包装参数
    input := requestMap (删除 model 字段)
    body := map[string]any{
        "input": input,
        "webhook": webhookURL,
        "webhook_events_filter": []string{"completed"},
    }
    return json.Marshal(body)
}
```

**`DoRequest` Replicate 分支伪代码：**
```go
if isReplicate(meta.BaseURL) {
    replicateModel := ReplicateModelMap[meta.OriginModelName]
    // 格式: /v1/models/{owner}/{name}/predictions
    // replicateModel 格式为 "owner/name"
    parts := strings.SplitN(replicateModel, "/", 2)
    path = fmt.Sprintf("/v1/models/%s/%s/predictions", parts[0], parts[1])
    req.Header.Set("Prefer", "wait=60")
    req.Header.Set("Authorization", "Bearer "+meta.APIKey)
} else {
    // BFL: 原逻辑
    req.Header.Set("x-key", meta.APIKey)
}
```

**`DoResponse` Replicate 分支伪代码（统一异步策略）：**
```go
if isReplicate(meta.BaseURL) {
    var replicateResp ReplicateResponse
    json.Unmarshal(body, &replicateResp)

    if replicateResp.Status == "succeeded" && replicateResp.Output != "" {
        // 计费：按固定价格/张
        groupRatio := getGroupRatio(...)
        quota := CalculateReplicateQuota(meta.OriginModelName, 1, groupRatio)

        // 立即写 DB success + 图片 URL
        a.ImageRecord.TaskId   = replicateResp.ID
        a.ImageRecord.Status   = TaskStatusSucceed
        a.ImageRecord.StoreUrl = replicateResp.Output
        a.ImageRecord.Quota    = quota
        a.ImageRecord.Result   = string(body)
        a.ImageRecord.Update()

        // 扣费
        model.DecreaseUserQuota(meta.UserId, quota)

        // 返回 BFL 兼容格式
        pollingURL := fmt.Sprintf("%s/flux/v1/get_result?id=%s", config.ServerAddress, replicateResp.ID)
        c.JSON(200, gin.H{
            "id":          replicateResp.ID,
            "polling_url": pollingURL,
        })
        return nil, nil
    }

    // status == "starting"/"processing"（60s 内未完成）
    // 更新为 submitted，等回调
    a.ImageRecord.TaskId = replicateResp.ID
    a.ImageRecord.Status = TaskStatusSubmitted
    a.ImageRecord.Update()

    pollingURL := fmt.Sprintf("%s/flux/v1/get_result?id=%s", config.ServerAddress, replicateResp.ID)
    c.JSON(200, gin.H{
        "id":          replicateResp.ID,
        "polling_url": pollingURL,
    })
    return nil, nil
}
```

### Step 3：`model.go`

新增：
```go
type ReplicateResponse struct {
    ID          string           `json:"id"`
    Model       string           `json:"model"`
    Status      string           `json:"status"`
    Output      string           `json:"output"`       // 图片 URL（字符串，非数组）
    Error       interface{}      `json:"error"`
    Logs        string           `json:"logs"`
    Metrics     ReplicateMetrics `json:"metrics"`
    URLs        ReplicateURLs    `json:"urls"`
    CreatedAt   string           `json:"created_at"`
    StartedAt   string           `json:"started_at"`
    CompletedAt string           `json:"completed_at"`
}

type ReplicateMetrics struct {
    PredictTime               float64 `json:"predict_time"`
    TotalTime                 float64 `json:"total_time"`
    ImageOutputCount          int     `json:"image_output_count"`
    ImageOutputMegapixelCount float64 `json:"image_output_megapixel_count"`
}

type ReplicateURLs struct {
    Get    string `json:"get"`
    Cancel string `json:"cancel"`
}
```

### Step 4：`billing.go`

新增：
```go
// CalculateReplicateQuota 按固定价格/张计算配额
func CalculateReplicateQuota(modelName string, imageCount int, groupRatio float64) int64 {
    price, ok := ReplicatePriceMap[modelName]
    if !ok {
        price = 0.05 // 默认 $0.05/张
    }
    totalUSD := price * float64(imageCount)
    return int64(totalUSD * 500000 * groupRatio)
}
```

### Step 5：回调处理（Replicate webhook，60s 超时场景）

Replicate 回调格式与 BFL 不同，需在 `HandleCallback` 中分支处理。
由于 `HandleFluxCallback` 目前固定解析 `FluxCallbackNotification`（含 `task_id`、`result.sample` 等字段），
需要新增 Replicate 回调路由或在同一路由中做格式探测：

**方案**：新增路由 `POST /flux/internal/replicate/callback`，使用独立的 `HandleReplicateCallback`，
解析 `ReplicateResponse` 格式，提取 `id`（作为 task_id）和 `output`（图片 URL），
然后复用现有 DB 更新与扣费逻辑。

---

## 六、渠道配置方式

用户在管理后台配置渠道时：

| 字段 | BFL 配置 | Replicate 配置 |
|---|---|---|
| 渠道类型 | Flux | Flux（相同） |
| Base URL | `https://api.bfl.ai` | `https://api.replicate.com` |
| API Key | BFL API Key | Replicate API Token（`r8_xxx`）|
| 模型 | 全部支持 | 除 `flux-pro-1.0-fill`、`flux-pro-1.0-expand` 外均支持 |

---

## 七、调用流程图

### 7.1 图像生成（POST /flux/v1/*model）

#### BFL 渠道（异步 + 回调）

```
Client
  │
  │ POST /flux/v1/flux-2-pro
  ▼
router/relay-router.go
  │ middleware: TokenAuth → Distribute（选渠道）
  ▼
controller/flux.go · RelayFlux()
  │ 读取 requestBody，获取 channelId / model
  ▼
relayFluxHelper()
  ├─ flux.Adaptor.CreatePendingRecord()   → DB: INSERT image(status=pending)
  ├─ flux.Adaptor.ConvertFluxRequest()
  │     删除 model 字段
  │     注入 webhook_url = {SERVER}/flux/internal/callback
  ├─ flux.Adaptor.DoRequest()
  │     Header: x-key: <api_key>
  │     POST https://api.bfl.ai/v1/flux-2-pro
  │                │
  │                │ 立即返回 202
  │                ▼
  │           { id, polling_url, cost }
  └─ flux.Adaptor.DoResponse()
        解析 FluxResponse
        DB: UPDATE image(status=submitted, task_id, quota)
        删除 webhook_url，构造 polling_url
        → 返回客户端 { id, polling_url }

        ┌── 异步等待 BFL 回调 ──────────────────────────┐
        │                                               │
        │  POST /flux/internal/callback                 │
        │    ↓                                          │
        │  controller/flux.go · HandleFluxCallback()    │
        │    ↓                                          │
        │  flux.HandleCallback()                        │
        │    status=SUCCESS → handleSuccessCallback()   │
        │      DB: UPDATE image(status=success, url)    │
        │      model.DecreaseUserQuota()  ← 真正扣费    │
        │    status=FAILED → handleFailedCallback()     │
        │      DB: UPDATE image(status=failed)          │
        └───────────────────────────────────────────────┘

失败重试（RetryTimes 次）：
  RelayFlux() → selectRetryChannel() → relayFluxHelper() 重走上述流程
```

#### Replicate 渠道（同步 → 包装为异步格式）

```
Client
  │
  │ POST /flux/v1/flux-2-pro
  ▼
router/relay-router.go
  │ middleware: TokenAuth → Distribute（选渠道）
  ▼
controller/flux.go · RelayFlux()
  ▼
relayFluxHelper()
  ├─ flux.Adaptor.CreatePendingRecord()   → DB: INSERT image(status=pending)
  ├─ flux.Adaptor.ConvertFluxRequest()
  │     ⚠️ flux-pro-1.0-expand → 直接返回 400，流程终止
  │     包装参数到 input: { prompt, width, ... }
  │     注入 webhook = {SERVER}/flux/internal/replicate/callback
  │     模型名映射: flux-2-pro → black-forest-labs/flux-2-pro
  │             flux-2-pro-preview → black-forest-labs/flux-2-pro（降级）
  ├─ flux.Adaptor.DoRequest()
  │     Header: Authorization: Bearer <r8_xxx>
  │     Header: Prefer: wait=60
  │     POST https://api.replicate.com/v1/models/black-forest-labs/flux-2-pro/predictions
  │                │
  │                │ 同步等待（最长 60s）
  │                ▼
  │           { id, status:"succeeded", output:"https://...", metrics }
  └─ flux.Adaptor.DoResponse()
        ── status=="succeeded" 分支 ──
        解析 ReplicateResponse
        output 字段直接取图片 URL（字符串）
        CalculateReplicateQuota(model, 1, groupRatio)
        DB: UPDATE image(status=success, store_url, quota)
        model.DecreaseUserQuota()  ← 直接扣费（同步完成）
        构造 polling_url = {SERVER}/flux/v1/get_result?id={id}
        → 返回客户端 { id, polling_url }  ← 与 BFL 格式相同!

        ── status=="starting"/"processing" 分支（60s 超时）──
        DB: UPDATE image(status=submitted, task_id)
        → 返回客户端 { id, polling_url }
        等待 Replicate 异步回调 /flux/internal/replicate/callback

Client
  │
  │ GET /flux/v1/get_result?id={id}
  ▼
controller/flux.go · GetFlux()
  │ isFromSource=false（默认）
  ▼
DB 查询: image.status == "success"
  → 立即返回图片结果（无需真正轮询）✓
```

### 7.2 查询任务结果（GET /flux/v1/get_result）

```
Client
  │
  │ GET /flux/v1/get_result?id={task_id}[&from_source=true]
  ▼
controller/flux.go · GetFlux()
  │
  ├─ from_source=false（默认）
  │     model.GetImageByTaskId(task_id)
  │     status=success → 直接返回 DB 中缓存的 result JSON（含图片 URL）
  │     其他 → 返回 { id, status, error }
  │
  └─ from_source=true
        model.GetImageByTaskId(task_id)  → 拿到 channel_id
        model.GetChannelById(channel_id)
        flux.Adaptor.QueryResult()
          │
          ├─ BFL渠道:
          │    GET https://api.bfl.ai/v1/get_result?id={task_id}
          │    Header: x-key: <api_key>
          │    透传响应
          │
          └─ Replicate渠道:
               GET https://api.replicate.com/v1/predictions/{task_id}
               Header: Authorization: Bearer <r8_xxx>
               透传响应
```

### 7.3 关键差异对比（流程层面）

```
              BFL                          Replicate（改造后）
         ─────────────────────────────────────────────────────
创建任务  POST /v1/{model}                POST /v1/models/{owner}/{model}/predictions
认证      x-key: <key>                   Authorization: Bearer <key>
请求体    { prompt, ...params }           { input: { prompt, ...params } }
响应      立即返回 task_id（异步）         同步等待返回图片 URL（Prefer: wait=60）
扣费时机  回调 SUCCESS 后                 DoResponse 中直接扣（同步完成时）
计费依据  回调携带的 cost 字段（美分）     模型固定价格表（USD/张）
查询      GET /v1/get_result?id=          GET /v1/predictions/{id}
回调路由  POST /flux/internal/callback    POST /flux/internal/replicate/callback
客户端    {id, polling_url}              {id, polling_url}  ← 统一格式 ✓
```

---

## 八、Replicate Webhook 机制

### 8.1 Replicate 支持 Webhook

Replicate 原生支持 Webhook，在创建 prediction 时通过请求体传入：

```json
{
  "input": { ... },
  "webhook": "https://your-server/flux/internal/replicate/callback",
  "webhook_events_filter": ["completed"]
}
```

支持的事件类型：
| 事件 | 触发时机 |
|---|---|
| `start` | 预测开始时立即触发 |
| `output` | 每次产生新输出时（最多 500ms 一次）|
| `logs` | 每次产生新日志时（最多 500ms 一次）|
| `completed` | 预测终态（succeeded / canceled / failed）|

**我们只订阅 `completed`**，避免无用回调。

### 8.2 回调体格式

与查询结果响应相同（`ReplicateResponse`）：

```json
{
  "id": "4h9ajx4vrhrmy0cy415935qz7m",
  "status": "succeeded",
  "output": "https://replicate.delivery/xezq/xxx.webp",
  "metrics": { "predict_time": 8.74 },
  "webhook-id": "...",
  "webhook-timestamp": "1715671234"
}
```

### 8.3 签名验证（建议接入）

Replicate 对每次回调使用 **HMAC-SHA256** 签名，三个关键 header：

| Header | 说明 |
|---|---|
| `webhook-id` | 本次推送的唯一 ID（重试时相同）|
| `webhook-timestamp` | Unix 秒级时间戳（防重放）|
| `webhook-signature` | `v1,<base64_signature>` |

**签名计算方式：**
```
signed_content = "{webhook-id}.{webhook-timestamp}.{raw_body}"
key = base64_decode(whsec_xxxxx 中 "whsec_" 之后的部分)
signature = HMAC-SHA256(key, signed_content)
```

**获取 signing key（一次性，可缓存）：**
```bash
curl -H "Authorization: Bearer <token>" \
  https://api.replicate.com/v1/webhooks/default/secret
# 返回: {"key": "whsec_C2FVsBQIhrscChlQIMV+b5sSYspob7oD"}
```

**Go 验签伪代码：**
```go
func verifyReplicateWebhook(webhookID, timestamp, rawBody, sigHeader, signingKey string) bool {
    // 1. 防重放：时间戳不超过 5 分钟
    ts, _ := strconv.ParseInt(timestamp, 10, 64)
    if time.Now().Unix()-ts > 300 {
        return false
    }
    // 2. 构造 signed content
    signedContent := fmt.Sprintf("%s.%s.%s", webhookID, timestamp, rawBody)
    // 3. 解码 key（去掉 "whsec_" 前缀后 base64 decode）
    key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(signingKey, "whsec_"))
    // 4. 计算 HMAC-SHA256
    mac := hmac.New(sha256.New, key)
    mac.Write([]byte(signedContent))
    expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
    // 5. 与 header 中的签名比对（可能有多个，空格分隔）
    for _, sig := range strings.Fields(sigHeader) {
        sig = strings.TrimPrefix(sig, "v1,")
        if hmac.Equal([]byte(sig), []byte(expected)) {
            return true
        }
    }
    return false
}
```

### 8.4 与当前架构的集成方案

**方案：新增独立路由 + 签名验证中间件**

```
POST /flux/internal/replicate/callback
  ↓
verifyReplicateWebhook()  ← 验签（可选 feature flag 控制开关）
  ↓
HandleReplicateCallback()
  ↓
复用 DB 更新 + 扣费逻辑（参考 handleSuccessCallback）
```

**该路由仅在 Replicate 渠道的 `Prefer: wait=60` 60s 超时场景下触发**（正常同步完成时不走回调）。

### 8.5 signing key 管理

- 从 `GET /v1/webhooks/default/secret` 获取后**缓存到内存**（或 Redis）
- 启动时预加载一次，每 24h 刷新一次（或监听到验签失败时主动刷新）
- signing key 是账号级别的，与模型/渠道无关

---

## 九、待确认事项

1. `flux-2-pro` 在 Replicate 上是否支持 `output_format` / `aspect_ratio` 参数（需实测）
2. Replicate webhook 签名验证是否第一期就接入，还是先跳过（建议接入，防伪造回调）
3. 是否需要支持 Replicate 独有但 BFL 没有的模型（如 `flux-schnell`、`flux-kontext-dev`）
4. 60s 超时后客户端收到 `{id, polling_url}` 但图片尚未就绪，需确认轮询间隔建议文案
5. `flux-pro-1.0-fill` 后续是否计划接入（参数适配工作量约 1-2天）
