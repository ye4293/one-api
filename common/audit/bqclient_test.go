package audit

import "testing"

func TestBigQuerySchemaHasAllColumns(t *testing.T) {
	schema := buildBQSchema()
	want := []string{
		"event_time", "x_request_id", "user_id", "username", "channel_id",
		"token_name", "origin_model", "actual_model", "is_stream", "status_code",
		"duration_ms", "original_req_headers", "original_req_body",
		"converted_req_headers", "converted_req_body", "converted_same_as_original",
		"upstream_response", "client_response", "truncated_fields", "dropped_note",
	}
	got := map[string]bool{}
	for _, f := range schema {
		got[f.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("BigQuery schema 缺少列 %s", w)
		}
	}
}
