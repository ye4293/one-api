# Video Adaptor 全量迁移设计

**日期**: 2026-03-16
**目标**: 将 `relay/controller/video.go` 中剩余 9 个供应商全部迁移到 `VideoAdaptor` 接口，在不影响现有功能的前提下消除代码重复。

---

## 背景

`relay/controller/video.go` 当前 6070 行，68 个函数，12 个视频供应商。上一次重构（`d5bf107`）引入了 `VideoAdaptor` 接口并迁移了 minimax、zhipu、runway 三个供应商。剩余 9 个仍在旧架构中：sora、kling、luma、ali、pixverse、doubao、veo（vertexai）、grok。

重复模式出现 10+ 次：
- 预扣费余额检查（5 行）
- HTTP POST JSON 发送（15 行）
- 响应体读取
- `CreateVideoLog` 调用
- `GeneralVideoResponse` 写回客户端

---

## 架构设计：3 层

```
relay/channel/video_helper.go         ← 纯工具函数（无状态，可测试）
relay/channel/base_video_adaptor.go   ← BaseVideoAdaptor（嵌入复用）
relay/channel/{provider}/video_adaptor.go  ← 每家各自实现
relay/controller/video.go             ← 越来越薄，最终只剩路由分发
```

### 层 1：`relay/channel/video_helper.go`

两个纯工具函数，无状态：

```go
// PostJSONRequest 封装 HTTP POST + ReadAll
func PostJSONRequest(url string, body []byte, headers map[string]string) (respBody []byte, statusCode int, err error)

// CheckUserQuota 预扣费检查
func CheckUserQuota(ctx context.Context, userId int, required int64) error
```

### 层 2：`relay/channel/base_video_adaptor.go`

```go
type BaseVideoAdaptor struct {
    Meta *util.RelayMeta
}

func (b *BaseVideoAdaptor) Init(meta *util.RelayMeta)  { b.Meta = meta }
func (b *BaseVideoAdaptor) GetPrePaymentQuota() int64  { return int64(0.2 * config.QuotaPerUnit) }
func (b *BaseVideoAdaptor) GetChannelName() string     { return "" } // 各自覆盖
```

供应商嵌入 `BaseVideoAdaptor`，只覆盖有差异的方法。

### 层 3：9 个供应商迁移

| 供应商 | 文件位置 | 特殊性 | 预计行数 |
|--------|----------|--------|---------|
| luma | `relay/channel/luma/video_adaptor.go` | 无特殊，最简单 | ~80 |
| ali | `relay/channel/ali/video_adaptor.go` | 分辨率解析逻辑 | ~100 |
| pixverse | `relay/channel/pixverse/video_adaptor.go` | 多种请求类型 | ~120 |
| grok | `relay/channel/xai/video_adaptor.go` | 自定义配额计算 | ~140 |
| doubao | `relay/channel/doubao/video_adaptor.go` | CNY→USD 汇率（移入包内） | ~160 |
| kling | `relay/channel/keling/video_adaptor.go` | JWT 认证、5 种视频类型 URL | ~200 |
| veo | `relay/channel/vertexai/video_adaptor.go` | GCS URL 转换、并发上传、凭证 | ~250 |
| sora | `relay/channel/openai/sora_video_adaptor.go` | multipart、remix 模式 | ~300 |

---

## 关键决策

### `calculateQuota` 的处理
当前是一个混杂所有供应商计费逻辑的"神函数"。迁移后：
- 每个供应商的配额计算逻辑移入其 `HandleVideoRequest` 内部
- `calculateQuota` 函数从 `video.go` 删除

### `GetVideoResult` 的演进
随着供应商迁移，`GetVideoResult` 中的 `switch videoTask.Provider` 分支逐一删除，最终整个 switch 消失，只剩：
```go
if adaptor := relayhelper.GetVideoAdaptorByProvider(videoTask.Provider); adaptor != nil {
    return invokeVideoAdaptorResult(c, adaptor, videoTask, channel, &cfg)
}
```

### 汇率管理（Doubao 专用）
`ExchangeRateManager`、`convertCNYToUSD`、`fetchRateFromExchangeRateAPI`、`fetchRateFromFixer` 移入 `relay/channel/doubao/` 包内。

### Sora Remix 的处理
Remix 是 Sora 独有的特殊模式（需要查找原 channel），作为 `SoraVideoAdaptor` 的内部逻辑处理，不暴露为独立 adaptor。

---

## 迁移顺序（由简到难）

**Phase 1 - 简单供应商**：luma、ali、pixverse
**Phase 2 - 中等供应商**：grok、doubao
**Phase 3 - 复杂供应商**：kling、veo、sora
**Phase 4 - 清理**：删除 video.go 中所有旧的 handle* 函数，删除 calculateQuota

---

## 完成后的效果

| 指标 | 迁移前 | 迁移后 |
|------|--------|--------|
| `video.go` 行数 | 6070 | ~300（只剩 DoVideoRequest、GetVideoResult、invokeVideoAdaptor*、CreateVideoLog、UpdateVideoTaskStatus、CompensateVideoTask） |
| 重复预扣费检查 | 8 处 | 0 处（全部在 invokeVideoAdaptorRequest 中） |
| 重复 HTTP 发送 | 10 处 | 0 处（全部调用 PostJSONRequest） |
| calculateQuota | 166 行混杂逻辑 | 0 行（分散到各 adaptor） |
