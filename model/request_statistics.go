package model

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"
)

const RequestStatsMetricVersion = 1

// 统计既有消费/错误日志行，不推断协议终态，也不按请求标识去重。
const requestStatsScope = "logs_created_at_consume_and_error"

// RequestStatsQuery 只接收 controller 已校验、已授权的查询，不读取 HTTP 参数。
type RequestStatsQuery struct {
	Kind     string
	ActorID  int
	Admin    bool
	Start    int64
	End      int64
	Interval int64
	TopN     int
	Page     int
	PageSize int
	Filters  RequestStatsFilters
}

type RequestStatsFilters struct {
	// 仅供普通用户授权隔离使用，不接受 HTTP 请求指定用户 ID。
	UserID    *int64  `json:"user_id,omitempty"`
	Username  *string `json:"username,omitempty"`
	TokenName *string `json:"token_name,omitempty"`
	ModelName *string `json:"model_name,omitempty"`
	ChannelID *int64  `json:"channel_id,omitempty"`
}

type RequestStatsError struct {
	Status  int
	Message string
}

func (e *RequestStatsError) Error() string        { return e.Message }
func statsError(status int, message string) error { return &RequestStatsError{status, message} }

// statsInteger 保持数据库 DECIMAL(30,0) 的精度，直到最终响应才做除法和舍入。
type statsInteger struct{ big.Int }

func (n *statsInteger) Scan(v any) error {
	var s string
	switch v := v.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	case int64:
		s = fmt.Sprint(v)
	case nil:
		s = "0"
	default:
		return fmt.Errorf("invalid integer type %T", v)
	}
	if _, ok := n.SetString(s, 10); !ok || n.Sign() < 0 {
		return fmt.Errorf("invalid nonnegative integer")
	}
	return nil
}
func statsRatio(n *big.Int, denominator *big.Int, places int) *float64 {
	if denominator.Sign() == 0 {
		return nil
	}
	r := new(big.Rat).SetFrac(n, denominator)
	// FloatString 用十进制舍入，避免先转 float64 丢失大整数精度。
	var value float64
	fmt.Sscan(r.FloatString(places), &value)
	return &value
}
func statsRate(n *big.Int, seconds int64) *float64 {
	return statsRatio(new(big.Int).Mul(n, big.NewInt(60)), big.NewInt(seconds), 6)
}
func statsUint(v uint64) *big.Int { return new(big.Int).SetUint64(v) }

type statsAggregate struct {
	Bucket                                 int64
	Total, Success, Failure, DurationCount uint64
	Tokens, DurationSum                    statsInteger
	Min, Max                               sql.NullInt64
}

func (a *statsAggregate) add(b *statsAggregate) error {
	if a.Total > math.MaxUint64-b.Total || a.Success > math.MaxUint64-b.Success || a.Failure > math.MaxUint64-b.Failure || a.DurationCount > math.MaxUint64-b.DurationCount {
		return fmt.Errorf("statistics count overflow")
	}
	a.Total += b.Total
	a.Success += b.Success
	a.Failure += b.Failure
	a.DurationCount += b.DurationCount
	a.Tokens.Add(&a.Tokens.Int, &b.Tokens.Int)
	a.DurationSum.Add(&a.DurationSum.Int, &b.DurationSum.Int)
	if b.Min.Valid && (!a.Min.Valid || b.Min.Int64 < a.Min.Int64) {
		a.Min = b.Min
	}
	if b.Max.Valid && (!a.Max.Valid || b.Max.Int64 > a.Max.Int64) {
		a.Max = b.Max
	}
	return nil
}

type statsMetrics struct {
	Incomplete bool     `json:"incomplete"`
	Total      *uint64  `json:"total_requests"`
	RPM        *float64 `json:"rpm"`
	TPM        *float64 `json:"tpm"`
}
type statsSuccessMetrics struct {
	statsMetrics
	Success       *uint64  `json:"success_count"`
	Failure       *uint64  `json:"failure_count"`
	SuccessRate   *float64 `json:"success_rate"`
	DurationCount *uint64  `json:"duration_count"`
	Avg           *float64 `json:"avg_duration_ms"`
	Min           *float64 `json:"min_duration_ms"`
	Max           *float64 `json:"max_duration_ms"`
}

func (a *statsAggregate) requestMetrics(start, end int64, observed, complete bool) statsMetrics {
	m := statsMetrics{Incomplete: !complete}
	if !observed {
		return m
	}
	m.Total = &a.Total
	if complete {
		m.RPM = statsRate(statsUint(a.Total), end-start)
		m.TPM = statsRate(&a.Tokens.Int, end-start)
	}
	return m
}

func (a *statsAggregate) metrics(start, end int64, observed, complete bool) statsSuccessMetrics {
	m := statsSuccessMetrics{statsMetrics: a.requestMetrics(start, end, observed, complete)}
	if !observed {
		return m
	}
	m.Success = &a.Success
	m.Failure = &a.Failure
	m.DurationCount = &a.DurationCount
	m.SuccessRate = statsRatio(statsUint(a.Success), statsUint(a.Total), 6)
	m.Avg = statsRatio(&a.DurationSum.Int, new(big.Int).Mul(statsUint(a.DurationCount), big.NewInt(1000)), 3)
	if a.Min.Valid {
		m.Min = statsRatio(big.NewInt(a.Min.Int64), big.NewInt(1000), 3)
	}
	if a.Max.Valid {
		m.Max = statsRatio(big.NewInt(a.Max.Int64), big.NewInt(1000), 3)
	}
	return m
}

type statsBounds struct {
	BucketStart    int64 `json:"bucket_start"`
	BucketEnd      int64 `json:"bucket_end"`
	EffectiveStart int64 `json:"effective_start"`
	EffectiveEnd   int64 `json:"effective_end"`
	Partial        bool  `json:"is_partial"`
}

func statsTimeAxis(q RequestStatsQuery) []statsBounds {
	points := make([]statsBounds, 0, (q.End-q.Start)/q.Interval+2)
	for b := q.Start / q.Interval * q.Interval; b < q.End; b += q.Interval {
		start, end := max(q.Start, b), min(q.End, b+q.Interval)
		points = append(points, statsBounds{b, b + q.Interval, start, end, start != b || end != b+q.Interval})
	}
	return points
}

type statsSuccessPoint struct {
	statsBounds
	statsSuccessMetrics
}
type statsModelPoint struct {
	statsBounds
	statsMetrics
	Values map[string]*uint64         `json:"values"`
	Rates  map[string]statsModelRates `json:"rates"`
}
type statsModelRates struct {
	RPM *float64 `json:"rpm"`
	TPM *float64 `json:"tpm"`
}
type statsSeries struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	Model   *string      `json:"model_name,omitempty"`
	Label   string       `json:"label"`
	Summary statsMetrics `json:"summary"`
}
type statsFreshness struct {
	CoverageStart    int64    `json:"coverage_start"`
	RetainedFrom     int64    `json:"retained_from"`
	Coverage         string   `json:"coverage"`
	Incomplete       bool     `json:"incomplete"`
	Latest           *int64   `json:"latest_stored_minute,omitempty"`
	Before           *int64   `json:"aggregated_before,omitempty"`
	SourceMutability string   `json:"source_mutability"`
	SourceHealthy    bool     `json:"source_healthy"`
	CoverageIssues   []string `json:"coverage_issues,omitempty"`
}
type statsResponse struct {
	Version         int                 `json:"metric_version"`
	Scope           string              `json:"metric_scope"`
	Start           int64               `json:"start_timestamp"`
	End             int64               `json:"end_timestamp"`
	Interval        string              `json:"interval"`
	IntervalSeconds int64               `json:"interval_seconds"`
	TopN            int                 `json:"top_n,omitempty"`
	Filters         RequestStatsFilters `json:"filters"`
	Freshness       statsFreshness      `json:"freshness"`
	Summary         any                 `json:"summary"`
	Timeseries      any                 `json:"timeseries"`
	Series          *[]statsSeries      `json:"series,omitempty"`
}

// 使用显式转义符，关键字中的 %、_、! 均按字面值匹配。
var statsLikeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

func statsContainsPattern(keyword string) string {
	return "%" + statsLikeEscaper.Replace(keyword) + "%"
}

// statsWhere 只绑定实际存在的筛选；裸时间列保留索引范围扫描。
func statsWhere(q RequestStatsQuery, source bool) (string, []any) {
	column := "bucket_start"
	if source {
		column = "created_at"
	}
	parts := []string{column + " >= ?", column + " < ?", "user_id > 0"}
	args := []any{q.Start, q.End}
	if q.Filters.UserID != nil {
		parts = append(parts, "user_id = ?")
		args = append(args, *q.Filters.UserID)
	}
	if q.Filters.Username != nil {
		username := "username"
		if source {
			username = statsUsernameSQL
		}
		parts = append(parts, username+" LIKE ? ESCAPE '!'")
		args = append(args, statsContainsPattern(*q.Filters.Username))
	}
	if q.Filters.ChannelID != nil {
		parts = append(parts, "channel_id = ?")
		args = append(args, *q.Filters.ChannelID)
	}
	token, model := "token_name", "model_name"
	if source {
		token = "CONVERT(COALESCE(token_name, '') USING utf8mb4) COLLATE utf8mb4_0900_bin"
		model = "CONVERT(model_name USING utf8mb4) COLLATE utf8mb4_bin"
	}
	if q.Filters.TokenName != nil {
		parts = append(parts, token+" LIKE ? ESCAPE '!'")
		args = append(args, statsContainsPattern(*q.Filters.TokenName))
	}
	if q.Filters.ModelName != nil {
		parts = append(parts, model+" = ?")
		args = append(args, *q.Filters.ModelName)
	}
	if source {
		parts = append(parts, fmt.Sprintf("type = %d", LogTypeError))
	}
	return strings.Join(parts, " AND "), args
}
func statsReadSnapshot(ctx context.Context, db *sql.DB, fn func(*sql.Tx) (any, error)) (any, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *RequestStatistics) freshness(ctx context.Context, tx *sql.Tx, q RequestStatsQuery) (statsFreshness, int64, error) {
	state, err := readStatsBootstrap(ctx, tx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = statsError(503, "分钟统计尚未生成首批汇总")
		}
		return statsFreshness{}, 0, err
	}
	var latest sql.NullInt64
	err = tx.QueryRowContext(ctx, statsLatestMinuteSQL).Scan(&latest)
	if err != nil {
		return statsFreshness{}, 0, err
	}
	// 覆盖范围由已成功读取的汇总记录推导；指标只描述已汇总日志，不依赖人工确认开关。
	f := statsFreshness{CoverageStart: state.CoverageStart, RetainedFrom: statsRetainedFrom(state.CoverageStart, time.Now()), Coverage: "unknown", Incomplete: true, SourceMutability: "late_data_requires_explicit_recompute", SourceHealthy: true}
	if q.Start < f.RetainedFrom {
		f.CoverageIssues = append(f.CoverageIssues, "查询范围包含统计覆盖或保留边界之前的时间；该部分暂无数据")
	}
	before := int64(0)
	if latest.Valid {
		if latest.Int64%60 != 0 || latest.Int64 < state.CoverageStart || latest.Int64 >= time.Now().Unix()/60*60 {
			return f, 0, fmt.Errorf("invalid statistics boundary")
		}
		before = latest.Int64 + 60
		if max(q.Start, f.RetainedFrom) < min(q.End, before) {
			f.Coverage = "observed"
		}
		f.Incomplete = q.Start < f.RetainedFrom || q.End > before || !f.SourceHealthy
		if q.Admin {
			f.Latest = &latest.Int64
			f.Before = &before
		}
	}
	return f, before, err
}
func newStatsResponse(q RequestStatsQuery, f statsFreshness) statsResponse {
	intervals := map[int64]string{60: "1m", 300: "5m", 900: "15m", 3600: "1h"}
	interval := intervals[q.Interval]
	if interval == "" {
		interval = fmt.Sprintf("%dh", q.Interval/3600)
	}
	return statsResponse{Version: RequestStatsMetricVersion, Scope: requestStatsScope, Start: q.Start, End: q.End, Interval: interval, IntervalSeconds: q.Interval, Filters: q.Filters, Freshness: f}
}

// SQL 只扫描仍有覆盖的部分，响应时间轴仍使用用户提交的完整范围。
func statsCoveredQuery(q RequestStatsQuery, f statsFreshness, before int64) RequestStatsQuery {
	q.Start = max(q.Start, f.RetainedFrom)
	q.End = max(q.Start, min(q.End, before))
	return q
}

func (f statsFreshness) pointCoverage(start, end, before int64) (observed, complete bool) {
	observed = f.SourceHealthy && max(start, f.RetainedFrom) < min(end, before)
	complete = f.SourceHealthy && start >= f.RetainedFrom && end <= before
	return
}

const statsAggregateColumns = "SUM(total_requests), SUM(success_count), SUM(failure_count), SUM(duration_count), SUM(total_tokens), SUM(duration_sum_us), MIN(duration_min_us), MAX(duration_max_us)"

func scanStatsAggregate(rows *sql.Rows, a *statsAggregate, prefix ...any) error {
	return rows.Scan(append(prefix, &a.Total, &a.Success, &a.Failure, &a.DurationCount, &a.Tokens, &a.DurationSum, &a.Min, &a.Max)...)
}
func (s *RequestStatistics) querySuccess(ctx context.Context, q RequestStatsQuery) (any, error) {
	return statsReadSnapshot(ctx, s.success.db, func(tx *sql.Tx) (any, error) {
		f, before, err := s.freshness(ctx, tx, q)
		if err != nil {
			return nil, err
		}
		where, args := statsWhere(statsCoveredQuery(q, f, before), false)
		rows, err := tx.QueryContext(ctx, "SELECT bucket_start DIV ? * ? AS bucket, "+statsAggregateColumns+" FROM request_stats_minute WHERE "+where+" GROUP BY bucket ORDER BY bucket", append([]any{q.Interval, q.Interval}, args...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		buckets := make(map[int64]*statsAggregate)
		total := new(statsAggregate)
		for rows.Next() {
			a := new(statsAggregate)
			if err = scanStatsAggregate(rows, a, &a.Bucket); err != nil {
				return nil, err
			}
			if len(buckets) >= 2000 {
				return nil, statsError(422, "图表点数超限")
			}
			buckets[a.Bucket] = a
			if err = total.add(a); err != nil {
				return nil, err
			}
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
		axis := statsTimeAxis(q)
		points := make([]statsSuccessPoint, 0, len(axis))
		for _, b := range axis {
			a := buckets[b.BucketStart]
			if a == nil {
				a = new(statsAggregate)
			}
			observed, complete := f.pointCoverage(b.EffectiveStart, b.EffectiveEnd, before)
			points = append(points, statsSuccessPoint{b, a.metrics(b.EffectiveStart, b.EffectiveEnd, buckets[b.BucketStart] != nil || observed, complete)})
		}
		result := newStatsResponse(q, f)
		observed, complete := f.pointCoverage(q.Start, q.End, before)
		result.Summary = total.metrics(q.Start, q.End, len(buckets) > 0 || observed, complete)
		result.Timeseries = points
		return result, nil
	})
}
func (s *RequestStatistics) queryModels(ctx context.Context, q RequestStatsQuery) (any, error) {
	return statsReadSnapshot(ctx, s.models.db, func(tx *sql.Tx) (any, error) {
		f, before, err := s.freshness(ctx, tx, q)
		if err != nil {
			return nil, err
		}
		covered := statsCoveredQuery(q, f, before)
		where, args := statsWhere(covered, false)
		// Top N 与趋势同一条 SQL、同一快照；数据库端收敛到最多六个序列。
		query := `WITH filtered AS (SELECT bucket_start,model_name,total_requests,total_tokens FROM request_stats_minute WHERE ` + where + `),
   top_models AS (SELECT model_name FROM filtered GROUP BY model_name ORDER BY SUM(total_requests) DESC,model_name ASC LIMIT ?)
   SELECT f.bucket_start DIV ? * ? AS bucket, t.model_name, SUM(f.total_requests), SUM(f.total_tokens)
   FROM filtered f LEFT JOIN top_models t ON f.model_name=t.model_name GROUP BY bucket,t.model_name ORDER BY bucket,t.model_name`
		args = append(args, q.TopN, q.Interval, q.Interval)
		if q.Filters.ModelName != nil {
			query = "SELECT bucket_start DIV ? * ? AS bucket,model_name,SUM(total_requests),SUM(total_tokens) FROM request_stats_minute WHERE " + where + " GROUP BY bucket,model_name ORDER BY bucket"
			_, args = statsWhere(covered, false)
			args = append([]any{q.Interval, q.Interval}, args...)
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		buckets := map[int64]*statsAggregate{}
		values := map[int64]map[string]*statsAggregate{}
		series := make([]statsSeries, 0, 6)
		seen := map[string]bool{}
		seriesTotals := map[string]*statsAggregate{}
		if q.Filters.ModelName != nil {
			name := *q.Filters.ModelName
			id := "m_" + hex.EncodeToString([]byte(name))
			seen[id] = true
			series = append(series, statsSeries{ID: id, Kind: "model", Model: &name, Label: name})
			seriesTotals[id] = new(statsAggregate)
		}
		total := new(statsAggregate)
		n := 0
		for rows.Next() {
			var bucket int64
			var name sql.NullString
			a := new(statsAggregate)
			if err = rows.Scan(&bucket, &name, &a.Total, &a.Tokens); err != nil {
				return nil, err
			}
			n++
			if n > 12000 {
				return nil, statsError(422, "序列点数超限")
			}
			id := "other"
			kind, label := "other", "其他"
			var modelName *string
			if name.Valid {
				id = "m_" + hex.EncodeToString([]byte(name.String))
				kind = "model"
				label = name.String
				modelName = &name.String
			}
			if !seen[id] {
				if len(series) >= q.TopN+1 {
					return nil, fmt.Errorf("too many model series")
				}
				seen[id] = true
				series = append(series, statsSeries{ID: id, Kind: kind, Model: modelName, Label: label})
				seriesTotals[id] = new(statsAggregate)
			}
			if buckets[bucket] == nil {
				buckets[bucket] = new(statsAggregate)
				values[bucket] = map[string]*statsAggregate{}
			}
			if err = buckets[bucket].add(a); err != nil {
				return nil, err
			}
			values[bucket][id] = a
			if err = total.add(a); err != nil {
				return nil, err
			}
			if err = seriesTotals[id].add(a); err != nil {
				return nil, err
			}
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
		sort.Slice(series, func(i, j int) bool {
			a, b := series[i], series[j]
			if a.Kind != b.Kind {
				return a.Kind == "model"
			}
			if seriesTotals[a.ID].Total != seriesTotals[b.ID].Total {
				return seriesTotals[a.ID].Total > seriesTotals[b.ID].Total
			}
			return a.Label < b.Label
		})
		axis := statsTimeAxis(q)
		points := make([]statsModelPoint, 0, len(axis))
		for _, b := range axis {
			a := buckets[b.BucketStart]
			if a == nil {
				a = new(statsAggregate)
			}
			observed, complete := f.pointCoverage(b.EffectiveStart, b.EffectiveEnd, before)
			observed = observed || buckets[b.BucketStart] != nil
			point := statsModelPoint{
				statsBounds:  b,
				statsMetrics: a.requestMetrics(b.EffectiveStart, b.EffectiveEnd, observed, complete),
				Values:       make(map[string]*uint64, len(series)),
				Rates:        make(map[string]statsModelRates, len(series)),
			}
			for _, line := range series {
				model := values[b.BucketStart][line.ID]
				if model == nil {
					model = new(statsAggregate)
				}
				metrics := model.requestMetrics(b.EffectiveStart, b.EffectiveEnd, observed, complete)
				point.Values[line.ID] = metrics.Total
				point.Rates[line.ID] = statsModelRates{RPM: metrics.RPM, TPM: metrics.TPM}
			}
			points = append(points, point)
		}
		result := newStatsResponse(q, f)
		result.TopN = q.TopN
		result.Series = &series
		result.Timeseries = points
		observed, complete := f.pointCoverage(q.Start, q.End, before)
		result.Summary = total.requestMetrics(q.Start, q.End, len(buckets) > 0 || observed, complete)
		for i := range series {
			series[i].Summary = seriesTotals[series[i].ID].requestMetrics(q.Start, q.End, len(buckets) > 0 || observed, complete)
		}
		return result, nil
	})
}

type RequestStatsFailure struct {
	ID         int64    `json:"id"`
	RequestID  string   `json:"request_id"`
	CreatedAt  int64    `json:"created_at"`
	DurationMS *float64 `json:"duration_ms"`
	ModelName  string   `json:"model_name"`
	TokenName  string   `json:"token_name"`
	Content    string   `json:"content"`
	UserID     *int64   `json:"user_id,omitempty"`
	Username   *string  `json:"username,omitempty"`
	ChannelID  *int64   `json:"channel_id,omitempty"`
}
type statsFailureResponse struct {
	Version  int                   `json:"metric_version"`
	List     []RequestStatsFailure `json:"list"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"pagesize"`
	Total    uint64                `json:"total"`
}

func (s *RequestStatistics) queryFailures(ctx context.Context, q RequestStatsQuery) (any, error) {
	return statsReadSnapshot(ctx, s.failures.db, func(tx *sql.Tx) (any, error) {
		f, _, err := s.freshness(ctx, tx, q)
		if err != nil {
			return nil, err
		}
		if q.Start < f.RetainedFrom {
			return nil, statsError(422, "查询范围早于统计覆盖或保留边界")
		}
		where, args := statsWhere(q, true)
		result := statsFailureResponse{Version: RequestStatsMetricVersion, List: make([]RequestStatsFailure, 0, q.PageSize), Page: q.Page, PageSize: q.PageSize}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs WHERE "+where, args...).Scan(&result.Total); err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,COALESCE(request_id,''),created_at,"+statsDurationUSSQL+",model_name,COALESCE(token_name,''),COALESCE(content,''),user_id,channel_id,COALESCE(username,'') FROM logs WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", append(args, q.PageSize, (q.Page-1)*q.PageSize)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var item RequestStatsFailure
			var duration sql.NullInt64
			var user, channel int64
			var username string
			if err = rows.Scan(&item.ID, &item.RequestID, &item.CreatedAt, &duration, &item.ModelName, &item.TokenName, &item.Content, &user, &channel, &username); err != nil {
				return nil, err
			}
			if duration.Valid {
				item.DurationMS = statsRatio(big.NewInt(duration.Int64), big.NewInt(1000), 3)
			}
			if q.Admin {
				item.UserID = &user
				item.Username = &username
				item.ChannelID = &channel
			}
			result.List = append(result.List, item)
		}
		return result, rows.Err()
	})
}
