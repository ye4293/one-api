package audit

import (
	"encoding/json"
	"time"
)

type bqRow struct {
	EventTime               string   `json:"event_time"`
	XRequestID              string   `json:"x_request_id"`
	UserID                  int      `json:"user_id"`
	Username                string   `json:"username"`
	ChannelID               int      `json:"channel_id"`
	TokenName               string   `json:"token_name"`
	OriginModel             string   `json:"origin_model"`
	ActualModel             string   `json:"actual_model"`
	IsStream                bool     `json:"is_stream"`
	StatusCode              int      `json:"status_code"`
	DurationMS              int64    `json:"duration_ms"`
	OriginalReqHeaders      string   `json:"original_req_headers"`
	OriginalReqBody         string   `json:"original_req_body"`
	ConvertedReqHeaders     string   `json:"converted_req_headers"`
	ConvertedReqBody        string   `json:"converted_req_body"`
	ConvertedSameAsOriginal bool     `json:"converted_same_as_original"`
	UpstreamResponse        string   `json:"upstream_response"`
	ClientResponse          string   `json:"client_response"`
	TruncatedFields         []string `json:"truncated_fields"`
	DroppedNote             string   `json:"dropped_note"`
}

func toNDJSONLine(r *AuditRecord) string {
	convBody := r.ConvertedReqBody
	if r.ConvertedSameAsOriginal {
		convBody = ""
	}
	row := bqRow{
		EventTime:               r.EventTime.UTC().Format("2006-01-02 15:04:05.000000"),
		XRequestID:              r.XRequestID,
		UserID:                  r.UserID,
		Username:                r.Username,
		ChannelID:               r.ChannelID,
		TokenName:               r.TokenName,
		OriginModel:             r.OriginModel,
		ActualModel:             r.ActualModel,
		IsStream:                r.IsStream,
		StatusCode:              r.StatusCode,
		DurationMS:              r.DurationMS,
		OriginalReqHeaders:      r.OriginalReqHeaders,
		OriginalReqBody:         r.OriginalReqBody,
		ConvertedReqHeaders:     r.ConvertedReqHeaders,
		ConvertedReqBody:        convBody,
		ConvertedSameAsOriginal: r.ConvertedSameAsOriginal,
		UpstreamResponse:        r.UpstreamResponse,
		ClientResponse:          r.ClientResponse,
		TruncatedFields:         r.TruncatedFields,
		DroppedNote:             r.DroppedNote,
	}
	b, _ := json.Marshal(row)
	return string(b) + "\n"
}

var _ = time.Now
