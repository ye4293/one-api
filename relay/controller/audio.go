package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/constant"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func RelayAudioHelper(c *gin.Context, relayMode int) *relaymodel.ErrorWithStatusCode {
	ctx := c.Request.Context()
	audioModel := "whisper-1"
	startTime := time.Now()

	// 获取上下文信息
	tokenId := c.GetInt("token_id")
	channelType := c.GetInt("channel")
	channelId := c.GetInt("channel_id")
	userId := c.GetInt("id")
	group := c.GetString("group")
	tokenName := c.GetString("token_name")

	// 处理文本转语音请求
	var ttsRequest openai.TextToSpeechRequest
	if relayMode == constant.RelayModeAudioSpeech {
		err := common.UnmarshalBodyReusable(c, &ttsRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "invalid_json", http.StatusBadRequest)
		}
		audioModel = ttsRequest.Model
		if len(ttsRequest.Input) > 4096 {
			return openai.ErrorWrapper(errors.New("input is too long (over 4096 characters)"), "text_too_long", http.StatusBadRequest)
		}
	}

	// 配额相关处理
	modelRatio := common.GetModelRatio(audioModel)
	groupRatio := common.GetGroupRatio(group)
	ratio := modelRatio * groupRatio
	var quota int64
	var preConsumedQuota int64

	switch relayMode {
	case constant.RelayModeAudioSpeech:
		preConsumedQuota = int64(float64(len(ttsRequest.Input)) * ratio)
		quota = preConsumedQuota
	default:
		preConsumedQuota = int64(float64(config.PreConsumedQuota) * ratio)
	}

	// 检查用户配额
	userQuota, err := model.CacheGetUserQuota(ctx, userId)
	if err != nil {
		return openai.ErrorWrapper(err, "get_user_quota_failed", http.StatusInternalServerError)
	}
	if userQuota-preConsumedQuota < 0 {
		return openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}

	// 预扣除配额
	err = model.CacheDecreaseUserQuota(userId, preConsumedQuota)
	if err != nil {
		return openai.ErrorWrapper(err, "decrease_user_quota_failed", http.StatusInternalServerError)
	}

	// 配额预处理
	if userQuota > 100*preConsumedQuota {
		preConsumedQuota = 0
	}
	if preConsumedQuota > 0 {
		err := model.PreConsumeTokenQuota(tokenId, preConsumedQuota)
		if err != nil {
			return openai.ErrorWrapper(err, "pre_consume_token_quota_failed", http.StatusForbidden)
		}
	}

	succeed := false
	defer func() {
		if !succeed && preConsumedQuota > 0 {
			go func(ctx context.Context) {
				err := model.PostConsumeTokenQuota(tokenId, -preConsumedQuota)
				if err != nil {
					logger.Error(ctx, fmt.Sprintf("error rollback pre-consumed quota: %s", err.Error()))
				}
			}(ctx)
		}
	}()

	// 处理模型映射
	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" {
		modelMap := make(map[string]string)
		if err := json.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
			return openai.ErrorWrapper(err, "unmarshal_model_mapping_failed", http.StatusInternalServerError)
		}
		if modelMap[audioModel] != "" {
			audioModel = modelMap[audioModel]
		}
	}

	// 构建请求URL
	baseURL := common.ChannelBaseURLs[channelType]
	if c.GetString("base_url") != "" {
		baseURL = c.GetString("base_url")
	}
	fullRequestURL := util.GetFullRequestURL(baseURL, c.Request.URL.String(), channelType)

	// 处理Azure特殊情况
	if channelType == common.ChannelTypeAzure {
		apiVersion := util.GetAzureAPIVersion(c)
		if relayMode == constant.RelayModeAudioTranscription {
			fullRequestURL = fmt.Sprintf("%s/openai/deployments/%s/audio/transcriptions?api-version=%s", baseURL, audioModel, apiVersion)
		} else if relayMode == constant.RelayModeAudioSpeech {
			fullRequestURL = fmt.Sprintf("%s/openai/deployments/%s/audio/speech?api-version=%s", baseURL, audioModel, apiVersion)
		}
	}

	var req *http.Request
	contentType := c.Request.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// 获取表单数据
		form, err := c.MultipartForm()
		if err != nil {
			return openai.ErrorWrapper(err, "get_multipart_form_failed", http.StatusBadRequest)
		}

		fmt.Printf("=== Form Data ===\n")
		for key, values := range form.Value {
			fmt.Printf("Form field %s: %v\n", key, values)
		}
		for key, files := range form.File {
			for _, file := range files {
				fmt.Printf("File field %s: filename=%s, size=%d\n",
					key, file.Filename, file.Size)
			}
		}

		// 创建新的 multipart writer
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// 添加文件
		for _, fileHeader := range form.File["file"] {
			file, err := fileHeader.Open()
			if err != nil {
				return openai.ErrorWrapper(err, "open_file_failed", http.StatusInternalServerError)
			}

			part, err := writer.CreateFormFile("file", fileHeader.Filename)
			if err != nil {
				file.Close()
				return openai.ErrorWrapper(err, "create_form_file_failed", http.StatusInternalServerError)
			}

			_, err = io.Copy(part, file)
			file.Close()
			if err != nil {
				return openai.ErrorWrapper(err, "copy_file_failed", http.StatusInternalServerError)
			}
		}

		// 添加其他表单字段
		for key, values := range form.Value {
			for _, value := range values {
				err := writer.WriteField(key, value)
				if err != nil {
					return openai.ErrorWrapper(err, "write_field_failed", http.StatusInternalServerError)
				}
			}
		}

		// 关闭 writer
		err = writer.Close()
		if err != nil {
			return openai.ErrorWrapper(err, "close_writer_failed", http.StatusInternalServerError)
		}

		// 创建新的请求
		req, err = http.NewRequest(c.Request.Method, fullRequestURL, body)
		if err != nil {
			return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
		}

		// 设置新的 Content-Type，使用新的 boundary
		req.Header.Set("Content-Type", writer.FormDataContentType())
	} else {
		// 处理非 multipart/form-data 请求
		req, err = http.NewRequest(c.Request.Method, fullRequestURL, c.Request.Body)
		if err != nil {
			return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
		}
		req.Header.Set("Content-Type", contentType)
	}

	// 设置其他请求头
	if channelType == common.ChannelTypeAzure && (relayMode == constant.RelayModeAudioTranscription || relayMode == constant.RelayModeAudioSpeech) {
		apiKey := strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer ")
		req.Header.Set("api-key", apiKey)
	} else {
		req.Header.Set("Authorization", c.Request.Header.Get("Authorization"))
	}
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))

	// 发送请求
	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	// 处理非语音合成的响应
	if relayMode != constant.RelayModeAudioSpeech {
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		}

		// 检查错误响应
		var openAIErr openai.SlimTextResponse
		if err = json.Unmarshal(responseBody, &openAIErr); err == nil && openAIErr.Error.Message != "" {
			return openai.ErrorWrapper(fmt.Errorf("type %s, code %v, message %s",
				openAIErr.Error.Type, openAIErr.Error.Code, openAIErr.Error.Message),
				"request_error", http.StatusInternalServerError)
		}

		// 根据响应格式处理文本
		responseFormat := c.DefaultPostForm("response_format", "json")
		text, err := getTextFromResponse(responseBody, responseFormat)
		if err != nil {
			return openai.ErrorWrapper(err, "get_text_from_body_err", http.StatusInternalServerError)
		}
		quota = int64(openai.CountTokenText(text, audioModel))
		resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
	}

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return util.RelayErrorHandler(resp)
	}

	// 设置成功标志
	succeed = true

	// 计算配额变化
	quotaDelta := quota - preConsumedQuota
	referer := c.Request.Header.Get("HTTP-Referer")
	title := c.Request.Header.Get("X-Title")

	// 异步处理配额消费
	defer func(ctx context.Context) {
		duration := math.Round(time.Since(startTime).Seconds()*1000) / 1000
		go util.PostConsumeQuota(ctx, tokenId, quotaDelta, quota, userId, channelId,
			modelRatio, groupRatio, audioModel, tokenName, duration, title, referer)
	}(ctx)

	// 写入响应
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError)
	}

	return nil
}

// 辅助函数：根据不同格式获取文本
func getTextFromResponse(responseBody []byte, format string) (string, error) {
	switch format {
	case "json":
		return getTextFromJSON(responseBody)
	case "text":
		return getTextFromText(responseBody)
	case "srt":
		return getTextFromSRT(responseBody)
	case "verbose_json":
		return getTextFromVerboseJSON(responseBody)
	case "vtt":
		return getTextFromVTT(responseBody)
	default:
		return "", errors.New("unexpected_response_format")
	}
}

func getTextFromVTT(body []byte) (string, error) {
	return getTextFromSRT(body)
}

func getTextFromVerboseJSON(body []byte) (string, error) {
	var whisperResponse openai.WhisperVerboseJSONResponse
	if err := json.Unmarshal(body, &whisperResponse); err != nil {
		return "", fmt.Errorf("unmarshal_response_body_failed err :%w", err)
	}
	return whisperResponse.Text, nil
}

func getTextFromSRT(body []byte) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	var builder strings.Builder
	var textLine bool
	for scanner.Scan() {
		line := scanner.Text()
		if textLine {
			builder.WriteString(line)
			textLine = false
			continue
		} else if strings.Contains(line, "-->") {
			textLine = true
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func getTextFromText(body []byte) (string, error) {
	return strings.TrimSuffix(string(body), "\n"), nil
}

func getTextFromJSON(body []byte) (string, error) {
	var whisperResponse openai.WhisperJSONResponse
	if err := json.Unmarshal(body, &whisperResponse); err != nil {
		return "", fmt.Errorf("unmarshal_response_body_failed err :%w", err)
	}
	return whisperResponse.Text, nil
}
