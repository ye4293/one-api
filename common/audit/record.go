package audit

import "time"

type AuditRecord struct {
	EventTime               time.Time
	XRequestID              string
	UserID                  int
	Username                string
	ChannelID               int
	TokenName               string
	OriginModel             string
	ActualModel             string
	IsStream                bool
	StatusCode              int
	DurationMS              int64
	OriginalReqHeaders      string
	OriginalReqBody         string
	ConvertedReqHeaders     string
	ConvertedReqBody        string
	ConvertedSameAsOriginal bool
	UpstreamResponse        string
	ClientResponse          string
	TruncatedFields         []string
	DroppedNote             string
}

// Size 估算单条记录占用字节，用于内存计量。
func (r *AuditRecord) Size() int {
	return len(r.OriginalReqHeaders) + len(r.OriginalReqBody) +
		len(r.ConvertedReqHeaders) + len(r.ConvertedReqBody) +
		len(r.UpstreamResponse) + len(r.ClientResponse) + 256
}
