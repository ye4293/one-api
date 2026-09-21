package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFluxVideoUpscaleModelComesFromPath(t *testing.T) {
	for _, body := range []string{
		`{"input_video":"https://example.com/source.mp4","upscale_factor":2,"creativity":1}`,
		`{"model":"unrelated-model","input_video":"video"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/flux-tools/video-upscale-v1", strings.NewReader(body))
		req, selectChannel := getModelRequest(c)
		if req.Model != "flux-upscale" || !selectChannel {
			t.Fatalf("视频放大必须按路径选择渠道：%+v selectChannel=%v", req, selectChannel)
		}
	}
}
