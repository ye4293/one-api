package controller

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExtractFluxValidationDetails(t *testing.T) {
	body := []byte(`{
		"detail": [
			{
				"type": "missing",
				"loc": ["body", "image"],
				"msg": "Field required",
				"input": {"prompt": "secret"}
			}
		]
	}`)

	details := extractFluxValidationDetails(body)
	if len(details) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(details))
	}

	if details[0].Type != "missing" {
		t.Fatalf("expected type missing, got %q", details[0].Type)
	}
	if details[0].Msg != "Field required" {
		t.Fatalf("expected msg Field required, got %q", details[0].Msg)
	}
	if len(details[0].Loc) != 2 || details[0].Loc[0] != "body" || details[0].Loc[1] != "image" {
		t.Fatalf("unexpected loc: %#v", details[0].Loc)
	}

	marshaled, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal details failed: %v", err)
	}
	if string(marshaled) != `[{"type":"missing","loc":["body","image"],"msg":"Field required"}]` {
		t.Fatalf("unexpected sanitized json: %s", string(marshaled))
	}
}

func TestBuildFluxUnifiedErrorResponse(t *testing.T) {
	resp := buildFluxUnifiedErrorResponse(422, []fluxValidationDetail{
		{
			Type: "missing",
			Loc:  []string{"body", "image"},
			Msg:  "Field required",
		},
	})

	var errorMap gin.H
	switch v := resp["error"].(type) {
	case gin.H:
		errorMap = v
	case map[string]interface{}:
		errorMap = gin.H(v)
	default:
		t.Fatalf("expected error map, got %#v", resp["error"])
	}

	if errorMap["message"] != "Field required missing image" {
		t.Fatalf("unexpected message: %#v", errorMap["message"])
	}
	if errorMap["type"] != "api_error" {
		t.Fatalf("unexpected type: %#v", errorMap["type"])
	}
	if errorMap["param"] != "" {
		t.Fatalf("unexpected param: %#v", errorMap["param"])
	}
	if code, exists := errorMap["code"]; !exists || code != nil {
		t.Fatalf("expected nil code, got %#v", errorMap["code"])
	}
}

func TestBuildFluxUnifiedErrorResponseFallback(t *testing.T) {
	resp := buildFluxUnifiedErrorResponse(422, nil)

	errorMap := resp["error"].(gin.H)
	if errorMap["message"] != "API 返回错误状态: 422" {
		t.Fatalf("unexpected fallback message: %#v", errorMap["message"])
	}
}
