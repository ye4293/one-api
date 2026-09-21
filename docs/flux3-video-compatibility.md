# FLUX 3 视频兼容行为

`POST /v1/flux-3-video` 使用 BFL 风格请求体，由渠道配置决定调用 BFL 或 Replicate。

## 关键帧

BFL 的 `keyframes` 原样发送，支持图片字符串、图片数组，以及带时间点的关键帧：

```json
{
  "mode": "i2v",
  "prompt": "镜头从街道转向远处的山",
  "duration": 10,
  "keyframes": [[0, "https://example.com/start.png"], [7.5, "https://example.com/end.png"]]
}
```

Replicate 接收单图或 1–10 张图片组成的数组，转换为 `images`。三张及以上图片必须指定整数时长。
Replicate 没有等价的定时关键帧字段，因此这种请求会在提交前返回 HTTP 400，提示使用 BFL 渠道。
不会移除时间点、丢弃图片或自动切换渠道。

## 草稿与草稿增强

生成草稿使用 `draft: true`，分辨率只能为 `hd` 或其别名 `720p`。
BFL 草稿返回的 `result.draft_cache` 会保留在首次查询和数据库终态查询的响应中。

草稿增强仅支持 BFL：

```json
{
  "mode": "draft_enhance",
  "draft_cache": "https://example.com/draft.bin",
  "resolution": "fhd"
}
```

该模式只接受 `mode`、`draft_cache`、`resolution`、`safety_tolerance`、`user`。
不发送 `prompt`，也不接受重设时长、图片、视频、音频、画幅或版本；这些内容沿用原草稿。
缺省分辨率为 `fhd`，也支持 `hd/qhd/uhd`。缓存可以是 BFL 接受的 base64 或有效 URL。
Replicate 请求草稿增强会在提交前返回 HTTP 400，不会退化成普通视频生成。

## 时长与分辨率

| 模式 | BFL | Replicate |
| --- | --- | --- |
| `t2v` / `i2v` | 整数 5–20 秒或 `"auto"` | 整数 5–20 秒或 `"auto"` |
| `v2v` | 整数 5–15 秒或 `"auto"` | schema 允许整数 5–20 秒或 `"auto"` |
| `draft_enhance` | 不接受时长参数 | 不支持 |

入口兼容数字 `10` 和字符串 `"10"`，统一后向 BFL 发送整数，向 Replicate 发送数字字符串。
普通生成未传时长时使用 `"auto"`。小数秒请求、越界值、空字符串及无效类型返回 HTTP 400，
不再截断、钳制或改成自动时长。提交、预扣和落库使用同一归一化值。

`720p/1080p` 在入口统一为 `hd/fhd`；BFL 收到统一档位，Replicate 收到 `720p/1080p`。
`qhd/uhd` 保留真实档位，仅能提交至 BFL。普通生成默认 `hd`，草稿增强默认 `fhd`。

## 完成结算

1. 提交时沿用配置的视频定价规则预扣；自动时长或草稿增强未知时长时，暂按 5 秒估算。
2. 完成时优先使用上游顶层 `cost`（美分）结算，适用于 BFL 和返回此字段的 Replicate 代理。
3. 标准 Replicate 未返回 `cost` 时，读取 `metrics.video_output_duration_seconds`，按当前配置价格补退。
   保留实际小数秒，不使用 `predict_time`；沿用请求时长选择定价规则，按实际输出秒数计算按秒价格。
   固定价规则只收一次。输出分辨率指标已知时使用该档位，否则使用请求档位。
4. 实际时长写入任务的 `duration`。金额沿用美分取整，再转换为配额。
5. 上游费用和有效输出时长均缺失时保留预扣，不根据推理耗时猜测费用。

客户端轮询与后台对账共用成功结算入口，以 `processing → succeed` 的条件更新保证同一任务只补退一次。
已失败或已成功的任务不会被迟到的成功响应重新结算。

本次适配沿用已有价格配置，没有设置新的渠道单价或草稿折扣；配置价格不等同于 Replicate 官方账单。
内置 FHD 单价仍是占位估算，QHD/UHD 和草稿价格需要运营配置核对。

## 验证

```sh
go test -race ./relay/channel/flux ./common ./relay/controller
```

回归测试使用本地模拟上游和内存数据库，覆盖实际请求体、定时关键帧拒绝、草稿缓存保留、时长边界、
小数秒结算、固定价、零费用、费用优先级，以及并发补扣和退款。不会发起付费生成。

接口依据：[BFL OpenAPI](https://api.bfl.ai/openapi.json)、[Replicate FLUX 3 schema](https://replicate.com/black-forest-labs/flux-3/api/schema)。
