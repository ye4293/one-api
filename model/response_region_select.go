package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
)

// GetAzureChannelRegion 解析渠道 Config JSON 里的 region 字段。
// CRM 创建 Azure 渠道时会写入 region；解析失败返回空串。
// config.region 对 Bedrock 渠道同样适用，此处仅按字段名解析，不区分渠道类型。
func GetAzureChannelRegion(ch *Channel) string {
	if ch == nil || ch.Config == "" {
		return ""
	}
	var cfg ChannelConfig
	if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Region)
}

// selectChannelByRegion 在同 region 的可用渠道中做加权随机选择（L2 兜底）。
// 供 CacheGetRandomSatisfiedChannel 在「response_id 缓存渠道已失效但记录了区域」时调用：
// Azure encrypted_content / previous_response_id 绑定产生它的资源区域，
// 跨区域必然 400，因此宁可放弃优先级也要先在同区域里找。
// 找不到同区域渠道时返回 nil（fail-open，由调用方退回正常随机选渠）。
func selectChannelByRegion(ctx context.Context, region string, group string, model string, excludeIds []int) (*Channel, int) {
	if region == "" {
		return nil, -1
	}

	groupCol := "`group`"
	trueVal := "1"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
		trueVal = "true"
	}

	// 查询该 group+model 下所有启用渠道（不带优先级过滤——区域正确性优先于优先级）
	var channels []Channel
	channelQuery := DB.Table("channels").
		Joins("JOIN abilities ON channels.id = abilities.channel_id").
		Where("abilities."+groupCol+" = ? AND abilities.model = ? AND abilities.enabled = ? AND channels.status = ?",
			group, model, trueVal, common.ChannelStatusEnabled)
	if len(excludeIds) > 0 {
		channelQuery = channelQuery.Where("channels.id NOT IN ?", excludeIds)
	}
	if err := channelQuery.Find(&channels).Error; err != nil || len(channels) == 0 {
		return nil, -1
	}

	// 与 Azure 重试保持一致，区域名称忽略大小写。
	var matched []*Channel
	for i := range channels {
		ch := &channels[i]
		if strings.EqualFold(GetAzureChannelRegion(ch), region) {
			matched = append(matched, ch)
		}
	}
	if len(matched) == 0 {
		logger.Info(ctx, fmt.Sprintf("[ResponsesAffinity] selectChannelByRegion: 同区域渠道数=0 区域=%s group=%s model=%s",
			region, group, model))
		return nil, -1
	}

	// 加权随机（与主选渠逻辑同风格）
	totalWeight := 0
	weights := make([]int, len(matched))
	for i, ch := range matched {
		w := 1
		if ch.Weight != nil && int(*ch.Weight) > 0 {
			w = int(*ch.Weight)
		}
		weights[i] = w
		totalWeight += w
	}
	if totalWeight <= 0 {
		return matched[0], -1
	}
	randGen := rand.New(rand.NewSource(time.Now().UnixNano() + int64(rand.Intn(10000))))
	threshold := randGen.Intn(totalWeight) + 1
	current := 0
	for i, ch := range matched {
		current += weights[i]
		if current >= threshold {
			return ch, -1
		}
	}
	return matched[0], -1
}
