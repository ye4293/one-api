package controller

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range common.GroupRatio {
		groupNames = append(groupNames, groupName)
	}

	// 对groupNames按字母顺序进行排序
	sort.Strings(groupNames)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}
