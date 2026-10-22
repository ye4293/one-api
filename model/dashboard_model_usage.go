package model

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"sort"
)

type dashboardModelUsage struct {
	Requests         *uint64 `json:"total_requests"`
	USD              *string `json:"used_quota_usd"`
	Incomplete       bool    `json:"incomplete"`
	AmountIncomplete bool    `json:"amount_incomplete"`
}

type dashboardUsageAggregate struct {
	requests uint64
	usd      big.Rat
	unknown  bool
}

func (a *dashboardUsageAggregate) add(b *dashboardUsageAggregate) error {
	if a.requests > math.MaxUint64-b.requests {
		return fmt.Errorf("dashboard request count overflow")
	}
	a.requests += b.requests
	a.usd.Add(&a.usd, &b.usd)
	a.unknown = a.unknown || b.unknown
	return nil
}

func (a *dashboardUsageAggregate) metrics(observed, complete bool) dashboardModelUsage {
	m := dashboardModelUsage{Incomplete: !complete, AmountIncomplete: !complete || a.unknown}
	if observed {
		m.Requests = &a.requests
		if !a.unknown {
			usd := a.usd.FloatString(6)
			m.USD = &usd
		}
	}
	return m
}

type dashboardModelSeries struct {
	ID      string              `json:"id"`
	Model   string              `json:"model_name"`
	Summary dashboardModelUsage `json:"summary"`
}

type dashboardModelHour struct {
	statsBounds
	dashboardModelUsage
	Values map[string]dashboardModelUsage `json:"values"`
}

type dashboardModelReport struct {
	Start           int64                  `json:"start_timestamp"`
	End             int64                  `json:"end_timestamp"`
	AggregatedUntil int64                  `json:"aggregated_until"`
	Freshness       statsFreshness         `json:"freshness"`
	Summary         dashboardModelUsage    `json:"summary"`
	Series          []dashboardModelSeries `json:"series"`
	Timeseries      []dashboardModelHour   `json:"timeseries"`
}

// 复用模型报表的连接池、授权筛选和查询预算，每次读取最新分钟汇总。
func (s *RequestStatistics) queryDashboardModels(ctx context.Context, q RequestStatsQuery) (any, error) {
	return statsReadSnapshot(ctx, s.models.db, func(tx *sql.Tx) (any, error) {
		freshness, before, err := s.freshness(ctx, tx, q)
		if err != nil {
			return nil, err
		}
		where, args := statsWhere(statsCoveredQuery(q, freshness, before), false)
		rows, err := tx.QueryContext(ctx, `SELECT bucket_start DIV 3600 * 3600 AS hour,
 model_name,SUM(total_requests),SUM(used_quota_usd),SUM(used_quota_usd IS NULL)
 FROM request_stats_minute WHERE `+where+` GROUP BY hour,model_name ORDER BY hour,model_name LIMIT 12001`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		total := new(dashboardUsageAggregate)
		models := map[string]*dashboardUsageAggregate{}
		hours := map[int64]*dashboardUsageAggregate{}
		values := map[int64]map[string]*dashboardUsageAggregate{}
		series := make([]dashboardModelSeries, 0)
		count := 0
		for rows.Next() {
			count++
			if count > 12000 {
				return nil, statsError(422, "模型小时统计超过图表容量")
			}
			var hour int64
			var name string
			var usd sql.NullString
			var missing uint64
			a := new(dashboardUsageAggregate)
			if err := rows.Scan(&hour, &name, &a.requests, &usd, &missing); err != nil {
				return nil, err
			}
			a.unknown = missing > 0 || !usd.Valid
			if usd.Valid {
				if _, ok := a.usd.SetString(usd.String); !ok {
					return nil, fmt.Errorf("invalid dashboard usage amount")
				}
			}
			id := "m_" + hex.EncodeToString([]byte(name))
			if models[id] == nil {
				if len(series) >= 500 {
					return nil, statsError(422, "模型数量超过图表容量（500）")
				}
				models[id] = new(dashboardUsageAggregate)
				series = append(series, dashboardModelSeries{ID: id, Model: name})
			}
			if hours[hour] == nil {
				hours[hour] = new(dashboardUsageAggregate)
				values[hour] = map[string]*dashboardUsageAggregate{}
			}
			values[hour][id] = a
			for _, sum := range []*dashboardUsageAggregate{total, models[id], hours[hour]} {
				if err := sum.add(a); err != nil {
					return nil, err
				}
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// 返回请求量基础顺序；前端金额视图在保持模型颜色的同时按金额重排。
		sort.Slice(series, func(i, j int) bool {
			a, b := series[i], series[j]
			if models[a.ID].requests != models[b.ID].requests {
				return models[a.ID].requests > models[b.ID].requests
			}
			return a.Model < b.Model
		})
		observed, complete := freshness.pointCoverage(q.Start, q.End, before)
		for i := range series {
			series[i].Summary = models[series[i].ID].metrics(observed, complete)
		}
		result := dashboardModelReport{
			Start: q.Start, End: q.End, AggregatedUntil: min(q.End, before), Freshness: freshness, Series: series,
			Summary: total.metrics(observed, complete), Timeseries: make([]dashboardModelHour, 0, 24),
		}
		// 始终保留当前小时这一根柱，包括整点尚无完整分钟时。
		axis := q
		axis.End = q.Start + 24*3600
		for _, bounds := range statsTimeAxis(axis) {
			bounds.EffectiveEnd = min(bounds.BucketEnd, q.End)
			bounds.Partial = bounds.EffectiveEnd < bounds.BucketEnd
			observed, complete := freshness.pointCoverage(bounds.EffectiveStart, bounds.EffectiveEnd, before)
			sum := hours[bounds.BucketStart]
			if sum == nil {
				sum = new(dashboardUsageAggregate)
			}
			point := dashboardModelHour{statsBounds: bounds, dashboardModelUsage: sum.metrics(observed, complete), Values: make(map[string]dashboardModelUsage, len(series))}
			for _, line := range series {
				a := values[bounds.BucketStart][line.ID]
				if a == nil {
					a = new(dashboardUsageAggregate)
				}
				point.Values[line.ID] = a.metrics(observed, complete)
			}
			result.Timeseries = append(result.Timeseries, point)
		}
		return result, nil
	})
}
