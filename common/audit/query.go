package audit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/athena"
	athenaTypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
)

type AuditSummary struct {
	EventTime   time.Time `json:"event_time"`
	XRequestID  string    `json:"x_request_id"`
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`
	ChannelID   int64     `json:"channel_id"`
	TokenName   string    `json:"token_name"`
	OriginModel string    `json:"origin_model"`
	ActualModel string    `json:"actual_model"`
	IsStream    bool      `json:"is_stream"`
	StatusCode  int64     `json:"status_code"`
	DurationMS  int64     `json:"duration_ms"`
	DroppedNote string    `json:"dropped_note"`
}

type AuditDetail struct {
	AuditSummary
	OriginalReqBody         string   `json:"original_req_body"`
	OriginalReqHeaders      string   `json:"original_req_headers"`
	ConvertedReqBody        string   `json:"converted_req_body"`
	ConvertedReqHeaders     string   `json:"converted_req_headers"`
	ConvertedSameAsOriginal bool     `json:"converted_same_as_original"`
	UpstreamResponse        string   `json:"upstream_response"`
	ClientResponse          string   `json:"client_response"`
	TruncatedFields         []string `json:"truncated_fields"`
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

var (
	ErrAuditNotEnabled = errors.New("audit is not enabled")
	ErrInvalidParam    = errors.New("invalid query parameter")

	reXRequestID = regexp.MustCompile(`^[a-zA-Z0-9\-\_\.]{1,128}$`)
	reModel      = regexp.MustCompile(`^[a-zA-Z0-9\-\.\/\:\_]{1,128}$`)
)

func QueryLogs(ctx context.Context, params QueryParams) ([]AuditSummary, int64, error) {
	if awsClient == nil {
		return nil, 0, ErrAuditNotEnabled
	}

	tableRef := fmt.Sprintf(`"%s"."%s"`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)

	where, err := buildAthenaWhere(params)
	if err != nil {
		return nil, 0, err
	}

	offset := (params.Page - 1) * params.PageSize
	rowStart := offset + 1
	rowEnd := offset + params.PageSize
	// Athena 不支持 LIMIT...OFFSET，用 ROW_NUMBER() 子查询分页
	sql := fmt.Sprintf(
		`SELECT event_time, x_request_id, user_id, username, channel_id,
		 token_name, origin_model, actual_model, is_stream,
		 status_code, duration_ms, dropped_note, _total
		 FROM (
		   SELECT event_time, x_request_id, user_id, username, channel_id,
		          token_name, origin_model, actual_model, is_stream,
		          status_code, duration_ms, dropped_note,
		          COUNT(*) OVER() AS _total,
		          ROW_NUMBER() OVER(ORDER BY event_time DESC) AS _rn
		   FROM %s %s
		 ) t
		 WHERE _rn >= %d AND _rn <= %d`,
		tableRef, where, rowStart, rowEnd)

	result, err := awsClient.executeQuery(ctx, sql)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}

	summaries, total := parseAuditSummaryRowsWithTotal(result)
	return summaries, total, nil
}

func QueryDetail(ctx context.Context, xRequestID string, startTS, endTS int64) (*AuditDetail, error) {
	if awsClient == nil {
		return nil, ErrAuditNotEnabled
	}

	if !reXRequestID.MatchString(xRequestID) {
		return nil, fmt.Errorf("%w: invalid x_request_id format", ErrInvalidParam)
	}

	tableRef := fmt.Sprintf(`"%s"."%s"`, pkgConfig.AthenaDatabase, pkgConfig.AthenaTable)
	startTime := time.Unix(startTS, 0).UTC().Format("2006-01-02 15:04:05")
	endTime := time.Unix(endTS, 0).UTC().Format("2006-01-02 15:04:05")

	sql := fmt.Sprintf(
		`SELECT * FROM %s
		 WHERE x_request_id = '%s'
		 AND event_time >= TIMESTAMP '%s'
		 AND event_time < TIMESTAMP '%s'
		 LIMIT 1`, tableRef, xRequestID, startTime, endTime)

	result, err := awsClient.executeQuery(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("detail query: %w", err)
	}

	if len(result.ResultSet.Rows) <= 1 {
		return nil, nil
	}

	detail := parseAuditDetailRow(result.ResultSet.Rows[1], result.ResultSet.ResultSetMetadata.ColumnInfo)

	// 若四个 body 字段均为空且配置了 S3，按规则推导 key 尝试拉取
	if detail.OriginalReqBody == "" && detail.UpstreamResponse == "" &&
		detail.ClientResponse == "" && pkgConfig.BodyS3Bucket != "" {
		s3Key := bodyS3Key(pkgConfig, detail.EventTime, xRequestID)
		doc, err := awsClient.fetchBodyFromS3(ctx, s3Key)
		if err == nil {
			detail.OriginalReqBody = doc.OriginalReqBody
			detail.ConvertedReqBody = doc.ConvertedReqBody
			detail.UpstreamResponse = doc.UpstreamResponse
			detail.ClientResponse = doc.ClientResponse
		}
	}

	return detail, nil
}

func buildAthenaWhere(params QueryParams) (string, error) {
	startTime := time.Unix(params.StartTimestamp, 0).UTC().Format("2006-01-02 15:04:05")
	endTime := time.Unix(params.EndTimestamp, 0).UTC().Format("2006-01-02 15:04:05")

	clauses := []string{
		fmt.Sprintf("event_time >= TIMESTAMP '%s'", startTime),
		fmt.Sprintf("event_time < TIMESTAMP '%s'", endTime),
	}

	if params.XRequestID != "" {
		if !reXRequestID.MatchString(params.XRequestID) {
			return "", fmt.Errorf("%w: invalid x_request_id format", ErrInvalidParam)
		}
		clauses = append(clauses, fmt.Sprintf("x_request_id = '%s'", params.XRequestID))
	}
	if params.UserID > 0 {
		clauses = append(clauses, fmt.Sprintf("user_id = %d", params.UserID))
	}
	if params.ChannelID > 0 {
		clauses = append(clauses, fmt.Sprintf("channel_id = %d", params.ChannelID))
	}
	if params.ActualModel != "" {
		if !reModel.MatchString(params.ActualModel) {
			return "", fmt.Errorf("%w: invalid actual_model format", ErrInvalidParam)
		}
		clauses = append(clauses, fmt.Sprintf("actual_model = '%s'", params.ActualModel))
	}
	if params.StatusCode > 0 {
		clauses = append(clauses, fmt.Sprintf("status_code = %d", params.StatusCode))
	}

	return "WHERE " + strings.Join(clauses, " AND "), nil
}

func parseAuditSummaryRowsWithTotal(result *athena.GetQueryResultsOutput) ([]AuditSummary, int64) {
	if result == nil || len(result.ResultSet.Rows) <= 1 {
		return []AuditSummary{}, 0
	}

	headers := make([]string, len(result.ResultSet.ResultSetMetadata.ColumnInfo))
	for i, col := range result.ResultSet.ResultSetMetadata.ColumnInfo {
		headers[i] = *col.Name
	}

	var summaries []AuditSummary
	var total int64
	for _, row := range result.ResultSet.Rows[1:] {
		m := rowToMap(row, headers)
		if total == 0 {
			total = parseInt64(m["_total"])
		}
		s := AuditSummary{
			EventTime:   parseAthenaTimestamp(m["event_time"]),
			XRequestID:  m["x_request_id"],
			UserID:      parseInt64(m["user_id"]),
			Username:    m["username"],
			ChannelID:   parseInt64(m["channel_id"]),
			TokenName:   m["token_name"],
			OriginModel: m["origin_model"],
			ActualModel: m["actual_model"],
			IsStream:    m["is_stream"] == "true",
			StatusCode:  parseInt64(m["status_code"]),
			DurationMS:  parseInt64(m["duration_ms"]),
			DroppedNote: m["dropped_note"],
		}
		summaries = append(summaries, s)
	}
	return summaries, total
}

func parseAuditDetailRow(row athenaTypes.Row, columns []athenaTypes.ColumnInfo) *AuditDetail {
	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = *col.Name
	}
	m := rowToMap(row, headers)

	detail := &AuditDetail{
		AuditSummary: AuditSummary{
			EventTime:   parseAthenaTimestamp(m["event_time"]),
			XRequestID:  m["x_request_id"],
			UserID:      parseInt64(m["user_id"]),
			Username:    m["username"],
			ChannelID:   parseInt64(m["channel_id"]),
			TokenName:   m["token_name"],
			OriginModel: m["origin_model"],
			ActualModel: m["actual_model"],
			IsStream:    m["is_stream"] == "true",
			StatusCode:  parseInt64(m["status_code"]),
			DurationMS:  parseInt64(m["duration_ms"]),
			DroppedNote: m["dropped_note"],
		},
		OriginalReqHeaders:      m["original_req_headers"],
		OriginalReqBody:         m["original_req_body"],
		ConvertedReqHeaders:     m["converted_req_headers"],
		ConvertedReqBody:        m["converted_req_body"],
		ConvertedSameAsOriginal: m["converted_same_as_original"] == "true",
		UpstreamResponse:        m["upstream_response"],
		ClientResponse:          m["client_response"],
	}

	if tf := m["truncated_fields"]; tf != "" {
		tf = strings.Trim(tf, "[]")
		if tf != "" {
			for _, f := range strings.Split(tf, ",") {
				f = strings.TrimSpace(strings.Trim(f, "\" "))
				if f != "" {
					detail.TruncatedFields = append(detail.TruncatedFields, f)
				}
			}
		}
	}

	return detail
}

func rowToMap(row athenaTypes.Row, headers []string) map[string]string {
	m := make(map[string]string, len(headers))
	for i, datum := range row.Data {
		if i < len(headers) && datum.VarCharValue != nil {
			m[headers[i]] = *datum.VarCharValue
		}
	}
	return m
}

func parseAthenaTimestamp(s string) time.Time {
	for _, layout := range []string{
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02 15:04:05.000000",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
