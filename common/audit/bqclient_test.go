package audit

import (
	"testing"
	"time"

	"cloud.google.com/go/bigquery/storage/managedwriter/adapt"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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

func testDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	storageSchema, err := adapt.BQSchemaToStorageTableSchema(buildBQSchema())
	if err != nil {
		t.Fatalf("BQSchemaToStorageTableSchema: %v", err)
	}
	desc, err := adapt.StorageSchemaToProto2Descriptor(storageSchema, "AuditRow")
	if err != nil {
		t.Fatalf("StorageSchemaToProto2Descriptor: %v", err)
	}
	md, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("descriptor type: %T", desc)
	}
	return md
}

func TestProtoDescriptorMatchesSchema(t *testing.T) {
	md := testDescriptor(t)
	schema := buildBQSchema()
	if int(md.Fields().Len()) != len(schema) {
		t.Errorf("proto descriptor 字段数 %d != BQ schema 列数 %d", md.Fields().Len(), len(schema))
	}
	for _, f := range schema {
		if md.Fields().ByName(protoreflect.Name(f.Name)) == nil {
			t.Errorf("proto descriptor 缺少字段 %s", f.Name)
		}
	}
}

func TestMarshalProtoRow(t *testing.T) {
	md := testDescriptor(t)
	r := &AuditRecord{
		EventTime:               time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC),
		XRequestID:              "req-test-1",
		UserID:                  42,
		Username:                "alice",
		ChannelID:               7,
		TokenName:               "tok-1",
		OriginModel:             "gpt-4",
		ActualModel:             "gpt-4-0613",
		IsStream:                true,
		StatusCode:              200,
		DurationMS:              1500,
		OriginalReqHeaders:      `{"Content-Type":["application/json"]}`,
		OriginalReqBody:         `{"model":"gpt-4"}`,
		ConvertedReqBody:        `{"model":"gpt-4"}`,
		ConvertedSameAsOriginal: true,
		TruncatedFields:         []string{"upstream_response"},
		DroppedNote:             "",
	}
	b, err := marshalProtoRow(r, md)
	if err != nil {
		t.Fatalf("marshalProtoRow 失败: %v", err)
	}
	if len(b) == 0 {
		t.Error("序列化结果不应为空")
	}
}

func TestMarshalProtoRowEmptyRecord(t *testing.T) {
	md := testDescriptor(t)
	r := &AuditRecord{EventTime: time.Now()}
	b, err := marshalProtoRow(r, md)
	if err != nil {
		t.Fatalf("空记录序列化不应失败: %v", err)
	}
	if len(b) == 0 {
		t.Error("空记录序列化结果不应为空")
	}
}
