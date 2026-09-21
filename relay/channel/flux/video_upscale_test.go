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

func TestVideoUpscaleSubmit(t *testing.T) {
	for _, tc := range []struct{ name, body, serverAddress string }{
		{"视频URL", `{"input_video":"https://example.com/source.mp4","upscale_factor":2.0,"creativity":1}`, "https://one-api.example/"},
		{"base64和零创造力", `{"input_video":"AAAAHGZ0eXBtcDQy","upscale_factor":1.5,"creativity":0}`, ""},
		{"保留上游默认参数", `{"input_video":"https://example.com/source.mp4"}`, ""},
		{"提示词和最低安全容忍度", `{"input_video":"https://example.com/source.mp4","upscale_factor":3,"prompt":"渔夫站在岸边，保留水面的细节","safety_tolerance":0}`, ""},
		{"显式回调优先于本站默认值", `{"input_video":"https://example.com/source.mp4","safety_tolerance":4,"webhook_url":"https://client.example/callback","webhook_secret":"test-secret"}`, "https://one-api.example/"},
		{"保留显式空签名密钥", `{"input_video":"https://example.com/source.mp4","webhook_url":"http://client.example/callback","webhook_secret":""}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FLUX_WEBHOOK_SECRET", "")
			db := setupVideoSettlementDB(t)
			oldAddress := config.ServerAddress
			config.ServerAddress = tc.serverAddress
			t.Cleanup(func() { config.ServerAddress = oldAddress })
			var want map[string]any
			if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
				t.Fatal(err)
			}
			if tc.serverAddress != "" && want["webhook_url"] == nil {
				want["webhook_url"] = "https://one-api.example/flux/internal/callback"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/flux-tools/video-upscale-v1" || r.Header.Get("x-key") != "test-key" {
					t.Error("视频放大上游路径或鉴权错误")
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("上游参数不一致：got=%v want=%v", got, want)
				}
				_, _ = w.Write([]byte(`{"id":"upscale-task","polling_url":"https://cluster.example/v1/get_result?id=upscale-task"}`))
			}))
			defer server.Close()
			if err := db.Create(&dbmodel.Channel{Id: 1, Key: "test-key"}).Error; err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/flux-tools/video-upscale-v1", strings.NewReader(tc.body))
			meta := &util.RelayMeta{ChannelId: 1, BaseURL: server.URL + "/", OriginModelName: VideoUpscaleModel, ActualModelName: VideoUpscaleModel}
			result, apiErr := (&VideoAdaptor{}).HandleVideoRequest(c, nil, meta)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			wantPollingURL := strings.TrimRight(tc.serverAddress, "/") + "/flux/v1/get_result?id=upscale-task"
			if result.TaskId != "upscale-task" || result.PollingUrl != wantPollingURL || result.Credentials != "https://cluster.example/v1/get_result?id=upscale-task" || result.Mode != "upscale" {
				t.Fatalf("提交结果不正确：%+v", result)
			}
			wantPrompt, _ := want["prompt"].(string)
			if result.Prompt != wantPrompt {
				t.Fatalf("任务提示词未保留：got=%q want=%q", result.Prompt, wantPrompt)
			}
		})
	}
}

func TestVideoUpscaleRejectsInvalidInputBeforeSubmission(t *testing.T) {
	for _, tc := range []struct{ name, body, baseURL string }{
		{"缺视频", `{}`, "https://api.bfl.ai"},
		{"空视频", `{"input_video":"  "}`, "https://api.bfl.ai"},
		{"零倍数", `{"input_video":"video","upscale_factor":0}`, "https://api.bfl.ai"},
		{"低于最小倍数", `{"input_video":"video","upscale_factor":1.49}`, "https://api.bfl.ai"},
		{"高于最大倍数", `{"input_video":"video","upscale_factor":3.01}`, "https://api.bfl.ai"},
		{"创造力为负数", `{"input_video":"video","creativity":-1}`, "https://api.bfl.ai"},
		{"创造力超过枚举", `{"input_video":"video","creativity":2}`, "https://api.bfl.ai"},
		{"创造力必须是整数", `{"input_video":"video","creativity":0.5}`, "https://api.bfl.ai"},
		{"安全容忍度为负数", `{"input_video":"video","safety_tolerance":-1}`, "https://api.bfl.ai"},
		{"安全容忍度超过上限", `{"input_video":"video","safety_tolerance":5}`, "https://api.bfl.ai"},
		{"安全容忍度必须是整数", `{"input_video":"video","safety_tolerance":1.5}`, "https://api.bfl.ai"},
		{"回调地址不能是相对路径", `{"input_video":"video","webhook_url":"/callback"}`, "https://api.bfl.ai"},
		{"回调地址不能是FTP", `{"input_video":"video","webhook_url":"ftp://client.example/callback"}`, "https://api.bfl.ai"},
		{"回调地址不能为空", `{"input_video":"video","webhook_url":""}`, "https://api.bfl.ai"},
		{"签名密钥必须是字符串", `{"input_video":"video","webhook_secret":123}`, "https://api.bfl.ai"},
		{"提示词必须是字符串", `{"input_video":"video","prompt":123}`, "https://api.bfl.ai"},
		{"错误类型", `{"input_video":123}`, "https://api.bfl.ai"},
		{"无效JSON", `{`, "https://api.bfl.ai"},
		{"Replicate无效视频", `{"input_video":"video"}`, "https://api.replicate.com"},
		{"Replicate不支持请求级签名密钥", `{"input_video":"https://example.com/video.mp4","webhook_secret":"test-secret"}`, "https://api.replicate.com"},
		{"Replicate回调必须HTTPS", `{"input_video":"https://example.com/video.mp4","webhook_url":"http://client.example/callback"}`, "https://api.replicate.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/flux-tools/video-upscale-v1", strings.NewReader(tc.body))
			// 不设置数据库，确保非法参数在查渠道和发送上游请求之前被拒绝。
			_, err := (&VideoAdaptor{}).HandleVideoRequest(c, nil, &util.RelayMeta{OriginModelName: VideoUpscaleModel, BaseURL: tc.baseURL})
			if err == nil || err.StatusCode != http.StatusBadRequest {
				t.Fatalf("期望 400，实际=%v", err)
			}
		})
	}
}

func TestBFLVideoTaskSubmitErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		status, wantStatus int
	}{
		{"上游拒绝", `{"detail":"invalid video"}`, 422, 422},
		{"空任务ID", `{"polling_url":"https://example.com/result"}`, 200, 500},
		{"无效响应", `not-json`, 200, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, _, err := submitBFLVideoTask(server.URL, FluxVideoUpscaleRequest{InputVideo: "video"}, "test-key")
			if err == nil || err.StatusCode != tc.wantStatus {
				t.Fatalf("错误响应不正确：%v", err)
			}
		})
	}
}
