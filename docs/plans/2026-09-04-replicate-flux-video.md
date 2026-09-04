# Replicate 接入 flux-3 视频生成（复用 flux-3-video 模型名）

## 背景与目标
Replicate 上线了 `black-forest-labs/flux-3`（视频生成，与 BFL flux-3-video 同款能力）。需求：让 `flux-3-video` 既能走 BFL 原生（`x-key` + `/v1/flux-3-video`），也能走 Replicate（Bearer + `/v1/models/black-forest-labs/flux-3/predictions`），**由渠道 baseURL 决定走哪个上游**——与 flux 图片"同一模型名、baseURL 决定 BFL/Replicate"的哲学一致。

**已确认决策（用户）**：
1. 走**视频框架**（扩 `VideoAdaptor`），而非图片框架。视频落 `videos` 表、复用按秒计费，语义正确。
2. **复用模型名 `flux-3-video`**，不新增独立模型名。渠道 baseURL 含 `replicate.com` → 走 Replicate 分支。
3. 本期**不做** `flux-video-upscale`（视频放大器：输入是视频、按输出 MP·秒计费、提交时算不准，形态与生成模型不同，单独排期）。

**关键约束（已核实）**：
- `relay/controller/video.go` 视频计费**不套渠道折扣/分组倍率**，`taskResult.Quota` 原样扣。故复用 `flux-3-video` 名 → BFL 与 Replicate **计费完全相同、无法按渠道调差价**。Replicate 实际单价未知，v1 沿用现有 `flux-3-video` 占位价（hd 0.17 / fhd 0.34 USD/s）。若两家价差大，后续须改用独立模型名或给视频计费加渠道折扣（超出本期）。
- 现有 Replicate 图片链路（`flux.Adaptor` + Image 表 + webhook + reconciler）**不复用**——那套产图片进 Image 表，视频套进去语义错。仅复用其数据结构（`ReplicateResponse`/`ReplicateOutput`/`ReplicateURLs`）与 `isReplicate()` 判据。

## 方案设计（改 2 个代码文件 + 2 个文档）

### 1. `relay/channel/flux/video_model.go`
- 新增 `ReplicateVideoModelMap = {"flux-3-video": "black-forest-labs/flux-3"}`（决定 predictions URL 的模型 id）。
- 新增 `buildReplicateVideoInput(req FluxVideoRequest) map[string]any`：把 BFL 请求字段转成 Replicate `input`：
  - `prompt` → `prompt`
  - `keyframes` → `images`（string 包成 `[string]`；`[]string` 透传；`[秒,图]` 对形态本期不支持，记录在案）
  - `start_video` → `start_video`
  - `resolution`：`hd`→`720p`、`fhd`→`1080p`（Replicate 用分辨率档位而非 hd/fhd）
  - `duration` / `aspect_ratio` / `generate_audio` / `safety_tolerance` / `draft` → 同名透传
  - `mode` **不传**（Replicate 由 images/start_video 存在与否自行推断），但内部仍用 mode 派生 videoType 计费。
- 轮询/提交响应**复用现有 `ReplicateResponse`**（含 ID/Status/Output/Error/URLs），不新建结构体。

### 2. `relay/channel/flux/video_adaptor.go`
`HandleVideoRequest` 与 `HandleVideoResult` 各加 `if isReplicate(baseURL)` 分支：

**HandleVideoRequest（Replicate 分支）**：
- `replicateID := ReplicateVideoModelMap[meta.ActualModelName]`，缺失则 400。
- `requestURL := meta.BaseURL + "/v1/models/" + replicateID + "/predictions"`。
- body：`{"input": buildReplicateVideoInput(fluxReq)}`（**不带 webhook**，本期走轮询；**不设 `Prefer: wait`**，提交立即返回不阻塞）。
- 认证 `relaychannel.BearerAuthHeaders(ch.Key)`。
- 解析 `ReplicateResponse` 取 `id` 作 TaskId；非 2xx 返回 ErrorWrapper。
- 计费与落库字段（quota/mode/duration/resolution/sound/videoType）与 BFL 分支**完全一致**（`resolution` 保持 hd/fhd 供 `CalculateVideoQuota` 命中现有规则）。

**HandleVideoResult（Replicate 分支）**：
- `queryURL := baseURL + "/v1/predictions/" + taskId`，Bearer 认证 GET。
- 解析 `ReplicateResponse`，status 映射：
  - `starting`/`processing` → `processing`
  - `succeeded` → `succeed`，`VideoResult = string(resp.Output)`（mp4 URL），填 `VideoResults`
  - `failed`/`canceled` → `failed`，Message 取 `resp.Error`
- 返回 `GeneralFinalVideoResponse`。

### 3. 无需改动
- **路由**：提交复用 `POST /v1/flux-3-video`（及 `/v1/video/generations`），查询复用 `GET /flux/v1/get_result?id=`（上一版已改造为图片/视频统一分发）。
- **distributor**：`/v1/flux-3-video` 分支已注入 model；渠道选择由 Distribute 按 model 名完成，Replicate 渠道只要 models 列表含 `flux-3-video` 即可被选中。
- **helper/main.go**：`GetVideoAdaptor` 前缀 `flux-3-video` 与 `GetVideoAdaptorByProvider("flux")` 已命中 `flux.VideoAdaptor`，模型名复用故无需改。
- **计费规则**：复用 `common/video-pricing.go` 现有 `flux-3-video` 按秒规则。

## 影响范围
- **不影响** BFL flux-3-video（分支互斥，baseURL 不含 replicate.com 时走原逻辑）。
- **不影响** flux 图片 Replicate 链路（图片走 `Adaptor`，视频走 `VideoAdaptor`，两条独立）。
- **无数据库 schema 变更**（videos 表现有字段够用，Provider 仍为 "flux"）。

## 已知限制（记录在案）
1. **【已确认方案 A：同名共价】** BFL 与 Replicate 的 `flux-3-video` **计费完全相同**，视频计费链路不支持按渠道调差价。Replicate 真实单价待核；若日后价差过大需另立模型名（方案 B）。
2. `keyframes` 的 `[秒,图]` 对形态无法映射到 Replicate `images`，本期仅支持 string / string 数组。
3. 输出直接透传 Replicate 签名 mp4 URL（不转存 R2）；如需防过期可后续接 `UploadVideoURLToR2`。
4. Replicate 结果轮询靠客户端查询触发；如需服务端兜底轮询，依赖既有视频 poller（`ENABLE_VIDEO_TASK_POLLER`），需确认其 provider=flux 是否纳入扫描（本期先按需查询）。

## 验证方式
1. `go build ./... && go vet ./...`（提交前必跑）。
2. 后台建一个 Flux 渠道（type 46），`base_url=https://api.replicate.com`，key 填 Replicate token，models 含 `flux-3-video`。
3. `POST /v1/flux-3-video`，body `{"mode":"t2v","prompt":"...","resolution":"hd","duration":5}` → 返回 Replicate prediction id。
4. `GET /flux/v1/get_result?id=<id>` → processing → succeed，`video_result` 为可播放 mp4。
5. i2v：带 `keyframes`（单图 URL）复测，确认转成 Replicate `images` 提交成功。
6. 回归：BFL 渠道的 flux-3-video 仍走 `x-key` + `/v1/flux-3-video`（未受影响）。
7. 核对 videos 表落库（provider=flux）与 logs 扣费。

## 实测确认（2026-09-04，用 REPLICATE_API_KEY 真实跑通）

**真实 Input schema**（`GET /v1/models/black-forest-labs/flux-3`）：
- `prompt` string（唯一必填）
- `images` array（default `[]`）
- `start_video` string
- `resolution` **enum 仅 `720p`/`1080p`**（default `720p`）
- `aspect_ratio` enum：auto/21:9/2:1/16:9/4:3/1:1/3:4/9:16（default auto）
- `duration` **enum 字符串** `'auto','5'…'20'`（default `auto`）—— **不是数字**
- `generate_audio` bool（default true）、`draft` bool（default false）、`safety_tolerance` int（default 2）
- **Output：单个 string（mp4 uri）**

**完整流程**（实测 t2v/5s/720p，约 45s 完成）：
- 提交 `POST /v1/models/black-forest-labs/flux-3/predictions` → 201，`{id, status:"starting", urls:{get,cancel,stream,web}, ...}`
- 轮询 `GET /v1/predictions/{id}`（= `urls.get`）→ `starting→processing→succeeded`
- 成功：`output` 为 mp4 URL 字符串；`metrics` 含 `video_output_duration_seconds`（真实时长）、`resolution_target`、`predict_time`

**据此修复（本轮 A+B）**：
- **A** `duration` 数字→字符串枚举归一（`normalizeReplicateDuration`），杜绝客户端传 JSON 数字导致的 422。
- **B** 计费口径归一（`normalizeBillingResolution`）：`720p→hd`/`1080p→fhd`，堵住 `1080p` 落兜底通配价少收一半。
- 字段名核对：BFL 与 Replicate flux-3 字段**同源全对**，无未知字段 422 风险（此前 P0-1 担忧证伪）。

**待下轮（C，已排期）**：计费时机由"提交时按请求时长估算"改为"succeeded 时按 `metrics.video_output_duration_seconds` 真实结算"。注意 BFL `get_result` 不返回时长 metrics，C 仅对 Replicate 生效、会引入两条路径计费机制分叉 + videos 表补扣/退款流转，需单独计划。`duration=auto`/缺省在 C 落地前仍按 5 秒计费（资损口子未闭）。
