package controller

// dynamic_priority_view.go — Model 视图 API。
//
// 供前端「Model」菜单页使用，两个接口：
//   - ListModelsOverview：模型汇总分页列表（一行一模型），点击进入详情
//   - ListModelChannels：单模型下挂载的渠道列表（扁平表格）
//
// 模型↔渠道的一对多关系由 abilities 表承载（(group, model, channel_id) 三元组），
// 无需新建关系表。abilities.model 有独立索引 idx_ability_model，前缀筛选命中范围扫描。
// （主键 (group, model, channel_id) 以 group 打头，跨 group 的 model 筛选靠独立索引。）

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// ModelOverviewItem 是模型汇总列表的一行。
type ModelOverviewItem struct {
	Model string `json:"model"`
	// 挂载该模型的渠道总数（跨 group）
	TotalChannels int `json:"total_channels"`
	// 其中 enabled=true 的渠道数
	EnabledChannels int `json:"enabled_channels"`
	// 该模型下渠道的最高有效分（COALESCE(NULLIF(dynamic_priority,0), priority)），
	// 用于列表展示「当前最优渠道分数」，与选渠道热路径排序口径一致
	TopDynamicPriority int64 `json:"top_dynamic_priority"`
}

// ModelChannelItem 是某 model 下单个渠道的视图项（按渠道聚合，跨 group 合并为一行）。
//
// 设计：同一渠道挂同一模型时，不同 group 在 abilities 表是独立行，但业务上
// 「渠道+模型」才是逻辑实体——priority/dynamic_priority/enabled 对所有 group 同步。
// 故详情页按 channel 聚合，groups 列出该渠道该模型挂载的所有分组。
type ModelChannelItem struct {
	ChannelId       int      `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	ChannelType     int      `json:"channel_type"`
	Groups          []string `json:"groups"`           // 该渠道该模型挂载的所有 group
	GroupCount      int      `json:"group_count"`      // group 数量
	Enabled         bool     `json:"enabled"`          // 该渠道该模型是否启用（所有 group 同步）
	ChannelStatus   int      `json:"channel_status"`   // 渠道状态（来自 channels 表）
	Priority        int64    `json:"priority"`         // 静态优先级（所有 group 同步）
	DynamicPriority int64    `json:"dynamic_priority"` // 动态优先级（所有 group 同步）
	Weight          int      `json:"weight"`
	UnitPrice       float64  `json:"unit_price"`
}

// ListModelsOverview 模型汇总分页列表。
//
// 查询逻辑：abilities JOIN channels 后按 model GROUP BY，聚合渠道数/启用数/最高分。
// 筛选：model_prefix（前缀，命中 idx_ability_model）、channel_type（渠道类型）。
// 分页：page / page_size，与渠道列表 API 口径一致。
//
// 注意 GROUP BY 后的分页必须用子查询或外层 LIMIT——这里用聚合后结果再分页，
// 即先 GROUP BY 出全部模型行，再 Offset/Limit。模型总数通常远小于渠道数（几百到几千），
// 全量 GROUP BY 后内存分页可接受；若未来模型数爆炸再改用 keyset 分页。
func ListModelsOverview(c *gin.Context) {
	modelPrefix := strings.TrimSpace(c.Query("model_prefix"))
	channelTypeStr := strings.TrimSpace(c.Query("channel_type"))
	page := parseIntSafeDefault(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	pageSize := parseIntSafeDefault(c.Query("page_size"), 10)
	if pageSize < 1 || pageSize > 500 {
		pageSize = 10
	}

	// 聚合查询：按 model 分组统计
	baseQuery := model.DB.Table("abilities a").
		Joins("JOIN channels c ON a.channel_id = c.id").
		Where("a.enabled = ?", true).
		Group("a.model")

	if modelPrefix != "" {
		baseQuery = baseQuery.Where("a.model LIKE ?", modelPrefix+"%")
	}
	if ct := parseIntSafe(channelTypeStr); ct > 0 {
		baseQuery = baseQuery.Where("c.type = ?", ct)
	}

	// 先取总数（distinct model 数）。Group 后 Count 需要 .Count(&n) 配合，gorm 会包一层子查询。
	var total int64
	countQuery := model.DB.Table("abilities a").
		Joins("JOIN channels c ON a.channel_id = c.id").
		Where("a.enabled = ?", true)
	if modelPrefix != "" {
		countQuery = countQuery.Where("a.model LIKE ?", modelPrefix+"%")
	}
	if ct := parseIntSafe(channelTypeStr); ct > 0 {
		countQuery = countQuery.Where("c.type = ?", ct)
	}
	if err := countQuery.Distinct("a.model").Count(&total).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "failed to count models: " + err.Error(),
		})
		return
	}

	type overviewRow struct {
		Model              string `gorm:"column:model"`
		TotalChannels      int    `gorm:"column:total_channels"`
		EnabledChannels    int    `gorm:"column:enabled_channels"`
		TopDynamicPriority int64  `gorm:"column:top_dynamic_priority"`
	}
	// 用 COALESCE(NULLIF(dynamic_priority,0), priority) 作为有效分，与选渠道热路径一致。
	selectSQL :=
		"a.model AS model, " +
			"COUNT(*) AS total_channels, " +
			"SUM(CASE WHEN c.status = ? AND a.enabled = ? THEN 1 ELSE 0 END) AS enabled_channels, " +
			"MAX(COALESCE(NULLIF(a.dynamic_priority, 0), COALESCE(a.priority, 0))) AS top_dynamic_priority"
	var rows []overviewRow
	err := baseQuery.
		Select(selectSQL, common.ChannelStatusEnabled, true).
		Order("top_dynamic_priority DESC, a.model ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "failed to list models: " + err.Error(),
		})
		return
	}

	items := make([]ModelOverviewItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ModelOverviewItem{
			Model:              r.Model,
			TotalChannels:      r.TotalChannels,
			EnabledChannels:    r.EnabledChannels,
			TopDynamicPriority: r.TopDynamicPriority,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":     items,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
		},
	})
}

// ListModelChannels 单个模型下挂载的所有渠道（跨 group），扁平列表。
//
// 用于模型详情页。model 参数必填（精确匹配），channel_type 可选筛选。
// 渠道按有效分 DESC 排序（与选渠道热路径一致）。
func ListModelChannels(c *gin.Context) {
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "model 参数必填",
		})
		return
	}
	channelTypeStr := strings.TrimSpace(c.Query("channel_type"))

	groupCol := "`group`"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
	}

	// groups 聚合函数：MySQL/SQLite 用 GROUP_CONCAT，PostgreSQL 用 string_agg
	groupsExpr := "GROUP_CONCAT(a." + groupCol + ")"
	if common.UsingPostgreSQL {
		groupsExpr = "STRING_AGG(a." + groupCol + ", ',')"
	}

	query := model.DB.Table("abilities a").
		Joins("JOIN channels c ON a.channel_id = c.id").
		Select(
			"a.channel_id AS channel_id, c.name AS channel_name, "+
				"c.type AS channel_type, "+groupsExpr+" AS groups, "+
				"COUNT(*) AS group_count, "+
				"MAX(a.enabled) AS enabled, "+
				"c.status AS channel_status, "+
				"MAX(COALESCE(a.priority, 0)) AS priority, "+
				"MAX(COALESCE(a.dynamic_priority, 0)) AS dynamic_priority, "+
				"MAX(COALESCE(c.weight, 0)) AS weight, MAX(COALESCE(c.unit_price, 0)) AS unit_price",
		).
		Where("a.model = ?", modelName).
		Group("a.channel_id, c.name, c.type, c.status").
		Order("MAX(COALESCE(NULLIF(a.dynamic_priority, 0), COALESCE(a.priority, 0))) DESC, MAX(COALESCE(a.priority, 0)) DESC, a.channel_id ASC")

	if ct := parseIntSafe(channelTypeStr); ct > 0 {
		query = query.Where("c.type = ?", ct)
	}

	type row struct {
		ChannelId       int     `gorm:"column:channel_id"`
		ChannelName     string  `gorm:"column:channel_name"`
		ChannelType     int     `gorm:"column:channel_type"`
		Groups          string  `gorm:"column:groups"`
		GroupCount      int     `gorm:"column:group_count"`
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

	items := make([]ModelChannelItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ModelChannelItem{
			ChannelId:       r.ChannelId,
			ChannelName:     r.ChannelName,
			ChannelType:     r.ChannelType,
			Groups:          splitGroups(r.Groups),
			GroupCount:      r.GroupCount,
			Enabled:         r.Enabled,
			ChannelStatus:   r.ChannelStatus,
			Priority:        r.Priority,
			DynamicPriority: r.DynamicPriority,
			Weight:          r.Weight,
			UnitPrice:       r.UnitPrice,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    items,
	})
}

// splitGroups 把 GROUP_CONCAT 的逗号分隔结果拆成切片，去空白去空。
func splitGroups(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// UpdateModelChannelPriority 同步更新某渠道某模型下所有 group 行的静态优先级。
//
// 「所有 group 同步」是业务约束：渠道+模型是逻辑实体，priority 对各 group 一致。
// 一次 UPDATE 覆盖该 (channel_id, model) 的全部行，天然同步。
//
// 注意：abilities.priority 是 (channel,model) 级副本，与 channel.priority（渠道级单值）
// 不同。编辑渠道（UpdateChannel→UpdateAbilities 重建）会用 channel.priority 覆盖本值——
// 这是 abilities 作为副本的固有特性，非本次引入。若需根治需改数据模型，见计划文档。
func UpdateModelChannelPriority(c *gin.Context) {
	var req struct {
		ChannelId int    `json:"channel_id" binding:"required"`
		Model     string `json:"model" binding:"required"`
		Priority  int64  `json:"priority"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误: " + err.Error()})
		return
	}

	// 同步更新该渠道该模型所有 group 行
	result := model.DB.Model(&model.Ability{}).
		Where("channel_id = ? AND model = ?", req.ChannelId, req.Model).
		Update("priority", req.Priority)
	if result.Error != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "更新失败: " + result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "",
		"affected": result.RowsAffected,
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

// parseIntSafeDefault 解析整数，失败返回 def。
func parseIntSafeDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := parseIntSafe(s)
	if n == 0 {
		return def
	}
	return n
}
