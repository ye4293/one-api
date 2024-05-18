package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/midjourney"
	"github.com/songquanpeng/one-api/relay/util"
)

func RelayMidjourneyImage(c *gin.Context) {
	taskId := c.Param("id")
	midjourneyTask := model.GetByOnlyMJId(taskId)
	if midjourneyTask == nil {
		c.JSON(400, gin.H{
			"error": "midjourney_task_not_found",
		})
		return
	}
	resp, err := http.Get(midjourneyTask.ImageUrl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "http_get_image_failed",
		})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{
			"error": string(responseBody),
		})
		return
	}
	// 从Content-Type头获取MIME类型
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		// 如果无法确定内容类型，则默认为jpeg
		contentType = "image/jpeg"
	}
	// 设置响应的内容类型
	c.Writer.Header().Set("Content-Type", contentType)
	// 将图片流式传输到响应体
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		log.Println("Failed to stream image:", err)
	}
	return
}

func UpdateMidjourneyTaskBulk() {
	//imageModel := "midjourney"
	ctx := context.TODO()
	for {
		time.Sleep(time.Duration(15) * time.Second)

		tasks := model.GetAllUnFinishTasks()
		if len(tasks) == 0 {
			continue
		}

		logger.Info(ctx, fmt.Sprintf("检测到未完成的任务数有: %v", len(tasks)))
		taskChannelM := make(map[int][]string)
		taskM := make(map[string]*model.Midjourney)
		nullTaskIds := make([]int, 0)
		for _, task := range tasks {
			if task.MjId == "" {
				// 统计失败的未完成任务
				nullTaskIds = append(nullTaskIds, task.Id)
				continue
			}
			taskM[task.MjId] = task
			taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task.MjId)
		}
		if len(nullTaskIds) > 0 {
			err := model.MjBulkUpdateByTaskIds(nullTaskIds, map[string]any{
				"status":   "FAILURE",
				"progress": "100%",
			})
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Fix null mj_id task error: %v", err))
			} else {
				logger.Info(ctx, fmt.Sprintf("Fix null mj_id task success: %v", nullTaskIds))
			}
		}
		if len(taskChannelM) == 0 {
			continue
		}

		for channelId, taskIds := range taskChannelM {
			logger.Info(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
			if len(taskIds) == 0 {
				continue
			}
			midjourneyChannel, err := model.CacheGetChannel(channelId)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("CacheGetChannel: %v", err))
				err := model.MjBulkUpdate(taskIds, map[string]any{
					"fail_reason": fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId),
					"status":      "FAILURE",
					"progress":    "100%",
				})
				if err != nil {
					logger.Info(ctx, fmt.Sprintf("UpdateMidjourneyTask error: %v", err))
				}
				continue
			}
			requestUrl := fmt.Sprintf("%s/mj/task/list-by-condition", *midjourneyChannel.BaseURL)

			body, _ := json.Marshal(map[string]any{
				"ids": taskIds,
			})
			req, err := http.NewRequest("POST", requestUrl, bytes.NewBuffer(body))
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Get Task error: %v", err))
				continue
			}
			// 设置超时时间
			timeout := time.Second * 5
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			// 使用带有超时的 context 创建新的请求
			req = req.WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("mj-api-secret", midjourneyChannel.Key)
			resp, err := util.GetHttpClient().Do(req)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Get Task Do req error: %v", err))
				continue
			}
			if resp.StatusCode != http.StatusOK {
				logger.Error(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
				continue
			}
			responseBody, err := io.ReadAll(resp.Body)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Get Task parse body error: %v", err))
				continue
			}
			var responseItems []midjourney.MidjourneyDto
			err = json.Unmarshal(responseBody, &responseItems)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Get Task parse body error2: %v, body: %s", err, string(responseBody)))
				continue
			}
			resp.Body.Close()
			req.Body.Close()
			cancel()

			for _, responseItem := range responseItems {
				task := taskM[responseItem.MjId]

				useTime := (time.Now().UnixNano() / int64(time.Millisecond)) - task.SubmitTime
				// 如果时间超过一小时，且进度不是100%，则认为任务失败
				if useTime > 3600000 && task.Progress != "100%" {
					responseItem.FailReason = "上游任务超时（超过1小时）"
					responseItem.Status = "FAILURE"
				}
				if !checkMjTaskNeedUpdate(task, responseItem) {
					continue
				}
				task.Code = 1
				task.Progress = responseItem.Progress
				task.PromptEn = responseItem.PromptEn
				task.State = responseItem.State
				task.SubmitTime = responseItem.SubmitTime
				task.StartTime = responseItem.StartTime
				task.FinishTime = responseItem.FinishTime
				task.ImageUrl = responseItem.ImageUrl
				task.Status = responseItem.Status
				task.FailReason = responseItem.FailReason
				if responseItem.Properties != nil {
					propertiesStr, _ := json.Marshal(responseItem.Properties)
					task.Properties = string(propertiesStr)
				}
				if responseItem.Buttons != nil {
					buttonStr, _ := json.Marshal(responseItem.Buttons)
					task.Buttons = string(buttonStr)
				}

				if task.Progress != "100%" && responseItem.FailReason != "" {
					logger.Info(ctx, task.MjId+" 构建失败，"+task.FailReason)
					task.Progress = "100%"
					err = model.CacheUpdateUserQuota2(task.UserId)
					if err != nil {
						logger.Error(ctx, "error update user quota cache: "+err.Error())
					} else {
						quota := task.Quota
						if quota != 0 {
							err = model.IncreaseUserQuota(task.UserId, quota)
							if err != nil {
								logger.Error(ctx, "fail to increase user quota: "+err.Error())
							}
							logContent := fmt.Sprintf("构图失败 %s，补偿 %s", task.MjId, common.LogQuota(quota))
							model.RecordLog(task.UserId, model.LogTypeSystem, logContent)
						}
					}
				}
				if task.Progress == "100%" && config.CfR2storeEnabled {
					objectKey := task.MjId
					imageData, err := DownloadImage(task.ImageUrl)
					if err != nil {
						logger.SysLog(fmt.Sprintf("err:%+v\n", err))
					}
					r2Url, err := UploadToR2WithURL(ctx, imageData, config.CfBucketImageName, objectKey, config.CfImageAccessKey, config.CfImageSecretKey, config.CfImageEndpoint)
					if err != nil {
						logger.SysLog(fmt.Sprintf("err:%+v\n", err))
					}
					task.StoreUrl = r2Url
				}
				err = task.Update()
				if err != nil {
					logger.Error(ctx, "UpdateMidjourneyTask task error: "+err.Error())
				}
			}
		}
	}
}

func checkMjTaskNeedUpdate(oldTask *model.Midjourney, newTask midjourney.MidjourneyDto) bool {
	if oldTask.Code != 1 {
		return true
	}
	if oldTask.Progress != newTask.Progress {
		return true
	}
	if oldTask.PromptEn != newTask.PromptEn {
		return true
	}
	if oldTask.State != newTask.State {
		return true
	}
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if oldTask.ImageUrl != newTask.ImageUrl {
		return true
	}
	if oldTask.Status != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if oldTask.Progress != "100%" && newTask.FailReason != "" {
		return true
	}

	return false
}

func GetAllMidjourney(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page
	// 解析其他查询参数
	queryParams := model.TaskQueryParams{
		ChannelID:      c.Query("channel_id"),
		UserName:       c.Query("username"),
		MjID:           c.Query("mj_id"),
		StartTimestamp: c.Query("start_timestamp"),
		EndTimestamp:   c.Query("end_timestamp"),
	}

	logs, total, err := model.GetAllTask(page, pagesize, queryParams)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        logs,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
}

func GetUserMidjourney(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 0 {
		page = 0
	}
	pagesize, _ := strconv.Atoi(c.Query("pagesize"))
	currentPage := page

	userId := c.GetInt("id")
	log.Printf("userId = %d \n", userId)

	queryParams := model.TaskQueryParams{
		MjID:           c.Query("mj_id"),
		StartTimestamp: c.Query("start_timestamp"),
		EndTimestamp:   c.Query("end_timestamp"),
	}

	logs, total, err := model.GetAllUserTask(userId, page, pagesize, queryParams)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if !strings.Contains(config.ServerAddress, "localhost") {
		for i, midjourney := range logs {
			midjourney.ImageUrl = config.ServerAddress + "/mj/image/" + midjourney.MjId
			logs[i] = midjourney
		}
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        logs,
			"currentPage": currentPage,
			"pageSize":    pagesize,
			"total":       total,
		},
	})
}
