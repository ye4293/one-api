package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/keling"
	"github.com/songquanpeng/one-api/relay/channel/luma"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/pixverse"
	"github.com/songquanpeng/one-api/relay/channel/runway"
	"github.com/songquanpeng/one-api/relay/channel/viggle"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func DoVideoRequest(c *gin.Context, modelName string) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	var videoRequest model.VideoRequest
	err := common.UnmarshalBodyReusable(c, &videoRequest)
	meta := util.GetRelayMeta(c)
	if err != nil {
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}

	if modelName == "video-01" ||
		modelName == "video-01-live2d" ||
		modelName == "S2V-01" ||
		modelName == "T2V-01" ||
		modelName == "I2V-01" ||
		modelName == "T2V-01-Director" ||
		modelName == "I2V-01-Director" ||
		modelName == "I2V-01-live" {
		return handleMinimaxVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "cogvideox" {
		return handleZhipuVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "kling") {
		return handleKelingVideoRequest(c, ctx, meta)
	} else if modelName == "gen3a_turbo" {
		return handleRunwayVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "luma") {
		return handleLumaVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "viggle" {
		return handleViggleVideoRequest(c, ctx, videoRequest, meta)
	} else if modelName == "v3.5" {
		return handlePixverseVideoRequest(c, ctx, videoRequest, meta)
	} else if strings.HasPrefix(modelName, "Doubao") {
		return handleDoubaoVideoRequest(c, ctx, videoRequest, meta)
	} else {
		return openai.ErrorWrapper(fmt.Errorf("Unsupported model"), "unsupported_model", http.StatusBadRequest)
	}
}

func handleDoubaoVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	var fullRequestUrl string

}

func handlePixverseVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	var fullRequestUrl string
	var jsonData []byte

	// 1. 读取原始请求体
	jsonData, err = io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		return openai.ErrorWrapper(err, "read_request_error", http.StatusBadRequest)
	}
	// 重新设置请求体
	c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))

	var imageCheck struct {
		Image      string      `json:"image"`
		Duration   interface{} `json:"duration"`
		Quality    string      `json:"quality"`
		MotionMode string      `json:"motion_mode"`
	}

	if err := common.UnmarshalBodyReusable(c, &imageCheck); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	// Convert duration to int
	var duration int
	switch v := imageCheck.Duration.(type) {
	case float64:
		duration = int(v)
	case string:
		var err error
		duration, err = strconv.Atoi(v)
		if err != nil {
			return openai.ErrorWrapper(err, "invalid_duration_format", http.StatusBadRequest)
		}
	case int:
		duration = v
	default:
		return openai.ErrorWrapper(fmt.Errorf("unsupported duration type"), "invalid_duration_type", http.StatusBadRequest)
	}

	c.Set("Duration", duration)
	c.Set("Quality", imageCheck.Quality)
	c.Set("MotionMode", imageCheck.MotionMode)

	if imageCheck.Image != "" {
		// 1. 先上传图片
		uploadUrl := meta.BaseURL + "/openapi/v2/image/upload"

		// 创建multipart表单
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// 创建文件表单字段
		part, err := writer.CreateFormFile("image", "image.png")
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_create_form", http.StatusInternalServerError)
		}

		// 检查是否为base64格式
		isBase64 := strings.HasPrefix(imageCheck.Image, "data:")

		if isBase64 {
			// 处理base64格式
			// 移除 "data:image/jpeg;base64," 这样的前缀
			base64Data := imageCheck.Image
			if i := strings.Index(base64Data, ","); i != -1 {
				base64Data = base64Data[i+1:]
			}

			// 解码base64数据
			imgData, err := base64.StdEncoding.DecodeString(base64Data)
			if err != nil {
				return openai.ErrorWrapper(err, "invalid_base64_image", http.StatusBadRequest)
			}

			// 写入图片数据
			if _, err = part.Write(imgData); err != nil {
				return openai.ErrorWrapper(err, "failed_to_write_image", http.StatusInternalServerError)
			}
		} else {
			// 处理URL格式
			// 检查是否是有效的URL
			if !strings.HasPrefix(imageCheck.Image, "http://") && !strings.HasPrefix(imageCheck.Image, "https://") {
				return openai.ErrorWrapper(fmt.Errorf("invalid URL format"), "invalid_url", http.StatusBadRequest)
			}

			resp, err := http.Get(imageCheck.Image)
			if err != nil {
				return openai.ErrorWrapper(err, "failed_to_download_image", http.StatusBadRequest)
			}
			defer resp.Body.Close()

			// 复制图片数据到表单
			if _, err = io.Copy(part, resp.Body); err != nil {
				return openai.ErrorWrapper(err, "failed_to_copy_image", http.StatusInternalServerError)
			}
		}

		writer.Close()

		// 创建上传请求
		uploadReq, err := http.NewRequest("POST", uploadUrl, body)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_create_request", http.StatusInternalServerError)
		}

		log.Printf("key:%s", channel.Key)
		// 设置请求头
		uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
		uploadReq.Header.Set("API-KEY", channel.Key)
		uploadReq.Header.Set("AI-trace-id", helper.GetUUID())

		// 发送请求
		client := &http.Client{}
		uploadResp, err := client.Do(uploadReq)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_upload_image", http.StatusInternalServerError)
		}
		defer uploadResp.Body.Close()

		// 解析响应
		var uploadResponse pixverse.UploadImageResponse
		if err := json.NewDecoder(uploadResp.Body).Decode(&uploadResponse); err != nil {
			return openai.ErrorWrapper(err, "failed_to_parse_upload_response", http.StatusInternalServerError)
		}

		// 检查上传是否成功
		if uploadResponse.ErrCode != 0 {
			return openai.ErrorWrapper(
				fmt.Errorf("image upload failed: %s", uploadResponse.ErrMsg),
				"image_upload_failed",
				http.StatusBadRequest,
			)
		}

		// 2. 使用返回的图片ID构建视频生成请求
		fullRequestUrl = meta.BaseURL + "/openapi/v2/video/img/generate"

		// 将原始请求体中的img_url替换为img_id
		var originalBody pixverse.PixverseRequest2
		if err := common.UnmarshalBodyReusable(c, &originalBody); err != nil {
			return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}

		// Convert duration to int in originalBody
		switch v := originalBody.Duration.(type) {
		case float64:
			originalBody.Duration = int(v)
		case string:
			duration, err := strconv.Atoi(v)
			if err != nil {
				return openai.ErrorWrapper(err, "invalid_duration_format", http.StatusBadRequest)
			}
			originalBody.Duration = duration
		case int:
			// already in correct format
		default:
			return openai.ErrorWrapper(fmt.Errorf("unsupported duration type"), "invalid_duration_type", http.StatusBadRequest)
		}

		originalBody.ImgId = uploadResponse.Resp.ImgId
		originalBody.Image = ""

		// 将修改后的请求体重新设置到context中，同时更新jsonData
		jsonData, err = json.Marshal(originalBody)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
	} else {
		// 处理 PixverseRequest1 的情况
		var textRequest pixverse.PixverseRequest1
		if err := common.UnmarshalBodyReusable(c, &textRequest); err != nil {
			return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}
		// Convert duration to int in textRequest
		switch v := textRequest.Duration.(type) {
		case float64:
			textRequest.Duration = int(v)
		case string:
			duration, err := strconv.Atoi(v)
			if err != nil {
				return openai.ErrorWrapper(err, "invalid_duration_format", http.StatusBadRequest)
			}
			textRequest.Duration = duration
		case int:
			// already in correct format
		default:
			return openai.ErrorWrapper(fmt.Errorf("unsupported duration type"), "invalid_duration_type", http.StatusBadRequest)
		}

		jsonData, err = json.Marshal(textRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))

		fullRequestUrl = meta.BaseURL + "/openapi/v2/video/text/generate"
	}
	return sendRequestAndHandlePixverseResponse(c, ctx, fullRequestUrl, jsonData, meta, "pixverse")
}

func sendRequestAndHandlePixverseResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, s string) *model.ErrorWithStatusCode {
	// // 添加请求体日志
	// log.Printf("Request URL: %s", fullRequestUrl)
	// log.Printf("Request Body: %s", string(jsonData))

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		log.Printf("Get channel error: %v", err)
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Create request error: %v", err)
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 添加请求头日志
	req.Header.Set("Ai-trace-id", helper.GetUUID())
	req.Header.Set("API-KEY", channel.Key)
	req.Header.Set("Content-Type", "application/json") // 添加 Content-Type 头
	// log.Printf("Request Headers: %v", req.Header)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Request error: %v", err)
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Read response error: %v", err)
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// // 添加响应日志
	// log.Printf("Response Status: %d", resp.StatusCode)
	// log.Printf("Response Body: %s", string(body))

	var PixverseFinalResp pixverse.PixverseVideoResponse
	err = json.Unmarshal(body, &PixverseFinalResp)
	if err != nil {
		log.Printf("Response parse error: %v", err)
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	PixverseFinalResp.StatusCode = resp.StatusCode
	return handlePixverseVideoResponse(c, ctx, PixverseFinalResp, body, meta, "")
}

func handlePixverseVideoResponse(c *gin.Context, ctx context.Context, videoResponse pixverse.PixverseVideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	duration := c.GetInt("Duration")
	quality := c.GetString("Quality")
	motionMode := c.GetString("MotionMode")
	if videoResponse.ErrCode == 0 && videoResponse.StatusCode == 200 {
		// 先计算quota
		quota := calculateQuota(meta, "v3.5", "", strconv.Itoa(duration), c)

		err := CreateVideoLog("pixverse", strconv.Itoa(videoResponse.Resp.VideoId), meta,
			quality,
			strconv.Itoa(duration),
			motionMode,
			"",
			quota,
		)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     strconv.Itoa(videoResponse.Resp.VideoId),
			Message:    videoResponse.ErrMsg,
			TaskStatus: "succeed",
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, "v3.5", "", "", quota)

	} else {
		return openai.ErrorWrapper(
			fmt.Errorf("error: %s", videoResponse.ErrMsg),
			"internal_error",
			http.StatusInternalServerError,
		)
	}
}

func handleViggleVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 使用map定义URL映射关系
	urlMap := map[string]string{
		"mix":   "/api/video/gen",
		"multi": "/api/video/gen/multi",
		"move":  "/api/video/gen/move",
	}

	// 获取type参数，默认为"mix"
	typeValue := c.DefaultPostForm("type", "mix")

	// 获取对应的URL路径
	path, exists := urlMap[typeValue]
	if !exists {
		return openai.ErrorWrapper(errors.New("invalid type"), "invalid_type", http.StatusBadRequest)
	}

	fullRequestUrl := meta.BaseURL + path

	// 直接转发原始请求
	return sendRequestAndHandleViggleResponse(c, ctx, fullRequestUrl, meta, "viggle")
}

func sendRequestAndHandleViggleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, meta *util.RelayMeta, s string) *model.ErrorWithStatusCode {

	// 先读取请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_request_error", http.StatusInternalServerError)
	}

	// 打印完整请求体
	// log.Printf("Original request body: %s", string(bodyBytes))

	// 重新设置请求体，因为读取后需要重置
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 创建新请求时使用保存的请求体
	req, err := http.NewRequest(c.Request.Method, fullRequestUrl, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// // 打印请求的详细信息
	// log.Printf("Request Method: %s", req.Method)
	// log.Printf("Request URL: %s", fullRequestUrl)
	// log.Printf("Request Headers: %+v", req.Header)

	// 复制原始请求头
	req.Header.Set("Access-Token", meta.APIKey)
	// 确保设置正确的 Content-Type
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	log.Printf("Raw response body: %s", string(respBody))

	// 解析响应
	var viggleResponse viggle.ViggleResponse
	if err := json.Unmarshal(respBody, &viggleResponse); err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	viggleResponse.StatusCode = resp.StatusCode
	return handleViggleVideoResponse(c, ctx, viggleResponse, respBody, meta, "")
}

func handleViggleVideoResponse(c *gin.Context, ctx context.Context, viggleResponse viggle.ViggleResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	if viggleResponse.Code == 0 && viggleResponse.Message == "成功" {
		// 先计算quota
		quota := calculateQuota(meta, "viggle", "", strconv.Itoa(viggleResponse.Data.SubtractScore), c)

		err := CreateVideoLog("viggle", viggleResponse.Data.TaskID, meta, "", strconv.Itoa(viggleResponse.Data.SubtractScore), "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     viggleResponse.Data.TaskID,
			Message:    viggleResponse.Message,
			TaskStatus: "succeed",
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		if viggleResponse.Data.SubtractScore == 2 {
			return handleSuccessfulResponseWithQuota(c, ctx, meta, "viggle", "", "2", quota)
		} else {
			return handleSuccessfulResponseWithQuota(c, ctx, meta, "viggle", "", "1", quota)
		}

	} else {
		return openai.ErrorWrapper(
			fmt.Errorf("error: %s", viggleResponse.Message),
			"internal_error",
			http.StatusInternalServerError,
		)
	}
}

func handleLumaVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	var fullRequestUrl string
	if meta.ChannelType == 44 {
		fullRequestUrl = baseUrl + "/dream-machine/v1/generations"
	} else {
		fullRequestUrl = baseUrl + "/luma/dream-machine/v1/generations"
	}

	var lumaVideoRequest luma.LumaGenerationRequest
	if err := common.UnmarshalBodyReusable(c, &lumaVideoRequest); err != nil {
		return openai.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	jsonData, err := json.Marshal(lumaVideoRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestAndHandleLumaResponse(c, ctx, fullRequestUrl, jsonData, meta, "luma")
}

func sendRequestAndHandleLumaResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, s string) *model.ErrorWithStatusCode {
	// 1. 获取频道信息
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	// 2. 创建请求
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	// 3. 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	// 4. 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 5. 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 6. 解析响应
	var lumaResponse luma.LumaGenerationResponse
	err = json.Unmarshal(body, &lumaResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	// 7. 设置状态码
	lumaResponse.StatusCode = resp.StatusCode

	// 8. 处理响
	result := handleLumaVideoResponse(c, ctx, lumaResponse, body, meta, "")

	return result
}

func handleMinimaxVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 验证必填参数
	if videoRequest.Prompt == "" {
		return openai.ErrorWrapper(
			fmt.Errorf("prompt is required"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}

	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/v1/video_generation"

	// 直接绑定请求体到 VideoRequestMinimax 结构体
	var videoRequestMinimax model.VideoRequestMinimax
	if err := c.ShouldBindJSON(&videoRequestMinimax); err != nil {
		return openai.ErrorWrapper(err, "invalid_request_error", http.StatusBadRequest)
	}

	// 如果存在 image 参数，将其值赋给 FirstFrameImage 并清空 image
	if videoRequestMinimax.Image != "" {
		videoRequestMinimax.FirstFrameImage = videoRequestMinimax.Image
		videoRequestMinimax.Image = ""
	}

	jsonData, err := json.Marshal(videoRequestMinimax)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestMinimaxAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, videoRequest.Model)
}

func handleZhipuVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	fullRequestUrl := baseUrl + "/api/paas/v4/videos/generations"

	videoRequestZhipu := model.VideoRequestZhipu{
		Model:    videoRequest.Model,
		Prompt:   videoRequest.Prompt,
		ImageURL: videoRequest.ImageURL,
	}

	jsonData, err := json.Marshal(videoRequestZhipu)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}

	return sendRequestZhipuAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, "cogvideox")
}

func handleKelingVideoRequest(c *gin.Context, ctx context.Context, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	// 构建基础URL和路由映射
	baseUrl := meta.BaseURL
	routeMap := map[string]map[int]string{
		"kling-lip": {
			41: "/v1/videos/lip-sync",
			0:  "/kling/v1/videos/lip2video",
		},
		"text-to-video": {
			41: "/v1/videos/text2video",
			0:  "/kling/v1/videos/text2video",
		},
		"image-to-video": {
			41: "/v1/videos/image2video",
			0:  "/kling/v1/videos/image2video",
		},
		"multi-image-to-video": {
			41: "/v1/videos/multi-image2video",
			0:  "/kling/v1/videos/multi-image2video",
		},
	}

	// 确定请求类型和URL
	var requestType string
	var requestBody interface{}
	var videoType string
	var videoId string
	var mode string
	var duration string

	if meta.OriginModelName == "kling-lip" {
		requestType = "kling-lip"
		videoType = "kling-lip"
		var lipRequest keling.KlingLipRequest
		if err := common.UnmarshalBodyReusable(c, &lipRequest); err != nil {
			return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		}
		requestBody = lipRequest
		videoId = lipRequest.Input.VideoId
	} else {
		// 检查是否为多图生视频请求或单图生视频请求
		var imageCheck struct {
			Image     string      `json:"image,omitempty"`
			Mode      string      `json:"mode,omitempty"`
			Duration  interface{} `json:"duration,omitempty"`
			ImageTail string      `json:"image_tail,omitempty"`
			ImageList []struct {
				Image string `json:"image"`
			} `json:"image_list,omitempty"`
		}
		if err := common.UnmarshalBodyReusable(c, &imageCheck); err != nil {
			return openai.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}

		// 只有当请求体中包含这些字段时才设置它们
		if imageCheck.Mode != "" {
			mode = imageCheck.Mode
		}

		if imageCheck.Duration != nil {
			switch v := imageCheck.Duration.(type) {
			case float64:
				duration = strconv.Itoa(int(v))
			case string:
				duration = v
			}
		}

		// 检查是否为多图生视频请求
		if len(imageCheck.ImageList) > 0 {
			requestType = "multi-image-to-video"
			videoType = "multi-image-to-video"
			var multiImageToVideoReq keling.MultiImageToVideoRequest
			if err := common.UnmarshalBodyReusable(c, &multiImageToVideoReq); err != nil {
				return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
			}

			// 只有当有值时才设置这些字段
			if mode != "" {
				multiImageToVideoReq.Mode = mode
			}
			if duration != "" {
				multiImageToVideoReq.Duration = duration
			}

			// 如果 Model 有值，将其赋给 ModelName
			if multiImageToVideoReq.Model != "" {
				multiImageToVideoReq.ModelName = multiImageToVideoReq.Model
				multiImageToVideoReq.Model = "" // 清除 Model 字段
			}

			requestBody = multiImageToVideoReq
		} else if imageCheck.Image != "" || imageCheck.ImageTail != "" {
			requestType = "image-to-video"
			videoType = "image-to-video"
			var imageToVideoReq keling.ImageToVideoRequest
			if err := common.UnmarshalBodyReusable(c, &imageToVideoReq); err != nil {
				return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
			}

			// 只有当有值时才设置这些字段
			if mode != "" {
				imageToVideoReq.Mode = mode
			}
			if duration != "" {
				imageToVideoReq.Duration = duration
			}

			// 如果 Model 有值，将其赋给 ModelNames
			if imageToVideoReq.Model != "" {
				imageToVideoReq.ModelName = imageToVideoReq.Model
				imageToVideoReq.Model = "" // 清除 Model 字段
			}

			requestBody = imageToVideoReq
		} else {
			requestType = "text-to-video"
			videoType = "text-to-video"
			var textToVideoReq keling.TextToVideoRequest
			if err := common.UnmarshalBodyReusable(c, &textToVideoReq); err != nil {
				return openai.ErrorWrapper(err, "invalid_request", http.StatusBadRequest)
			}

			// 只有当有值时才设置这些字段
			if mode != "" {
				textToVideoReq.Mode = mode
			}
			if duration != "" {
				textToVideoReq.Duration = duration
			}

			// 如果 Model 有值，将其赋给 ModelName
			if textToVideoReq.Model != "" {
				textToVideoReq.ModelName = textToVideoReq.Model
				textToVideoReq.Model = "" // 清除 Model 字段
			}

			requestBody = textToVideoReq
		}
	}

	// 构建完整URL
	channelType := meta.ChannelType
	if channelType != 41 {
		channelType = 0
	}
	fullRequestUrl := baseUrl + routeMap[requestType][channelType]

	// 序列化请求体
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "json_marshal_error", http.StatusInternalServerError)
	}
	// log.Printf("Request body JSON: %s", string(jsonData))

	return sendRequestKelingAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, meta.OriginModelName, mode, duration, videoType, videoId)
}

func handleRunwayVideoRequest(c *gin.Context, ctx context.Context, videoRequest model.VideoRequest, meta *util.RelayMeta) *model.ErrorWithStatusCode {
	baseUrl := meta.BaseURL
	var fullRequestUrl string
	if meta.ChannelType == 42 {
		fullRequestUrl = baseUrl + "/v1/image_to_video"
	} else {
		fullRequestUrl = baseUrl + "/runwayml/v1/image_to_video"
	}

	// 解析请求体
	var runwayRequest runway.VideoGenerationRequest
	if err := common.UnmarshalBodyReusable(c, &runwayRequest); err != nil {
		return openai.ErrorWrapper(err, "invalid_video_generation_request", http.StatusBadRequest)
	}

	// 设置默认时长
	if runwayRequest.Duration == 0 {
		runwayRequest.Duration = 10
	}

	// 设置 duration 到上下文
	c.Set("duration", strconv.Itoa(runwayRequest.Duration))

	// 序列化请求
	jsonData, err := json.Marshal(runwayRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "failed to marshal request body", http.StatusInternalServerError)
	}

	return sendRequestRunwayAndHandleResponse(c, ctx, fullRequestUrl, jsonData, meta, "gen3a_turbo")
}

func sendRequestMinimaxAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse model.VideoResponse
	err = json.Unmarshal(body, &videoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	videoResponse.StatusCode = resp.StatusCode
	return handleMinimaxVideoResponse(c, ctx, videoResponse, body, meta, modelName)

}

func sendRequestZhipuAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse model.VideoResponse
	err = json.Unmarshal(body, &videoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	videoResponse.StatusCode = resp.StatusCode
	return handleMZhipuVideoResponse(c, ctx, videoResponse, body, meta, modelName)

}

func sendRequestKelingAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string, mode string, duration string, videoType string, videoId string) *model.ErrorWithStatusCode {
	// log.Printf("Request body JSON: %s", string(jsonData))
	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	var token string

	if meta.OriginModelName == "kling-lip" {
		video, err := dbmodel.GetVideoTaskByVideoId(videoId)
		if err != nil {
			return openai.ErrorWrapper(err, "get_video_task_error", http.StatusInternalServerError)
		}
		meta.ChannelId = video.ChannelId
		channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
		if err != nil {
			return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
		}
		meta.ChannelType = channel.Type
	}

	if meta.ChannelType == 41 {
		ak := meta.Config.AK
		sk := meta.Config.SK

		// Add logging for AK and SK
		log.Printf("AK: %s", ak)
		log.Printf("SK: %s", sk)

		// Generate JWT token
		token = encodeJWTToken(ak, sk)

		// Add logging for generated token
		log.Printf("Generated JWT token: %s", token)
	} else {
		token = meta.APIKey

		// Add logging for API key token
		log.Printf("Using API key as token: %s", token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	// 添加原始响应日志
	log.Printf("Raw response body: %s", string(body))

	var KelingvideoResponse keling.KelingVideoResponse
	err = json.Unmarshal(body, &KelingvideoResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	KelingvideoResponse.StatusCode = resp.StatusCode
	return handleKelingVideoResponse(c, ctx, KelingvideoResponse, body, meta, modelName, mode, duration, videoType)
}

func sendRequestRunwayAndHandleResponse(c *gin.Context, ctx context.Context, fullRequestUrl string, jsonData []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {

	channel, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return openai.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	req, err := http.NewRequest("POST", fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Runway-Version", "2024-11-06")
	req.Header.Set("authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var videoResponse runway.VideoResponse
	err = json.Unmarshal(body, &videoResponse)
	if err != nil {
		log.Printf("Unmarshal error: %v", err)
		return openai.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}

	videoResponse.StatusCode = resp.StatusCode
	return handleRunwayVideoResponse(c, ctx, videoResponse, body, meta, modelName)
}

func encodeJWTToken(ak, sk string) string {
	claims := jwt.MapClaims{
		"iss": ak,
		"exp": time.Now().Add(30 * time.Minute).Unix(),
		"nbf": time.Now().Add(-5 * time.Second).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(sk))
	if err != nil {
		// Handle error (you might want to return an error instead of panicking in production)
		panic(err)
	}

	return tokenString
}

func getStatusMessage(statusCode int) string {
	switch statusCode {
	case 0:
		return "请求成功"
	case 1002:
		return "触发限流，请稍后再试"
	case 1004:
		return "账号鉴权失败，请检查 API-Key 是否填写正确"
	case 1008:
		return "账号余额不足"
	case 1013:
		return "传入参数异常，请检查入参是否按要求填写"
	case 1026:
		return "视频描述涉及敏感内容"
	default:
		return fmt.Sprintf("未知错误码: %d", statusCode)
	}
}

func handleMinimaxVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.BaseResp.StatusCode {
	case 0:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		err := CreateVideoLog("minimax", videoResponse.TaskID, meta, "", "", "", "", quota)
		if err != nil {

		}
		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  videoResponse.TaskID,
			Message: videoResponse.BaseResp.StatusMsg,
		}

		switch videoResponse.BaseResp.StatusCode {
		case 0:
			generalResponse.TaskStatus = "succeed"
		default:
			generalResponse.TaskStatus = "failed"
		}
		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)
	case 1002, 1008:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusTooManyRequests,
		)
	case 1004:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusForbidden,
		)
	case 1013, 1026:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusBadRequest,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.BaseResp.StatusMsg),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

func handleMZhipuVideoResponse(c *gin.Context, ctx context.Context, videoResponse model.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.StatusCode {
	case 200:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		err := CreateVideoLog("zhipu", videoResponse.ID, meta, "", "", "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  videoResponse.ID,
			Message: "",
		}

		// 修改 TaskStatus 处理逻辑
		switch videoResponse.TaskStatus {
		case "FAIL":
			generalResponse.TaskStatus = "failed"
		default:
			generalResponse.TaskStatus = "succeed"
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.ZhipuError.Message),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.ZhipuError.Message),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

func handleKelingVideoResponse(c *gin.Context, ctx context.Context, videoResponse keling.KelingVideoResponse, body []byte, meta *util.RelayMeta, modelName string, mode string, duration string, videoType string) *model.ErrorWithStatusCode {
	modelName2 := c.GetString("original_model")
	switch videoResponse.StatusCode {
	case 200:
		// 首先打印完整的响应内容以便调试
		log.Printf("Video Response: %+v", videoResponse)

		// 先计算quota
		quota := calculateQuota(meta, modelName2, mode, duration, c)

		// 现在可以安全地访问这些字段
		err := CreateVideoLog(
			"kling",
			videoResponse.Data.TaskID,
			meta,
			mode,
			duration,
			videoType,
			"",
			quota,
		)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  videoResponse.Data.TaskID,
			Message: videoResponse.Message,
		}

		switch videoResponse.Data.TaskStatus {
		case "failed":
			generalResponse.TaskStatus = "failed"
		default:
			generalResponse.TaskStatus = "succeed"
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName2, mode, duration, quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (400): %s\nFull response: %s", videoResponse.Message, string(body)),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (429): %s\nFull response: %s", videoResponse.Message, string(body)),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		// 对于未知错误，我们需要更详细的信息
		errorMessage := fmt.Sprintf("Unknown API error (Status Code: %d): %s\nFull response: %s",
			videoResponse.StatusCode,
			videoResponse.Message,
			string(body))

		log.Printf("Error occurred: %s", errorMessage)

		return openai.ErrorWrapper(
			fmt.Errorf(errorMessage),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

func handleRunwayVideoResponse(c *gin.Context, ctx context.Context, videoResponse runway.VideoResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch videoResponse.StatusCode {
	case 200:
		// 先计算quota
		quota := calculateQuota(meta, modelName, "", "", c)

		err := CreateVideoLog("runway", videoResponse.Id, meta, "", "", "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err.Error()),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:     videoResponse.Id,
			Message:    "",
			TaskStatus: "succeed",
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return handleSuccessfulResponseWithQuota(c, ctx, meta, modelName, "", "", quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.Error),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", videoResponse.Error),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("Unknown API error: %s", videoResponse.Error),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

// Add this function definition to resolve the error
func handleLumaVideoResponse(c *gin.Context, ctx context.Context, lumaResponse luma.LumaGenerationResponse, body []byte, meta *util.RelayMeta, modelName string) *model.ErrorWithStatusCode {
	switch lumaResponse.StatusCode {
	case 201:
		// 先计算quota
		quota := calculateQuota(meta, "luma", "", "", c)

		err := CreateVideoLog("luma", lumaResponse.ID, meta, "", "", "", "", quota)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("API error: %s", err),
				"api_error",
				http.StatusBadRequest,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralVideoResponse{
			TaskId:  lumaResponse.ID,
			Message: "",
		}

		switch lumaResponse.State {
		case "failed":
			generalResponse.TaskStatus = "failed"
		default:
			generalResponse.TaskStatus = "succeed"
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return handleSuccessfulResponseWithQuota(c, ctx, meta, "luma", "", "", quota)
	case 400:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (400): %s\nFull response: %s", *lumaResponse.FailureReason, string(body)),
			"api_error",
			http.StatusBadRequest,
		)
	case 429:
		return openai.ErrorWrapper(
			fmt.Errorf("API error (429): %s\nFull response: %s", *lumaResponse.FailureReason, string(body)),
			"api_error",
			http.StatusTooManyRequests,
		)
	default:
		// 对于未知错误，我们需要更详细的信息
		errorMessage := fmt.Sprintf("Unknown API error (Status Code: %d): %s\nFull response: %s",
			lumaResponse.StatusCode,
			*lumaResponse.FailureReason,
			string(body))

		log.Printf("Error occurred: %s", errorMessage)

		return openai.ErrorWrapper(
			fmt.Errorf(errorMessage),
			"api_error",
			http.StatusInternalServerError,
		)
	}
}

// 新增计算quota的函数
func calculateQuota(meta *util.RelayMeta, modelName string, mode string, duration string, c *gin.Context) int64 {
	var modelPrice float64
	defaultPrice, ok := common.DefaultModelPrice[modelName]
	if !ok {
		modelPrice = 0.1
	} else {
		modelPrice = defaultPrice
	}
	quota := int64(modelPrice * config.QuotaPerUnit)

	// 特殊处理 kling-v1 模型
	if modelName == "kling-v1" {
		var multiplier float64
		switch {
		case mode == "std" && duration == "5":
			multiplier = 1
		case mode == "std" && duration == "10":
			multiplier = 2
		case mode == "pro" && duration == "5":
			multiplier = 3.5
		case mode == "pro" && duration == "10":
			multiplier = 7
		default:
			multiplier = 1
		}
		quota = int64(float64(quota) * multiplier)
	}
	if modelName == "kling-v1-5" || modelName == "kling-v1-6" {
		var multiplier float64
		switch {
		case mode == "std" && duration == "5":
			multiplier = 1
		case mode == "std" && duration == "10":
			multiplier = 2
		case mode == "pro" && duration == "5":
			multiplier = 1.75
		case mode == "pro" && duration == "10":
			multiplier = 3.5
		default:
			multiplier = 1
		}
		quota = int64(float64(quota) * multiplier)
	}

	if modelName == "viggle" && duration == "2" {
		quota = quota * 2
	}

	value, exists := c.Get("duration")
	if exists {
		runwayDuration := value.(string)
		if runwayDuration == "10" {
			quota = quota * 2
		}
	}

	if modelName == "v3.5" {
		durationInt := c.GetInt("Duration")
		modeStr := c.GetString("Mode")
		motionMode := c.GetString("MotionMode")
		var multiplier float64
		switch {
		case modeStr == "Turbo" && durationInt == 5 && motionMode == "Normal":
			multiplier = 1
		case modeStr == "Turbo" && durationInt == 5 && motionMode == "Performance":
			multiplier = 2
		case modeStr == "Turbo" && durationInt == 8 && motionMode == "Normal":
			multiplier = 2
		case modeStr == "540P" && durationInt == 5 && motionMode == "Normal":
			multiplier = 1
		case modeStr == "540P" && durationInt == 5 && motionMode == "Performance":
			multiplier = 2
		case modeStr == "540P" && durationInt == 8 && motionMode == "Normal":
			multiplier = 2
		case modeStr == "720P" && durationInt == 5 && motionMode == "Normal":
			multiplier = 1.33
		case modeStr == "720P" && durationInt == 5 && motionMode == "Performance":
			multiplier = 2.67
		case modeStr == "720P" && durationInt == 8 && motionMode == "Normal":
			multiplier = 2.67
		case modeStr == "1080P" && durationInt == 5 && motionMode == "Normal":
			multiplier = 2.67
		default:
			multiplier = 1
		}
		quota = int64(float64(45) * multiplier)
	}

	return quota
}

// 新增带quota参数的成功响应处理函数
func handleSuccessfulResponseWithQuota(c *gin.Context, ctx context.Context, meta *util.RelayMeta, modelName string, mode string, duration string, quota int64) *model.ErrorWithStatusCode {
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	err := dbmodel.PostConsumeTokenQuota(meta.TokenId, quota)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
	}

	err = dbmodel.CacheUpdateUserQuota(ctx, meta.UserId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
	}

	if quota != 0 {
		var modelPrice float64
		defaultPrice, ok := common.DefaultModelPrice[modelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}

		tokenName := c.GetString("token_name")
		logContent := fmt.Sprintf("模型固定价格 %.2f$", modelPrice)
		dbmodel.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, modelName, tokenName, quota, logContent, 0, title, referer)
		dbmodel.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		channelId := c.GetInt("channel_id")
		dbmodel.UpdateChannelUsedQuota(channelId, quota)
	}

	return nil
}

func CreateVideoLog(provider string, taskId string, meta *util.RelayMeta, mode string, duration string, videoType string, videoId string, quota int64) error {
	// 创建新的 Video 实例
	video := &dbmodel.Video{
		Prompt:    "prompt",
		CreatedAt: time.Now().Unix(), // 使用当前时间戳
		TaskId:    taskId,
		Provider:  provider,
		Username:  dbmodel.GetUsernameById(meta.UserId),
		ChannelId: meta.ChannelId,
		UserId:    meta.UserId,
		Mode:      mode, //keling
		Type:      videoType,
		Model:     meta.OriginModelName,
		Duration:  duration,
		VideoId:   videoId,
		Quota:     quota,
	}

	// 调用 Insert 方法插入记录
	err := video.Insert()
	if err != nil {
		return fmt.Errorf("failed to insert video log: %v", err)
	}

	return nil
}

func mapTaskStatus(status string) string {
	switch status {
	case "PROCESSING":
		return "processing"
	case "SUCCESS":
		return "succeed"
	case "FAIL":
		return "failed"
	default:
		return "unknown"
	}
}

func mapTaskStatusMinimax(status string) string {
	switch status {
	case "Processing":
		return "processing"
	case "Success":
		return "succeed"
	case "Fail":
		return "failed"
	default:
		return "unknown"
	}
}

func mapTaskStatusLuma(status string) string {
	switch status {
	case "completed":
		return "scucceed"
	case "dreaming":
		return "processing"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

func mapTaskStatusRunway(status string) string {
	switch status {
	case "PENDING":
		return "processing"
	case "SUCCEEDED":
		return "succeed"
	default:
		return "unknown"
	}
}

func GetVideoResult(c *gin.Context, taskId string) *model.ErrorWithStatusCode {
	videoTask, err := dbmodel.GetVideoTaskById(taskId)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get video: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}

	channel, err := dbmodel.GetChannelById(videoTask.ChannelId, true)
	logger.SysLog(fmt.Sprintf("channelId2:%d", channel.Id))
	cfg, _ := channel.LoadConfig()
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get channel: %v", err),
			"database_error",
			http.StatusInternalServerError,
		)
	}
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to get videoTask: %v", err),
			"database_error",
			http.StatusBadRequest,
		)
	}

	var fullRequestUrl string
	switch videoTask.Provider {
	case "zhipu":
		fullRequestUrl = fmt.Sprintf("https://open.bigmodel.cn/api/paas/v4/async-result/%s", taskId)
	case "minimax":
		fullRequestUrl = fmt.Sprintf("%s/v1/query/video_generation?task_id=%s", *channel.BaseURL, taskId)
	case "kling":
		if videoTask.Type == "text-to-video" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/text2video/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/text2video/%s", *channel.BaseURL, taskId)
			}
		} else if videoTask.Type == "image-to-video" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/image2video/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/image2video/%s", *channel.BaseURL, taskId)
			}
		} else if videoTask.Type == "kling-lip" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/lip-sync/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/lip2video/%s", *channel.BaseURL, taskId)
			}
		} else if videoTask.Type == "multi-image-to-video" {
			if channel.Type == 41 {
				fullRequestUrl = fmt.Sprintf("%s/v1/videos/multi-image2video/%s", *channel.BaseURL, taskId)
			} else {
				fullRequestUrl = fmt.Sprintf("%s/kling/v1/videos/multi-image2video/%s", *channel.BaseURL, taskId)
			}
		}
	case "runway":
		if channel.Type != 42 {
			fullRequestUrl = fmt.Sprintf("%s/runwayml/v1/tasks/%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/v1/tasks/%s", *channel.BaseURL, taskId)
		}

	case "luma":
		if channel.Type != 44 {
			fullRequestUrl = fmt.Sprintf("%s/dream-machine/v1/generations/%s", *channel.BaseURL, taskId)
		} else {
			fullRequestUrl = fmt.Sprintf("%s/luma/dream-machine/v1/generations/%s", *channel.BaseURL, taskId)
		}

	case "viggle":
		fullRequestUrl = fmt.Sprintf("%s/api/video/task?task_id=%s", *channel.BaseURL, taskId)
	case "pixverse":
		fullRequestUrl = fmt.Sprintf("%s/openapi/v2/video/result/%s", *channel.BaseURL, taskId)
	default:
		return openai.ErrorWrapper(
			fmt.Errorf("unsupported model type:"),
			"invalid_request_error",
			http.StatusBadRequest,
		)
	}
	// 创建新的请求
	req, err := http.NewRequest("GET", fullRequestUrl, nil)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to create request: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}
	if videoTask.Provider == "kling" && channel.Type == 41 {
		token := encodeJWTToken(cfg.AK, cfg.SK)

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

	} else if videoTask.Provider == "runway" && channel.Type == 42 {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Runway-Version", "2024-11-06")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	} else if videoTask.Provider == "viggle" {
		req.Header.Set("Access-Token", channel.Key)
	} else if videoTask.Provider == "pixverse" {
		req.Header.Set("API-KEY", channel.Key)
		req.Header.Set("Ai-trace-id", "aaaaa")
	} else {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}

	// 发送 HTTP 请求获取结果
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(
			fmt.Errorf("failed to fetch video result: %v", err),
			"api_error",
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return openai.ErrorWrapper(
			fmt.Errorf("API error: %s", string(body)),
			"api_error",
			resp.StatusCode,
		)
	}

	if videoTask.Provider == "zhipu" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		defer resp.Body.Close()

		// 解析JSON响应
		var zhipuResp model.FinalVideoResponse
		if err := json.Unmarshal(body, &zhipuResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  mapTaskStatus(zhipuResp.TaskStatus), // 使用 mapTaskStatus 函数
			Message:     "",
			VideoResult: "",
		}

		// 如果任务成功且有视频结果，添加到响应中
		if zhipuResp.TaskStatus == "SUCCESS" && len(zhipuResp.VideoResults) > 0 {
			generalResponse.VideoResult = zhipuResp.VideoResults[0].URL
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "minimax" {
		err := handleMinimaxResponse(c, channel, taskId)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
	} else if videoTask.Provider == "kling" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		defer resp.Body.Close()

		// 打印完整响应体
		log.Printf("Kling response body: %s", string(body))

		// 解析JSON响应
		var klingResp keling.KelingVideoResponse
		if err := json.Unmarshal(body, &klingResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      klingResp.Data.TaskID,
			Message:     klingResp.Data.TaskStatusMsg,
			VideoResult: "",
			Duration:    "",
		}

		// 检查是否有视频结果
		if len(klingResp.Data.TaskResult.Videos) > 0 {
			generalResponse.VideoId = klingResp.Data.TaskResult.Videos[0].ID
			generalResponse.Duration = klingResp.Data.TaskResult.Videos[0].Duration
		}

		// 处理任务状态
		switch klingResp.Data.TaskStatus {
		case "submitted":
			generalResponse.TaskStatus = "processing"
		default:
			generalResponse.TaskStatus = klingResp.Data.TaskStatus
		}

		// 如果任务成功且有视频结果，添加到响应中
		if klingResp.Data.TaskStatus == "succeed" && len(klingResp.Data.TaskResult.Videos) > 0 {
			generalResponse.VideoResult = klingResp.Data.TaskResult.Videos[0].URL
			generalResponse.Duration = klingResp.Data.TaskResult.Videos[0].Duration
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)

		return nil
	} else if videoTask.Provider == "runway" {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var runwayResp runway.VideoFinalResponse
		if err := json.Unmarshal(body, &runwayResp); err != nil {
			log.Printf("Failed to parse response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  mapTaskStatusRunway(runwayResp.Status),
			Message:     "", // 添加错误信息
			VideoResult: "",
		}

		// 如果任务成功且有视频结果，添加到响应中
		if runwayResp.Status == "SUCCEEDED" && len(runwayResp.Output) > 0 {
			generalResponse.VideoResult = runwayResp.Output[0]
		} else {
			log.Printf("Task not succeeded or no output. Status: %s, Output length: %d",
				runwayResp.Status, len(runwayResp.Output))
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "luma" {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var lumaResp luma.LumaGenerationResponse
		if err := json.Unmarshal(body, &lumaResp); err != nil {
			log.Printf("Failed to parse response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  mapTaskStatusLuma(lumaResp.State),
			Message:     "", // 添加错误信息
			VideoResult: "",
		}

		// 如果任务成功且有视频结果，添加到响应中
		if lumaResp.State == "completed" && lumaResp.Assets != nil {
			// 将 interface{} 转换为 map[string]interface{}
			if assets, ok := lumaResp.Assets.(map[string]interface{}); ok {
				// 获取 video URL
				if videoURL, ok := assets["video"].(string); ok {
					generalResponse.VideoResult = videoURL
				} else {
					log.Printf("Video URL not found or invalid type in assets")
				}
			} else {
				log.Printf("Failed to convert assets to map")
			}
		} else {
			log.Printf("Task not completed or no assets. State: %s, Assets: %v",
				lumaResp.State, lumaResp.Assets)
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "viggle" {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 解析JSON响应
		var viggleResp viggle.ViggleFinalResponse
		if err := json.Unmarshal(body, &viggleResp); err != nil {
			log.Printf("Failed to parse response: %v, body: %s", err, string(body))
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建 GeneralVideoResponse 结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      taskId,
			TaskStatus:  "",
			Message:     viggleResp.Message, // 添加错误信息
			VideoResult: "",
		}

		// 首先检查 Data 切片是否为空
		if len(viggleResp.Data.Data) == 0 {
			generalResponse.TaskStatus = "failed"
		} else {
			// 处理不同状态的情况
			if viggleResp.Data.Code == 0 {
				if viggleResp.Data.Data[0].Result == "" {
					generalResponse.TaskStatus = "processing"
				} else {
					generalResponse.TaskStatus = "succeed"
					generalResponse.VideoResult = viggleResp.Data.Data[0].Result
				}
			} else {
				// code 不为 0 的情况都视为失败
				generalResponse.TaskStatus = "failed"
				// 如果有错误信息，可以更新 Message
				if viggleResp.Data.Message != "" {
					generalResponse.Message = viggleResp.Data.Message
				}
			}
		}

		// 将 GeneralVideoResponse 结构体转换为 JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送 JSON 响应给客户端
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	} else if videoTask.Provider == "pixverse" {
		// 读取响应体
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to read response body: %v", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}
		defer resp.Body.Close()

		// 打印响应体用于调试
		log.Printf("Pixverse response body: %s", string(body))

		// 解析JSON响应
		var pixverseResp pixverse.PixverseFinalResponse
		if err := json.Unmarshal(body, &pixverseResp); err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("failed to parse response JSON: %v", err),
				"json_parse_error",
				http.StatusInternalServerError,
			)
		}

		// 创建通用响应结构体
		generalResponse := model.GeneralFinalVideoResponse{
			TaskId:      strconv.Itoa(pixverseResp.Resp.Id),
			VideoResult: "",
			VideoId:     strconv.Itoa(pixverseResp.Resp.Id),
			TaskStatus:  "succeed",
			Message:     pixverseResp.ErrMsg,
		}

		if pixverseResp.Resp.Url != "" {
			generalResponse.VideoResult = pixverseResp.Resp.Url

		}

		// 将响应转换为JSON
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(
				fmt.Errorf("Error marshaling response: %s", err),
				"internal_error",
				http.StatusInternalServerError,
			)
		}

		// 发送响应
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	}
	return nil
}

func handleMinimaxResponse(c *gin.Context, channel *dbmodel.Channel, taskId string) *model.ErrorWithStatusCode {
	// 第一次请求，获取初始状态
	url := fmt.Sprintf("%s/v1/query/video_generation?task_id=%s", *channel.BaseURL, taskId)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to create request: %v", err), "api_error", http.StatusInternalServerError)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to send request: %v", err), "api_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to read response body: %v", err), "internal_error", http.StatusInternalServerError)
	}

	var minimaxResp model.FinalVideoResponse
	if err := json.Unmarshal(body, &minimaxResp); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse := model.GeneralFinalVideoResponse{
		TaskId:      taskId,
		TaskStatus:  mapTaskStatusMinimax(minimaxResp.Status),
		Message:     minimaxResp.BaseResp.StatusMsg,
		VideoResult: "",
	}

	// 如果 FileID 为空，直接返回当前状态
	if minimaxResp.FileID == "" {
		jsonResponse, err := json.Marshal(generalResponse)
		if err != nil {
			return openai.ErrorWrapper(fmt.Errorf("Error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
		}
		c.Data(http.StatusOK, "application/json", jsonResponse)
		return nil
	}

	// 如果 FileID 不为空，获取文件信息
	fileUrl := fmt.Sprintf("%s/v1/files/retrieve?file_id=%s", *channel.BaseURL, minimaxResp.FileID)
	fileReq, err := http.NewRequest("GET", fileUrl, nil)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to create file request: %v", err), "api_error", http.StatusInternalServerError)
	}
	fileReq.Header.Set("Content-Type", "application/json")
	fileReq.Header.Set("Authorization", "Bearer "+channel.Key)

	fileResp, err := client.Do(fileReq)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to send file request: %v", err), "api_error", http.StatusInternalServerError)
	}
	defer fileResp.Body.Close()

	fileBody, err := io.ReadAll(fileResp.Body)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to read file response body: %v", err), "internal_error", http.StatusInternalServerError)
	}

	var fileResponse model.MinimaxFinalResponse
	if err := json.Unmarshal(fileBody, &fileResponse); err != nil {
		return openai.ErrorWrapper(fmt.Errorf("failed to parse file response JSON: %v", err), "json_parse_error", http.StatusInternalServerError)
	}

	generalResponse.VideoResult = fileResponse.File.DownloadURL
	generalResponse.TaskStatus = "success" // 假设有 FileID 且能获取到下载 URL 就意味着成功

	jsonResponse, err := json.Marshal(generalResponse)
	if err != nil {
		return openai.ErrorWrapper(fmt.Errorf("Error marshaling response: %s", err), "internal_error", http.StatusInternalServerError)
	}

	c.Data(http.StatusOK, "application/json", jsonResponse)
	return nil
}

func UpdateVideoTaskStatus(taskid string, status string, failreason string) {
	videoTask, err := dbmodel.GetVideoTaskById(taskid)
	if err != nil {
		log.Printf("Failed to get video task: %v", err)
		return
	}
	videoTask.Status = status
	if failreason != "" {
		videoTask.FailReason = failreason
	}
	err = videoTask.Update()
	if err != nil {
		log.Printf("Failed to update video task: %v", err)
	}
}

func CompensateVideoTask(taskid string) {
	videoTask, err := dbmodel.GetVideoTaskById(taskid)
	if err != nil {
		log.Printf("Failed to get video task: %v", err)
		return
	}
	quota := videoTask.Quota
	dbmodel.IncreaseUserQuota(videoTask.UserId, quota)
}
