package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

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

func getAndValidateTextRequest(c *gin.Context, relayMode int) (*relaymodel.GeneralOpenAIRequest, error) {
	textRequest := &relaymodel.GeneralOpenAIRequest{}
	err := common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}
	if relayMode == constant.RelayModeModerations && textRequest.Model == "" {
		textRequest.Model = "text-moderation-latest"
	}
	if relayMode == constant.RelayModeEmbeddings && textRequest.Model == "" {
		textRequest.Model = c.Param("model")
	}
	err = util.ValidateTextRequest(textRequest, relayMode)
	if err != nil {
		return nil, err
	}
	return textRequest, nil
}

func getImageRequest(c *gin.Context, relayMode int) (*relaymodel.ImageRequest, error) {
	// 检查内容类型
	contentType := c.GetHeader("Content-Type")
	isFormRequest := strings.Contains(contentType, "multipart/form-data") || strings.Contains(contentType, "application/x-www-form-urlencoded")

	if isFormRequest {
		// 对于表单请求，我们只需要获取基本信息，不消费请求体
		imageRequest := &relaymodel.ImageRequest{
			N:     1,
			Size:  "1024x1024",
			Model: "dall-e-2",
		}

		// 尝试获取model字段，但不解析整个表单
		if strings.Contains(contentType, "multipart/form-data") {
			// 尝试只解析model字段，不读取文件
			c.Request.ParseMultipartForm(1 << 10) // 只解析1KB的数据
			if model := c.Request.FormValue("model"); model != "" {
				imageRequest.Model = model
			}
		} else {
			// 对于url编码的表单
			c.Request.ParseForm()
			if model := c.Request.FormValue("model"); model != "" {
				imageRequest.Model = model
			}
		}

		return imageRequest, nil
	} else {
		// 对于JSON请求，使用原有逻辑
		imageRequest := &relaymodel.ImageRequest{}
		err := common.UnmarshalBodyReusable(c, imageRequest)
		if err != nil {
			return nil, err
		}
		if imageRequest.N == 0 {
			imageRequest.N = 1
		}
		if imageRequest.Size == "" {
			imageRequest.Size = "1024x1024"
		}
		if imageRequest.Model == "" {
			imageRequest.Model = "dall-e-2"
		}
		return imageRequest, nil
	}
}

func getImageCostRatio(imageRequest *relaymodel.ImageRequest) (float64, error) {
	// 检查空指针
	if imageRequest == nil {
		return 0, errors.New("imageRequest is nil")
	}

	// 初始化基础倍率
	var imageCostRatio float64 = 1.0

	switch imageRequest.Model {
	case "dall-e-2":
		switch imageRequest.Size {
		case "1024x1024":
			imageCostRatio = 1.0 // 1x 基准价格
		case "512x512":
			imageCostRatio = 0.9 // 0.9x 基准价格
		case "256x256":
			imageCostRatio = 0.8 // 0.8x 基准价格
		default:
			return 0, fmt.Errorf("size not supported for DALL-E 2: %s", imageRequest.Size)
		}
	case "dall-e-3":
		switch imageRequest.Size {
		case "1024x1024":
			imageCostRatio = 1.0 // 1x 基准价格
		case "1024x1792", "1792x1024":
			imageCostRatio = 2.0 // 2x 基准价格
		default:
			return 0, fmt.Errorf("size not supported for DALL-E 3: %s", imageRequest.Size)
		}

		// HD质量的价格调整
		if imageRequest.Quality == "hd" && imageRequest.Model == "dall-e-3" {
			if imageRequest.Size == "1024x1024" {
				imageCostRatio = 2 // 2x 基准价格
			} else {
				imageCostRatio = 3 // 3x 基准价格
			}
		}
	default:
		return 1, nil // 处理所有其他模型
	}

	return imageCostRatio, nil
}

func getPromptTokens(textRequest *relaymodel.GeneralOpenAIRequest, relayMode int) int {
	switch relayMode {
	case constant.RelayModeChatCompletions:
		return openai.CountTokenMessages(textRequest.Messages, textRequest.Model)
	case constant.RelayModeCompletions:
		return openai.CountTokenInput(textRequest.Prompt, textRequest.Model)
	case constant.RelayModeModerations:
		return openai.CountTokenInput(textRequest.Input, textRequest.Model)
	}
	return 0
}

func getPreConsumedQuota(textRequest *relaymodel.GeneralOpenAIRequest, promptTokens int, ratio float64) int64 {
	preConsumedTokens := config.PreConsumedQuota + int64(promptTokens)
	if textRequest.MaxTokens != 0 {
		preConsumedTokens += int64(textRequest.MaxTokens)
	}
	return int64(float64(preConsumedTokens) * ratio)
}

func preConsumeQuota(ctx context.Context, textRequest *relaymodel.GeneralOpenAIRequest, promptTokens int, ratio float64, meta *util.RelayMeta) (int64, *relaymodel.ErrorWithStatusCode) {
	preConsumedQuota := getPreConsumedQuota(textRequest, promptTokens, ratio)

	userQuota, err := model.CacheGetUserQuota(ctx, meta.UserId)
	if err != nil {
		return preConsumedQuota, openai.ErrorWrapper(err, "get_user_quota_failed", http.StatusInternalServerError)
	}
	if userQuota-preConsumedQuota < 0 {
		return preConsumedQuota, openai.ErrorWrapper(errors.New("user quota is not enough"), "insufficient_user_quota", http.StatusForbidden)
	}
	err = model.CacheDecreaseUserQuota(meta.UserId, preConsumedQuota)
	if err != nil {
		return preConsumedQuota, openai.ErrorWrapper(err, "decrease_user_quota_failed", http.StatusInternalServerError)
	}
	if userQuota > 100*preConsumedQuota {
		// in this case, we do not pre-consume quota
		// because the user has enough quota
		preConsumedQuota = 0
		logger.Info(ctx, fmt.Sprintf("user %d has enough quota %d, trusted and no need to pre-consume", meta.UserId, userQuota))
	}
	if preConsumedQuota > 0 {
		err := model.PreConsumeTokenQuota(meta.TokenId, preConsumedQuota)
		if err != nil {
			return preConsumedQuota, openai.ErrorWrapper(err, "pre_consume_token_quota_failed", http.StatusForbidden)
		}
	}
	return preConsumedQuota, nil
}

func postConsumeQuota(ctx context.Context, usage *relaymodel.Usage, meta *util.RelayMeta, textRequest *relaymodel.GeneralOpenAIRequest, ratio float64, preConsumedQuota int64, modelRatio float64, groupRatio float64, userModelTypeRatio float64, duration float64, title string, httpReferer string) {
	if usage == nil {
		logger.Error(ctx, "usage is nil, which is unexpected")
		return
	}
	var quota int64
	completionRatio := common.GetCompletionRatio(textRequest.Model)
	promptTokens := usage.PromptTokens
	completionTokens := usage.CompletionTokens
	quota = int64(math.Ceil((float64(promptTokens) + float64(completionTokens)*completionRatio) * ratio))
	if ratio != 0 && quota <= 0 {
		quota = 1
	}
	totalTokens := promptTokens + completionTokens
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
	}
	quotaDelta := quota - preConsumedQuota
	err := model.PostConsumeTokenQuota(meta.TokenId, quotaDelta)
	if err != nil {
		logger.Error(ctx, "error consuming token remain quota: "+err.Error())
	}
	err = model.CacheUpdateUserQuota(ctx, meta.UserId)
	if err != nil {
		logger.Error(ctx, "error update user quota cache: "+err.Error())
	}
	if quota != 0 {
		logContent := fmt.Sprintf("模型倍率 %.2f，分组倍率 %.2f，补全倍率 %.2f 用户模型倍率 %.2f", modelRatio, groupRatio, completionRatio, userModelTypeRatio)
		model.RecordConsumeLog(ctx, meta.UserId, meta.ChannelId, promptTokens, completionTokens, textRequest.Model, meta.TokenName, quota, logContent, duration, title, httpReferer)
		model.UpdateUserUsedQuotaAndRequestCount(meta.UserId, quota)
		model.UpdateChannelUsedQuota(meta.ChannelId, quota)
	}
}
