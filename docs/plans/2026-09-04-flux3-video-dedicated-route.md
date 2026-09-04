# flux-3-video 专用路由（BFL 原生形态）

## 背景与目标
现有 flux-3-video 只能走通用 `POST /v1/video/generations`（请求体带 `model` 字段）+ `GET /v1/video/generations/result?taskid=` 查询。
需求：暴露与 BFL 原生一致的专用路由，让客户端把 baseURL 指向 one-api 即可无缝调用：
- 提交：`POST /v1/flux-3-video`
- 查询：`GET /flux/v1/get_result?id=<taskId>`（复用 flux 图片已有的查询入口，改造为图片/视频统一分发）

**关键判断**：不复刻 flux 图片体系（那套走 Image 表 + flux_reconciler + GetFlux，与视频语义不符）。真正对口的参照是 **kling 专用路由**——同为视频、同走 VideoAdaptor，实现方式是「在 distributor 按路径注入 model + 复用现有 relay handler」。因此完全复用上一版已实现的 `flux.VideoAdaptor`（videos 表 + video-pricing 计费 + 轮询），不新增落库/计费逻辑。

**查询路由冲突处理**：`/flux/v1/get_result` 已被 flux 图片的 `GetFlux` 占用，gin 不允许同路径重复注册。故不新增查询路由，而是**改造 `GetFlux` 为统一分发**：先查 Image 表命中走图片逻辑，未命中再查 Video 表命中走视频结果查询。

## 方案设计（改动 3 个代码文件 + 2 个文档）

### 1. `middleware/distributor.go` — `getModelRequest`
在 else-if 链的 `/flux/v1/` 分支之后、最终 `else`（OpenAI 兜底）之前，新增：
```go
} else if strings.HasPrefix(path, "/v1/flux-3-video") {
    // BFL FLUX 3 Video 专用路由：原生请求体无 model 字段，按路径硬编码注入
    modelRequest.Model = "flux-3-video"
}
```
理由：BFL 原生 body 只有 mode/prompt/...，无 model。不注入则 Distribute 报 "Model name is required"。参照 kling `/kling/v1/`、`/ali/api/v1/` 同款做法。

### 2. `router/relay-router.go`
仅新增**提交路由**（挂在 line 68 `.Use(...Distribute())` 之后的 block，与 `/video/generations` 同段）：
```go
relayV1Router.POST("/flux-3-video", controller.RelayVideoGenerate)
```
查询路由**不新增**——复用现有 `GET /flux/v1/get_result`（`controller.GetFlux`）。

### 3. `controller/flux.go` — 改造 `GetFlux`
图片任务未命中时，尝试按视频任务处理：
```go
image, err := model.GetImageByTaskId(taskID)
if err != nil {
    if _, verr := model.GetVideoTaskById(taskID); verr == nil {
        c.Set("response_format", c.Query("response_format"))
        if bizErr := relaycontroller.GetVideoResult(c, taskID); bizErr != nil {
            c.JSON(bizErr.StatusCode, gin.H{"error": util.ProcessString(bizErr.Error.Message)})
        }
        return
    }
    c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
    return
}
```
`relaycontroller.GetVideoResult` 内部按 taskId 查 videos 表拿 provider → `GetVideoAdaptorByProvider("flux")`（上一版已注册）。新增 import `relaycontroller "relay/controller"`（`controller` 包已依赖该包，无循环）。

## 影响范围
- 不动通用 `/v1/video/generations`（两个提交入口并存）。
- `GetFlux` 改造后同时服务图片与视频查询；图片查询逻辑不变（仅在图片未命中时新增视频回退分支）。
- 复用已实现的 VideoAdaptor / 计费 / videos 表，无新增落库或计费逻辑。
- 无数据库 schema 变更。

## 冲突核对（已验证）
- `/flux/v1/get_result` 未重复注册（改造复用 `GetFlux`，非新增路由）。
- `/v1/flux-3-video` 前缀不被 getModelRequest 前面任何分支拦截（`/v1/models/` 等前缀不匹配）。
- 提交路由 `/v1/flux-3-video` 放在 `/v1` 组而非 `/flux/v1/`：因 flux 图片的 `POST /flux/v1/*model` 通配会捕获 `/flux/v1/flux-3-video` 并当成图片模型，故提交必须走 `/v1` 前缀。
- `controller` → `relay/controller` 依赖方向已存在（controller/relay.go 已 import），无循环 import。

## 已知不一致（记录在案）
- 提交走 `/v1/flux-3-video`、查询走 `/flux/v1/get_result`，两者前缀不同，客户端 baseURL 需分别处理。这是「提交不能进 `/flux/v1/` 通配、查询要对齐 flux 图片入口」共同约束下的结果。

## 验证方式
1. `go build ./... && go vet ./...`（提交前必跑）—— 已通过；gofmt 无差异。
2. 后台 Flux 渠道 models 列表加入 `flux-3-video`、填 BFL key。
3. `POST /v1/flux-3-video`（body `{"mode":"t2v","prompt":"...","resolution":"hd","duration":5}`，无 model 字段）→ 返回 task id。
4. `GET /flux/v1/get_result?id=<taskId>` → processing → succeed，`video_result` 为可播放 mp4。
5. 回归：图片 task_id 走 `GET /flux/v1/get_result?id=` 仍返回图片结果（未受影响）。
6. 核对 videos 表落库（provider=flux）与 logs 扣费。
