# flux-3-video / flux-upscale 提交响应对齐 BFL 原生 usage 形状

## 背景与目标

`/v1/flux-3-video`（生成）与 `/v1/flux-tools/video-upscale-v1`（放大）当前提交响应形状不一致：
- upscale 已返回 `{id, polling_url, cost, input_mp, output_mp}`；
- 生成仍返回 `{task_id, task_status, message, video_duration, polling_url}`（`GeneralVideoResponse`）。

目标：**生成端点也返回 BFL 原生的 `{id, polling_url, cost, input_mp, output_mp}` 形状**，两个端点提交响应统一。

## 实测结论（官方 key 验证，2026-09-22）

| 阶段 | flux-3-video | flux-upscale |
|---|---|---|
| create（POST 提交） | `cost:null, input_mp:null, output_mp:null` | `cost:null, input_mp:null, output_mp:null` |
| get_result **Ready** | `cost:30.0` | `cost:452.0` + `result.duration` |

**关键**：BFL 在**提交时算不出** cost/mp（尚未下载/分析素材），三者恒为 `null`；真实 cost 仅在**完成态**（get_result Ready）顶层返回。

因此：
- 提交响应的 `cost/input_mp/output_mp` **只能忠实透传 null**（用 `*float64`，避免 `0` 误导为“免费”）。
- 真实计费链路走**完成结算**（本就存在）：`HandleVideoResult` 在 Ready 读 `pollResp.Cost → UpstreamCost`，`ApplyVideoSuccess` 按官方 cost 多退少补；客户端轮询 get_result 时 `buildFluxVideoResult` 用已结算 quota 反算出真实 cost（美分）。
- 上个方案“提交时抓 cost 精确扣费”是**空转**（cost 恒 null），本次移除该死分支，提交仍按 `CalculateVideoQuota` 预扣。

## 方案设计（改动文件）

1. `relay/channel/flux/video_model.go`：`FluxVideoSubmitResponse.Cost/InputMP/OutputMP` 改 `*float64`（无 omitempty，nil 序列化为 `null`，忠实还原 BFL create 响应）。
2. `relay/channel/interface.go`：`VideoTaskResult.UpstreamCost/InputMP/OutputMP` 改 `*float64`，承载提交透传值。
3. `relay/channel/flux/video_adaptor.go`：
   - `submitBFLVideo`（生成）返回 `(FluxVideoSubmitResponse, *ErrorWithStatusCode)`；
   - `HandleVideoRequest` 生成路径把 `submitResp.Cost/InputMP/OutputMP` 回填到 `VideoTaskResult`。
4. `relay/channel/flux/video_upscale.go`：移除死的 `if submitResp.Cost>0 { quota=... }`；`cost/mp` 指针回填 `VideoTaskResult`。
5. `relay/controller/video.go`：提交响应分支由 `Mode=="upscale"` 放宽为 `GetProviderName()=="flux"`（覆盖 flux-3-video + upscale，BFL + Replicate 后端），统一返回 `FluxVideoSubmitResponse`。

## 影响范围

- **破坏性**：flux-3-video 提交响应字段名变化（`task_id`→`id` 等），字段集也变（去掉 `task_status/message/video_duration`）。已与用户确认要对齐 BFL 形状。
- 不改数据库表；计费与落库逻辑不变（提交预扣 + 完成结算）。
- get_result（查询）响应形状不变，仍是 `FluxVideoGetResultResponse` 7 字段，含真实 cost。

## 验证方式

1. `go build ./... && go vet ./... && go test ./relay/...`。
2. 官方 key 实测：flux-3-video 提交返回 `{id, polling_url, cost:null, input_mp:null, output_mp:null}`；轮询 get_result 到 Ready 返回真实 cost。
