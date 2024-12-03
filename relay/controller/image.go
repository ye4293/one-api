package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/channel/replicate"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"

	"github.com/gin-gonic/gin"
)

// func isWithinRange(element string, value int) bool {
// 	if _, ok := constant.DalleGenerationImageAmounts[element]; !ok {
// 		return false
// 	}
// 	min := constant.DalleGenerationImageAmounts[element][0]
// 	max := constant.DalleGenerationImageAmounts[element][1]

// 	return value >= min && value <= max
// }

func RelayImageHelper(c *gin.Context, relayMode int) *relaymodel.ErrorWithStatusCode {
	startTime := time.Now()
	ctx := c.Request.Context()
	meta := util.GetRelayMeta(c)
	imageRequest, err := getImageRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getImageRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_image_request", http.StatusBadRequest)
	}

	// map model name
	var isModelMapped bool
	meta.OriginModelName = imageRequest.Model
	imageRequest.Model, isModelMapped = util.GetMappedModelName(imageRequest.Model, meta.ModelMapping)
	meta.ActualModelName = imageRequest.Model

	// model validation
	// bizErr := validateImageRequest(imageRequest, meta)
	// if bizErr != nil {
	// 	return bizErr
	// }

	imageCostRatio, err := getImageCostRatio(imageRequest)
	if err != nil {
		return openai.ErrorWrapper(err, "get_image_cost_ratio_failed", http.StatusInternalServerError)
	}
	var fullRequestURL string
	requestURL := c.Request.URL.String()
	fullRequestURL = util.GetFullRequestURL(meta.BaseURL, requestURL, meta.ChannelType)
	if meta.ChannelType == common.ChannelTypeAzure {
		apiVersion := util.GetAzureAPIVersion(c)
		fullRequestURL = fmt.Sprintf("%s/openai/deployments/%s/images/generations?api-version=%s", meta.BaseURL, imageRequest.Model, apiVersion)
	}

	var requestBody io.Reader
	if isModelMapped || meta.ChannelType == common.ChannelTypeAzure {
		jsonStr, err := json.Marshal(imageRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "marshal_image_request_failed", http.StatusInternalServerError)
		}
		requestBody = bytes.NewBuffer(jsonStr)
	} else {
		requestBody = c.Request.Body
	}

	adaptor := helper.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)

	if meta.ChannelType == common.ChannelTypeReplicate {
		fullRequestURL, err = adaptor.GetRequestURL(meta)
		finalRequest, err := adaptor.ConvertImageRequest(imageRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "convert_image_request_failed", http.StatusInternalServerError)
		}
		jsonStr, err := json.Marshal(finalRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "marshal_image_request_failed", http.StatusInternalServerError)
		}
		requestBody = bytes.NewBuffer(jsonStr)
	}

	modelRatio := common.GetModelRatio(imageRequest.Model)
	groupRatio := common.GetGroupRatio(meta.Group)
	userModelTypeRatio := common.GetUserModelTypeRation(meta.Group, imageRequest.Model)
	ratio := modelRatio * groupRatio * userModelTypeRatio
	userQuota, err := model.CacheGetUserQuota(ctx, meta.UserId)

	var modelPrice float64
	defaultPrice, ok := common.DefaultModelPrice[imageRequest.Model]
	if !ok {
		modelPrice = 0.1
	} else {
		modelPrice = defaultPrice
	}
	quota := int64(modelPrice*500000*imageCostRatio) * int64(imageRequest.N)

	// quota := int64(ratio*imageCostRatio*1000) * int64(imageRequest.N)

	if userQuota-quota < 0 {
		return openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}

	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return openai.ErrorWrapper(err, "new_request_failed", http.StatusInternalServerError)
	}
	token := c.Request.Header.Get("Authorization")
	if meta.ChannelType == common.ChannelTypeAzure {
		token = strings.TrimPrefix(token, "Bearer ")
		req.Header.Set("api-key", token)
	} else {
		req.Header.Set("Authorization", token)
	}

	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))

	resp, err := util.HTTPClient.Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}

	err = req.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", http.StatusInternalServerError)
	}
	err = c.Request.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", http.StatusInternalServerError)
	}
	var imageResponse openai.ImageResponse

	defer func(ctx context.Context) {
		if resp == nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) {
			return
		}

		err := model.PostConsumeTokenQuota(meta.TokenId, quota)
		if err != nil {
			logger.SysError("error consuming token remain quota: " + err.Error())
		}
		err = model.CacheUpdateUserQuota(ctx, meta.UserId)
		if err != nil {
			logger.SysError("error update user quota cache: " + err.Error())
		}
		if quota != 0 {
			referer := c.Request.Header.Get("HTTP-Referer")
			title := c.Request.Header.Get("X-Title")
			rowDuration := time.Since(startTime).Seconds()
			duration := math.Round(rowDuration*1000) / 1000
			tokenName := c.GetString("token_name")
			if meta.ChannelType == common.ChannelTypeReplicate {
				// quota = int64(ratio*500000) * int64(imageRequest.N)
				quota = int64(ratio * 500000)
			}
			logContent := fmt.Sprintf("模型倍率 %.2f，分组倍率 %.2f 用户模型倍率 %.2f", modelRatio, groupRatio, userModelTypeRatio)
			model.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, 0, 0, meta.ActualModelName, tokenName, quota, logContent, duration, title, referer)
			model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
			channelId := c.GetInt("channel_id")
			model.UpdateChannelUsedQuota(channelId, quota)
		}
	}(c.Request.Context())

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError)
	}
	err = json.Unmarshal(responseBody, &imageResponse)
	if err != nil {
		return openai.ErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError)
	}

	if meta.ChannelType == common.ChannelTypeReplicate {
		var replicateResp replicate.ReplicateResponse
		err = json.Unmarshal(responseBody, &replicateResp)
		if err != nil {
			return openai.ErrorWrapper(err, "unmarshal_replicate_response_failed", http.StatusInternalServerError)
		}

		channel, err := model.GetChannelById(meta.ChannelId, true)
		if err != nil {
			return openai.ErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError)
		}

		finalResult, err := getReplicateFinalResult(replicateResp.URLs.Get, channel.Key)
		if err != nil {
			return openai.ErrorWrapper(err, "get_replicate_final_result_failed", http.StatusInternalServerError)
		}

		logger.SysLog(fmt.Sprintf("finalResult:%+v", finalResult))

		// 构造 DALL-E 3 格式的响应
		dalleResp := ImageResponse{
			Created: time.Now().Unix(),
		}

		// 处理 Output 字段
		var outputURLs []string
		switch output := finalResult.Output.(type) {
		case string:
			outputURLs = []string{output}
		case []interface{}:
			for _, url := range output {
				if strURL, ok := url.(string); ok {
					outputURLs = append(outputURLs, strURL)
				}
			}
		case []string:
			outputURLs = output
		}

		// 构造 Data 字段
		dalleResp.Data = make([]ImageData, len(outputURLs))
		for i, url := range outputURLs {
			dalleResp.Data[i] = ImageData{
				RevisedPrompt: imageRequest.Prompt,
				URL:           url,
			}
		}

		flux := model.Flux{
			Id:        finalResult.ID,
			UserId:    meta.UserId,
			Prompt:    finalResult.Input.Prompt,
			ChannelId: meta.ChannelId,
		}

		err = flux.Insert()

		if err != nil {
			return openai.ErrorWrapper(err, "failed to insert flux", http.StatusInternalServerError)
		}

		modifiedResponseBody, err := json.Marshal(dalleResp)
		if err != nil {
			return openai.ErrorWrapper(err, "marshal_modified_response_failed", http.StatusInternalServerError)
		}

		responseBody = modifiedResponseBody

		logger.SysLog(fmt.Sprintf("Modified Response: %+v", dalleResp))
	}

	// 设置响应头
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}

	// 设置新的 Content-Length
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(responseBody)))

	// 设置状态码
	c.Writer.WriteHeader(http.StatusOK)

	// 写入响应体
	_, err = c.Writer.Write(responseBody)
	if err != nil {
		return openai.ErrorWrapper(err, "write_response_body_failed", http.StatusInternalServerError)
	}

	return nil

}

type ImageResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
}

type ImageData struct {
	RevisedPrompt string `json:"revised_prompt"`
	URL           string `json:"url"`
}

// func extractString(inputString string) string {
// 	re := regexp.MustCompile(`\/([\w]+)$`)
// 	result := re.FindStringSubmatch(inputString)
// 	if len(result) > 1 {
// 		return result[1]
// 	} else {
// 		return ""
// 	}
// }

func getReplicateFinalResult(url, apiKey string) (*replicate.FinalRequestResponse, error) {
	client := &http.Client{
		Timeout: time.Minute, // 1分钟超时
	}

	maxRetries := 30
	retryDelay := time.Second * 2

	for attempt := 0; attempt < maxRetries; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}

		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error sending request: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading response body: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
		}

		var replicateResp replicate.FinalRequestResponse
		err = json.Unmarshal(body, &replicateResp)
		if err != nil {
			logger.SysLog(fmt.Sprintf("Error unmarshalling response: %v", err))
			return nil, fmt.Errorf("error unmarshalling response: %v", err)
		}

		// 检查状态和输出
		if replicateResp.Status == "succeeded" {
			switch output := replicateResp.Output.(type) {
			case string:
				if output != "" {
					return &replicateResp, nil
				}
			case []interface{}:
				if len(output) > 0 {
					return &replicateResp, nil
				}
			case []string:
				if len(output) > 0 {
					return &replicateResp, nil
				}
			}
		}

		// 如果状态为 "failed"，立即返回错误
		if replicateResp.Status == "failed" {
			return nil, fmt.Errorf("prediction failed: %v", replicateResp.Error)
		}

		time.Sleep(retryDelay)
	}

	return nil, fmt.Errorf("failed to get valid output after %d attempts", maxRetries)
}
