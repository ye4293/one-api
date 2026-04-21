package common

import (
	"bytes"
	"testing"
)

func TestExtractPreviousResponseID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"with id", `{"model":"gpt-5","previous_response_id":"resp_abc123"}`, "resp_abc123"},
		{"without id", `{"model":"gpt-5"}`, ""},
		{"empty body", ``, ""},
		{"malformed", `{not json`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractPreviousResponseID([]byte(tt.body))
			if got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestExtractEncryptedContentHashes(t *testing.T) {
	body := `{
		"model":"gpt-5",
		"input":[
			{"type":"reasoning","encrypted_content":"AAA","id":"r1"},
			{"type":"message","content":"hi"},
			{"type":"reasoning","encrypted_content":"BBB","id":"r2"}
		]
	}`
	hashes := ExtractEncryptedContentHashes([]byte(body))
	if len(hashes) != 2 {
		t.Fatalf("expected 2 hashes got %d", len(hashes))
	}
	for _, h := range hashes {
		if len(h) != 64 {
			t.Errorf("hash len = %d want 64", len(h))
		}
	}
}

func TestExtractEncryptedContentHashes_Empty(t *testing.T) {
	body := `{"model":"gpt-5","input":[{"type":"message","content":"hi"}]}`
	hashes := ExtractEncryptedContentHashes([]byte(body))
	if len(hashes) != 0 {
		t.Errorf("expected 0 hashes got %d", len(hashes))
	}
}

func TestStripEncryptedContentFromInput(t *testing.T) {
	in := `{"input":[
		{"type":"reasoning","encrypted_content":"AAA","id":"r1"},
		{"type":"message","content":"hi"}
	]}`
	out, err := StripEncryptedContentFromInput([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hashes := ExtractEncryptedContentHashes(out); len(hashes) > 0 {
		t.Errorf("strip did not remove encrypted_content: %s", string(out))
	}
	if !bytes.Contains(out, []byte(`"id":"r1"`)) || !bytes.Contains(out, []byte(`"content":"hi"`)) {
		t.Errorf("strip removed unrelated fields: %s", string(out))
	}
}

func TestExtractOutputEncryptedContentHashes(t *testing.T) {
	body := `{
		"id":"resp_1",
		"output":[
			{"type":"reasoning","encrypted_content":"OUT_A","id":"r3"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}
		]
	}`
	hashes := ExtractOutputEncryptedContentHashes([]byte(body))
	if len(hashes) != 1 {
		t.Fatalf("expected 1 hash got %d", len(hashes))
	}
	if len(hashes[0]) != 64 {
		t.Errorf("hash len = %d want 64", len(hashes[0]))
	}
}

func TestIsInvalidEncryptedContentError(t *testing.T) {
	cases := []struct {
		code    string
		message string
		want    bool
	}{
		{"invalid_encrypted_content", "blah", true},
		{"status_400", "invalid_encrypted_content in request", true},
		{"status_400", "could not be decrypted or parsed", true},
		{"status_400", "random 400 error", false},
		{"status_401", "invalid_encrypted_content", false},
	}
	for _, c := range cases {
		got := IsInvalidEncryptedContentError(c.code, c.message)
		if got != c.want {
			t.Errorf("code=%q msg=%q got=%v want=%v", c.code, c.message, got, c.want)
		}
	}
}

