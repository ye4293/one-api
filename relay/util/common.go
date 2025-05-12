package util

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"

	"github.com/gin-gonic/gin"
)

func ShouldDisableChannel(err *relaymodel.Error, statusCode int) bool {
	if !config.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if statusCode == http.StatusUnauthorized {
		return true
	}
	switch err.Type {
	case "insufficient_quota":
		return true
	case "authentication_error":
		return true
	case "permission_error":
		return true
	case "forbidden":
		return true
	}
	if err.Code == "invalid_api_key" || err.Code == "account_deactivated" {
		return true
	}
	if strings.HasPrefix(err.Message, "Your credit balance is too low") {
		return true
	} else if strings.HasPrefix(err.Message, "This organization has been disabled.") {
		return true
	}
	if strings.Contains(err.Message, "credit") {
		return true
	}
	if strings.Contains(err.Message, "not_enough_credits") { //recraft
		return true
	}
	if strings.Contains(err.Message, "balance") {
		return true
	}
	// 添加对 "Operation not allowed" 错误的处理
	if strings.Contains(err.Message, "Operation not allowed") {
		return true
	}
	// 添加对 "resource pack exhausted" 错误的处理
	if strings.Contains(err.Message, "resource pack exhausted") {
		return true
	}
	// if strings.Contains(err.Message, "quota") || strings.Contains(err.Message, "enough") {
	// 	return true
	// }

	return false
}

func ShouldEnableChannel(err error, openAIErr *relaymodel.Error) bool {
	if !config.AutomaticEnableChannelEnabled {
		return false
	}
	if err != nil {
		return false
	}
	if openAIErr != nil {
		return false
	}
	return true
}

type GeneralErrorResponse struct {
	Error    relaymodel.Error `json:"error"`
	Message  string           `json:"message"`
	Msg      string           `json:"msg"`
	Err      string           `json:"err"`
	ErrorMsg string           `json:"error_msg"`
	Header   struct {
		Message string `json:"message"`
	} `json:"header"`
	Response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

func (e GeneralErrorResponse) ToMessage() string {
	if e.Error.Message != "" {
		return e.Error.Message
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != "" {
		return e.Err
	}
	if e.ErrorMsg != "" {
		return e.ErrorMsg
	}
	if e.Header.Message != "" {
		return e.Header.Message
	}
	if e.Response.Error.Message != "" {
		return e.Response.Error.Message
	}
	return ""
}

func RelayErrorHandler(resp *http.Response) (ErrorWithStatusCode *relaymodel.ErrorWithStatusCode) {
	ErrorWithStatusCode = &relaymodel.ErrorWithStatusCode{
		StatusCode: resp.StatusCode,
		Error: relaymodel.Error{
			Message: "",
			Type:    "upstream_error",
			Code:    "bad_response_status_code",
			Param:   strconv.Itoa(resp.StatusCode),
		},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	if config.DebugEnabled {
		logger.SysLog(fmt.Sprintf("error happened, status code: %d, response: \n%s", resp.StatusCode, string(responseBody)))
	}
	err = resp.Body.Close()
	if err != nil {
		return
	}
	var errResponse GeneralErrorResponse
	err = json.Unmarshal(responseBody, &errResponse)
	if err != nil {
		return
	}
	if errResponse.Error.Message != "" {
		// OpenAI format error, so we override the default one
		ErrorWithStatusCode.Error = errResponse.Error
	} else {
		ErrorWithStatusCode.Error.Message = errResponse.ToMessage()
	}
	if ErrorWithStatusCode.Error.Message == "" {
		ErrorWithStatusCode.Error.Message = fmt.Sprintf("bad response status code %d", resp.StatusCode)
	}
	return
}

func GetFullRequestURL(baseURL string, requestURL string, channelType int) string {
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	if strings.HasPrefix(baseURL, "https://gateway.ai.cloudflare.com") {
		switch channelType {
		case common.ChannelTypeOpenAI:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/v1"))
		case common.ChannelTypeAzure:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/openai/deployments"))
		}
	}
	if channelType == 24 { //google gemini
		fullRequestURL = fmt.Sprintf("%s/v1beta/openai/images/generations", baseURL)
	}
	return fullRequestURL
}

func PostConsumeQuota(ctx context.Context, tokenId int, quotaDelta int64, totalQuota int64, userId int, channelId int, modelRatio float64, groupRatio float64, modelName string, tokenName string, duration float64, title string, httpReferer string) {
	// quotaDelta is remaining quota to be consumed
	err := model.PostConsumeTokenQuota(tokenId, quotaDelta)
	if err != nil {
		logger.SysError("error consuming token remain quota: " + err.Error())
	}
	err = model.CacheUpdateUserQuota(ctx, userId)
	if err != nil {
		logger.SysError("error update user quota cache: " + err.Error())
	}
	// totalQuota is total quota consumed
	if totalQuota != 0 {
		logContent := fmt.Sprintf("模型倍率 %.2f，分组倍率 %.2f", modelRatio, groupRatio)
		model.RecordConsumeLog(ctx, userId, channelId, int(totalQuota), 0, modelName, tokenName, totalQuota, logContent, duration, title, httpReferer)
		model.UpdateUserUsedQuotaAndRequestCount(userId, totalQuota)
		model.UpdateChannelUsedQuota(channelId, totalQuota)
	}
	if totalQuota <= 0 {
		logger.Error(ctx, fmt.Sprintf("totalQuota consumed is %d, something is wrong", totalQuota))
	}
}

func GetAzureAPIVersion(c *gin.Context) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion == "" {
		apiVersion = c.GetString(common.ConfigKeyAPIVersion)
	}
	return apiVersion
}

func ProcessString(input string) string {
	// 使用正则表达式匹配域名和IP
	re := regexp.MustCompile(`(https?://)?([a-zA-Z0-9.-]+\.[a-zA-Z]{2,}|[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3})(:[0-9]+)?`)

	// 替换函数
	replacer := func(match string) string {
		// 去除协议部分
		if strings.HasPrefix(match, "http://") || strings.HasPrefix(match, "https://") {
			match = match[strings.Index(match, "://")+3:]
		}

		// 去除可能存在的端口号
		parts := strings.Split(match, ":")
		host := parts[0]

		// 判断是域名还是IP
		if ip := net.ParseIP(host); ip != nil {
			// 如果是IP,替换为<host>
			return "<host>"
		} else {
			// 如果是域名,移除域名部分,保留路径
			pathIndex := strings.Index(match, "/")
			if pathIndex != -1 {
				return match[pathIndex:]
			} else {
				return ""
			}
		}
	}

	// 替换字符串中的域名和IP
	result := re.ReplaceAllStringFunc(input, replacer)

	return result
}
