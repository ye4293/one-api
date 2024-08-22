package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
)

func RelayReplicateImage(c *gin.Context) {
	id := c.Param("id")
	channelId := model.GetChannelIdByFluxId(id)
	channel, err := model.GetChannelById(channelId, true)
	url := fmt.Sprintf("%s/v1/predictions/%s", *channel.BaseURL, id)

	// 创建一个新的请求
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	// 设置必要的header
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch image"})
		return
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		c.JSON(resp.StatusCode, gin.H{"error": string(responseBody)})
		return
	}

	// 解析响应体中的 JSON 数据
	var responseData map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&responseData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse response"})
		return
	}

	// 获取图片 URL
	output := responseData["output"].([]interface{})
	imageURL := output[0].(string)

	// 获取图片数据
	imageResp, err := http.Get(imageURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch image"})
		return
	}
	defer imageResp.Body.Close()

	// 设置响应的内容类型为图片类型
	c.Writer.Header().Set("Content-Type", "image/jpeg")

	// 将图片流式传输到响应体
	_, err = io.Copy(c.Writer, imageResp.Body)
	if err != nil {
		log.Println("Failed to stream image:", err)
	}
}
