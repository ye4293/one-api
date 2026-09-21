# Flux 视频放大接口

在 Flux 类型的渠道中添加模型 `flux-upscale`，通过渠道地址选择 BFL 或 Replicate：

| 服务 | 渠道地址 | 渠道密钥 | 上游模型或接口 |
| --- | --- | --- | --- |
| BFL | `https://api.bfl.ai` | BFL API Key | `/v1/flux-tools/video-upscale-v1` |
| Replicate | `https://api.replicate.com` | Replicate API Token | `black-forest-labs/flux-video-upscale` |

模型名用于渠道选择、任务记录和计费；请求路径仍为 `/v1/flux-tools/video-upscale-v1`。
如已按此前版本配置渠道模型或定价规则，需要将模型名同步改为 `flux-upscale`。
已创建的任务继续按任务 ID 查询，无需修改历史记录。

## 提交任务

客户端使用 one-api 令牌，支持 `x-key` 或 `Authorization: Bearer` 鉴权：

```sh
curl -sS -X POST "$ONE_API_BASE_URL/v1/flux-tools/video-upscale-v1" \
  -H "Content-Type: application/json" \
  -H "x-key: $ONE_API_TOKEN" \
  -d '{
    "input_video": "https://your-storage.example.com/source-clip.mp4",
    "upscale_factor": 2.0,
    "creativity": 1,
    "prompt": "渔夫站在岸边，保留水面的细节",
    "safety_tolerance": 2
  }'
```

接口无需提供 `model` 或 `mode`，仅 `input_video` 必填。

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `input_video` | string | 必填，HTTP(S) URL 或 base64 编码的 MP4；源视频最大 50MB、20 秒、2560×1440 |
| `upscale_factor` | number | 1.5–3，默认 2；保持源视频画幅比例 |
| `creativity` | integer | 0 精确保留源视频，1 创意增强细节；默认 1 |
| `prompt` | string | 可选的视频内容描述，用于引导细节增强；空值为中性放大 |
| `safety_tolerance` | integer | 0–4，默认 2 |
| `webhook_url` | string | 可选回调地址，最长 2083 字符；BFL 支持 HTTP(S)，Replicate 要求 HTTPS；未传时按 `ServerAddress` 默认填写本站地址 |
| `webhook_secret` | string | BFL 可选回调签名密钥；Replicate 不支持请求级签名密钥，传入非空值返回 HTTP 400 |

省略可选参数时采用上游默认值，显式传入的 `creativity: 0` 和 `safety_tolerance: 0` 会保留。
越界数值、非整数的枚举参数或无效回调地址在提交前返回 HTTP 400。
视频格式、时长、分辨率和 URL 可访问性由上游校验，本站不下载视频进行预处理。
Replicate 的模型参数放入预测请求的 `input` 对象，五个视频处理参数保持同名。
裸 base64 视频会补充 `data:video/mp4;base64,` 前缀，已有 MP4 data URL 和 HTTP(S) URL 保留。
Replicate 官方建议大于 256KB 的输入优先使用 HTTP(S) URL；base64 输入会在本站校验编码及 50MB 大小上限。

配置了 `ServerAddress` 且未传 `webhook_url` 时，默认向 BFL 提交
`{ServerAddress}/flux/internal/callback`。配置了 `FLUX_WEBHOOK_SECRET` 时，沿用现有 Flux 的
`?key=...` 查询参数鉴权。本站回调入口根据任务 ID 区分图片和视频，成功时保存视频结果并结算，失败时退款。
回调、客户端查询和后台轮询共用条件更新，重复或迟到的通知不会反转终态、覆盖结果或重复扣退费。

Replicate 默认回调为 `{ServerAddress}/flux/internal/replicate/callback`，与现有 Flux 图片适配一致。
回调地址转换为预测请求外层的 `webhook`，并设置 `webhook_events_filter: ["completed"]`。
本站沿用 `REPLICATE_WEBHOOK_SIGNING_KEY` 校验 Replicate 的 `webhook-id`、`webhook-timestamp` 和 `webhook-signature` 请求头。
Replicate 使用账户级签名密钥，不能用 BFL 的 `webhook_secret` 参数替换。

显式提供 `webhook_url` 时保留该地址及 `webhook_secret`，由 BFL 直接回调；例如：

```json
{
  "input_video": "https://your-storage.example.com/source-clip.mp4",
  "upscale_factor": 2,
  "creativity": 0,
  "safety_tolerance": 0,
  "webhook_url": "https://your-app.example/video-callback",
  "webhook_secret": "your-callback-secret"
}
```

Replicate 自定义回调也使用 `webhook_url`，但需省略上例的 `webhook_secret`。

回调签名密钥不写入视频任务记录，也不包含在本站提交响应中。提示词会保存到任务记录。
未配置 `ServerAddress` 且未传回调地址时，不注入 webhook，继续使用客户端查询和后台轮询。

提交成功返回：

```json
{
  "id": "8b6a4d16-2b52-4a5e-9f1c-3e7d0a92c48b",
  "polling_url": "https://your-one-api.example/flux/v1/get_result?id=8b6a4d16-2b52-4a5e-9f1c-3e7d0a92c48b"
}
```

`polling_url` 指向本站。配置了 `ServerAddress` 时返回完整地址，否则返回 `/flux/v1/get_result?id=...` 相对路径。
服务端保存 BFL 返回的上游 `polling_url`，后续按原地址查询任务所在集群。
BFL 的回调提交响应可能不含 `polling_url`；此时本站仍返回自己的查询地址，上游查询回退到渠道的 `/v1/get_result?id=...`。
Replicate 提交调用 `/v1/models/black-forest-labs/flux-video-upscale/predictions`，轮询调用 `/v1/predictions/{id}`，
上游使用渠道的 Bearer Token。客户端仍然使用本站令牌访问返回的 `polling_url`。

## 查询结果

```sh
curl -sS "$ONE_API_BASE_URL/flux/v1/get_result?id=YOUR_TASK_ID" \
  -H "x-key: $ONE_API_TOKEN"
```

处理中保留 BFL 的 `Pending` / `Processing` 状态。成功响应沿用现有 Flux 视频的原生字段结构：

```json
{
  "id": "8b6a4d16-2b52-4a5e-9f1c-3e7d0a92c48b",
  "status": "Ready",
  "cost": 10,
  "result": {
    "sample": "https://delivery.bfl.ai/results/8b6a4d16-2b52-4a5e-9f1c-3e7d0a92c48b/sample.mp4?se=..."
  },
  "progress": 0,
  "details": null,
  "preview": null
}
```

Replicate 的 `starting/processing/succeeded/failed/canceled` 分别转换为
`Pending/Processing/Ready/Error/Error`，成功时将 `output` 视频地址转换为 `result.sample`。
回调和轮询使用相同的转换及结算逻辑，原始 Replicate 响应保存在视频任务记录中。

`result.sample` 是上游签名的视频下载地址。`cost` 为实际计费的美分数，示例值不代表官方价格。
终态结果会写入视频任务表，再次查询从数据库返回。

后台轮询复用现有 Flux 视频对账器，每 30 秒扫描未完成任务，由 `ENABLE_VIDEO_TASK_POLLER` 开关控制。
即使客户端不再查询，启用的后台轮询也会推进状态、保存结果并结算；失败或超过 4 小时未完成时退还预扣费用。

## 计费与验证

提交时沿用视频定价配置，模型为 `flux-upscale`，类型为 `video-to-video`，模式为 `upscale`。
请求不包含输入时长，按秒规则的预估时长沿用现有默认值 5 秒；建议为该模型配置明确的预扣规则。
未匹配规则时沿用通用兜底预扣 $0.10。成功时若上游返回正数 `cost`，按该费用多退少补。
Replicate 未返回 `cost` 时，若提供 `metrics.video_output_duration_seconds`，沿用现有视频定价规则按实际时长结算；
不使用 `predict_time` 作为视频时长。费用和有效视频时长均缺失时保留预扣。
本次没有新增或假定 BFL 官方单价。

```sh
go test -race ./relay/channel/flux ./relay/controller ./middleware ./router
go test -race ./controller -run '^Test(Flux|Replicate)VideoUpscaleLifecycle$'
```

测试使用模拟 BFL 服务和内存数据库，覆盖模型名与接口路径区分、参数透传及边界校验、鉴权、任务保存、
客户端查询、后台轮询、回调提交缺少上游轮询地址、默认本站回调、回调鉴权、结算退款及终态重复通知，
同时覆盖 BFL 和 Replicate 两种上游。

参数依据：[BFL 视频放大官方文档](https://docs.bfl.ai/flux_tools/flux_video_upscale)、[BFL OpenAPI](https://api.bfl.ai/openapi.json)、
[Replicate 模型 schema](https://replicate.com/black-forest-labs/flux-video-upscale/api/schema)、[Replicate HTTP API](https://replicate.com/docs/reference/http)。
