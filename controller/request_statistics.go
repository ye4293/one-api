package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/model"
)

type requestStatsInput struct {
	QueryType *string `json:"query_type"`
	Start     *int64  `json:"start_timestamp"`
	End       *int64  `json:"end_timestamp"`
	Interval  *string `json:"interval"`
	Username  *string `json:"username"`
	TokenName *string `json:"token_name"`
	ModelName *string `json:"model_name"`
	ChannelID *int64  `json:"channel_id"`
	Version   *int    `json:"metric_version"`
	TopN      *int    `json:"top_n"`
	Page      *int    `json:"page"`
	PageSize  *int    `json:"pagesize"`
}

func requestStatsError(status int, message string) error {
	return &model.RequestStatsError{Status: status, Message: message}
}

// 局部严格解码，保留缺省与零值差异，并拒绝重复键和显式 null。
func decodeRequestStats(c *gin.Context) (requestStatsInput, error) {
	var input requestStatsInput
	if c.Request.URL.RawQuery != "" {
		return input, requestStatsError(400, "筛选条件只能放在 JSON 请求体中")
	}
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		return input, requestStatsError(415, "请求体必须为 application/json")
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return input, requestStatsError(413, "请求体超过 8 KiB")
		}
		return input, requestStatsError(400, "无法读取请求体")
	}
	if !utf8.Valid(body) {
		return input, requestStatsError(400, "JSON 必须为有效 UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return input, requestStatsError(400, "请求体必须为 JSON 对象")
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return input, requestStatsError(400, "JSON 格式错误")
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return input, requestStatsError(400, "JSON 包含重复字段")
		}
		seen[name] = true
		switch name {
		case "query_type", "start_timestamp", "end_timestamp", "interval", "username", "token_name", "model_name", "channel_id", "metric_version", "top_n", "page", "pagesize":
		default:
			return input, requestStatsError(400, "JSON 包含未知字段")
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return input, requestStatsError(400, "字段不能为 null，且必须为有效 JSON")
		}
	}
	if _, err = decoder.Token(); err != nil {
		return input, requestStatsError(400, "JSON 格式错误")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return input, requestStatsError(400, "JSON 对象后有多余内容")
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return input, requestStatsError(400, "字段未知或类型错误；数字必须为整数")
	}
	return input, nil
}

func normalizeRequestStats(in requestStatsInput, models bool, actor, role int, now time.Time) (model.RequestStatsQuery, error) {
	q := model.RequestStatsQuery{Kind: "summary", ActorID: actor, Admin: role >= common.RoleAdminUser, TopN: 5, Page: 1, PageSize: 20}
	if actor <= 0 {
		return q, requestStatsError(401, "请先登录")
	}
	if !q.Admin && (in.Username != nil || in.ChannelID != nil) {
		return q, requestStatsError(403, "普通用户不能指定用户或渠道")
	}
	if models {
		q.Kind = "models"
		if in.QueryType != nil {
			return q, requestStatsError(400, "模型接口不接受 query_type")
		}
	} else if in.QueryType != nil {
		q.Kind = *in.QueryType
		if q.Kind != "summary" && q.Kind != "failures" {
			return q, requestStatsError(400, "无效的 query_type")
		}
	}
	if q.Kind != "summary" && q.Kind != "models" && q.Kind != "failures" {
		return q, requestStatsError(400, "无效的 query_type")
	}
	if in.Start == nil || in.End == nil {
		return q, requestStatsError(400, "必须指定起止时间")
	}
	q.Start, q.End = *in.Start, *in.End
	if q.Start < 0 || q.Start%60 != 0 || q.End%60 != 0 || q.End <= q.Start {
		return q, requestStatsError(400, "时间必须分钟对齐且开始早于结束")
	}
	if q.End > now.Unix()/60*60 {
		return q, requestStatsError(422, "仅支持已结束的分钟")
	}
	if in.Username != nil && utf8.RuneCountInString(*in.Username) > 191 {
		return q, requestStatsError(400, "用户名不能超过 191 字符")
	}
	if in.TokenName != nil && utf8.RuneCountInString(*in.TokenName) > 255 {
		return q, requestStatsError(400, "Token 名称超过 255 字符")
	}
	// 输入框留空不筛选；非空关键字保留大小写和空格，按历史名称包含匹配。
	if in.Username != nil && strings.TrimSpace(*in.Username) == "" {
		in.Username = nil
	}
	if in.TokenName != nil && strings.TrimSpace(*in.TokenName) == "" {
		in.TokenName = nil
	}
	q.Filters = model.RequestStatsFilters{Username: in.Username, TokenName: in.TokenName, ModelName: in.ModelName, ChannelID: in.ChannelID}
	if in.ChannelID != nil && *in.ChannelID < 0 {
		return q, requestStatsError(400, "渠道 ID 不能为负数")
	}
	if !q.Admin {
		id := int64(actor)
		q.Filters.UserID = &id
	}
	if in.ModelName != nil && (*in.ModelName == "" || strings.TrimSpace(*in.ModelName) != *in.ModelName || utf8.RuneCountInString(*in.ModelName) > 200) {
		return q, requestStatsError(400, "模型名为空、含首尾空白或超过 200 字符")
	}
	if in.Version != nil && *in.Version != model.RequestStatsMetricVersion {
		return q, requestStatsError(422, "不支持的统计版本")
	}
	if !models && in.TopN != nil {
		return q, requestStatsError(400, "仅模型接口接受 top_n")
	}
	if q.Kind == "failures" {
		if in.Interval != nil || in.Version == nil {
			return q, requestStatsError(400, "失败明细必须传 metric_version，且不能传 interval")
		}
		if q.End-q.Start > 3600 {
			return q, requestStatsError(422, "失败明细一次最多查询 1 小时")
		}
		if in.Page != nil {
			q.Page = *in.Page
		}
		if in.PageSize != nil {
			q.PageSize = *in.PageSize
		}
		if q.Page < 1 || q.PageSize < 1 || q.PageSize > 100 {
			return q, requestStatsError(400, "页码或每页条数不合法")
		}
		if q.Page-1 > 10000/q.PageSize {
			return q, requestStatsError(422, "分页偏移超过 10000，请缩小时间或筛选范围")
		}
		return q, nil
	}
	if in.Page != nil || in.PageSize != nil {
		return q, requestStatsError(400, "图表查询不接受分页字段")
	}
	if in.TopN != nil {
		q.TopN = *in.TopN
	}
	if q.TopN < 1 || q.TopN > 5 {
		return q, requestStatsError(400, "top_n 必须为 1 到 5")
	}
	interval := "auto"
	if in.Interval != nil {
		interval = *in.Interval
	}
	intervals := map[string]int64{"1m": 60, "5m": 300, "15m": 900, "1h": 3600}
	q.Interval = 60
	if interval != "auto" {
		q.Interval = intervals[interval]
		if q.Interval == 0 {
			return q, requestStatsError(400, "无效的时间粒度")
		}
	}
	// 保留完整查询范围；指定粒度过细时也自动提升，避免整张报表报错。
	for _, seconds := range []int64{300, 900, 3600} {
		if (q.End-1)/q.Interval-q.Start/q.Interval+1 <= 2000 {
			break
		}
		q.Interval = max(q.Interval, seconds)
	}
	if (q.End-1)/q.Interval-q.Start/q.Interval+1 > 2000 {
		// 超长范围按整小时倍数分桶，预留一个首尾裁剪桶。
		q.Interval = ((q.End-q.Start-1)/(1999*3600) + 1) * 3600
	}
	return q, nil
}

func GetRequestStatistics(c *gin.Context)      { serveRequestStats(c, false) }
func GetModelRequestStatistics(c *gin.Context) { serveRequestStats(c, true) }

func serveRequestStats(c *gin.Context, models bool) {
	c.Header("Cache-Control", "private, no-store")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	in, err := decodeRequestStats(c)
	var q model.RequestStatsQuery
	if err == nil {
		q, err = normalizeRequestStats(in, models, c.GetInt("id"), c.GetInt("role"), time.Now())
	}
	writeRequestStatistics(c, ctx, q, err)
}

func writeRequestStatistics(c *gin.Context, ctx context.Context, q model.RequestStatsQuery, err error) {
	var body []byte
	if err == nil {
		body, err = model.DefaultRequestStatistics.Query(ctx, q)
	}
	if err != nil {
		status, message := 503, "统计查询暂不可用"
		var known *model.RequestStatsError
		if errors.As(err, &known) {
			status, message = known.Status, known.Message
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status, message = 504, "统计查询超时或已取消"
		} else {
			logger.Error(ctx, "request statistics query failed: "+err.Error())
		}
		c.JSON(status, gin.H{"success": false, "message": message})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}
