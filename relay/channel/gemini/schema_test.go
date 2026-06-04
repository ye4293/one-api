package gemini

import (
	"reflect"
	"testing"
)

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	return m
}

func TestCleanFunctionParameters_StripsIllegalFields(t *testing.T) {
	in := map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"additionalProperties": false,
		"type":                 "object",
		"properties": map[string]any{
			"age": map[string]any{
				"type":             "integer",
				"exclusiveMinimum": float64(0),
			},
		},
	}
	out := asMap(t, cleanFunctionParameters(in))

	if _, ok := out["$schema"]; ok {
		t.Error("$schema should be removed")
	}
	if _, ok := out["additionalProperties"]; ok {
		t.Error("additionalProperties should be removed")
	}
	if out["type"] != "OBJECT" {
		t.Errorf("type want OBJECT, got %v", out["type"])
	}
	age := asMap(t, asMap(t, out["properties"])["age"])
	if _, ok := age["exclusiveMinimum"]; ok {
		t.Error("exclusiveMinimum should be removed")
	}
	if age["minimum"] != float64(0) {
		t.Errorf("exclusiveMinimum should downgrade to minimum:0, got %v", age["minimum"])
	}
	if age["type"] != "INTEGER" {
		t.Errorf("type want INTEGER, got %v", age["type"])
	}
}

func TestCleanFunctionParameters_ConstToEnum(t *testing.T) {
	in := map[string]any{
		"type":  "string",
		"const": "fixed",
	}
	out := asMap(t, cleanFunctionParameters(in))
	enum, ok := out["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "fixed" {
		t.Errorf("const should downgrade to enum:[fixed], got %v", out["enum"])
	}
	if _, ok := out["const"]; ok {
		t.Error("const should be removed")
	}
}

func TestCleanFunctionParameters_UnionTypeNull(t *testing.T) {
	in := map[string]any{
		"type": []any{"string", "null"},
	}
	out := asMap(t, cleanFunctionParameters(in))
	if out["type"] != "STRING" {
		t.Errorf("union type want STRING, got %v", out["type"])
	}
	if out["nullable"] != true {
		t.Errorf("union with null want nullable:true, got %v", out["nullable"])
	}
}

func TestCleanFunctionParameters_NestedRecursion(t *testing.T) {
	in := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outer": map[string]any{
				"type":    "object",
				"$schema": "x",
				"properties": map[string]any{
					"inner": map[string]any{
						"type":                 "string",
						"additionalProperties": true,
					},
				},
			},
		},
	}
	out := asMap(t, cleanFunctionParameters(in))
	outer := asMap(t, asMap(t, out["properties"])["outer"])
	if _, ok := outer["$schema"]; ok {
		t.Error("nested $schema should be removed")
	}
	inner := asMap(t, asMap(t, outer["properties"])["inner"])
	if _, ok := inner["additionalProperties"]; ok {
		t.Error("deep additionalProperties should be removed")
	}
	if inner["type"] != "STRING" {
		t.Errorf("deep type want STRING, got %v", inner["type"])
	}
}

func TestCleanFunctionParameters_ArrayItems(t *testing.T) {
	// items 为对象 → 递归清洗
	in := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":    "string",
			"$schema": "x",
		},
	}
	out := asMap(t, cleanFunctionParameters(in))
	items := asMap(t, out["items"])
	if _, ok := items["$schema"]; ok {
		t.Error("items $schema should be removed")
	}
	if items["type"] != "STRING" {
		t.Errorf("items type want STRING, got %v", items["type"])
	}

	// items 为元组数组 → 取首个
	in2 := map[string]any{
		"type": "array",
		"items": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "integer"},
		},
	}
	out2 := asMap(t, cleanFunctionParameters(in2))
	items2 := asMap(t, out2["items"])
	if items2["type"] != "STRING" {
		t.Errorf("tuple items should take first (STRING), got %v", items2["type"])
	}
}

func TestCleanFunctionParameters_OneOfToAnyOf(t *testing.T) {
	in := map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "integer"},
		},
	}
	out := asMap(t, cleanFunctionParameters(in))
	if _, ok := out["oneOf"]; ok {
		t.Error("oneOf should be removed")
	}
	anyOf, ok := out["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Fatalf("oneOf should downgrade to anyOf with 2 items, got %v", out["anyOf"])
	}
	first := asMap(t, anyOf[0])
	if first["type"] != "STRING" {
		t.Errorf("anyOf items should be recursively cleaned, got %v", first["type"])
	}
}

func TestCleanFunctionParameters_FormatFilter(t *testing.T) {
	// string + email → 删除 format
	in := map[string]any{
		"type":   "string",
		"format": "email",
	}
	out := asMap(t, cleanFunctionParameters(in))
	if _, ok := out["format"]; ok {
		t.Error("unsupported string format 'email' should be removed")
	}
	if out["type"] != "STRING" {
		t.Errorf("type should remain STRING, got %v", out["type"])
	}

	// string + date-time → 保留
	in2 := map[string]any{
		"type":   "string",
		"format": "date-time",
	}
	out2 := asMap(t, cleanFunctionParameters(in2))
	if out2["format"] != "date-time" {
		t.Errorf("supported format date-time should be kept, got %v", out2["format"])
	}
}

func TestCleanFunctionParameters_DepthGuard(t *testing.T) {
	// 构造 >64 层嵌套，确保不崩且深层被截断
	leaf := map[string]any{"type": "string", "$schema": "x"}
	cur := leaf
	for i := 0; i < 80; i++ {
		cur = map[string]any{
			"type":       "object",
			"properties": map[string]any{"child": cur},
		}
	}
	out := cleanFunctionParameters(cur)
	if out == nil {
		t.Fatal("deep schema should not produce nil")
	}
	asMap(t, out) // 不 panic 即通过
}

func TestCleanFunctionParameters_Nil(t *testing.T) {
	if got := cleanFunctionParameters(nil); got != nil {
		t.Errorf("nil input want nil, got %v", got)
	}
}

func TestCleanFunctionParameters_PreservesValidSubset(t *testing.T) {
	in := map[string]any{
		"type":        "object",
		"description": "a tool",
		"required":    []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "the name",
				"enum":        []any{"a", "b"},
			},
		},
	}
	out := asMap(t, cleanFunctionParameters(in))
	if out["description"] != "a tool" {
		t.Error("description should be preserved")
	}
	if !reflect.DeepEqual(out["required"], []any{"name"}) {
		t.Errorf("required should be preserved, got %v", out["required"])
	}
	name := asMap(t, asMap(t, out["properties"])["name"])
	if name["description"] != "the name" {
		t.Error("nested description should be preserved")
	}
	if !reflect.DeepEqual(name["enum"], []any{"a", "b"}) {
		t.Errorf("enum should be preserved, got %v", name["enum"])
	}
}
