package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToNDJSONLine(t *testing.T) {
	r := &AuditRecord{
		EventTime:   time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		XRequestID:  "req-1",
		OriginModel: "gpt-4",
	}
	line := toNDJSONLine(r)
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("NDJSON 行应以换行结尾")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("应为合法 JSON: %v", err)
	}
	if m["x_request_id"] != "req-1" {
		t.Errorf("字段名应为 BigQuery snake_case")
	}
	if m["event_time"] == nil {
		t.Errorf("event_time 应存在（BigQuery TIMESTAMP 可解析格式）")
	}
}

func TestConvertedSameAsOriginalEmptiesBody(t *testing.T) {
	r := &AuditRecord{
		OriginalReqBody:         `{"a":1}`,
		ConvertedReqBody:        `{"a":1}`,
		ConvertedSameAsOriginal: true,
	}
	line := toNDJSONLine(r)
	var m map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(line)), &m)
	if m["converted_req_body"] != "" {
		t.Errorf("converted_same_as_original=true 时 converted_req_body 应为空")
	}
}
