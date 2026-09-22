package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/service"
)

func abortResponsesError(c *gin.Context, err error) {
	status, code := http.StatusServiceUnavailable, "responses_routing_unavailable"
	var stateErr *model.ResponsesStateError
	if errors.As(err, &stateErr) {
		status, code = stateErr.Status, stateErr.Code
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"message": err.Error(), "type": "responses_state_error", "code": code}})
}

func prepareResponsesBinding(c *gin.Context) bool {
	if !service.IsResponsesPath(c.Request.URL.Path) {
		return true
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		abortResponsesError(c, err)
		return false
	}
	constraint, err := service.ResolveResponsesConstraint(c.Request.Context(), c.GetInt("id"), body, c.GetHeader(service.ResponsesProviderHeader), c.GetHeader("X-Response-ID"))
	if err != nil {
		abortResponsesError(c, err)
		return false
	}
	c.Request = c.Request.WithContext(model.WithResponsesConstraint(c.Request.Context(), constraint))
	return true
}

func freezeResponsesProvider(c *gin.Context, channel *model.Channel, modelName, group string) bool {
	constraint, active := model.GetResponsesConstraint(c.Request.Context())
	if !active {
		return true
	}
	// 亲和命中可能来自旧缓存，发送前使用数据库的当前配置创建尝试快照。
	var current model.Channel
	if err := model.DB.WithContext(c.Request.Context()).First(&current, channel.Id).Error; err != nil {
		abortResponsesError(c, err)
		return false
	}
	*channel = current
	if err := model.ValidateResponsesChannel(c.Request.Context(), channel, group, modelName); err != nil {
		abortResponsesError(c, err)
		return false
	}
	if constraint.Provider == "" {
		provider, err := channel.EffectiveProvider()
		if err != nil {
			abortResponsesError(c, err)
			return false
		}
		constraint.Provider = provider
		c.Request = c.Request.WithContext(model.WithResponsesConstraint(c.Request.Context(), constraint))
	}
	return true
}
