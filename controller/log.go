package controller

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
)

// adminInfoRegex 匹配 ezlinkai 分号格式的 adminInfo:[...] 段
// 渠道历史是 []int，不会出现嵌套 ']'
var adminInfoRegex = regexp.MustCompile(`adminInfo:\[[^\]]*\];?`)

// stripAdminInfoFromLogs 从普通用户日志的 other 字段里剥离渠道链路信息（adminInfo）
// 用于 /api/log/self 等用户接口，避免向终端用户暴露后端渠道 ID
//
// 兼容两种 other 格式：
//   - 分号格式: "adminInfo:[1,2];usageDetails:{...};..."
//   - JSON 格式: {"admin_info":[...],"usageDetails":{...},...}
//
// fast-path: 不含 "admin" 子串直接跳过，避免对 99% 普通日志做无效 regex
func stripAdminInfoFromLogs(logs []*model.Log) {
	for _, log := range logs {
		if log == nil || log.Other == "" {
			continue
		}
		// fast-path: 没有 admin 关键字 → 跳过
		if !strings.Contains(log.Other, "admin") {
			continue
		}

		other := log.Other
		// JSON 格式：以 '{' 开头时尝试解析
		if other[0] == '{' {
			var obj map[string]any
			if err := json.Unmarshal([]byte(other), &obj); err == nil {
				if _, ok1 := obj["admin_info"]; ok1 {
					delete(obj, "admin_info")
				}
				if _, ok2 := obj["adminInfo"]; ok2 {
					delete(obj, "adminInfo")
				}
				if b, err := json.Marshal(obj); err == nil {
					log.Other = string(b)
				}
				continue
			}
			// JSON 解析失败，落回分号格式处理
		}

		// 分号格式
		cleaned := adminInfoRegex.ReplaceAllString(other, "")
		// 避免出现 ";;" 残留
		cleaned = strings.ReplaceAll(cleaned, ";;", ";")
		log.Other = strings.Trim(cleaned, ";")
	}
}

func GetAllLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	xRequestId := c.Query("x_request_id")
	xResponseId := c.Query("x_response_id")
	channel, _ := strconv.Atoi(c.Query("channel"))
	logs, total, err := model.GetCurrentAllLogsAndCount(logType, startTimestamp, endTimestamp, modelName, username, tokenName, xRequestId, xResponseId, page, pagesize, channel)
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
		"data": gin.H{
			"list":        logs,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}

func GetUserLogs(c *gin.Context) {
	pageStr := c.Query("page")
	pageSizeStr := c.Query("pagesize")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pagesize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pagesize <= 0 {
		pagesize = 10
	}
	currentPage := page

	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	xRequestId := c.Query("x_request_id")
	xResponseId := c.Query("x_response_id")
	logs, total, err := model.GetCurrentUserLogsAndCount(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, xRequestId, xResponseId, page, pagesize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	stripAdminInfoFromLogs(logs)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        logs,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
	return
}

func SearchAllLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	logs, err := model.SearchAllLogs(keyword)
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
		"data":    logs,
	})
	return
}

func SearchUserLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	userId := c.GetInt("id")
	logs, err := model.SearchUserLogs(userId, keyword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	stripAdminInfoFromLogs(logs)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
	return
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	quotaNum := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel)
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum,
			//"token": tokenNum,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	quotaNum := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel)
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum,
			//"token": tokenNum,
		},
	})
	return
}

func DeleteHistoryLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}
	count, err := model.DeleteOldLog(targetTimestamp)
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
		"data":    count,
	})
	return
}

// parseTimeBucket 将前端传入的时间粒度字符串转换为秒数
func parseTimeBucket(bucket string) int64 {
	switch bucket {
	case "15m":
		return 900
	case "1h":
		return 3600
	default: // "5m" 或其他
		return 300
	}
}

func GetLogsPerformanceStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	bucketSeconds := parseTimeBucket(c.Query("time_bucket"))

	if startTimestamp == 0 || endTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}

	result, err := model.GetPerformanceStat(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, bucketSeconds)
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
		"data":    result,
	})
}

func GetLogsSelfPerformanceStat(c *gin.Context) {
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	bucketSeconds := parseTimeBucket(c.Query("time_bucket"))

	if startTimestamp == 0 || endTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "start_timestamp and end_timestamp are required",
		})
		return
	}

	result, err := model.GetUserPerformanceStat(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, channel, bucketSeconds)
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
		"data":    result,
	})
}
