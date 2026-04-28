# Doubao Video Standalone Controller Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 新增路由 `doubao/api/v3/contents/generations/tasks`，走独立 controller + 后台轮询器，不改动任何现有代码。

**Architecture:** 参照 ali_video.go 模式：独立 Controller 处理创建和查询，后台 goroutine 定时轮询 processing 状态的任务并更新 DB；新路由与原 `/api/v3/...` 路由完全并存，互不干扰。provider 字段统一用 `"doubao"`，轮询器会自动处理所有 doubao 任务（含旧路由创建的任务）。

**Tech Stack:** Go, Gin, GORM, `relay/channel/doubao` package, `controller` package

---

## 关键参考文件（实现前必读）

| 文件 | 用途 |
|------|------|
| `controller/ali_video.go` | 主要参考：controller 结构、预扣费、轮询器模式 |
| `relay/channel/ali/video_adaptor.go` | 参考 `DoCreate/DoQuery/ParseCreateResponse/ParseQueryResponse` |
| `relay/channel/doubao/video_adaptor.go` | 现有 doubao 逻辑：计费函数、exchangeRate manager |
| `relay/channel/doubao/model.go` | doubao 数据结构：`DoubaoVideoResponse`, `DoubaoVideoResult` |
| `model/video.go` | `Video` struct 字段 |
| `router/relay-router.go` | 路由注册位置（在 doubaoApiRouter 附近新增） |
| `main.go` | 轮询器启动位置（在 ali-wan poller 启动之后） |

---

## Task 1: 新增 Adaptor 独立方法

**Files:**
- Create: `relay/channel/doubao/standalone.go`

不修改 `video_adaptor.go`，新建单独文件，在同一 package 内添加方法。

**Step 1: 创建文件**

```go
package doubao

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/util"
)

const defaultDoubaoBaseURL = "https://ark.cn-beijing.volces.com"
const doubaoProvider = "doubao"
const doubaoPollingInterval = 10 // minutes

// GetBaseURL 返回豆包 API base URL
func (a *VideoAdaptor) GetBaseURL(meta *util.RelayMeta) string {
	if meta != nil && meta.BaseURL != "" {
		return strings.TrimRight(meta.BaseURL, "/")
	}
	return defaultDoubaoBaseURL
}

// DoCreate 向豆包提交视频生成任务（不依赖 gin.Context）
func (a *VideoAdaptor) DoCreate(ctx context.Context, meta *util.RelayMeta, body []byte) (*http.Response, error) {
	url := a.GetBaseURL(meta) + "/api/v3/contents/generations/tasks"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return http.DefaultClient.Do(req)
}

// DoQuery 查询豆包视频任务状态（不依赖 gin.Context）
func (a *VideoAdaptor) DoQuery(ctx context.Context, meta *util.RelayMeta, taskID string) (*http.Response, error) {
	url := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", a.GetBaseURL(meta), taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+meta.APIKey)
	return http.DefaultClient.Do(req)
}

// ParseCreateResponse 解析创建任务响应
func (a *VideoAdaptor) ParseCreateResponse(body []byte) (*DoubaoVideoResponse, error) {
	var resp DoubaoVideoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseQueryResponse 解析任务查询响应
func (a *VideoAdaptor) ParseQueryResponse(body []byte) (*DoubaoVideoResult, error) {
	var resp DoubaoVideoResult
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CalcPrePayQuota 预扣费额度：固定 1.4 CNY 转换为 quota
// 使用 defaultExchangeManager（video_adaptor.go 已定义）
func CalcPrePayQuota() int64 {
	usd, err := convertCNYToUSD(1.4)
	if err != nil {
		usd = 1.4 / 7.2
	}
	return int64(usd * config.QuotaPerUnit)
}

// CalcActualQuota 根据实际 token 数精算额度（与 calculateQuotaForDoubao 逻辑一致）
func CalcActualQuota(modelName string, tokens int64) int64 {
	return calculateQuotaForDoubao(modelName, tokens)
}
```

**Step 2: 编译验证**

```bash
cd /Users/yueqingli/code/one-api && go build ./relay/channel/doubao/...
```

预期：无报错

**Step 3: Commit**

```bash
git add relay/channel/doubao/standalone.go
git commit -m "feat(doubao): add standalone DoCreate/DoQuery/ParseXxx/CalcQuota methods"
```

---

## Task 2: 创建专用 Controller

**Files:**
- Create: `controller/doubao_video.go`

**Step 1: 创建文件**

完整代码如下：

```go
package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	doubaomodel "github.com/songquanpeng/one-api/relay/channel/doubao"
	"github.com/songquanpeng/one-api/relay/util"
)

const (
	doubaoVideoProvider      = "doubao"
	doubaoPollingIntervalMin = 10 * time.Minute
)

// ─── 状态映射 ─────────────────────────────────────────────────────────────────

func mapDoubaoStatus(status string) string {
	switch status {
	case "succeeded":
		return "succeed"
	case "failed":
		return "failed"
	default: // queued, running
		return "processing"
	}
}

// ─── 请求上下文 ───────────────────────────────────────────────────────────────

// buildDoubaoMeta 从渠道信息构造最小 RelayMeta（供 poller 使用）
func buildDoubaoMeta(channel *dbmodel.Channel) *util.RelayMeta {
	meta := &util.RelayMeta{
		APIKey:    channel.Key,
		ChannelId: channel.Id,
	}
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}
	return meta
}

// ─── 创建任务 ─────────────────────────────────────────────────────────────────

// RelayDoubaoVideoCreate 处理 POST doubao/api/v3/contents/generations/tasks
func RelayDoubaoVideoCreate(c *gin.Context) {
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		respondError(c, err, "read_body_failed", http.StatusBadRequest)
		return
	}

	// 预扣费额度检查
	quota := doubaomodel.CalcPrePayQuota()
	userQuota, err := dbmodel.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		respondError(c, err, "get_quota_failed", http.StatusInternalServerError)
		return
	}
	if userQuota < quota {
		respondError(c, fmt.Errorf("insufficient quota"), "insufficient_quota", http.StatusPaymentRequired)
		return
	}

	// 获取渠道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		respondError(c, err, "get_channel_failed", http.StatusInternalServerError)
		return
	}
	meta.APIKey = channel.Key
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		meta.BaseURL = *channel.BaseURL
	}

	adaptor := &doubaomodel.VideoAdaptor{}
	resp, err := adaptor.DoCreate(ctx, meta, body)
	if err != nil {
		respondError(c, err, "upstream_request_failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		respondError(c, err, "read_upstream_response_failed", http.StatusInternalServerError)
		return
	}

	createResp, parseErr := adaptor.ParseCreateResponse(respBody)
	if parseErr != nil {
		// 解析失败：透传原始响应，不扣费
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	// 上游业务错误：不扣费
	if createResp.Error != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] upstream error: code=%s, msg=%s", createResp.Error.Code, createResp.Error.Message))
		c.Data(resp.StatusCode, "application/json", respBody)
		return
	}

	taskID := createResp.ID

	// 预扣费
	if err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota); err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] pre-deduct quota failed: %v", err))
	}
	_ = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)

	// 解析模型和提示词（供 DB 记录）
	model, prompt := parseDoubaoRequestMeta(body)

	// 写 DB 记录
	video := &dbmodel.Video{
		TaskId:    taskID,
		Provider:  doubaoVideoProvider,
		Model:     model,
		Type:      "text-to-video",
		Prompt:    prompt,
		Status:    "processing",
		Quota:     quota,
		UserId:    meta.UserId,
		Username:  dbmodel.GetUsernameById(meta.UserId),
		ChannelId: meta.ChannelId,
		CreatedAt: time.Now().Unix(),
	}
	if err := video.Insert(); err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] insert video record failed: task_id=%s, %v", taskID, err))
	}

	logger.Info(ctx, fmt.Sprintf("[doubao] task created: task_id=%s, model=%s, user_id=%d, channel_id=%d, quota=%d",
		taskID, model, meta.UserId, meta.ChannelId, quota))

	c.Data(resp.StatusCode, "application/json", respBody)
}

// parseDoubaoRequestMeta 从请求体提取 model 和 prompt（任一失败返回默认值）
func parseDoubaoRequestMeta(body []byte) (model, prompt string) {
	import_json_inline := func() {
		// 使用简单 map 解析
	}
	_ = import_json_inline

	var req struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "doubao-unknown", ""
	}
	model = req.Model
	if model == "" {
		model = "doubao-unknown"
	}
	for _, c := range req.Content {
		if c.Type == "text" && c.Text != "" {
			prompt = c.Text
			break
		}
	}
	return
}

// ─── 查询任务结果 ──────────────────────────────────────────────────────────────

// RelayDoubaoVideoResult 查询任务状态，同步更新 DB 并透传上游响应
func RelayDoubaoVideoResult(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := c.Param("taskId")

	videoTask, err := dbmodel.GetVideoTaskById(taskID)
	if err != nil {
		respondError(c, fmt.Errorf("task not found: %s", taskID), "task_not_found", http.StatusNotFound)
		return
	}

	// 已成功且有缓存 URL，直接返回
	if videoTask.Status == "succeed" && videoTask.StoreUrl != "" {
		c.JSON(http.StatusOK, buildDoubaoCachedResponse(videoTask))
		return
	}

	relayMeta := util.GetRelayMeta(c)
	channel, err := dbmodel.GetChannelById(relayMeta.ChannelId, true)
	if err != nil {
		respondError(c, err, "get_channel_failed", http.StatusInternalServerError)
		return
	}

	adaptor := &doubaomodel.VideoAdaptor{}
	meta := buildDoubaoMeta(channel)

	resp, err := adaptor.DoQuery(ctx, meta, taskID)
	if err != nil {
		respondError(c, err, "upstream_request_failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		respondError(c, err, "read_response_failed", http.StatusInternalServerError)
		return
	}

	if queryResp, parseErr := adaptor.ParseQueryResponse(respBody); parseErr == nil {
		updateDoubaoTaskStatus(ctx, taskID, videoTask, queryResp)
	}

	c.Data(resp.StatusCode, "application/json", respBody)
}

func buildDoubaoCachedResponse(v *dbmodel.Video) map[string]interface{} {
	return map[string]interface{}{
		"id":     v.TaskId,
		"status": "succeeded",
		"content": map[string]interface{}{
			"video_url": v.StoreUrl,
		},
	}
}

// ─── 共享：任务状态更新 ────────────────────────────────────────────────────────

// updateDoubaoTaskStatus 处理状态变更并更新 DB（handler 和 poller 共用）
func updateDoubaoTaskStatus(ctx context.Context, taskID string, videoTask *dbmodel.Video, queryResp *doubaomodel.DoubaoVideoResult) {
	dbStatus := mapDoubaoStatus(queryResp.Status)

	if dbStatus == videoTask.Status {
		return
	}

	updates := map[string]interface{}{
		"status":     dbStatus,
		"updated_at": time.Now().Unix(),
	}
	if queryResp.Status == "succeeded" && queryResp.Content != nil && queryResp.Content.VideoURL != "" {
		updates["store_url"] = queryResp.Content.VideoURL
	}
	if dbStatus == "failed" {
		updates["fail_reason"] = buildDoubaoFailMessage(queryResp)
	}

	if err := dbmodel.DB.Model(&dbmodel.Video{}).
		Where("task_id = ?", taskID).
		Updates(updates).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] update task status failed: task_id=%s, %v", taskID, err))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[doubao] status updated: task_id=%s, %s -> %s", taskID, videoTask.Status, dbStatus))

	// 失败时异步退款补偿
	if dbStatus == "failed" && videoTask.Status == "processing" {
		go compensateDoubaoTask(taskID, videoTask)
	}
}

func buildDoubaoFailMessage(resp *doubaomodel.DoubaoVideoResult) string {
	if resp.Error != nil && resp.Error.Message != "" {
		if resp.Error.Code != "" {
			return fmt.Sprintf("[%s] %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Error.Message
	}
	return "task failed"
}

func compensateDoubaoTask(taskID string, v *dbmodel.Video) {
	ctx := context.Background()
	if v.Quota <= 0 {
		return
	}
	if err := dbmodel.IncreaseUserQuota(v.UserId, v.Quota); err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao] compensate quota failed: task_id=%s, user_id=%d, quota=%d, err=%v",
			taskID, v.UserId, v.Quota, err))
		return
	}
	logger.Info(ctx, fmt.Sprintf("[doubao] compensated: task_id=%s, user_id=%d, quota=%d", taskID, v.UserId, v.Quota))
}

// ─── 定时轮询器 ───────────────────────────────────────────────────────────────

// StartDoubaoTaskPoller 启动轮询器，定期扫描 processing 状态的 doubao 任务
func StartDoubaoTaskPoller(ctx context.Context) {
	ticker := time.NewTicker(doubaoPollingIntervalMin)
	defer ticker.Stop()

	logger.Info(ctx, fmt.Sprintf("[doubao-poller] started, interval=%v", doubaoPollingIntervalMin))

	pollDoubaoTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, "[doubao-poller] stopped")
			return
		case <-ticker.C:
			pollDoubaoTasks(ctx)
		}
	}
}

func pollDoubaoTasks(ctx context.Context) {
	var tasks []dbmodel.Video
	if err := dbmodel.DB.Where("provider = ? AND status = ?", doubaoVideoProvider, "processing").
		Find(&tasks).Error; err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] query tasks failed: %v", err))
		return
	}

	if len(tasks) == 0 {
		logger.Info(ctx, "[doubao-poller] no processing tasks found")
		return
	}

	logger.Info(ctx, fmt.Sprintf("[doubao-poller] found %d processing tasks", len(tasks)))

	for _, task := range tasks {
		go pollSingleDoubaoTask(ctx, &task)
	}
}

func pollSingleDoubaoTask(ctx context.Context, task *dbmodel.Video) {
	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] get channel failed: task_id=%s, channel_id=%d, err=%v",
			task.TaskId, task.ChannelId, err))
		return
	}

	adaptor := &doubaomodel.VideoAdaptor{}
	meta := buildDoubaoMeta(channel)

	resp, err := adaptor.DoQuery(ctx, meta, task.TaskId)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] request failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] read response failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	queryResp, err := adaptor.ParseQueryResponse(respBody)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("[doubao-poller] parse response failed: task_id=%s, err=%v", task.TaskId, err))
		return
	}

	logger.Info(ctx, fmt.Sprintf("[doubao-poller] polled: task_id=%s, status=%s", task.TaskId, queryResp.Status))
	updateDoubaoTaskStatus(ctx, task.TaskId, task, queryResp)
}
```

> ⚠️ **注意**：`parseDoubaoRequestMeta` 中有一个 `json.Unmarshal` 调用，需要在 import 中加上 `"encoding/json"`。上面代码中有个错误的 `import_json_inline` 占位，**实际代码中请直接在 import 块加上 `"encoding/json"`，并删除 `import_json_inline` 那几行**。

正确的 import 块：
```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	doubaomodel "github.com/songquanpeng/one-api/relay/channel/doubao"
	"github.com/songquanpeng/one-api/relay/util"
)
```

> ⚠️ **注意**：import 中有 `"strings"` 和 `"github.com/songquanpeng/one-api/common"` 但当前代码没有用到，编译会报错。删除未使用的 import：
> - `"strings"` → 用不到，删除
> - `"github.com/songquanpeng/one-api/common"` → 用不到，删除

**Step 2: 编译验证**

```bash
cd /Users/yueqingli/code/one-api && go build ./controller/...
```

预期：无报错

**Step 3: Commit**

```bash
git add controller/doubao_video.go
git commit -m "feat(doubao): add dedicated video controller with background poller"
```

---

## Task 3: 注册新路由

**Files:**
- Modify: `router/relay-router.go`（在第 202 行 doubaoApiRouter 定义之后新增）

**Step 1: 定位插入位置**

文件 `router/relay-router.go` 第 199-202 行是现有豆包路由：
```go
// 豆包API兼容路由组 - 支持原始豆包API路径格式
doubaoApiRouter := router.Group("/api/v3/contents/generations")
doubaoApiRouter.Use(middleware.TokenAuth()).GET("/tasks/:taskid", controller.RelayDouBaoVideoResultById)
doubaoApiRouter.Use(middleware.TokenAuth(), middleware.Distribute()).POST("/tasks", controller.RelayVideoGenerate)
```

**Step 2: 在第 202 行之后插入新路由**

```go
// 豆包 v2 路由组 - 带 doubao/ 前缀，走独立 controller + 后台轮询器
doubaoV2Router := router.Group("/doubao/api/v3/contents/generations")
doubaoV2Router.Use(middleware.TokenAuth(), middleware.Distribute()).POST("/tasks", controller.RelayDoubaoVideoCreate)
doubaoV2Router.Use(middleware.TokenAuth()).GET("/tasks/:taskId", controller.RelayDoubaoVideoResult)
```

> 注意：POST 路由需要 `Distribute` 中间件（选择渠道），GET 路由不需要。

**Step 3: 编译验证**

```bash
cd /Users/yueqingli/code/one-api && go build ./router/...
```

**Step 4: Commit**

```bash
git add router/relay-router.go
git commit -m "feat(router): add /doubao/api/v3/contents/generations routes"
```

---

## Task 4: 启动轮询器

**Files:**
- Modify: `main.go`（在第 177 行 ali-wan poller 启动之后新增）

**Step 1: 定位插入位置**

`main.go` 第 173-177 行：
```go
// 启动阿里云万相视频任务轮询器
common.SafeGoroutine(func() {
    controller.StartAliWanTaskPoller(context.Background())
})
logger.SysLog("ali-wan video task poller started")
```

**Step 2: 在其之后插入**

```go
// 启动豆包视频任务轮询器
common.SafeGoroutine(func() {
    controller.StartDoubaoTaskPoller(context.Background())
})
logger.SysLog("doubao video task poller started")
```

**Step 3: 编译验证（全量）**

```bash
cd /Users/yueqingli/code/one-api && go build ./... && go vet ./...
```

预期：无报错、无警告

**Step 4: Commit**

```bash
git add main.go
git commit -m "feat(main): start doubao video task poller on startup"
```

---

## Task 5: 全量编译与静态检查

**Step 1: 运行**

```bash
cd /Users/yueqingli/code/one-api && go build ./... && go vet ./...
```

**Step 2: 预期输出**

无任何错误或警告输出，命令退出码为 0。

---

## 实现注意事项

### 关于现有任务与新轮询器的兼容性

- 旧路由（`/api/v3/...`）创建的任务也会存 `provider="doubao"`，新轮询器会一并轮询——这是**预期行为**，可以帮助更新旧任务状态
- 旧任务的配额调整逻辑仍在 `HandleVideoResult`（用户 GET 时触发），不冲突

### 关于 respondError

`controller/doubao_video.go` 调用了 `respondError(c, err, type, code)`，该函数已在 `controller/ali_video.go` 中定义，同 package 可直接使用，无需重新实现。

### 关于 `resolveChannelForTaskQuery`

如需在 `RelayDoubaoVideoResult` 中支持多渠道绑定（同 ali_video.go 的 channel override），可参照 ali_video.go 第 293-299 行加入 `resolveChannelForTaskQuery`。当前方案暂不实现，可后续扩展。
