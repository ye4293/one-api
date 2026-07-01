# Gemini Omni 视频任务保存 prompt

## 背景与目标
- 现象：`gemini-omni-flash-preview` 提交任务后，`videos` 表 `prompt` 列存的是字面量 `"prompt"`，而非用户输入。
- 根因：`CreateVideoLog` 原实现硬编码 `Prompt: "prompt"`；且 `VideoTaskResult` 不携带 prompt，Gemini adaptor 返回时也没把 `req.Prompt` 传出。
- 目标：让 Gemini 任务的 `prompt` 列正确落库；同时修复当前因签名变更导致的编译失败。

## 方案设计
核心思路：**prompt 走 `VideoTaskResult`**，避免给 `CreateVideoLog` 加参数导致全量调用点被污染。

1. `relay/channel/interface.go`
   - `VideoTaskResult` 增加 `Prompt string` 字段。

2. `relay/channel/gemini/video_adaptor.go`（`HandleVideoRequest` 返回处，约 :139）
   - `VideoTaskResult{...}` 中补 `Prompt: req.Prompt`。

3. `relay/controller/video.go`
   - `invokeVideoAdaptorRequest`（:758）调 `CreateVideoLog` 时，末尾传 `taskResult.Prompt`。
   - 其余 3 处直接调用（minimax :352 / zhipu :414 / runway :477）末尾补 `""` 占位。

4. `relay/controller/directvideo.go`（5 处：:167 / :1126 / :2023 / :2327 / :2475）
   - 末尾补 `""` 占位。

5. `relay/controller/directvideo_xai.go`（:78）
   - 末尾补 `""` 占位。

## 影响范围
- 非 Gemini provider 的 `prompt` 列从字面量 `"prompt"` 变为空串（行为更准确，前端展示原本也是错的）。
- 不涉及数据库 schema 变更，无需迁移。
- `result` 字段不在本次范围：Gemini 视频结果本就走 `store_url`，`result` 是 Kling 回调专用，保持现状。

## 验证方式
- `go build ./... && go vet ./...` 通过。
- 提交一个 `gemini-omni-flash-preview` 文本生视频请求，查 `videos` 表对应 `task_id` 的 `prompt` 列等于请求体中的 `prompt`。

---

## 追加：Gemini 查询结果落库到 `result` 字段

### 背景
`videos.result` 字段原注释为"保存 Kling 回调的完整 JSON 数据"，仅 Kling 回调路径写入。Gemini 走主动查询，完整上游响应当前未持久化。需求：Gemini 查询时把 Interactions API 返回的完整 JSON 存入 `result`。

### 方案设计
1. `model/video.go`
   - 新增 `UpdateVideoResult(taskId, result string) error`，仿 `UpdateVideoStoreUrl`。
2. `relay/channel/gemini/video_adaptor.go`
   - `FetchAndStoreVideoResult` 命名返回值增加 `rawJSON string`；在 `json.Unmarshal` 成功后赋值 `rawJSON = string(respBody)`，请求失败/HTTP 非 200 时保持空串。
   - `HandleVideoResult` 拿到 `rawJSON` 后调用 `dbmodel.UpdateVideoResult(taskId, rawJSON)`（缓存命中分支不调，因 result 此前已落库）。
3. `controller/gemini_video_poller.go`
   - 接收 `rawJSON`，在 succeed/failed 分支写入 `result` 字段（合并进现有 `Updates` map）。

### 影响范围
- 仅 Gemini provider，不影响其他 provider。
- 每次查询都会覆盖 `result` 为最新一次上游响应，符合"最后一次完整响应"语义。
- 无 schema 变更。

### 验证方式
- `go build ./... && go vet ./...` 通过。
- 提交 Gemini 视频任务并查询结果后，`videos.result` 列为非空 JSON，内容为 Interactions API 响应体。

