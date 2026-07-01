# Gemini Omni Flash VideoAdaptor 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将 Gemini Omni Flash (`gemini-omni-flash-preview`) 的视频生成能力通过 Interactions API 接入现有 VideoAdaptor 体系，支持 text-to-video 和 image-to-video。

**Architecture:** 新增 `relay/channel/gemini/video_adaptor.go`，实现 `VideoAdaptor` 接口。提交阶段用 `POST /v1beta/interactions`（带 `background: true`）获取 interaction ID；查询阶段用 `GET /v1beta/interactions/{id}` 轮询状态，完成后提取视频 URL 或 base64 数据。

**Tech Stack:** Go, Gemini Interactions API (v1beta), 现有 VideoAdaptor 接口 + video_helper 工具函数

---

## Task 1: 添加模型名称到 Gemini 模型列表

**Files:**
- Modify: `relay/channel/gemini/constants.go`

**Step 1: 在 ModelList 中添加 Gemini Omni 模型**

在 `ModelList` 数组末尾（Gemini 3 系列之后）添加：

```go
// Gemini Omni 系列（视频生成）
"gemini-omni-flash-preview",
```

**Step 2: 编译验证**

Run: `go build ./...`
Expected: 编译通过

**Step 3: Commit**

```bash
git add relay/channel/gemini/constants.go
git commit -m "feat(gemini): add gemini-omni-flash-preview to model list"
```

---

## Task 2: 添加视频定价规则

**Files:**
- Modify: `common/video-pricing.go`

**Step 1: 添加 Gemini Omni 的定价规则**

在 `videoPricingRules` 切片中添加：

```go
// gemini-omni-flash-preview: $0.025/秒，预估 8 秒 = $0.20
{Model: "gemini-omni-flash-preview", Type: "*", Mode: "*", Sound: "*", Duration: "*", Resolution: "*", PricingType: PricingTypeFixed, Price: 0.20, Currency: "USD", Priority: 10},
```

> 注：Google 尚未公布正式定价，先按预估值 $0.20/次固定收费。后续可按实际出账调整。

**Step 2: 编译验证**

Run: `go build ./...`
Expected: 编译通过

**Step 3: Commit**

```bash
git add common/video-pricing.go
git commit -m "feat(pricing): add gemini-omni-flash-preview video pricing rule"
```

---

## Task 3: 实现 Gemini Omni VideoAdaptor

**Files:**
- Create: `relay/channel/gemini/video_adaptor.go`

**Step 1: 实现完整的 video_adaptor.go**

```go
package gemini

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

// VideoAdaptor 实现 Gemini Omni Flash 通过 Interactions API 生成视频
type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string      { return "gemini-omni" }
func (a *VideoAdaptor) GetChannelName() string       { return "Gemini Omni Flash" }
func (a *VideoAdaptor) GetSupportedModels() []string { return []string{"gemini-omni-flash-preview"} }

// --- Interactions API 请求/响应结构体 ---

type interactionRequest struct {
	Model          string          `json:"model"`
	Input          any             `json:"input"`
	Background     bool            `json:"background"`
	Store          bool            `json:"store"`
	ResponseFormat *interactionRespFormat `json:"response_format,omitempty"`
	GenerationConfig *interactionGenConfig `json:"generation_config,omitempty"`
}

type interactionRespFormat struct {
	Type        string `json:"type,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Delivery    string `json:"delivery,omitempty"`
}

type interactionGenConfig struct {
	VideoConfig *videoConfig `json:"video_config,omitempty"`
}

type videoConfig struct {
	Task string `json:"task,omitempty"`
}

type interactionResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Object string `json:"object"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Steps   []interactionStep   `json:"steps,omitempty"`
	Outputs []interactionOutput `json:"outputs,omitempty"`
}

type interactionStep struct {
	Type    string              `json:"type"`
	Content []interactionOutput `json:"content,omitempty"`
}

type interactionOutput struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// --- HandleVideoRequest: 提交视频生成任务 ---

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	baseURL := meta.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	fullURL := baseURL + "/v1beta/interactions?key=" + meta.ActualAPIKey

	// 构建 input
	var input any
	var task string
	if req.ImageURL != "" {
		// image-to-video
		task = "image_to_video"
		input = []map[string]string{
			{"type": "image", "data": req.ImageURL, "mime_type": "image/jpeg"},
			{"type": "text", "text": req.Prompt},
		}
	} else {
		// text-to-video
		task = "text_to_video"
		input = req.Prompt
	}

	interReq := interactionRequest{
		Model:      "gemini-omni-flash-preview",
		Input:      input,
		Background: true,
		Store:      true,
		ResponseFormat: &interactionRespFormat{
			Type:     "video",
			Delivery: "uri",
		},
		GenerationConfig: &interactionGenConfig{
			VideoConfig: &videoConfig{Task: task},
		},
	}

	resp, respBody, err := relaychannel.SendJSONVideoRequest(fullURL, interReq, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	var interResp interactionResponse
	if err := json.Unmarshal(respBody, &interResp); err != nil {
		log.Printf("[GeminiOmni] Failed to parse response: %s", string(respBody))
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	if resp.StatusCode != http.StatusOK || interResp.Error != nil {
		errMsg := fmt.Sprintf("Interactions API error (HTTP %d)", resp.StatusCode)
		if interResp.Error != nil {
			errMsg = interResp.Error.Message
		}
		return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("%s", errMsg), "api_error", resp.StatusCode)
	}

	if interResp.ID == "" {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("no interaction ID in response"), "invalid_response", http.StatusInternalServerError)
	}

	quota := common.CalculateVideoQuota("gemini-omni-flash-preview", "", "", "8", "", "")

	// 将 API Key 保存到 Credentials 以便查询时使用
	return &relaychannel.VideoTaskResult{
		TaskId:      interResp.ID,
		TaskStatus:  "succeed",
		Credentials: meta.ActualAPIKey,
		Quota:       quota,
	}, nil
}

// --- HandleVideoResult: 轮询视频生成结果 ---

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId

	// 如果已有缓存 URL，直接返回
	if videoTask.StoreUrl != "" {
		return buildCachedResponse(taskId, videoTask), nil
	}

	// 获取 API Key
	apiKey := videoTask.Credentials
	if apiKey == "" {
		apiKey = ch.Key
	}

	baseURL := ch.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	fullURL := fmt.Sprintf("%s/v1beta/interactions/%s?key=%s", baseURL, taskId, apiKey)

	resp, respBody, err := relaychannel.SendVideoResultQuery(fullURL, nil)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("Interactions API returned HTTP %d: %s", resp.StatusCode, string(respBody)),
			"api_error", resp.StatusCode)
	}

	var interResp interactionResponse
	if err := json.Unmarshal(respBody, &interResp); err != nil {
		log.Printf("[GeminiOmni] Failed to parse result response: %s", string(respBody))
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	result := &model.GeneralFinalVideoResponse{
		TaskId:   taskId,
		VideoId:  taskId,
		Duration: videoTask.Duration,
	}

	switch interResp.Status {
	case "completed":
		videoURL := extractVideoFromInteraction(&interResp)
		if videoURL == "" {
			result.TaskStatus = "failed"
			result.Message = "Interaction completed but no video output found"
		} else {
			// 如果是 base64 数据，尝试上传到 R2
			finalURL := videoURL
			if strings.HasPrefix(videoURL, "data:video/") {
				base64Data := extractBase64FromDataURI(videoURL)
				if uploaded, uploadErr := relaychannel.UploadVideoBase64ToR2(base64Data, videoTask.UserId, "mp4"); uploadErr == nil {
					finalURL = uploaded
				}
			}
			result.TaskStatus = "succeed"
			result.Message = "Video generated successfully"
			result.VideoResult = finalURL
			result.VideoResults = []model.VideoResultItem{{Url: finalURL}}
		}
	case "failed":
		result.TaskStatus = "failed"
		result.Message = "Video generation failed"
		if interResp.Error != nil {
			result.Message = interResp.Error.Message
		}
	case "cancelled":
		result.TaskStatus = "failed"
		result.Message = "Video generation was cancelled"
	default: // "in_progress", etc.
		result.TaskStatus = "processing"
		result.Message = "Video is being generated"
	}

	return result, nil
}

// --- 辅助函数 ---

func buildCachedResponse(taskId string, videoTask *dbmodel.Video) *model.GeneralFinalVideoResponse {
	var videoUrls []string
	if err := json.Unmarshal([]byte(videoTask.StoreUrl), &videoUrls); err != nil {
		videoUrls = []string{videoTask.StoreUrl}
	}
	videoResults := make([]model.VideoResultItem, len(videoUrls))
	for i, u := range videoUrls {
		videoResults[i] = model.VideoResultItem{Url: u}
	}
	return &model.GeneralFinalVideoResponse{
		TaskId:       taskId,
		VideoResult:  videoUrls[0],
		VideoId:      taskId,
		TaskStatus:   "succeed",
		Message:      "Video retrieved from cache",
		VideoResults: videoResults,
		Duration:     videoTask.Duration,
	}
}

func extractVideoFromInteraction(resp *interactionResponse) string {
	// 先检查 steps（REST 原始格式）
	for _, step := range resp.Steps {
		if step.Type == "model_output" {
			for _, content := range step.Content {
				if content.Type == "video" {
					if content.URI != "" {
						return content.URI
					}
					if content.Data != "" {
						mime := content.MimeType
						if mime == "" {
							mime = "video/mp4"
						}
						return "data:" + mime + ";base64," + content.Data
					}
				}
			}
		}
	}
	// 再检查 outputs（SDK 便捷格式）
	for _, output := range resp.Outputs {
		if output.Type == "video" {
			if output.URI != "" {
				return output.URI
			}
			if output.Data != "" {
				mime := output.MimeType
				if mime == "" {
					mime = "video/mp4"
				}
				return "data:" + mime + ";base64," + output.Data
			}
		}
	}
	return ""
}

func extractBase64FromDataURI(dataURI string) string {
	idx := strings.Index(dataURI, ";base64,")
	if idx == -1 {
		return dataURI
	}
	return dataURI[idx+8:]
}
```

**Step 2: 编译验证**

Run: `go build ./...`
Expected: 编译通过

**Step 3: Commit**

```bash
git add relay/channel/gemini/video_adaptor.go
git commit -m "feat(gemini): implement Gemini Omni Flash VideoAdaptor via Interactions API"
```

---

## Task 4: 注册适配器到路由分发

**Files:**
- Modify: `relay/helper/main.go`

**Step 1: 在 GetVideoAdaptor 中注册 gemini-omni**

在 `case strings.HasPrefix(modelName, "veo"):` 之前添加：

```go
case strings.HasPrefix(modelName, "gemini-omni"):
    return &gemini.VideoAdaptor{}
```

**Step 2: 在 GetVideoAdaptorByProvider 中注册**

在 `case "vertexai":` 之前添加：

```go
case "gemini-omni":
    return &gemini.VideoAdaptor{}
```

**Step 3: 确认 import 已包含 gemini 包**

`relay/helper/main.go` 的 import 中已有：
```go
"github.com/songquanpeng/one-api/relay/channel/gemini"
```

**Step 4: 编译验证**

Run: `go build ./...`
Expected: 编译通过

**Step 5: Commit**

```bash
git add relay/helper/main.go
git commit -m "feat(gemini): register gemini-omni VideoAdaptor in dispatcher"
```

---

## Task 5: 处理 image_url 中的 base64 和 URL 双模式

**Files:**
- Modify: `relay/channel/gemini/video_adaptor.go`

**Step 1: 完善 HandleVideoRequest 中的图片输入处理**

当前实现将 `req.ImageURL` 直接作为 base64 data 发送，但用户可能传入 HTTP URL。需要判断：
- 若以 `http` 开头 → 需要下载转为 base64
- 若已是 base64 或 data URI → 直接提取

修改 `HandleVideoRequest` 中 image-to-video 分支的 input 构建逻辑：

```go
if req.ImageURL != "" {
    task = "image_to_video"
    imageData := req.ImageURL
    mimeType := "image/jpeg"

    if strings.HasPrefix(req.ImageURL, "data:") {
        // data URI 格式: data:image/png;base64,xxxxx
        parts := strings.SplitN(req.ImageURL, ",", 2)
        if len(parts) == 2 {
            imageData = parts[1]
            // 提取 MIME type
            if metaPart := strings.TrimPrefix(parts[0], "data:"); strings.Contains(metaPart, ";") {
                mimeType = strings.Split(metaPart, ";")[0]
            }
        }
    } else if strings.HasPrefix(req.ImageURL, "http") {
        // URL 格式 - 使用 uri 字段而非 data
        input = []map[string]string{
            {"type": "image", "uri": req.ImageURL, "mime_type": mimeType},
            {"type": "text", "text": req.Prompt},
        }
        // 提前设置 input 并跳过下面的赋值
        goto buildRequest
    }

    input = []map[string]string{
        {"type": "image", "data": imageData, "mime_type": mimeType},
        {"type": "text", "text": req.Prompt},
    }
}
```

> 注：实际实现中不使用 goto，改为 if-else 结构。上面的伪代码展示逻辑意图，实际实现使用清晰的条件分支。

**Step 2: 编译验证**

Run: `go build ./...`
Expected: 编译通过

**Step 3: Commit**

```bash
git add relay/channel/gemini/video_adaptor.go
git commit -m "fix(gemini): handle both URL and base64 image input for Omni"
```

---

## Task 6: 编译 + 静态分析最终验证

**Step 1: 完整编译**

Run: `go build ./...`
Expected: 编译通过，无错误

**Step 2: 静态分析**

Run: `go vet ./...`
Expected: 无警告

**Step 3: 运行测试**

Run: `go test ./relay/... -count=1 -timeout 30s`
Expected: 所有测试通过

---

## 验证方式

1. **编译验证**: `go build ./... && go vet ./...` 通过
2. **功能验证** (手动):
   - `POST /v1/video/generations` 带 `{ "model": "gemini-omni-flash-preview", "prompt": "A cat walking" }` → 返回 `task_id`
   - `GET /v1/video/generations/result?task_id=<id>` → 返回 `processing` 或 `succeed` + 视频 URL
3. **回归验证**: 现有视频适配器（Veo、Minimax 等）的路由不受影响

---

## 影响范围

- 新增文件: `relay/channel/gemini/video_adaptor.go` (1 个)
- 修改文件: `relay/channel/gemini/constants.go`, `relay/helper/main.go`, `common/video-pricing.go` (3 个)
- 不影响现有 Gemini 文本/聊天功能
- 不涉及数据库 schema 变更
- 不影响其他视频适配器的路由
