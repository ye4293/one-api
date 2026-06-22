package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
)

type AuditSummary struct {
	EventTime   time.Time `json:"event_time" bigquery:"event_time"`
	XRequestID  string    `json:"x_request_id" bigquery:"x_request_id"`
	UserID      int64     `json:"user_id" bigquery:"user_id"`
	Username    string    `json:"username" bigquery:"username"`
	ChannelID   int64     `json:"channel_id" bigquery:"channel_id"`
	TokenName   string    `json:"token_name" bigquery:"token_name"`
	OriginModel string    `json:"origin_model" bigquery:"origin_model"`
	ActualModel string    `json:"actual_model" bigquery:"actual_model"`
	IsStream    bool      `json:"is_stream" bigquery:"is_stream"`
	StatusCode  int64     `json:"status_code" bigquery:"status_code"`
	DurationMS  int64     `json:"duration_ms" bigquery:"duration_ms"`
	DroppedNote string    `json:"dropped_note" bigquery:"dropped_note"`
}

type AuditDetail struct {
	AuditSummary
	OriginalReqHeaders      string   `json:"original_req_headers" bigquery:"original_req_headers"`
	OriginalReqBody         string   `json:"original_req_body" bigquery:"original_req_body"`
	ConvertedReqHeaders     string   `json:"converted_req_headers" bigquery:"converted_req_headers"`
	ConvertedReqBody        string   `json:"converted_req_body" bigquery:"converted_req_body"`
	ConvertedSameAsOriginal bool     `json:"converted_same_as_original" bigquery:"converted_same_as_original"`
	UpstreamResponse        string   `json:"upstream_response" bigquery:"upstream_response"`
	ClientResponse          string   `json:"client_response" bigquery:"client_response"`
	TruncatedFields         []string `json:"truncated_fields" bigquery:"truncated_fields"`
}

type QueryParams struct {
	StartTimestamp int64
	EndTimestamp   int64
	Page           int
	PageSize       int
	XRequestID     string
	UserID         int
	ChannelID      int
	ActualModel    string
	StatusCode     int
}

var ErrAuditNotEnabled = errors.New("audit is not enabled")

func QueryLogs(ctx context.Context, params QueryParams) ([]AuditSummary, int64, error) {
	if gcp == nil {
		return nil, 0, ErrAuditNotEnabled
	}

	tableRef := fmt.Sprintf("`%s.%s.%s`", pkgConfig.GCPProject, pkgConfig.BQDataset, pkgConfig.BQTable)

	where, qParams := buildWhereClause(params)

	countSQL := fmt.Sprintf("SELECT COUNT(*) as total FROM %s %s", tableRef, where)
	countQ := gcp.bq.Query(countSQL)
	countQ.Parameters = qParams

	countIt, err := countQ.Read(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count query: %w", err)
	}
	var countRow struct{ Total int64 }
	if err := countIt.Next(&countRow); err != nil {
		return nil, 0, fmt.Errorf("count read: %w", err)
	}
	total := countRow.Total

	if total == 0 {
		return []AuditSummary{}, 0, nil
	}

	offset := (params.Page - 1) * params.PageSize
	dataSQL := fmt.Sprintf(
		`SELECT event_time, x_request_id, user_id, username, channel_id,
		 token_name, origin_model, actual_model, is_stream,
		 status_code, duration_ms, dropped_note
		 FROM %s %s ORDER BY event_time DESC LIMIT @limit OFFSET @offset`,
		tableRef, where)

	qParams = append(qParams,
		bigquery.QueryParameter{Name: "limit", Value: int64(params.PageSize)},
		bigquery.QueryParameter{Name: "offset", Value: int64(offset)},
	)

	dataQ := gcp.bq.Query(dataSQL)
	dataQ.Parameters = qParams

	it, err := dataQ.Read(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("data query: %w", err)
	}

	var results []AuditSummary
	for {
		var row AuditSummary
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("row read: %w", err)
		}
		results = append(results, row)
	}

	return results, total, nil
}

func QueryDetail(ctx context.Context, xRequestID string, startTS, endTS int64) (*AuditDetail, error) {
	if gcp == nil {
		return nil, ErrAuditNotEnabled
	}

	tableRef := fmt.Sprintf("`%s.%s.%s`", pkgConfig.GCPProject, pkgConfig.BQDataset, pkgConfig.BQTable)

	sql := fmt.Sprintf(
		`SELECT * FROM %s
		 WHERE x_request_id = @x_request_id
		 AND event_time >= TIMESTAMP_SECONDS(@start_ts)
		 AND event_time < TIMESTAMP_SECONDS(@end_ts)
		 LIMIT 1`, tableRef)

	q := gcp.bq.Query(sql)
	q.Parameters = []bigquery.QueryParameter{
		{Name: "x_request_id", Value: xRequestID},
		{Name: "start_ts", Value: startTS},
		{Name: "end_ts", Value: endTS},
	}

	it, err := q.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("detail query: %w", err)
	}

	var row AuditDetail
	err = it.Next(&row)
	if err == iterator.Done {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("detail read: %w", err)
	}

	return &row, nil
}

func buildWhereClause(params QueryParams) (string, []bigquery.QueryParameter) {
	where := "WHERE event_time >= TIMESTAMP_SECONDS(@start_ts) AND event_time < TIMESTAMP_SECONDS(@end_ts)"
	qParams := []bigquery.QueryParameter{
		{Name: "start_ts", Value: params.StartTimestamp},
		{Name: "end_ts", Value: params.EndTimestamp},
	}

	if params.XRequestID != "" {
		where += " AND x_request_id = @x_request_id"
		qParams = append(qParams, bigquery.QueryParameter{Name: "x_request_id", Value: params.XRequestID})
	}
	if params.UserID > 0 {
		where += " AND user_id = @user_id"
		qParams = append(qParams, bigquery.QueryParameter{Name: "user_id", Value: int64(params.UserID)})
	}
	if params.ChannelID > 0 {
		where += " AND channel_id = @channel_id"
		qParams = append(qParams, bigquery.QueryParameter{Name: "channel_id", Value: int64(params.ChannelID)})
	}
	if params.ActualModel != "" {
		where += " AND actual_model = @actual_model"
		qParams = append(qParams, bigquery.QueryParameter{Name: "actual_model", Value: params.ActualModel})
	}
	if params.StatusCode > 0 {
		where += " AND status_code = @status_code"
		qParams = append(qParams, bigquery.QueryParameter{Name: "status_code", Value: int64(params.StatusCode)})
	}

	return where, qParams
}
