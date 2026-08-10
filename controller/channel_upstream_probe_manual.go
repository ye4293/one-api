package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
)

// ──────────────────────────────────────────
// 手动模型探针（管理员在渠道列表 View Models 下方主动触发）
//
// 与定时任务探针的本质区别：
//   - 只做只读诊断，绝不写 settings（saveChannelUpstreamSettings 是无锁
//     read-modify-write 整个 JSON blob，写入会与定时任务抢同一个 blob）
//   - 探测结果不落服务端 session（管理员关弹框即重来），只即时返回给前端
//   - 不受全局 UpstreamModelProbeEnabled / 渠道级 UpstreamModelProbeDisabled 控制：
//     手动 = 显式管理员意图
//   - 用独立的 manualBudget（纯本次请求计数），绝不触碰定时任务的全局每轮余额，
//     也不累加 taskProbeStats
// ──────────────────────────────────────────

const (
	// manualProbeMaxPerRequest 是单次 HTTP 请求的探测模型数硬上限。
	// 前端按此值分批循环；后端强制校验，防止「以管理员身份向上游发任意数量付费请求」。
	manualProbeMaxPerRequest = 50
	// manualProbeMarkerTTL 是「该渠道正被手动探测」标记的存活时长。
	// 每批请求刷新一次；管理员关弹框后不再刷新，TTL 到期自动释放。
	// 定时任务据此避让（见 channel_upstream_update.go 的 manualProbeActive 检查）。
	manualProbeMarkerTTL = 90 * time.Second
)

func manualProbeMarkerKey(channelID int) string {
	return fmt.Sprintf("upstream_manual_probe:%d", channelID)
}

// manualProbeActive 返回该渠道当前是否有活跃的手动探测，以及发起者 user_id。
// Redis 未启用时恒返回 false（单实例降级：无跨实例锁，但功能仍可用）。
func manualProbeActive(channelID int) (int, bool) {
	if !common.RedisEnabled {
		return 0, false
	}
	v, err := common.RedisGet(manualProbeMarkerKey(channelID))
	if err != nil || v == "" {
		return 0, false
	}
	uid, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return uid, true
}

// manualProbeRefresh 刷新（或创建）该渠道的手动探测标记，值为发起者 user_id。
func manualProbeRefresh(channelID, userID int) {
	if !common.RedisEnabled {
		return
	}
	_ = common.RedisSet(manualProbeMarkerKey(channelID), strconv.Itoa(userID), manualProbeMarkerTTL)
}

type probeChannelUpstreamModelsRequest struct {
	ID     int      `json:"id"`
	Models []string `json:"models"`
}

type probeModelResultDTO struct {
	Model       string  `json:"model"`
	MappedModel string  `json:"mapped_model,omitempty"`
	Scene       string  `json:"scene"`
	Verdict     string  `json:"verdict"`
	StatusCode  int     `json:"status_code"`
	Duration    float64 `json:"duration"`
	Message     string  `json:"message,omitempty"`
	SkipReason  string  `json:"skip_reason,omitempty"`
	// Approve 是处置表的直译（probeVerdictApproves），仅供前端参考。
	// 前端的默认勾选另有更严格的规则（skipped 一律不勾）。
	Approve bool `json:"approve"`
}

type probeChannelUpstreamModelsResult struct {
	Supported bool                  `json:"supported"`
	Reason    string                `json:"reason,omitempty"`
	Results   []probeModelResultDTO `json:"results"`
	Rejected  []string              `json:"rejected"` // 不在 pending 列表里、未探测的模型
}

// splitManualProbeTargets 把管理员请求的模型名切成「按新增方向探」「按删除方向探」
// 「拒绝（不在任何 pending 列表里，不探）」三份。
//
// 这是本功能的成本/注入安全边界（约束 G）：无论前端传什么，只有落在
// pendingAdd / pendingRemove 里的模型才会真正发起上游请求。pendingAdd 与
// pendingRemove 天然互斥（一个是上游有本地无、一个是本地有上游无）。
func splitManualProbeTargets(models, pendingAdd, pendingRemove []string) (addBatch, removeBatch, rejected []string) {
	addBatch = upstreamIntersectModelNames(models, pendingAdd)
	removeBatch = upstreamIntersectModelNames(models, pendingRemove)
	probed := append(append([]string{}, addBatch...), removeBatch...)
	rejected = upstreamSubtractModelNames(models, probed)
	return addBatch, removeBatch, rejected
}

// ProbeChannelUpstreamModels 手动探测一批模型（POST .../upstream_updates/probe）。
func ProbeChannelUpstreamModels(c *gin.Context) {
	var req probeChannelUpstreamModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.ID <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	models := upstreamNormalizeModelNames(req.Models)
	if len(models) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "models 不能为空"})
		return
	}
	if len(models) > manualProbeMaxPerRequest {
		c.JSON(http.StatusOK, gin.H{"success": false,
			"message": fmt.Sprintf("单次探测模型数不能超过 %d", manualProbeMaxPerRequest)})
		return
	}

	channel, err := model.GetChannelById(req.ID, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 渠道级短路：类型不支持 chat-completions 探测时，直接返回，不进任何逐模型循环。
	// 否则每个模型都会被判 skipped（不消耗预算、无停止机制），UI 误呈现为「全部建议应用」。
	if reason := probeChannelUnsupportedReason(channel); reason != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true, "message": "",
			"data": probeChannelUpstreamModelsResult{Supported: false, Reason: reason, Results: []probeModelResultDTO{}, Rejected: []string{}},
		})
		return
	}

	// 只探 settings 里的 pending 列表（约束 G：防注入任意模型名的付费请求）。
	settings := channel.GetOtherSettings()
	pendingAdd := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)
	pendingRemove := upstreamNormalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)
	addBatch, removeBatch, rejected := splitManualProbeTargets(models, pendingAdd, pendingRemove)

	// 并发闸门：他人正在探同一渠道则拒绝；本人续跑则刷新标记。
	userID := c.GetInt("id")
	if uid, ok := manualProbeActive(channel.Id); ok && uid != userID {
		c.JSON(http.StatusOK, gin.H{"success": false,
			"message": fmt.Sprintf("该渠道正在被其他管理员(user_id=%d)探测，请稍后再试", uid)})
		return
	}
	manualProbeRefresh(channel.Id, userID)

	results := make([]probeModelResultDTO, 0, len(addBatch)+len(removeBatch))
	results = append(results, runManualProbeBatch(c, channel, addBatch, probeScenePendingAdd)...)
	results = append(results, runManualProbeBatch(c, channel, removeBatch, probeScenePendingRemove)...)

	c.JSON(http.StatusOK, gin.H{
		"success": true, "message": "",
		"data": probeChannelUpstreamModelsResult{
			Supported: true,
			Results:   results,
			Rejected:  rejected,
		},
	})
}

// runManualProbeBatch 探测一个方向的批次，按传入顺序返回 DTO（保证前端展示顺序稳定）。
func runManualProbeBatch(c *gin.Context, channel *model.Channel, batch []string, scene string) []probeModelResultDTO {
	if len(batch) == 0 {
		return nil
	}
	resultMap := probeChannelModels(channel, batch, probeRunOptions{
		scene:            scene,
		source:           probeSourceManual,
		budget:           &manualBudget{remaining: len(batch)},
		stats:            nil, // 不污染定时任务统计
		ctx:              c.Request.Context(),
		modelConcurrency: config.UpstreamModelProbeModelConcurrency,
	})
	dtos := make([]probeModelResultDTO, 0, len(batch))
	for _, name := range batch {
		res, ok := resultMap[name]
		if !ok {
			// ctx 取消导致未探到：按 inconclusive 呈现
			res = probeResult{Model: name, Verdict: verdictInconclusive}
		}
		dtos = append(dtos, probeModelResultDTO{
			Model:       res.Model,
			MappedModel: res.MappedModel,
			Scene:       scene,
			Verdict:     string(res.Verdict),
			StatusCode:  res.StatusCode,
			Duration:    res.Duration,
			Message:     res.Message,
			SkipReason:  res.SkipReason,
			Approve:     probeVerdictApproves(res.Verdict, scene),
		})
	}
	return dtos
}
