package controller

import (
	"context"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/flux"
)

// flux video 服务端兜底对账器
//
// 背景：flux-3-video 为异步任务（提交 → 轮询）。状态推进原本 100% 依赖客户端主动查询，
// 若客户端提交后不再查询，任务永远卡 processing、不结算、不退款。此对账器与图片侧
// StartFluxReconciler 同构，在用户不请求时也能把任务走完（succeed / failed + 退款）。
//
// 与图片对账的关键差异：图片创建时已扣费，失败不退款；video 创建时预扣费，失败必须退款。
// 因此本对账器在判失败时走退款链路，并用 CAS（status=processing 命中）+ RowsAffected 门控，
// 保证同一任务只在“赢得终态转换”的那一次退款，杜绝多实例/超时与对账双路径重复退款。
const (
	fluxVideoProvider          = "flux"
	fluxVideoReconcileInterval = 30 * time.Second
	fluxVideoReconcileBatch    = 50
	// 超过 4 小时仍未终态 → 直接判失败并退款，不再查上游
	fluxVideoExpireSecs = 4 * 60 * 60
	// 同时并发查询上游的 goroutine 上限，防止大批量任务打爆 BFL/Replicate/CPU
	fluxVideoQueryConcurrency = 50
)

// fluxVideoReconcilerMu 防止两次 tick 并发执行（上次未完成时跳过本轮）
var fluxVideoReconcilerMu sync.Mutex

// fluxVideoQuerySem 全局信号量，限制同时执行上游查询的 goroutine 数
var fluxVideoQuerySem = make(chan struct{}, fluxVideoQueryConcurrency)

// StartFluxVideoReconciler 启动后台 flux-3-video 任务对账 goroutine。
// 复用 ENABLE_VIDEO_TASK_POLLER 开关（isFluxReconcilerEnabled，与图片对账/其他 video poller 共用）。
func StartFluxVideoReconciler(ctx context.Context) {
	if !isFluxReconcilerEnabled() {
		logger.Info(ctx, "[flux-video-reconciler] disabled by ENABLE_VIDEO_TASK_POLLER env, not starting")
		return
	}

	ticker := time.NewTicker(fluxVideoReconcileInterval)
	defer ticker.Stop()

	logger.Info(ctx, "[flux-video-reconciler] started, interval=30s, expire=4h")

	// 启动时立即跑一次
	runFluxVideoReconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, "[flux-video-reconciler] stopped")
			return
		case <-ticker.C:
			runFluxVideoReconcile(ctx)
		}
	}
}

func runFluxVideoReconcile(ctx context.Context) {
	if !fluxVideoReconcilerMu.TryLock() {
		logger.Infof(ctx, "[flux-video-reconciler] 上次扫描尚未完成，跳过本轮")
		return
	}
	defer fluxVideoReconcilerMu.Unlock()

	now := time.Now().Unix()
	expireBefore := now - fluxVideoExpireSecs

	// ① 超过 4 小时仍 processing → 直接判失败并退款，不再查上游
	var expired []dbmodel.Video
	if err := dbmodel.DB.
		Where("provider = ? AND status = ? AND created_at < ?", fluxVideoProvider, "processing", expireBefore).
		Order("id ASC").Limit(fluxVideoReconcileBatch).
		Find(&expired).Error; err != nil {
		logger.Errorf(ctx, "[flux-video-reconciler] 查询超时任务失败: %v", err)
	} else if len(expired) > 0 {
		logger.Infof(ctx, "[flux-video-reconciler] 发现 %d 条超时(>4h)任务，判失败并退款", len(expired))
		for i := range expired {
			failFluxVideoTask(ctx, &expired[i], "任务超时(4小时未完成)", "")
		}
	}

	// ② 4 小时以内的 processing → 查上游尝试对账
	var tasks []dbmodel.Video
	if err := dbmodel.DB.
		Where("provider = ? AND status = ? AND created_at >= ?", fluxVideoProvider, "processing", expireBefore).
		Order("id ASC").Limit(fluxVideoReconcileBatch).
		Find(&tasks).Error; err != nil {
		logger.Errorf(ctx, "[flux-video-reconciler] 查询卡死任务失败: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	logger.Infof(ctx, "[flux-video-reconciler] 发现 %d 条待对账任务，开始查询（并发上限=%d）", len(tasks), fluxVideoQueryConcurrency)
	for i := range tasks {
		task := &tasks[i]
		go func() {
			fluxVideoQuerySem <- struct{}{}        // 占用一个并发槽，满额时阻塞等待
			defer func() { <-fluxVideoQuerySem }() // 完成后释放
			reconcileSingleFluxVideo(ctx, task)
		}()
	}
}

// reconcileSingleFluxVideo 查询单个 flux video 任务上游状态并结算。
// 复用 flux.VideoAdaptor.HandleVideoResult（内部按 baseURL 区分 BFL / Replicate，
// 且 BFL 分支使用落库的 polling_url 命中正确集群）。HandleVideoResult 全程不解引用
// gin.Context，此处传 nil 安全。
func reconcileSingleFluxVideo(ctx context.Context, task *dbmodel.Video) {
	defer func() {
		if r := recover(); r != nil {
			logger.SysError("[flux-video-reconciler] panic: task_id=" + task.TaskId)
		}
	}()

	channel, err := dbmodel.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.Errorf(ctx, "[flux-video-reconciler] 获取渠道失败: task_id=%s, channel_id=%d, err=%v",
			task.TaskId, task.ChannelId, err)
		return
	}
	cfg, err := channel.LoadConfig()
	if err != nil {
		logger.Errorf(ctx, "[flux-video-reconciler] 加载渠道配置失败: task_id=%s, err=%v", task.TaskId, err)
		return
	}

	adaptor := &flux.VideoAdaptor{}
	adaptor.Init(nil)
	result, apiErr := adaptor.HandleVideoResult(nil, task, channel, &cfg)
	if apiErr != nil {
		// 非 2xx 临时错误（401/403/429/5xx 等）→ 记日志跳过，等下一轮重试，不判失败不退款
		logger.Warnf(ctx, "[flux-video-reconciler] 查询上游失败（30s 后下一轮重试）: task_id=%s, err=%v",
			task.TaskId, apiErr.Error.Message)
		return
	}

	switch result.TaskStatus {
	case "succeed":
		succeedFluxVideoTask(ctx, task, result.VideoResult, result.RawResult)
	case "failed":
		failFluxVideoTask(ctx, task, result.Message, result.RawResult)
	default:
		// processing：仍在生成，等待下一轮
	}
}

// succeedFluxVideoTask 用 CAS（status=processing）原子转 succeed 并写 store_url + result（上游原始 JSON）。
// RowsAffected==0 表示已被其他路径（客户端查询/另一实例）处理，直接跳过。
func succeedFluxVideoTask(ctx context.Context, task *dbmodel.Video, videoURL string, rawResult string) {
	updates := map[string]interface{}{
		"status":     "succeed",
		"updated_at": time.Now().Unix(),
	}
	if videoURL != "" {
		updates["store_url"] = videoURL
	}
	if rawResult != "" {
		updates["result"] = rawResult // 上游 get_result 完整原始 JSON，供审计/排障
	}
	res := dbmodel.DB.Model(&dbmodel.Video{}).
		Where("task_id = ? AND status = ?", task.TaskId, "processing").
		Updates(updates)
	if res.Error != nil {
		logger.Errorf(ctx, "[flux-video-reconciler] 更新成功记录失败: task_id=%s, err=%v", task.TaskId, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		logger.Infof(ctx, "[flux-video-reconciler] 已被其他路径处理（成功跳过）: task_id=%s", task.TaskId)
		return
	}
	logger.Infof(ctx, "[flux-video-reconciler] 对账成功: task_id=%s, user_id=%d", task.TaskId, task.UserId)
}

// failFluxVideoTask 用 CAS（status=processing）原子转 failed，仅在赢得转换时退款。
// RowsAffected 门控是退款幂等的关键：多实例/超时与对账双路径并发时，只有一次 Update 生效，
// 也只有那一次触发退款，杜绝重复补偿。
func failFluxVideoTask(ctx context.Context, task *dbmodel.Video, reason string, rawResult string) {
	updates := map[string]interface{}{
		"status":         "failed",
		"fail_reason":    reason,
		"total_duration": time.Now().Unix() - task.CreatedAt,
		"updated_at":     time.Now().Unix(),
	}
	if rawResult != "" {
		updates["result"] = rawResult // 上游原始 JSON（失败详情），供审计/排障；超时段无上游查询则为空
	}
	res := dbmodel.DB.Model(&dbmodel.Video{}).
		Where("task_id = ? AND status = ?", task.TaskId, "processing").
		Updates(updates)
	if res.Error != nil {
		logger.Errorf(ctx, "[flux-video-reconciler] 更新失败记录失败: task_id=%s, err=%v", task.TaskId, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		logger.Infof(ctx, "[flux-video-reconciler] 已被其他路径处理（失败跳过）: task_id=%s", task.TaskId)
		return
	}
	// 赢得终态转换 → 退款（用户配额 + 渠道配额）
	if task.Quota > 0 {
		if err := dbmodel.CompensateVideoTaskQuota(task.UserId, task.Quota); err != nil {
			logger.Errorf(ctx, "[flux-video-reconciler] 退还用户配额失败: task_id=%s, err=%v", task.TaskId, err)
		}
		if err := dbmodel.CompensateChannelQuota(task.ChannelId, task.Quota); err != nil {
			logger.Errorf(ctx, "[flux-video-reconciler] 退还渠道配额失败: task_id=%s, err=%v", task.TaskId, err)
		}
	}
	logger.Infof(ctx, "[flux-video-reconciler] 任务判失败并退款: task_id=%s, user_id=%d, quota=%d, reason=%s",
		task.TaskId, task.UserId, task.Quota, reason)
}
