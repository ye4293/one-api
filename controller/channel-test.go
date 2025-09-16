package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/message"
	"github.com/songquanpeng/one-api/middleware"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/monitor"
	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/helper"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"

	"github.com/gin-gonic/gin"
)

func buildTestRequest() *relaymodel.GeneralOpenAIRequest {
	testRequest := &relaymodel.GeneralOpenAIRequest{
		Stream: false,
		Model:  "gpt-3.5-turbo",
	}
	testMessage := relaymodel.Message{
		Role:    "user",
		Content: "hi",
	}
	testRequest.Messages = append(testRequest.Messages, testMessage)
	return testRequest
}

func testChannel(channel *model.Channel, specifiedModel string) (err error, openaiErr *relaymodel.Error, actualModel string, keyIndex int) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: "/v1/chat/completions"},
		Body:   nil,
		Header: make(http.Header),
	}
	// 为多密钥渠道选择一个Key进行测试
	testKey := channel.Key
	keyIndex = -1
	if channel.MultiKeyInfo.IsMultiKey {
		actualKey, selectedIndex, err := channel.GetNextAvailableKey()
		if err != nil {
			return fmt.Errorf("no available key for testing: %v", err), nil, "", -1
		}
		testKey = actualKey
		keyIndex = selectedIndex
	}

	c.Request.Header.Set("Authorization", "Bearer "+testKey)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	c.Set("test_key_index", keyIndex) // 用于日志记录
	middleware.SetupContextForSelectedChannel(c, channel, "")
	meta := util.GetRelayMeta(c)
	apiType := constant.ChannelType2APIType(channel.Type)
	adaptor := helper.GetAdaptor(apiType)
	if adaptor == nil {
		return fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), nil, "", keyIndex
	}
	adaptor.Init(meta)

	var modelName string
	if specifiedModel != "" {
		// 如果指定了模型，检查渠道是否支持该模型
		if strings.Contains(channel.Models, specifiedModel) {
			modelName = specifiedModel
		} else {
			return fmt.Errorf("specified model '%s' is not supported by this channel", specifiedModel), nil, specifiedModel, keyIndex
		}
	} else {
		// 没有指定模型，使用原逻辑选择模型
		modelName = adaptor.GetModelList()[0]
		if !strings.Contains(channel.Models, modelName) {
			modelNames := strings.Split(channel.Models, ",")
			if len(modelNames) > 0 {
				modelName = strings.TrimSpace(modelNames[0])
			}
		}
	}
	request := buildTestRequest()
	request.Model = modelName
	meta.OriginModelName, meta.ActualModelName = modelName, modelName
	convertedRequest, err := adaptor.ConvertRequest(c, constant.RelayModeChatCompletions, request)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(requestBody)
	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	if resp.StatusCode != http.StatusOK {
		err := util.RelayErrorHandler(resp)
		return fmt.Errorf("status code %d: %s", resp.StatusCode, err.Error.Message), &err.Error, modelName, keyIndex
	}
	usage, respErr := adaptor.DoResponse(c, resp, meta)
	if respErr != nil {
		return fmt.Errorf("%s", respErr.Error.Message), &respErr.Error, modelName, keyIndex
	}
	if usage == nil {
		return errors.New("usage is nil"), nil, modelName, keyIndex
	}
	result := w.Result()
	// print result.Body
	respBody, err := io.ReadAll(result.Body)
	if err != nil {
		return err, nil, modelName, keyIndex
	}
	logger.SysLog(fmt.Sprintf("testing channel #%d with model %s, response: \n%s", channel.Id, modelName, string(respBody)))
	return nil, nil, modelName, keyIndex
}

func TestChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// 获取请求体中的模型参数（可选）
	var requestBody struct {
		Model string `json:"model"`
	}
	c.ShouldBindJSON(&requestBody)
	specifiedModel := strings.TrimSpace(requestBody.Model)

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	tik := time.Now()
	err, _, actualModel, usedKeyIndex := testChannel(channel, specifiedModel)
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	go channel.UpdateResponseTime(milliseconds)
	consumedTime := float64(milliseconds) / 1000.0

	// 构建详细的测试结果信息
	testResult := gin.H{
		"channel_id":   channel.Id,
		"channel_name": channel.Name,
		"model":        actualModel,
		"time":         consumedTime,
		"timestamp":    time.Now().Unix(),
	}

	// 为多密钥渠道添加额外信息
	if channel.MultiKeyInfo.IsMultiKey {
		testResult["is_multi_key"] = true
		testResult["used_key_index"] = usedKeyIndex
		testResult["total_keys"] = channel.MultiKeyInfo.KeyCount
	}

	if err != nil {
		testResult["success"] = false
		testResult["message"] = err.Error()

		// 增强错误提示
		if channel.MultiKeyInfo.IsMultiKey {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test failed for model '%s' on multi-key channel '%s' (using key #%d): %s",
					actualModel, channel.Name, usedKeyIndex, err.Error())
			} else {
				testResult["message"] = fmt.Sprintf("Test failed for multi-key channel '%s' with model '%s' (using key #%d): %s",
					channel.Name, actualModel, usedKeyIndex, err.Error())
			}
		} else {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test failed for model '%s' on channel '%s': %s",
					actualModel, channel.Name, err.Error())
			} else {
				testResult["message"] = fmt.Sprintf("Test failed for channel '%s' with model '%s': %s",
					channel.Name, actualModel, err.Error())
			}
		}

		logger.SysLog(fmt.Sprintf("Channel #%d (%s) test failed with model %s: %s",
			channel.Id, channel.Name, actualModel, err.Error()))
	} else {
		testResult["success"] = true

		// 增强成功提示
		if channel.MultiKeyInfo.IsMultiKey {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test succeeded for specified model '%s' on multi-key channel '%s' (using key #%d), took %.2fs",
					actualModel, channel.Name, usedKeyIndex, consumedTime)
			} else {
				testResult["message"] = fmt.Sprintf("Test succeeded for multi-key channel '%s' with model '%s' (using key #%d), took %.2fs",
					channel.Name, actualModel, usedKeyIndex, consumedTime)
			}
		} else {
			if specifiedModel != "" {
				testResult["message"] = fmt.Sprintf("Test succeeded for specified model '%s' on channel '%s', took %.2fs",
					actualModel, channel.Name, consumedTime)
			} else {
				testResult["message"] = fmt.Sprintf("Test succeeded for channel '%s' with model '%s', took %.2fs",
					channel.Name, actualModel, consumedTime)
			}
		}

		logger.SysLog(fmt.Sprintf("Channel #%d (%s) test succeeded with model %s, took %.2fs",
			channel.Id, channel.Name, actualModel, consumedTime))
	}

	c.JSON(http.StatusOK, testResult)
}

var testAllChannelsLock sync.Mutex
var testAllChannelsRunning bool = false

func testChannels(notify bool, scope string) error {
	if config.RootUserEmail == "" {
		config.RootUserEmail = model.GetRootUserEmail()
	}
	testAllChannelsLock.Lock()
	if testAllChannelsRunning {
		testAllChannelsLock.Unlock()
		return errors.New("测试已在运行中")
	}
	testAllChannelsRunning = true
	testAllChannelsLock.Unlock()
	channels, err := model.GetAllChannelsForTest(0, 0, scope)
	if err != nil {
		return err
	}
	var disableThreshold = int64(config.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // a impossible value
	}
	go func() {
		for _, channel := range channels {
			isChannelEnabled := channel.Status == common.ChannelStatusEnabled
			tik := time.Now()
			err, openaiErr, _, _ := testChannel(channel, "")
			tok := time.Now()
			milliseconds := tok.Sub(tik).Milliseconds()
			if isChannelEnabled && milliseconds > disableThreshold {
				err = fmt.Errorf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
				if config.AutomaticDisableChannelEnabled {
					monitor.DisableChannelSafelyWithStatusCode(channel.Id, channel.Name, err.Error(), "N/A (Test)", 0)
				} else {
					_ = message.Notify(message.ByAll, fmt.Sprintf("渠道 %s （%d）测试超时", channel.Name, channel.Id), "", err.Error())
				}
			}
			if isChannelEnabled && util.ShouldDisableChannel(openaiErr, -1) {
				monitor.DisableChannelSafelyWithStatusCode(channel.Id, channel.Name, err.Error(), "N/A (Test)", -1)
			}
			if !isChannelEnabled && util.ShouldEnableChannel(err, openaiErr) {
				monitor.EnableChannel(channel.Id, channel.Name)
			}
			channel.UpdateResponseTime(milliseconds)
			time.Sleep(config.RequestInterval)
		}
		testAllChannelsLock.Lock()
		testAllChannelsRunning = false
		testAllChannelsLock.Unlock()
		if notify {
			err := message.Notify(message.ByAll, "渠道测试完成", "", "渠道测试完成，如果没有收到禁用通知，说明所有渠道都正常")
			if err != nil {
				logger.SysError(fmt.Sprintf("failed to send email: %s", err.Error()))
			}
		}
	}()
	return nil
}

func TestChannels(c *gin.Context) {
	scope := c.Query("scope")
	if scope == "" {
		scope = "all"
	}
	err := testChannels(true, scope)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func AutomaticallyTestChannels(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Minute)
		logger.SysLog("testing all channels")
		_ = testChannels(false, "all")
		logger.SysLog("channel test finished")
	}
}
