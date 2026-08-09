package controller

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/model"
)

// healthProbeSettleSuccesses 是进入稳定节奏所需的连续成功次数。
// 不做配置项——改变它的动机只有「省钱」，而 steady interval 是同一目的更好的旋钮。
const healthProbeSettleSuccesses = 3

// healthProbeInterval 返回给定状态下模型应采用的探测间隔（秒）。
func healthProbeInterval(st config.ModelHealthState) int64 {
	if st.Successes >= healthProbeSettleSuccesses {
		mins := config.UpstreamModelHealthProbeSteadyIntervalMinutes
		if mins <= 0 {
			mins = 60
		}
		return int64(mins) * 60
	}
	mins := config.UpstreamModelHealthProbeFastIntervalMinutes
	if mins <= 0 {
		mins = 10
	}
	return int64(mins) * 60
}

// healthProbeCandidates 从本地模型算出：
//   - tracked：健康巡检的管辖范围（排除 ignored / removal-protected / pendingAdd / pendingRemove）
//   - due：本轮到期需要探测的子集，按 LastProbe 升序排列以优先最久没探的
func healthProbeCandidates(
	localModels, ignored, pendingAdd, pendingRemove []string,
	health map[string]config.ModelHealthState,
	channelType int, now int64,
) (tracked, due []string) {
	// 构建排除集（字面量匹配）
	excludeSet := make(map[string]struct{}, len(pendingAdd)+len(pendingRemove))
	for _, m := range pendingAdd {
		excludeSet[m] = struct{}{}
	}
	for _, m := range pendingRemove {
		excludeSet[m] = struct{}{}
	}

	// 解析忽略规则
	type ignoreRule struct {
		literal string
		re      *regexp.Regexp
	}
	var rules []ignoreRule
	for _, ign := range ignored {
		ign = strings.TrimSpace(ign)
		if ign == "" {
			continue
		}
		if body, ok := strings.CutPrefix(ign, "regex:"); ok {
			if re, err := regexp.Compile(strings.TrimSpace(body)); err == nil {
				rules = append(rules, ignoreRule{re: re})
			}
		} else {
			rules = append(rules, ignoreRule{literal: ign})
		}
	}

	isIgnored := func(m string) bool {
		for _, r := range rules {
			if r.re != nil {
				if r.re.MatchString(m) {
					return true
				}
			} else if r.literal == m {
				return true
			}
		}
		return false
	}

	tracked = make([]string, 0, len(localModels))
	for _, m := range localModels {
		if _, ok := excludeSet[m]; ok {
			continue
		}
		if upstreamRemovalProtected(channelType, m) {
			continue
		}
		if isIgnored(m) {
			continue
		}
		tracked = append(tracked, m)
	}

	// 从 tracked 中筛出本轮到期的
	due = make([]string, 0, len(tracked))
	for _, m := range tracked {
		st := health[m]
		interval := healthProbeInterval(st)
		if st.LastProbe == 0 || now-st.LastProbe >= interval {
			due = append(due, m)
		}
	}

	// 按 LastProbe 升序：最久没探的优先，预算不够时自然轮转
	sort.Slice(due, func(i, j int) bool {
		return health[due[i]].LastProbe < health[due[j]].LastProbe
	})

	return tracked, due
}

// applyHealthVerdicts 是纯函数状态转移：
// 消费 verdict map，输出新的 health map 和达到失败阈值的待删除模型列表。
// verdicts 中不存在的模型（预算耗尽未探测）状态完全不变。
func applyHealthVerdicts(
	health map[string]config.ModelHealthState,
	verdicts map[string]probeVerdict,
	now int64, failThreshold int,
) (next map[string]config.ModelHealthState, toRemove []string) {
	if health == nil {
		next = make(map[string]config.ModelHealthState, len(verdicts))
	} else {
		next = make(map[string]config.ModelHealthState, len(health))
		for k, v := range health {
			next[k] = v
		}
	}

	for m, verdict := range verdicts {
		st := next[m]
		st.LastProbe = now

		switch verdict {
		case verdictAlive:
			st.Successes++
			st.Fails = 0
		case verdictNotFound:
			st.Fails++
			st.Successes = 0
		default:
			// rate_limited / unavailable / inconclusive / skipped：不动计数器
		}

		next[m] = st
	}

	// 收集达到失败阈值的模型
	for m, st := range next {
		if st.Fails >= failThreshold {
			toRemove = append(toRemove, m)
		}
	}
	sort.Strings(toRemove) // 确定性输出

	return next, toRemove
}

// pruneHealthState 移除 health map 中不再在 localModels 里的 key，防止 map 无限增长。
func pruneHealthState(health map[string]config.ModelHealthState, localModels []string) map[string]config.ModelHealthState {
	if len(health) == 0 {
		return health
	}
	localSet := make(map[string]struct{}, len(localModels))
	for _, m := range localModels {
		localSet[m] = struct{}{}
	}
	for k := range health {
		if _, ok := localSet[k]; !ok {
			delete(health, k)
		}
	}
	if len(health) == 0 {
		return nil // omitempty 友好
	}
	return health
}

// runHealthProbe 编排：候选筛选 → 探测 → 状态转移 → 剪枝 → 全军覆没判定。
// 返回 healthRemove（待删除模型列表）和 channelWideFault（是否全军覆没）。
func runHealthProbe(
	channel *model.Channel, settings *config.ChannelOtherSettings,
	localModels, pendingAdd, pendingRemove []string, budget *probeBudget,
) (healthRemove []string, channelWideFault bool) {
	now := time.Now().Unix()

	if settings.UpstreamModelHealth == nil {
		settings.UpstreamModelHealth = make(map[string]config.ModelHealthState)
	}

	tracked, due := healthProbeCandidates(
		localModels,
		settings.UpstreamModelUpdateIgnoredModels,
		pendingAdd, pendingRemove,
		settings.UpstreamModelHealth,
		channel.Type, now,
	)

	if len(tracked) == 0 || len(due) == 0 {
		// 无管辖模型 或 本轮无到期模型 → 只做剪枝
		settings.UpstreamModelHealth = pruneHealthState(settings.UpstreamModelHealth, localModels)
		return nil, false
	}

	// 执行探测
	results := probeChannelModels(channel, due, probeRunOptions{
		scene:            probeSceneHealth,
		source:           probeSourceTask,
		budget:           budget,
		stats:            &taskProbeStats,
		modelConcurrency: config.UpstreamModelProbeModelConcurrency,
	})

	// 状态转移
	verdicts := projectVerdicts(results)
	settings.UpstreamModelHealth, healthRemove = applyHealthVerdicts(
		settings.UpstreamModelHealth, verdicts, now, config.UpstreamModelHealthProbeFailThreshold,
	)

	// 剪枝：丢弃已不在 channel.Models 里的 key
	settings.UpstreamModelHealth = pruneHealthState(settings.UpstreamModelHealth, localModels)

	// 全军覆没判定：tracked 中全部达到删除阈值 → 渠道级故障
	if len(healthRemove) > 0 && len(healthRemove) >= len(tracked) {
		upstreamInfo(fmt.Sprintf("health probe: channel-wide fault detected channel_id=%d channel_name=%s tracked=%d all_failed=%d",
			channel.Id, channel.Name, len(tracked), len(healthRemove)))
		if disabled, disableErr := model.AutoDisableChannelById(channel.Id, "全部模型健康探针连续失败", ""); disableErr != nil {
			upstreamError(fmt.Sprintf("health probe: failed to auto-disable channel_id=%d err=%v", channel.Id, disableErr))
		} else if disabled {
			upstreamInfo(fmt.Sprintf("health probe: auto-disabled channel_id=%d channel_name=%s", channel.Id, channel.Name))
		}
		// 不删任何模型，计数器清零
		settings.UpstreamModelHealth = nil
		return nil, true
	}

	if len(healthRemove) > 0 {
		upstreamInfo(fmt.Sprintf("health probe: models to remove channel_id=%d channel_name=%s models=%v",
			channel.Id, channel.Name, healthRemove))
		// 从 health map 中移除即将被删的模型（删除后不再跟踪）
		for _, m := range healthRemove {
			delete(settings.UpstreamModelHealth, m)
		}
	}

	return healthRemove, false
}
