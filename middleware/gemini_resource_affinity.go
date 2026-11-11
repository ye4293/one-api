package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/metrics"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/service"
)

// All initial selection paths pass this check before credentials are set.
// Explicit channels bypass signature routing. Only signed requests freeze a type;
// Response-ID hits keep their selected type, other paths restore the signature type.
func enforceGeminiResourceAffinity(c *gin.Context, channel *model.Channel, modelName, group string) (*model.Channel, *service.GeminiAffinityError) {
	if _, specific := c.Get("specific_channel_id"); specific {
		return channel, nil
	}
	if !service.GeminiRequestHasSignatures(c, modelName) {
		return channel, nil
	}
	channelType, pinned := service.GeminiResourceType(c.Request.Context())
	if !pinned {
		c.Request = c.Request.WithContext(service.WithGeminiResourceType(c.Request.Context(), channel.Type))
		return channel, nil
	}
	if channel.Type == channelType {
		return channel, nil
	}
	selected, err := model.CacheGetRandomSatisfiedChannelByType(c.Request.Context(), group, modelName, channelType)
	if err != nil {
		metrics.IncNoChannel(modelName, group)
		c.Set(metrics.CtxRelayFailedKey, metrics.ReasonNoChannel)
		c.Set(metrics.CtxModelKey, modelName)
		logger.Warnf(c.Request.Context(), "[Gemini Resource] no matching channel model=%s group=%s type=%d: %v", modelName, group, channelType, err)
		return nil, &service.GeminiAffinityError{
			StatusCode: http.StatusServiceUnavailable,
			Message:    fmt.Sprintf("No channels available for Gemini model %s with required resource type %d", modelName, channelType),
		}
	}
	// Both the cached key index and skip-retry flag belong to the discarded channel.
	service.ResetChannelAffinityAfterChannelChange(c)
	logger.Infof(c.Request.Context(), "[Gemini Resource] selected model=%s type=%d channel=%d instead_of=%d", modelName, channelType, selected.Id, channel.Id)
	return selected, nil
}
