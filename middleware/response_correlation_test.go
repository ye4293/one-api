package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseCorrelationInputs(t *testing.T) {
	for _, tc := range []struct {
		path, body, header, want string
		conflict                 bool
	}{
		{"/v1/responses", `{"previous_response_id":"resp_A"}`, "", "resp_A", false},
		{"/v1/responses/compact", `{"input":[{"type":"reasoning","id":"rs_A","encrypted_content":"opaque"}]}`, "", "rs_A", false},
		{"/v1/chat/completions", `{"messages":[]}`, "chatcmpl-A", "chatcmpl-A", false},
		{"/v1/responses", `{"previous_response_id":"resp_A"}`, "resp_B", "resp_A", true},
	} {
		t.Run(tc.path+tc.header, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("X-Response-ID", tc.header)
			id, err := captureResponseCorrelation(c)
			if (err != nil) != tc.conflict {
				t.Fatalf("conflict=%v err=%v", tc.conflict, err)
			}
			if !tc.conflict && id != tc.want {
				t.Fatalf("lookup=%q want=%q", id, tc.want)
			}
			if parent := model.PreviousResponseIDFromContext(c.Request.Context()); parent != tc.want {
				t.Fatalf("parent=%q want=%q", parent, tc.want)
			}
		})
	}
}
