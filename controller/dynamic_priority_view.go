package controller

// dynamic_priority_view.go — Model → 渠道挂载视图 API。
//
// 供前端「Model」菜单页使用：展示所有模型，及每个模型下挂载的渠道，含动态优先级/权重/状态。
// 支持按渠道类型、模型名前缀筛选。
//
// 查询走 abilities JOIN channels。abilities.model 有独立索引 idx_ability_model，
// 前缀筛选（LIKE 'prefix%'）能命中索引范围扫描，避免全表扫描。
// （主键是 (group, model, channel_id)，group 打头，故 model 筛选必须靠独立索引。）

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// ModelChannelItem 是某 model 下单个渠道的视图项。
type ModelChannelItem struct {
	ChannelId       int    `json:"channel_id"`
	ChannelName     string `json:"channel_name"`
	ChannelType     int    `json:"channel_type"`
	Group           string `json:"group"`
	Enabled         bool   `json:"enabled"`
	ChannelStatus   int    `json:"channel_status"`
	Priority        int64  `json:"priority"`
	DynamicPriority int64  `json:"dynamic_priority"`
	Weight          int    `json:"weight"`
	UnitPrice       float64 `json:"unit_price"`
}

// ModelChannelGroup 是单个 model 及其下挂渠道列表。
type ModelChannelGroup struct {
	Model     string             `json:"model"`
	Channels  []ModelChannelItem `json:"channels"`
	// 汇总：便于前端展示「该模型挂载 N 个渠道，M 个启用」
	TotalChannels    int `json:"total_channels"`
	EnabledChannels  int `json:"enabled_channels"`
}

// ListModelChannels 列出模型→渠道挂载关系，支持 model_prefix / channel_type 筛选。
//
// 查询逻辑：
//  1. abilities JOIN channels，按 model 前缀 + 渠道类型过滤
//  2. 按 model 分组聚合渠道
//  3. 渠道在组内按 dynamic_priority DESC, priority DESC 排序（与选渠道热路径排序一致）
//
// 性能：abilities.model 有独立索引，前缀筛选走索引；结果集通常较小，组内排序可接受。
func ListModelChannels(c *gin.Context) {
	modelPrefix := strings.TrimSpace(c.Query("model_prefix"))
	channelTypeStr := strings.TrimSpace(c.Query("channel_type"))

	groupCol := "`group`"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
	}

	// 用原生查询构造，避免 gorm struct 映射 group 列的歧义。
	// COALESCE(NULLIF(dynamic_priority,0), priority)：与选渠道热路径一致的「有效分」，
	// 让无评分渠道回退到静态 priority 参与排序展示。
	query := model.DB.Table("abilities a").
		Joins("JOIN channels c ON a.channel_id = c.id").
		Select(
			"a.model AS model, a.channel_id AS channel_id, c.name AS channel_name, "+
				"c.type AS channel_type, a."+groupCol+" AS `group`, a.enabled AS enabled, "+
				"c.status AS channel_status, COALESCE(a.priority, 0) AS priority, "+
				"COALESCE(a.dynamic_priority, 0) AS dynamic_priority, "+
				"COALESCE(c.weight, 0) AS weight, COALESCE(c.unit_price, 0) AS unit_price",
		).
		Order("a.model ASC, COALESCE(NULLIF(a.dynamic_priority, 0), COALESCE(a.priority, 0)) DESC, COALESCE(a.priority, 0) DESC")

	if modelPrefix != "" {
		// 前缀匹配，命中 idx_ability_model 索引范围扫描
		query = query.Where("a.model LIKE ?", modelPrefix+"%")
	}
	if channelTypeStr != "" {
		// 渠道类型筛选；非法值由 strconv 兜底，不报错
		if ct := parseIntSafe(channelTypeStr); ct > 0 {
			query = query.Where("c.type = ?", ct)
		}
	}

	type row struct {
		Model           string  `gorm:"column:model"`
		ChannelId       int     `gorm:"column:channel_id"`
		ChannelName     string  `gorm:"column:channel_name"`
		ChannelType     int     `gorm:"column:channel_type"`
		Group           string  `gorm:"column:group"`
		Enabled         bool    `gorm:"column:enabled"`
		ChannelStatus   int     `gorm:"column:channel_status"`
		Priority        int64   `gorm:"column:priority"`
		DynamicPriority int64   `gorm:"column:dynamic_priority"`
		Weight          int     `gorm:"column:weight"`
		UnitPrice       float64 `gorm:"column:unit_price"`
	}
	var rows []row
	if err := query.Scan(&rows).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "failed to list model channels: " + err.Error(),
		})
		return
	}

	// 按 model 分组
	groupMap := make(map[string]*ModelChannelGroup)
	var order []string // 保序：按首次出现的 model 顺序（已按 model ASC 排，故天然有序）
	for _, r := range rows {
		g, ok := groupMap[r.Model]
		if !ok {
			g = &ModelChannelGroup{Model: r.Model}
			groupMap[r.Model] = g
			order = append(order, r.Model)
		}
		item := ModelChannelItem{
			ChannelId:       r.ChannelId,
			ChannelName:     r.ChannelName,
			ChannelType:     r.ChannelType,
			Group:           r.Group,
			Enabled:         r.Enabled,
			ChannelStatus:   r.ChannelStatus,
			Priority:        r.Priority,
			DynamicPriority: r.DynamicPriority,
			Weight:          r.Weight,
			UnitPrice:       r.UnitPrice,
		}
		g.Channels = append(g.Channels, item)
		g.TotalChannels++
		if r.Enabled {
			g.EnabledChannels++
		}
	}

	groups := make([]ModelChannelGroup, 0, len(order))
	for _, m := range order {
		groups = append(groups, *groupMap[m])
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groups,
	})
}

// parseIntSafe 容错解析整数，失败返回 0。
func parseIntSafe(s string) int {
	var n int
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*10 + int(s[i]-'0')
		if n > 1<<31 {
			return 0
		}
	}
	return n
}
