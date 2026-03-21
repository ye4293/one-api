# Video Adaptor 全量迁移实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将 `relay/controller/video.go` 中剩余 9 个供应商（luma、ali、pixverse、grok、doubao、kling、vertexai/veo、sora）迁移到 `VideoAdaptor` 接口，消除 6000+ 行文件中的重复代码。

**Architecture:** 每个供应商在其 channel 包内实现 `VideoAdaptor` 接口（`HandleVideoRequest` + `HandleVideoResult`），并嵌入 `BaseVideoAdaptor` 获得默认的 `Init`/`GetPrePaymentQuota`。`relay/helper/main.go` 的路由表扩展后，`video.go` 中旧的 `handle*` 函数逐批删除。

**Tech Stack:** Go, gin, `relay/channel/video_helper.go`（已有 `SendJSONVideoRequest`、`SendVideoResultQuery`、`BearerAuthHeaders`），minimax 适配器作为参考实现。

**Reference:** 迁移前务必阅读已完成的参考实现：
- `relay/channel/minimax/videoAdaptor.go` — 完整实现模板
- `relay/channel/interface.go` — VideoAdaptor 接口定义
- `relay/channel/video_helper.go` — 已有的公共 HTTP 工具函数

---

## Task 0: 创建 BaseVideoAdaptor

**Files:**
- Create: `relay/channel/base_video_adaptor.go`

**Step 1: 创建文件**

```go
package channel

import (
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/util"
)

// BaseVideoAdaptor 为所有视频适配器提供默认的 Init 和 GetPrePaymentQuota 实现。
// 供应商通过嵌入此结构体来复用，覆盖有差异的方法。
type BaseVideoAdaptor struct {
	Meta *util.RelayMeta
}

func (b *BaseVideoAdaptor) Init(meta *util.RelayMeta) { b.Meta = meta }

// GetPrePaymentQuota 默认预扣 0.2 美元。预扣费不同的供应商覆盖此方法。
func (b *BaseVideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(0.2 * config.QuotaPerUnit)
}
```

**Step 2: 编译验证**

```bash
cd /Users/yueqingli/code/one-api && go build ./relay/channel/...
```
预期：无报错。

**Step 3: 提交**

```bash
git add relay/channel/base_video_adaptor.go
git commit -m "feat: add BaseVideoAdaptor with default Init and GetPrePaymentQuota"
```

---

## Task 1: 迁移 Luma（最简单，作为模板）

**Files:**
- Create: `relay/channel/luma/video_adaptor.go`
- Modify: `relay/helper/main.go`
- Modify: `relay/controller/video.go`（删除旧函数）

**Step 1: 创建 `relay/channel/luma/video_adaptor.go`**

查看现有 luma 包的类型：`relay/channel/luma/` 下已有 `LumaGenerationRequest`、`LumaGenerationResponse` 结构体。

```go
package luma

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string { return "luma" }
func (a *VideoAdaptor) GetChannelName() string  { return "Luma Dream Machine" }
func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{"luma-dream-machine", "luma-photon", "luma-photon-flash", "luma-ray2-flash", "luma-ray2", "luma-ray2-720"}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	var url string
	// channelType==44 表示直连 Luma，其他是代理
	if meta.ChannelType == 44 {
		url = meta.BaseURL + "/dream-machine/v1/generations"
	} else {
		url = meta.BaseURL + "/luma/dream-machine/v1/generations"
	}

	var lumaReq LumaGenerationRequest
	if err := c.ShouldBindJSON(&lumaReq); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	_, body, httpErr := relaychannel.SendJSONVideoRequest(url, lumaReq, relaychannel.BearerAuthHeaders(ch.Key))
	if httpErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}

	var lumaResp LumaGenerationResponse
	if err := json.Unmarshal(body, &lumaResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	if lumaResp.StatusCode != 201 {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("luma API error: status=%d body=%s", lumaResp.StatusCode, string(body)),
			"api_error", lumaResp.StatusCode)
	}

	// 计算配额
	defaultPrice, ok := common.DefaultModelPrice["luma"]
	if !ok {
		defaultPrice = 0.1
	}
	quota := int64(defaultPrice * config.QuotaPerUnit)

	taskStatus := "succeed"
	if lumaResp.State == "failed" {
		taskStatus = "failed"
	}

	return &relaychannel.VideoTaskResult{
		TaskId:     lumaResp.ID,
		TaskStatus: taskStatus,
		Quota:      quota,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	var url string
	if ch.Type != 44 {
		url = fmt.Sprintf("%s/dream-machine/v1/generations/%s", *ch.BaseURL, taskId)
	} else {
		url = fmt.Sprintf("%s/luma/dream-machine/v1/generations/%s", *ch.BaseURL, taskId)
	}

	_, body, err := relaychannel.SendVideoResultQuery(url, relaychannel.BearerAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var lumaResp LumaGenerationResponse
	if parseErr := json.Unmarshal(body, &lumaResp); parseErr != nil {
		log.Printf("Failed to parse luma response: %v, body: %s", parseErr, string(body))
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:     taskId,
		TaskStatus: mapLumaStatus(lumaResp.State),
		Duration:   videoTask.Duration,
	}

	if lumaResp.State == "completed" && lumaResp.Assets != nil {
		if assets, ok := lumaResp.Assets.(map[string]interface{}); ok {
			if videoURL, ok := assets["video"].(string); ok {
				generalResponse.VideoResult = videoURL
				generalResponse.VideoResults = []model.VideoResultItem{{Url: videoURL}}
			}
		}
	}

	if lumaResp.State == "failed" && lumaResp.FailureReason != nil {
		generalResponse.Message = *lumaResp.FailureReason
	}

	return generalResponse, nil
}

func mapLumaStatus(state string) string {
	switch state {
	case "completed":
		return "succeed"
	case "failed":
		return "failed"
	default:
		return "processing"
	}
}
```

**Step 2: 注册到 `relay/helper/main.go`**

在 `GetVideoAdaptor` 的 switch 中添加：
```go
case strings.HasPrefix(strings.ToLower(modelName), "luma"):
    return &luma.VideoAdaptor{}
```

在 `GetVideoAdaptorByProvider` 中添加：
```go
case "luma":
    return &luma.VideoAdaptor{}
```

同时在 import 中添加：`"github.com/songquanpeng/one-api/relay/channel/luma"`

**Step 3: 编译验证**

```bash
cd /Users/yueqingli/code/one-api && go build ./...
```
预期：无报错。

**Step 4: 删除 `video.go` 中的旧函数**

删除以下函数（按行号，删除前先 `go build` 确认无依赖）：
- `handleLumaVideoRequest`（2114 行）
- `sendRequestAndHandleLumaResponse`（2136 行）
- `handleLumaVideoResponse`（3224 行）
- `mapTaskStatusLuma`（3695 行）

**Step 5: 再次编译验证**

```bash
go build ./...
```

**Step 6: 提交**

```bash
git add relay/channel/luma/video_adaptor.go relay/helper/main.go relay/controller/video.go
git commit -m "feat: migrate Luma to VideoAdaptor interface"
```

---

## Task 2: 迁移 Ali（阿里云万相）

**Files:**
- Create: `relay/channel/ali/video_adaptor.go`
- Modify: `relay/helper/main.go`
- Modify: `relay/controller/video.go`

**Step 1: 创建 `relay/channel/ali/video_adaptor.go`**

关键点：
- 请求透传（不需要类型转换），直接把请求体 forward 给阿里云
- 需要设置 `X-DashScope-Async: enable` 请求头
- 分辨率解析逻辑（`parseAliVideoResolution`）移入此文件
- 配额使用 `common.CalculateVideoQuota(modelName, videoType, "*", duration, resolution)` 而非固定值
- `GetPrePaymentQuota()` 需要覆盖，因为 Ali 按实际计算而不是固定 0.2

```go
package ali

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string { return "ali" }
func (a *VideoAdaptor) GetChannelName() string  { return "阿里云万相" }
func (a *VideoAdaptor) GetSupportedModels() []string {
	return []string{"wan-x1", "wan-x1-14b"}
}

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	// 读取并解析请求体，提取 duration 和 resolution 用于计费
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_request_body_failed", http.StatusBadRequest)
	}

	var requestData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "parse_request_body_failed", http.StatusBadRequest)
	}

	duration := "5"
	resolution := "1080P"
	modelName := meta.ActualModelName
	if m, ok := requestData["model"].(string); ok && m != "" {
		modelName = m
	}

	if parameters, ok := requestData["parameters"].(map[string]interface{}); ok {
		if dv, exists := parameters["duration"]; exists {
			switch v := dv.(type) {
			case float64:
				duration = strconv.Itoa(int(v))
			case string:
				duration = v
			}
		}
		if sv, exists := parameters["size"].(string); exists {
			resolution = parseAliVideoResolution(sv)
		}
		if rv, exists := parameters["resolution"].(string); exists {
			if rv == "480P" || rv == "720P" || rv == "1080P" {
				resolution = rv
			}
		}
	}

	videoType := "image-to-video"
	if strings.Contains(strings.ToLower(modelName), "t2v") {
		videoType = "text-to-video"
	}
	quota := common.CalculateVideoQuota(modelName, videoType, "*", duration, resolution)

	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	url := fmt.Sprintf("%s/api/v1/services/aigc/video-generation/video-synthesis", meta.BaseURL)
	headers := map[string]string{
		"Authorization":    "Bearer " + ch.Key,
		"X-DashScope-Async": "enable",
	}
	relaychannel.ApplyHeadersOverride2(headers, meta)

	httpResp, respBody, httpErr := relaychannel.SendJSONVideoRequestRaw(url, bodyBytes, headers)
	if httpErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(httpErr, "request_error", http.StatusInternalServerError)
	}

	var aliResp AliVideoResponse
	if parseErr := json.Unmarshal(respBody, &aliResp); parseErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "parse_ali_video_response_failed", http.StatusInternalServerError)
	}

	if aliResp.Output.TaskStatus == "Failed" || (httpResp.StatusCode != 200 && aliResp.Output.TaskID == "") {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("ali API error: %s", aliResp.Message),
			"api_error", httpResp.StatusCode)
	}

	log.Printf("ali-video-duration: %s, resolution: %s, model: %s, quota: %d", duration, resolution, modelName, quota)

	return &relaychannel.VideoTaskResult{
		TaskId:     aliResp.Output.TaskID,
		TaskStatus: "succeed",
		Duration:   duration,
		Resolution: resolution,
		Quota:      quota,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId
	url := fmt.Sprintf("%s/api/v1/tasks/%s", *ch.BaseURL, taskId)

	_, body, err := relaychannel.SendVideoResultQuery(url, relaychannel.BearerAuthHeaders(ch.Key))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var aliResp AliVideoTaskResult
	if parseErr := json.Unmarshal(body, &aliResp); parseErr != nil {
		return nil, openaiAdaptor.ErrorWrapper(parseErr, "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		Duration: videoTask.Duration,
	}

	switch aliResp.Output.TaskStatus {
	case "SUCCEEDED":
		generalResponse.TaskStatus = "succeed"
		if len(aliResp.Output.VideoURL) > 0 {
			generalResponse.VideoResult = aliResp.Output.VideoURL[0]
			var items []model.VideoResultItem
			for _, u := range aliResp.Output.VideoURL {
				items = append(items, model.VideoResultItem{Url: u})
			}
			generalResponse.VideoResults = items
		}
	case "FAILED":
		generalResponse.TaskStatus = "failed"
		generalResponse.Message = aliResp.Message
	default:
		generalResponse.TaskStatus = "processing"
	}

	return generalResponse, nil
}

// parseAliVideoResolution 根据 size 字符串返回分辨率档位
func parseAliVideoResolution(size string) string {
	// （从 video.go 中的 parseAliVideoResolution 直接复制）
	sizes1080P := map[string]bool{
		"1920*1080": true, "1080*1920": true, "1440*1440": true,
		"1632*1248": true, "1248*1632": true,
	}
	sizes720P := map[string]bool{
		"1280*720": true, "720*1280": true, "960*960": true,
		"1088*832": true, "832*1088": true,
	}
	sizes480P := map[string]bool{
		"832*480": true, "480*832": true, "624*624": true,
	}
	normalized := strings.ReplaceAll(size, "x", "*")
	if sizes1080P[normalized] { return "1080P" }
	if sizes720P[normalized]  { return "720P" }
	if sizes480P[normalized]  { return "480P" }
	parts := strings.Split(normalized, "*")
	if len(parts) == 2 {
		w, e1 := strconv.Atoi(parts[0])
		h, e2 := strconv.Atoi(parts[1])
		if e1 == nil && e2 == nil {
			px := w * h
			if px >= 1500000 { return "1080P" }
			if px >= 600000  { return "720P" }
			return "480P"
		}
	}
	return "1080P"
}
```

> **注意：** 需要在 `relay/channel/video_helper.go` 中添加 `SendJSONVideoRequestRaw(url string, rawBody []byte, headers map[string]string)` 函数（接受 `[]byte` 而不是 `any`，避免重复序列化），并添加 `ApplyHeadersOverride2` 或复用现有的 `ApplyHeadersOverride` 函数（参考其实现）。同时需要确认 `ali` 包中是否已有 `AliVideoResponse`、`AliVideoTaskResult` 类型，如无需新增。

**Step 2: 注册到 `relay/helper/main.go`**

```go
// GetVideoAdaptor 中添加：
case strings.HasPrefix(strings.ToLower(modelName), "wan"):
    return &ali.VideoAdaptor{}

// GetVideoAdaptorByProvider 中添加：
case "ali":
    return &ali.VideoAdaptor{}
```

**Step 3: 编译并删除旧函数**

旧函数列表（从 `video.go` 删除）：
- `parseAliVideoResolution`（484 行）
- `handleAliVideoRequest`（541 行）
- `sendRequestAndHandleAliVideoResponse`（613 行）
- `handleAliVideoResponse`（1254 行）

```bash
go build ./... && echo "OK"
```

**Step 4: 提交**

```bash
git add relay/channel/ali/video_adaptor.go relay/helper/main.go relay/controller/video.go
git commit -m "feat: migrate Ali Wanxiang to VideoAdaptor interface"
```

---

## Task 3: 迁移 Pixverse

**Files:**
- Create: `relay/channel/pixverse/video_adaptor.go`
- Modify: `relay/helper/main.go`, `relay/controller/video.go`

**Step 1: 关键逻辑点**

Pixverse 的特殊之处：
- 有图片时，先调用 `/openapi/v2/image/upload`（multipart POST），返回 `img_id`，再调用 `/openapi/v2/video/img/generate`
- 无图片时，调用 `/openapi/v2/video/text/generate`
- 认证头是 `API-KEY`（非 Bearer）
- 结果查询头也是 `API-KEY` + `Ai-trace-id`

```go
package pixverse

import (
	// ...
)

type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string { return "pixverse" }
func (a *VideoAdaptor) GetChannelName() string  { return "Pixverse" }
func (a *VideoAdaptor) GetSupportedModels() []string { return []string{"v3.5"} }

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 解析请求决定是图生视频还是文生视频
	// （逻辑直接搬运自 handlePixverseVideoRequest，约 200 行）
	// 核心差异：图片上传用 multipart，最终构建 jsonData 和 fullRequestUrl
	// 然后调用内部 sendPixverseRequest(ch, fullRequestUrl, jsonData, meta)

	// ... （搬运现有逻辑）

	quota := int64(0.2 * config.QuotaPerUnit) // 保持与旧代码一致

	return &relaychannel.VideoTaskResult{
		TaskId:     strconv.Itoa(resp.Resp.Id),
		TaskStatus: "succeed",
		VideoId:    strconv.Itoa(resp.Resp.Id),
		Quota:      quota,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	url := fmt.Sprintf("%s/openapi/v2/video/result/%s", *ch.BaseURL, videoTask.TaskId)
	headers := map[string]string{
		"API-KEY":    ch.Key,
		"Ai-trace-id": "aaaaa",
	}
	// GET 请求查询结果，解析 PixverseFinalResponse
	// 逻辑直接搬运自 GetVideoResult 中的 pixverse 分支
}
```

> **完整实现**：将 `handlePixverseVideoRequest`（1790-1996 行）和 `GetVideoResult` 中 pixverse 分支（4444-4518 行）的逻辑逐行搬入对应方法。

**Step 2: 注册到 `relay/helper/main.go`**

```go
// GetVideoAdaptor
case modelName == "v3.5":
    return &pixverse.VideoAdaptor{}

// GetVideoAdaptorByProvider
case "pixverse":
    return &pixverse.VideoAdaptor{}
```

**Step 3: 删除旧函数**

- `handlePixverseVideoRequest`（1790 行）
- `sendRequestAndHandlePixverseResponse`（1998 行）
- `handlePixverseVideoResponse`（2060 行）

---

## Task 4: 迁移 Grok

**Files:**
- Create: `relay/channel/xai/video_adaptor.go`
- Modify: `relay/helper/main.go`, `relay/controller/video.go`

**Step 1: 关键逻辑点**

- URL 按 `video` 字段决定：有 `video` 走 `/v1/videos/edits`，否则走 `/v1/videos/generations`
- 认证使用 `meta.APIKey`（而非 `channel.Key`，因支持多 Key 选择）
- 创建任务成功后用 `dbmodel.UpdateVideoCredentials(taskId, meta.APIKey)` 保存 Key
- 结果查询时从 `videoTask.Credentials` 读回保存的 Key
- 配额计算：`calculateGrokVideoQuota(duration, resolution, hasVideoInput, hasImageInput)` → 移入 adaptor

```go
// computeQuota 封装 calculateGrokVideoQuota 逻辑
func (a *VideoAdaptor) computeQuota(duration int, resolution string, hasVideoInput, hasImageInput bool) int64 {
	outputPrice := 0.05
	if resolution == "720p" {
		outputPrice = 0.07
	}
	total := float64(duration) * outputPrice
	if hasVideoInput {
		total += float64(duration) * 0.01
	} else if hasImageInput {
		total += 0.002
	}
	return int64(total * config.QuotaPerUnit)
}
```

**Step 2: 注册**

```go
// GetVideoAdaptor
case strings.HasPrefix(modelName, "grok-imagine-video"):
    return &xai.VideoAdaptor{}

// GetVideoAdaptorByProvider
case "grok":
    return &xai.VideoAdaptor{}
```

**Step 3: 删除旧函数**

- `handleGrokVideoRequest`（2196 行）
- `sendRequestGrokAndHandleResponse`（2260 行）
- `handleGrokVideoResponse`（2324 行）
- `calculateGrokVideoQuota`（3299 行）

---

## Task 5: 迁移 Doubao

**Files:**
- Create: `relay/channel/doubao/video_adaptor.go`
- Modify: `relay/helper/main.go`, `relay/controller/video.go`

**Step 1: 关键逻辑点**

Doubao 最特殊的地方是 **CNY 计费**：
- 预扣费：将 1.4 元人民币转美元（`convertCNYToUSD`）
- 结果查询时补差价：基于实际 token 数重新计算（`calculateQuotaForDoubao`）

汇率相关代码（`ExchangeRateManager`、`fetchRateFromExchangeRateAPI`、`fetchRateFromFixer`、`convertCNYToUSD`、`refreshExchangeRate`）全部**移入** `relay/channel/doubao/exchange_rate.go`，供 adaptor 调用。

```go
// video_adaptor.go 中
func (a *VideoAdaptor) GetPrePaymentQuota() int64 {
	// 覆盖默认值：预扣 1.4 CNY ≈ 0.2 USD
	prePayUSD, err := convertCNYToUSD(1.4)
	if err != nil {
		prePayUSD = 1.4 / 7.2 // fallback
	}
	return int64(prePayUSD * config.QuotaPerUnit)
}

func (a *VideoAdaptor) HandleVideoResult(...) {
	// 正常查询结果后，若 status==succeeded，根据 token 数补差价
	// 逻辑搬运自 GetVideoResult 的 doubao 分支（4519-4631 行）
}
```

**Step 2: 创建 `relay/channel/doubao/exchange_rate.go`**

将 `video.go` 中以下内容搬入（约 120 行）：
- `ExchangeRateManager` 结构体及 `var globalExchangeRateManager`
- `ExchangeRateResponse` 结构体
- `getCNYToUSDRate`、`fetchRateFromExchangeRateAPI`、`fetchRateFromFixer`、`convertCNYToUSD`、`refreshExchangeRate`

**Step 3: 注册**

```go
case strings.HasPrefix(strings.ToLower(modelName), "doubao"):
    return &doubao.VideoAdaptor{}

case "doubao":
    return &doubao.VideoAdaptor{}
```

**Step 4: 删除旧函数**

- `handleDoubaoVideoRequest`（1341 行）
- `sendRequestDoubaoAndHandleResponse`（1391 行）
- `handleDoubaoVideoResponse`（1451 行）
- `calculateQuotaForDoubao`（5418 行）
- `ExchangeRateManager` 及相关（5718-5840 行）

---

## Task 6: 迁移 Kling（最复杂的 JWT 认证）

**Files:**
- Create: `relay/channel/keling/video_adaptor.go`
- Modify: `relay/helper/main.go`, `relay/controller/video.go`

**Step 1: 关键逻辑点**

- JWT 认证：channel.Type==41 时读 AK/SK 生成 JWT，否则直接用 `meta.APIKey`
- `EncodeJWTToken(ak, sk string)` 函数移入 `relay/channel/keling/` 包（已在 keling 包内或移入）
- 5 种视频类型，URL 按类型+channelType 决定（routeMap）
- 特殊类型 `kling-identify-face`、`kling-advanced-lip-sync` 直接调用现有的 `DoIdentifyFace`、`DoAdvancedLipSync`（这两个保留在 `video.go`，不删）
- 配额计算：`calculateQuota` 中的 kling 部分（mode×duration 矩阵）移入 adaptor

```go
func (a *VideoAdaptor) computeQuota(modelName, mode, duration string) int64 {
	defaultPrice, ok := common.DefaultModelPrice[modelName]
	if !ok {
		defaultPrice = 0.1
	}
	quota := int64(defaultPrice * config.QuotaPerUnit)

	multiplier := 1.0
	if modelName == "kling-v1" {
		switch {
		case mode == "std" && duration == "5":  multiplier = 1
		case mode == "std" && duration == "10": multiplier = 2
		case mode == "pro" && duration == "5":  multiplier = 3.5
		case mode == "pro" && duration == "10": multiplier = 7
		}
	} else if modelName == "kling-v1-5" || modelName == "kling-v1-6" {
		switch {
		case mode == "std" && duration == "5":  multiplier = 1
		case mode == "std" && duration == "10": multiplier = 2
		case mode == "pro" && duration == "5":  multiplier = 1.75
		case mode == "pro" && duration == "10": multiplier = 3.5
		}
	}
	return int64(float64(quota) * multiplier)
}
```

**Step 2: 注册**

```go
case strings.HasPrefix(strings.ToLower(modelName), "kling"):
    return &keling.VideoAdaptor{}

case "kling":
    return &keling.VideoAdaptor{}
```

**Step 3: 删除旧函数**

- `handleKelingVideoRequest`（2509 行）
- `sendRequestKelingAndHandleResponse`（2779 行）
- `handleKelingVideoResponse`（3081 行）
- `EncodeJWTToken`（2904 行，如已移入 keling 包）

---

## Task 7: 迁移 Veo（VertexAI）

**Files:**
- Create: `relay/channel/vertexai/video_adaptor.go`
- Modify: `relay/helper/main.go`, `relay/controller/video.go`

**Step 1: 关键逻辑点**

- **请求**：OAuth2 Bearer token（调用 `vertexai.GetAccessToken`），POST 到 `predictLongRunning` 端点
- **预扣费**：`6.0 * config.QuotaPerUnit`（须覆盖 `GetPrePaymentQuota`）
- **任务 ID**：提取 `operationName` 的最后一段
- **凭证保存**：`VideoTaskResult.Credentials` 存 JSON 序列化的 credentials，供结果查询时使用
- **结果查询**：POST 到 `fetchPredictOperation`，请求体 `{"operationName": "..."}` 需完整路径
- **视频提取**：`extractVeoVideoURIs` 从响应 map 中提取 GCS URI，用 `processVideosConcurrently` 并发上传 R2

```go
func (a *VideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(6.0 * config.QuotaPerUnit) // Veo3 单次约 $6
}
```

**Step 2: 注册**

```go
case strings.HasPrefix(strings.ToLower(modelName), "veo"):
    return &vertexai.VideoAdaptor{}

case "vertexai":
    return &vertexai.VideoAdaptor{}
```

**Step 3: 删除旧函数**

- `handleVeoVideoRequest`（1505 行）
- `sendRequestAndHandleVeoResponse`（1580 行）
- `handleVeoVideoResponse`（1648 行）
- `extractVeoVideoURI`（5895 行）
- `convertGCStoHTTPS`（5904 行）
- `extractVeoVideoURIs`（5916 行）
- `processVideosConcurrently`（6005 行）
- `printJSONStructure`（5841 行，仅 vertexai 使用）

---

## Task 8: 迁移 Sora（最复杂）

**Files:**
- Create: `relay/channel/openai/sora_video_adaptor.go`
- Modify: `relay/helper/main.go`, `relay/controller/video.go`

**Step 1: 关键逻辑点**

Sora 有两种完全不同的子流程：
1. **普通 Sora**：接受 JSON 或 multipart form-data（`handleSoraVideoRequestJSON` / `handleSoraVideoRequestFormData`）
2. **Sora Remix**：需要查数据库找原任务的 channel，模型名含 `remix`

```go
type SoraVideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *SoraVideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	modelName := meta.OriginModelName

	// Remix 模式
	if strings.Contains(modelName, "remix") {
		return a.handleRemix(c, meta)
	}

	// 普通 Sora：JSON 或 form-data
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		return a.handleFormData(c, meta)
	}
	return a.handleJSON(c, meta)
}
```

Remix 中查找原 channel 的逻辑（`handleSoraRemixRequest` 250-483 行）需完整搬运。

`downloadAndUploadSoraVideo`（5322-5417 行）移入本文件。

配额计算 `calculateSoraQuota`（686 行）移入 adaptor。

Azure 渠道结果查询头使用 `Api-key`（非 `Authorization: Bearer`）。

**Step 2: 注册**

```go
case strings.Contains(strings.ToLower(modelName), "remix"),
     strings.HasPrefix(strings.ToLower(modelName), "sora"):
    return &openai.SoraVideoAdaptor{}

case "sora":
    return &openai.SoraVideoAdaptor{}
```

**Step 3: 删除旧函数**

- `handleSoraVideoRequest`（180 行）
- `handleSoraVideoRequestFormData`（194 行）
- `handleSoraVideoRequestJSON`（224 行）
- `handleSoraRemixRequest`（250 行）
- `handleSoraRemixResponse`（372 行）
- `calculateSoraQuota`（686 行）
- `sendRequestAndHandleSoraVideoResponseFormData`（719 行）
- `sendRequestAndHandleSoraVideoResponseJSON`（821 行）
- `handleInputReference`（922 行）
- `handleInputReferenceURL`（937 行）
- `handleInputReferenceDataURL`（1028 行）
- `handleInputReferenceBase64`（1084 行）
- `detectImageFilename`（1125 行）
- `handleSoraVideoResponse`（1144 行）
- `downloadAndUploadSoraVideo`（5322 行）

---

## Task 9: 最终清理

**Files:**
- Modify: `relay/controller/video.go`（彻底清理）
- Modify: `relay/controller/video.go`（删除 `calculateQuota`）

**Step 1: 删除 `calculateQuota`（3325 行）**

此函数在迁移所有供应商后应无调用方。验证：
```bash
grep -n "calculateQuota\b" relay/controller/video.go
```
预期：0 个匹配（若 minimax 旧逻辑分支已迁移，此函数孤立可删）。

**Step 2: 删除 `getStatusMessage`（2921 行）**

验证是否有调用方：
```bash
grep -rn "getStatusMessage" relay/
```
若无调用则删除。

**Step 3: 验证 `DoVideoRequest` 的最终形态**

```go
func DoVideoRequest(c *gin.Context, modelName string) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	var videoRequest model.VideoRequest
	err := common.UnmarshalBodyReusable(c, &videoRequest)
	meta := util.GetRelayMeta(c)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}

	if adaptor := relayhelper.GetVideoAdaptor(modelName); adaptor != nil {
		return invokeVideoAdaptorRequest(c, ctx, adaptor, &videoRequest, meta)
	}

	return openai.ErrorWrapper(fmt.Errorf("unsupported model"), "unsupported_model", http.StatusBadRequest)
}
```

**Step 4: 验证 `GetVideoResult` 的最终形态**

`GetVideoResult` 中的 `switch videoTask.Provider` 分支应全部删除，只剩：
```go
if adaptor := relayhelper.GetVideoAdaptorByProvider(videoTask.Provider); adaptor != nil {
    return invokeVideoAdaptorResult(c, adaptor, videoTask, channel, &cfg)
}
return openai.ErrorWrapper(fmt.Errorf("unsupported provider: %s", videoTask.Provider), "unsupported_provider", http.StatusBadRequest)
```

**Step 5: 最终编译**

```bash
go build ./... && echo "ALL OK"
```

**Step 6: 统计最终行数**

```bash
wc -l relay/controller/video.go
```
预期：< 500 行（原 6070 行）。

**Step 7: 提交**

```bash
git add relay/controller/video.go relay/helper/main.go
git commit -m "refactor: complete VideoAdaptor migration, video.go shrinks from 6070 to ~400 lines"
```

---

## 验证清单

每个 Task 完成后必须通过：
- [ ] `go build ./...` 无错
- [ ] 新适配器已注册到 `GetVideoAdaptor` 和 `GetVideoAdaptorByProvider`
- [ ] 旧 `handle*` 函数已从 `video.go` 删除
- [ ] 对应的 `GetVideoResult` switch 分支已删除

最终清理后额外验证：
- [ ] `calculateQuota` 无残留调用
- [ ] `EncodeJWTToken` 已移入 keling 包（或在 `video.go` 中仅被 keling 调用）
- [ ] `ExchangeRateManager` 等已移入 doubao 包
- [ ] `DoVideoRequest` 仅含适配器路由和不支持模型的兜底
- [ ] `GetVideoResult` 仅含适配器路由和不支持供应商的兜底
