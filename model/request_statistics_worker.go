package model

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
)

const (
	statsSourceReadTimeout = time.Minute
	statsMinuteTimeout     = 90 * time.Second // 读取之外预留汇总写入与提交时间。
)

func (s *RequestStatistics) runWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.RunOnce(ctx, time.Now()); err != nil && ctx.Err() == nil {
			logger.Error(ctx, "request statistics worker stopped: "+err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// 角色检查先于任何任务连接/SQL；定时、人工补算、清理共享这一入口。
func (s *RequestStatistics) withWorkerLock(ctx context.Context, fn func(*sql.Conn) error) error {
	if s == nil || !s.enabled || !s.workerEnabled {
		return statsError(503, "分钟聚合任务未启用")
	}
	if !config.IsMasterNode {
		return statsError(403, "not_master：仅主节点可执行分钟任务")
	}
	if s.worker == nil {
		return statsError(503, "分钟任务连接池不可用")
	}
	select {
	case s.workerGate <- struct{}{}:
		defer func() { <-s.workerGate }()
	default:
		return statsError(429, "分钟任务正在运行")
	}
	conn, err := s.worker.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var acquired sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", s.lockName).Scan(&acquired); err != nil {
		discard()
		return err
	}
	if !acquired.Valid {
		return fmt.Errorf("GET_LOCK returned NULL")
	}
	if acquired.Int64 != 1 {
		return statsError(429, "另一主节点正在执行分钟任务")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var released sql.NullInt64
		if err := conn.QueryRowContext(release, "SELECT RELEASE_LOCK(?)", s.lockName).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
			discard()
		}
	}()
	return fn(conn)
}

type statsBootstrap struct {
	CoverageStart int64
	InitialLogID  uint64
}

const statsLatestMinuteSQL = "SELECT MAX(bucket_start) FROM request_stats_minute"

func readStatsBootstrap(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (statsBootstrap, error) {
	var state statsBootstrap
	var earliest sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT MIN(bucket_start) FROM request_stats_minute").Scan(&earliest)
	if err == nil && !earliest.Valid {
		return state, sql.ErrNoRows
	}
	state.CoverageStart = earliest.Int64
	if err == nil && (state.CoverageStart <= 0 || state.CoverageStart%60 != 0) {
		err = fmt.Errorf("invalid statistics coverage boundary")
	}
	return state, err
}

// 调用方持有 Worker 锁；空表仅在内存初始化，已有汇总直接恢复，不写额外记录。
func (s *RequestStatistics) ensureBootstrap(ctx context.Context, conn *sql.Conn) (statsBootstrap, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state, err := readStatsBootstrap(ctx, conn)
	if err == nil && s.initial != nil && s.initial.CoverageStart == state.CoverageStart {
		state.InitialLogID = s.initial.InitialLogID
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	if s.initial != nil {
		return *s.initial, nil
	}
	if !s.sourceHealthy() || s.SourceRetainedFrom <= 0 {
		return state, fmt.Errorf("source log coverage or retention is not verified; refuse initialization")
	}
	// MAX(id) 走主键端点，不扫描历史日志；读完后从下一完整分钟开始，不补历史。
	if err = conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM logs").Scan(&state.InitialLogID); err != nil {
		return state, err
	}
	state.CoverageStart = time.Now().Unix()/60*60 + 60
	if state.CoverageStart < s.SourceRetainedFrom {
		return state, fmt.Errorf("initial statistics minute precedes verified source retention")
	}
	// 首批汇总前重启会重新取 MAX(id)；首批落库后由汇总记录恢复进度和 ID 边界。
	s.initial = &state
	return state, nil
}

// 沿用项目按 ID 二分定位时间边界的方式，但使用 Worker 当前快照并直接返回查询错误。
// 沿用 ID 与时间大致同序、保留约一分钟乱序余量的约定；只返回实测早于截止时间的 ID。
func statsLogIDBefore(ctx context.Context, tx *sql.Tx, before int64) (uint64, error) {
	var first, last sql.Null[uint64]
	if err := tx.QueryRowContext(ctx, "SELECT MIN(id),MAX(id) FROM logs").Scan(&first, &last); err != nil {
		return 0, err
	}
	if !first.Valid || !last.Valid {
		return 0, nil
	}
	var boundary uint64
	for low, high := first.V, last.V; low <= high; {
		mid := low + (high-low)/2
		var id uint64
		var createdAt int64
		err := tx.QueryRowContext(ctx, "SELECT id,created_at FROM logs FORCE INDEX (PRIMARY) WHERE id>=? AND id<=? ORDER BY id LIMIT 1", mid, high).Scan(&id, &createdAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err == nil && createdAt < before {
			boundary = id
			if id == last.V {
				break
			}
			low = id + 1
		} else {
			if mid == 0 {
				break
			}
			high = mid - 1
		}
	}
	return boundary, nil
}

func (s *RequestStatistics) latestMinute(ctx context.Context, conn *sql.Conn, coverageStart int64) (sql.NullInt64, error) {
	var latest sql.NullInt64
	err := conn.QueryRowContext(ctx, statsLatestMinuteSQL).Scan(&latest)
	if err == nil && latest.Valid && (latest.Int64%60 != 0 || latest.Int64 < coverageStart || latest.Int64 >= time.Now().Unix()/60*60) {
		err = fmt.Errorf("invalid statistics recovery boundary")
	}
	return latest, err
}

func (s *RequestStatistics) RunOnce(ctx context.Context, trigger time.Time) error {
	return s.withWorkerLock(ctx, func(conn *sql.Conn) error {
		state, err := s.ensureBootstrap(ctx, conn)
		if err != nil {
			return err
		}
		latest, err := s.latestMinute(ctx, conn, state.CoverageStart)
		if err != nil {
			return err
		}
		start := state.CoverageStart
		if latest.Valid {
			start = latest.Int64
		}
		end := trigger.Unix() / 60 * 60
		if end > time.Now().Unix()/60*60 {
			return fmt.Errorf("cannot aggregate future minutes")
		}
		// 至少允许边界重算后推进一分钟；运行预算只在完整分钟提交后检查。
		began := time.Now()
		processed := 0
		for minute := start; minute < end; minute += 60 {
			if err = s.replaceMinute(ctx, conn, minute, state); err != nil {
				return fmt.Errorf("minute %d: %w", minute, err)
			}
			processed++
			if processed >= 2 && time.Since(began) >= 45*time.Second {
				break
			}
		}
		return nil
	})
}

// Recompute 不允许抬高 MAX；历史用量修订后由受控运维代码调用，无新增 HTTP 写入口。
func (s *RequestStatistics) Recompute(ctx context.Context, start, end int64) error {
	return s.withWorkerLock(ctx, func(conn *sql.Conn) error {
		state, err := readStatsBootstrap(ctx, conn)
		if err != nil {
			return err
		}
		latest, err := s.latestMinute(ctx, conn, state.CoverageStart)
		if err != nil {
			return err
		}
		if !latest.Valid || start < statsRetainedFrom(state.CoverageStart, time.Now()) || start%60 != 0 || end%60 != 0 || end <= start || end > latest.Int64+60 {
			return statsError(422, "补算只能覆盖已完成且仍保留的分钟")
		}
		for minute := start; minute < end; minute += 60 {
			if err = s.replaceMinute(ctx, conn, minute, state); err != nil {
				return fmt.Errorf("minute %d: %w", minute, err)
			}
		}
		return nil
	})
}

// Cleanup 只删除汇总保留期前缀，始终留下恢复锚点；源日志由部署保留策略保护。
func (s *RequestStatistics) Cleanup(ctx context.Context) error {
	return s.withWorkerLock(ctx, func(conn *sql.Conn) error {
		state, err := readStatsBootstrap(ctx, conn)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		latest, err := s.latestMinute(ctx, conn, state.CoverageStart)
		if err != nil || !latest.Valid {
			return err
		}
		// 额外保留两分钟边界，保留期内最早分钟重算时仍可使用 ID 下界。
		cutoff := min(statsRetainedFrom(state.CoverageStart, time.Now())-120, latest.Int64)
		cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err = conn.ExecContext(cleanup, "DELETE FROM request_stats_minute WHERE bucket_start < ? ORDER BY bucket_start LIMIT 5000", cutoff)
		if err == nil {
			s.clearCaches()
		}
		return err
	})
}

type statsMinuteGroup struct {
	User, Channel          sql.NullInt64
	Username, Token, Model sql.NullString
	statsAggregate
	Invalid, MaxLogID uint64
}

// 旧 duration 单位为秒；仅读取时量化为整数微秒，原日志保持不变。
// 排除缺失、非正、低于一微秒和超出 int64 微秒范围的耗时，仍保留日志计数。
const statsDurationUSSQL = "CASE WHEN duration >= 0.000001 AND duration < 9223372036854 THEN CAST(ROUND(duration * 1000000) AS SIGNED) END"

// 用户名来自日志快照；同用户改名前后的日志独立聚合，ID 查询仍合并全部历史名称。
const statsUsernameSQL = "CONVERT(COALESCE(username, '') USING utf8mb4) COLLATE utf8mb4_0900_bin"

var statsSourceSQL = fmt.Sprintf(`SELECT user_id,`+statsUsernameSQL+`,
 CONVERT(COALESCE(token_name, '') USING utf8mb4) COLLATE utf8mb4_0900_bin,
 CONVERT(model_name USING utf8mb4) COLLATE utf8mb4_bin, channel_id,
 COUNT(*), SUM(CASE WHEN type=%d THEN 1 ELSE 0 END), SUM(CASE WHEN type=%d THEN 1 ELSE 0 END),
 COUNT(`+statsDurationUSSQL+`),
 SUM(CAST(COALESCE(prompt_tokens,0) AS DECIMAL(30,0))+CAST(COALESCE(completion_tokens,0) AS DECIMAL(30,0))),
 COALESCE(SUM(CAST((`+statsDurationUSSQL+`) AS DECIMAL(30,0))),0),
 MIN(`+statsDurationUSSQL+`),MAX(`+statsDurationUSSQL+`),
 SUM(CASE WHEN user_id IS NULL OR user_id<=0
 OR CHAR_LENGTH(COALESCE(username,''))>191
 OR CHAR_LENGTH(COALESCE(token_name,''))>255 OR model_name IS NULL OR CHAR_LENGTH(model_name)>200 OR model_name=''
 OR channel_id IS NULL OR channel_id<0
 OR prompt_tokens<0 OR completion_tokens<0 THEN 1 ELSE 0 END), MAX(id)
 FROM logs FORCE INDEX (PRIMARY) WHERE id>? AND created_at>=? AND created_at<? AND user_id>0 AND type IN (%d,%d)
 GROUP BY user_id,`+statsUsernameSQL+`,CONVERT(COALESCE(token_name, '') USING utf8mb4) COLLATE utf8mb4_0900_bin,
 CONVERT(model_name USING utf8mb4) COLLATE utf8mb4_bin,channel_id`, LogTypeConsume, LogTypeError, LogTypeConsume, LogTypeError)

func (a *statsAggregate) validate() error {
	if a.Success > a.Total || a.Failure != a.Total-a.Success || a.DurationCount > a.Total || a.Tokens.Sign() < 0 || a.DurationSum.Sign() < 0 || len(a.Tokens.String()) > 30 || len(a.DurationSum.String()) > 30 {
		return fmt.Errorf("invalid statistics counts or decimal overflow")
	}
	if a.DurationCount == 0 {
		if a.DurationSum.Sign() != 0 || a.Min.Valid || a.Max.Valid {
			return fmt.Errorf("duration without samples")
		}
	} else {
		if !a.Min.Valid || !a.Max.Valid || a.Min.Int64 <= 0 || a.Max.Int64 < a.Min.Int64 {
			return fmt.Errorf("invalid duration extrema")
		}
		count := statsUint(a.DurationCount)
		if a.DurationSum.Cmp(new(big.Int).Mul(count, big.NewInt(a.Min.Int64))) < 0 || a.DurationSum.Cmp(new(big.Int).Mul(count, big.NewInt(a.Max.Int64))) > 0 {
			return fmt.Errorf("invalid duration sum")
		}
	}
	return nil
}

// 数据异常返回整分钟零汇总及原因；数据库查询失败仍返回 error，不发布零流量。
func readStatsMinute(ctx context.Context, tx *sql.Tx, minute int64, initialID uint64) ([]statsMinuteGroup, string, error) {
	read, cancel := context.WithTimeout(ctx, statsSourceReadTimeout)
	defer cancel()
	// 精确取前两分钟的全分钟最大 ID；首批边界缺失时使用内存初始 ID，始终走主键范围。
	// 例如汇总 [10:02,10:03) 使用 [10:00,10:01) 的边界，时间条件始终保留。
	var lowerID uint64
	if err := tx.QueryRowContext(read, "SELECT GREATEST(COALESCE(MAX(max_log_id),0),?) FROM request_stats_minute WHERE bucket_start = ?", initialID, minute-120).Scan(&lowerID); err != nil {
		return nil, "", err
	}
	if lowerID == 0 {
		// 旧版未记录 ID 时，定位前两分钟区间的结束时间；仍为当前分钟保留一分钟余量。
		var err error
		lowerID, err = statsLogIDBefore(read, tx, minute-60)
		if err != nil {
			return nil, "", err
		}
	}
	rows, err := tx.QueryContext(read, statsSourceSQL, lowerID, minute, minute+60)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	groups := make([]statsMinuteGroup, 0, 128)
	var invalidReason string
	var maxLogID uint64
	for rows.Next() {
		var g statsMinuteGroup
		a := &g.statsAggregate
		// 先完整接收结果，再校验 DECIMAL，避免负数提前中断 Scan 而丢失最大 ID。
		var tokens, durationSum string
		if err = rows.Scan(&g.User, &g.Username, &g.Token, &g.Model, &g.Channel, &a.Total, &a.Success, &a.Failure, &a.DurationCount, &tokens, &durationSum, &a.Min, &a.Max, &g.Invalid, &g.MaxLogID); err != nil {
			return nil, "", err
		}
		maxLogID = max(maxLogID, g.MaxLogID)
		// 异常后不再保存分组，但读完结果以取得全分钟 ID 边界并检查读取错误。
		if invalidReason != "" {
			continue
		}
		switch {
		case len(groups) >= 50000:
			invalidReason = "minute exceeds 50000 groups"
		case g.Invalid != 0 || !g.User.Valid || g.User.Int64 <= 0 || !g.Channel.Valid || g.Channel.Int64 < 0 || !g.Model.Valid || g.Model.String == "" || strings.TrimSpace(g.Model.String) != g.Model.String || utf8.RuneCountInString(g.Model.String) > 200 || utf8.RuneCountInString(g.Token.String) > 255 || utf8.RuneCountInString(g.Username.String) > 191 || a.Total == 0:
			invalidReason = "invalid source log group"
		default:
			if err = a.Tokens.Scan(tokens); err == nil {
				err = a.DurationSum.Scan(durationSum)
			}
			if err == nil {
				err = a.validate()
			}
			if err != nil {
				invalidReason = err.Error()
			}
		}
		if invalidReason != "" {
			groups = nil
			continue
		}
		groups = append(groups, g)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if err = rows.Close(); err != nil {
		return nil, "", err
	}
	if invalidReason != "" {
		return []statsMinuteGroup{{MaxLogID: maxLogID}}, invalidReason, nil
	}
	if len(groups) == 0 {
		// 空分钟保留已定位的边界，并继承上一分钟进度，避免以后再次定位或扩大扫描范围。
		var carryID uint64
		if err = tx.QueryRowContext(read, "SELECT GREATEST(COALESCE(MAX(max_log_id),0),?) FROM request_stats_minute WHERE bucket_start = ?", lowerID, minute-60).Scan(&carryID); err != nil {
			return nil, "", err
		}
		groups = append(groups, statsMinuteGroup{MaxLogID: carryID})
	}
	return groups, "", nil
}

func (s *RequestStatistics) replaceMinute(ctx context.Context, conn *sql.Conn, minute int64, state statsBootstrap) error {
	if !config.IsMasterNode {
		return statsError(403, "not_master")
	}
	if minute%60 != 0 || minute < state.CoverageStart || minute >= time.Now().Unix()/60*60 {
		return statsError(422, "只能聚合覆盖范围内已结束的分钟")
	}
	if !s.sourceHealthy() || s.SourceRetainedFrom <= 0 || minute < s.SourceRetainedFrom {
		return fmt.Errorf("source log coverage or retention is not verified; refuse publication")
	}
	ctx, cancel := context.WithTimeout(ctx, statsMinuteTimeout)
	defer cancel()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	groups, invalidReason, err := readStatsMinute(ctx, tx, minute, state.InitialLogID)
	if err != nil {
		return err
	}
	if !s.sourceHealthy() {
		return fmt.Errorf("source log health changed during scan")
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM request_stats_minute WHERE bucket_start = ?", minute); err != nil {
		return err
	}
	for from := 0; from < len(groups); from += 500 {
		batch := groups[from:min(from+500, len(groups))]
		args := make([]any, 0, len(batch)*15)
		values := make([]string, len(batch))
		for i, g := range batch {
			values[i] = "(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP(6))"
			args = append(args, minute, g.User.Int64, g.Username.String, g.Token.String, g.Model.String, g.Channel.Int64, g.Total, g.Tokens.String(), g.Success, g.Failure, g.DurationCount, g.DurationSum.String(), g.Min, g.Max, g.MaxLogID)
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO request_stats_minute (bucket_start,user_id,username,token_name,model_name,channel_id,total_requests,total_tokens,success_count,failure_count,duration_count,duration_sum_us,duration_min_us,duration_max_us,max_log_id,calculated_at) VALUES "+strings.Join(values, ","), args...)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if invalidReason != "" {
		logger.Warnf(ctx, "request statistics minute %d published zero summary: %s; max_log_id=%d", minute, invalidReason, groups[0].MaxLogID)
	}
	s.clearCaches()
	return nil
}
