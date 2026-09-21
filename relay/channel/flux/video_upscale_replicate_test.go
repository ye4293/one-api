package flux

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func TestReplicateVideoUpscaleSubmit(t *testing.T) {
	for _, tc := range []struct {
		name, body, serverAddress, video, webhook string
	}{
		{"默认本站回调", `{"input_video":"https://example.com/source.mp4","upscale_factor":3,"creativity":0,"prompt":"岸边的渔夫","safety_tolerance":0}`, "https://one-api.example/", "https://example.com/source.mp4", "https://one-api.example/flux/internal/replicate/callback"},
		{"裸base64转dataURL", `{"input_video":"AAAAHGZ0eXBtcDQy"}`, "", "data:video/mp4;base64,AAAAHGZ0eXBtcDQy", ""},
		{"已有dataURL保留", `{"input_video":"data:video/mp4;base64,AAAAHGZ0eXBtcDQy","creativity":1}`, "", "data:video/mp4;base64,AAAAHGZ0eXBtcDQy", ""},
		{"显式回调优先", `{"input_video":"https://example.com/source.mp4","webhook_url":"https://client.example/callback"}`, "https://one-api.example", "https://example.com/source.mp4", "https://client.example/callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupVideoSettlementDB(t)
			oldAddress := config.ServerAddress
			config.ServerAddress = tc.serverAddress
			t.Cleanup(func() { config.ServerAddress = oldAddress })
			t.Setenv("FLUX_WEBHOOK_SECRET", "仅BFL使用")
			var wantInput map[string]any
			if err := json.Unmarshal([]byte(tc.body), &wantInput); err != nil {
				t.Fatal(err)
			}
			delete(wantInput, "webhook_url")
			wantInput["input_video"] = tc.video
			want := map[string]any{"input": wantInput}
			if tc.webhook != "" {
				want["webhook"] = tc.webhook
				want["webhook_events_filter"] = []any{"completed"}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/replicate.com/v1/models/black-forest-labs/flux-video-upscale/predictions" || r.Header.Get("Authorization") != "Bearer upstream-key" || r.Header.Get("x-key") != "" {
					t.Error("Replicate 视频放大的路径或鉴权不正确")
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("Replicate 实际参数=%v，期望=%v", got, want)
				}
				_, _ = w.Write([]byte(`{"id":"replicate-task","status":"starting","output":null}`))
			}))
			defer server.Close()
			if err := db.Create(&dbmodel.Channel{Id: 1, Key: "upstream-key"}).Error; err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/flux-tools/video-upscale-v1", strings.NewReader(tc.body))
			meta := &util.RelayMeta{ChannelId: 1, BaseURL: server.URL + "/replicate.com/", OriginModelName: VideoUpscaleModel, ActualModelName: VideoUpscaleModel}
			result, err := (&VideoAdaptor{}).HandleVideoRequest(c, nil, meta)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || result.TaskId != "replicate-task" || result.Credentials != "" || result.PollingUrl != strings.TrimRight(tc.serverAddress, "/")+"/flux/v1/get_result?id=replicate-task" {
				t.Fatalf("Replicate 提交结果不正确：%+v，调用次数=%d", result, calls)
			}
		})
	}
}
