package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
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

var (
	maxConcurrentGoroutines = 60 // 可以根据需要调整
)

func UpdateMidjourneyTaskBulk() {
	defer func() {
		if r := recover(); r != nil {
			logger.SysError(fmt.Sprintf("UpdateMidjourneyTaskBulk panic recovered: %v\nStack: %s", r, debug.Stack()))
		}
	}()

	logger.Info(context.Background(), "Starting UpdateMidjourneyTaskBulk routine")

	for {
		ctx := context.Background()
		iterationStartTime := time.Now()

		logger.Info(ctx, "Waiting for 10 seconds before next iteration")
		time.Sleep(time.Duration(10) * time.Second)

		tasks, err := fetchUnfinishedTasks(ctx)
		if err != nil {
			continue
		}

		if len(tasks) == 0 {
			logger.Info(ctx, "No unfinished tasks found")
			continue
		}

		taskChannelM, taskM, nullTaskIds := processTasksData(ctx, tasks)

		if len(nullTaskIds) > 0 {
			updateNullTasks(ctx, nullTaskIds)
		}

		if len(taskChannelM) == 0 {
			logger.Info(ctx, "No tasks to process after filtering")
			continue
		}

		processTasks(ctx, taskChannelM, taskM)

		logger.Info(ctx, fmt.Sprintf("Iteration completed. Total time: %v", time.Since(iterationStartTime)))
	}
}

func fetchUnfinishedTasks(ctx context.Context) ([]*model.Midjourney, error) {
	startTime := time.Now()
	logger.Info(ctx, "Fetching unfinished tasks")
	tasks, err := safeGetAllUnFinishTasks()
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Error getting unfinished tasks: %v", err))
		return nil, err
	}
	logger.Info(ctx, fmt.Sprintf("Fetched %d unfinished tasks in %v", len(tasks), time.Since(startTime)))
	return tasks, nil
}

func processTasksData(ctx context.Context, tasks []*model.Midjourney) (map[int][]string, map[string]*model.Midjourney, []int) {
	logger.Info(ctx, "Processing tasks data")
	startTime := time.Now()
	taskChannelM := make(map[int][]string)
	taskM := make(map[string]*model.Midjourney)
	nullTaskIds := make([]int, 0)
	for _, task := range tasks {
		if task.MjId == "" {
			nullTaskIds = append(nullTaskIds, task.Id)
			continue
		}
		taskM[task.MjId] = task
		taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task.MjId)
	}
	logger.Info(ctx, fmt.Sprintf("Processed tasks data in %v. Channels: %d, Null tasks: %d", time.Since(startTime), len(taskChannelM), len(nullTaskIds)))
	return taskChannelM, taskM, nullTaskIds
}

func updateNullTasks(ctx context.Context, nullTaskIds []int) {
	logger.Info(ctx, fmt.Sprintf("Updating %d tasks with null MjId", len(nullTaskIds)))
	startTime := time.Now()
	err := model.MjBulkUpdateByTaskIds(nullTaskIds, map[string]any{
		"status":   "FAILURE",
		"progress": "100%",
	})
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Fix null mj_id task error: %v", err))
	} else {
		logger.Info(ctx, fmt.Sprintf("Fixed %d null mj_id tasks in %v", len(nullTaskIds), time.Since(startTime)))
	}
}

func processTasks(ctx context.Context, taskChannelM map[int][]string, taskM map[string]*model.Midjourney) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrentGoroutines)
	logger.Info(ctx, fmt.Sprintf("Processing tasks with %d goroutines", maxConcurrentGoroutines))

	for channelId, taskIds := range taskChannelM {
		wg.Add(1)
		go func(channelId int, taskIds []string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			processChannelTasks(ctx, channelId, taskIds, taskM)
		}(channelId, taskIds)
	}

	wg.Wait()
	logger.Info(ctx, "All channel tasks processed")
}

func processChannelTasks(ctx context.Context, channelId int, taskIds []string, taskM map[string]*model.Midjourney) {
	channelStartTime := time.Now()
	logger.Info(ctx, fmt.Sprintf("Start processing channel #%d, task count: %d", channelId, len(taskIds)))

	midjourneyChannel, err := safeGetChannel(channelId)
	if err != nil {
		handleChannelError(ctx, channelId, taskIds, err)
		return
	}

	responseItems, err := fetchTasksFromAPI(ctx, midjourneyChannel, taskIds)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Channel #%d API error: %v", channelId, err))
		return
	}

	updateTasks(ctx, channelId, responseItems, taskM)

	logger.Info(ctx, fmt.Sprintf("Finished processing channel #%d in %v", channelId, time.Since(channelStartTime)))
}

func handleChannelError(ctx context.Context, channelId int, taskIds []string, err error) {
	logger.Error(ctx, fmt.Sprintf("CacheGetChannel error for channel #%d: %v", channelId, err))
	updateErr := model.MjBulkUpdate(taskIds, map[string]any{
		"fail_reason": fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId),
		"status":      "FAILURE",
		"progress":    "100%",
	})
	if updateErr != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to update tasks for channel #%d: %v", channelId, updateErr))
	}
}

func fetchTasksFromAPI(ctx context.Context, midjourneyChannel *model.Channel, taskIds []string) ([]midjourney.MidjourneyDto, error) {
	requestUrl := fmt.Sprintf("%s/mj/task/list-by-condition", *midjourneyChannel.BaseURL)

	requestPrepStartTime := time.Now()
	body, _ := json.Marshal(map[string]any{"ids": taskIds})
	req, err := http.NewRequest("POST", requestUrl, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request error: %v", err)
	}
	logger.Info(ctx, fmt.Sprintf("Request preparation took %v", time.Since(requestPrepStartTime)))

	reqCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()
	req = req.WithContext(reqCtx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mj-api-secret", midjourneyChannel.Key)

	apiCallStartTime := time.Now()
	logger.Info(ctx, fmt.Sprintf("Sending request to %s", requestUrl))
	resp, err := util.GetHttpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("API call error: %v", err)
	}
	defer resp.Body.Close()
	logger.Info(ctx, fmt.Sprintf("API call took %v", time.Since(apiCallStartTime)))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned non-OK status: %d", resp.StatusCode)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body error: %v", err)
	}

	var responseItems []midjourney.MidjourneyDto
	err = json.Unmarshal(responseBody, &responseItems)
	if err != nil {
		return nil, fmt.Errorf("unmarshal response error: %v, body: %s", err, string(responseBody))
	}

	return responseItems, nil
}

func updateTasks(ctx context.Context, channelId int, responseItems []midjourney.MidjourneyDto, taskM map[string]*model.Midjourney) {
	logger.Info(ctx, fmt.Sprintf("Updating %d tasks for channel #%d", len(responseItems), channelId))
	for _, responseItem := range responseItems {
		task, exists := taskM[responseItem.MjId]
		if !exists {
			logger.Warn(ctx, fmt.Sprintf("Task %s not found in taskM for channel #%d", responseItem.MjId, channelId))
			continue
		}

		updateTask(ctx, task, responseItem)
	}
}

func updateTask(ctx context.Context, task *model.Midjourney, responseItem midjourney.MidjourneyDto) {
	useTime := (time.Now().UnixNano() / int64(time.Millisecond)) - task.SubmitTime
	if useTime > 3600000 && task.Progress != "100%" {
		responseItem.FailReason = "上游任务超时（超过1小时）"
		responseItem.Status = "FAILURE"
	}

	if !checkMjTaskNeedUpdate(task, responseItem) {
		logger.Info(ctx, fmt.Sprintf("Task %s does not need update", task.MjId))
		return
	}

	logger.Info(ctx, fmt.Sprintf("Updating task %s", task.MjId))
	updateTaskFields(task, responseItem)

	if (task.Progress != "100%" && responseItem.FailReason != "") || responseItem.FailReason == "未知集成" {
		handleFailedTask(ctx, task)
	}

	if task.Progress == "100%" && config.CfR2storeEnabled {
		uploadImageToR2(ctx, task)
	}

	err := task.Update()
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to update task %s: %v", task.MjId, err))
	} else {
		logger.Info(ctx, fmt.Sprintf("Successfully updated task %s", task.MjId))
	}
}

func updateTaskFields(task *model.Midjourney, responseItem midjourney.MidjourneyDto) {
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
}

func handleFailedTask(ctx context.Context, task *model.Midjourney) {
	logger.Info(ctx, fmt.Sprintf("%s 构建失败，%s", task.MjId, task.FailReason))
	task.Progress = "100%"
	err := model.CacheUpdateUserQuota2(task.UserId)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Error updating user quota cache: %v", err))
	} else {
		quota := task.Quota
		if quota != 0 {
			err = model.IncreaseUserQuota(task.UserId, quota)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Failed to increase user quota: %v", err))
			}
			logContent := fmt.Sprintf("构图失败 %s，补偿 %s", task.MjId, common.LogQuota(quota))
			model.RecordLog(task.UserId, model.LogTypeSystem, logContent)
		}
	}
}

func uploadImageToR2(ctx context.Context, task *model.Midjourney) {
	logger.Info(ctx, fmt.Sprintf("Uploading image for task %s to R2", task.MjId))
	objectKey := task.MjId
	uploadCtx, uploadCancel := context.WithTimeout(ctx, time.Minute*10)
	defer uploadCancel()

	imageData, err := DownloadImage(task.ImageUrl)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to download image for task %s: %v", task.MjId, err))
		return
	}

	r2Url, err := UploadToR2WithURL(uploadCtx, imageData, config.CfBucketImageName, objectKey, config.CfImageAccessKey, config.CfImageSecretKey, config.CfImageEndpoint)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("Failed to upload image to R2 for task %s: %v", task.MjId, err))
		return
	}

	task.StoreUrl = r2Url
	logger.Info(ctx, fmt.Sprintf("Successfully uploaded image to R2 for task %s", task.MjId))
}

func safeGetAllUnFinishTasks() (tasks []*model.Midjourney, err error) {
	defer func() {
		if r := recover(); r != nil {
			tasks = nil
			err = fmt.Errorf("panic in GetAllUnFinishTasks: %v", r)
		}
	}()
	tasks = model.GetAllUnFinishTasks()
	return tasks, nil
}

func safeGetChannel(channelId int) (*model.Channel, error) {
	var channel *model.Channel
	var err error
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in GetChannel: %v", r)
		}
	}()
	channel, err = model.CacheGetChannel(channelId)
	return channel, err
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
		Type:           c.Query("type"),
		Status:         c.Query("status"),
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
		Type:           c.Query("type"),
		Status:         c.Query("status"),
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
