package flux

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func TestBFLVideoSubmitPreservesModeFields(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       map[string]any
	}{
		{
			name: "草稿增强只发送允许字段并默认全高清",
			body: `{"mode":"draft-enhance","draft_cache":"https://example.com/draft.bin","safety_tolerance":0,"user":"test-user"}`,
			want: map[string]any{"mode": "draft_enhance", "draft_cache": "https://example.com/draft.bin", "resolution": "fhd", "safety_tolerance": float64(0), "user": "test-user"},
		},
		{
			name: "定时关键帧保留小数时间且不改变自动时长",
			body: `{"mode":"i2v","prompt":"测试","duration":"auto","keyframes":[[0,"https://example.com/a.png"],[3.5,"https://example.com/b.png"]]}`,
			want: map[string]any{"mode": "i2v", "prompt": "测试", "duration": "auto", "resolution": "hd", "keyframes": []any{[]any{float64(0), "https://example.com/a.png"}, []any{3.5, "https://example.com/b.png"}}},
		},
		{
			name: "二十秒数字字符串转为BFL整数",
			body: `{"mode":"text-to-video","prompt":"测试","duration":"20","resolution":"1080p"}`,
			want: map[string]any{"mode": "t2v", "prompt": "测试", "duration": float64(20), "resolution": "fhd"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/flux-3-video" || r.Header.Get("x-key") != "test-key" {
					t.Error("BFL 路径或鉴权不正确")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(body, tc.want) {
					t.Errorf("实际请求=%#v，期望=%#v", body, tc.want)
				}
				_, _ = w.Write([]byte(`{"id":"test-task","polling_url":"https://example.com/result"}`))
			}))
			defer server.Close()
			var req FluxVideoRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatal(err)
			}
			resp, apiErr := (&VideoAdaptor{}).submitBFLVideo(req, &util.RelayMeta{BaseURL: server.URL}, &dbmodel.Channel{Key: "test-key"})
			if apiErr != nil || resp.ID != "test-task" || calls != 1 {
				t.Fatalf("提交结果不正确: id=%s calls=%d err=%v", resp.ID, calls, apiErr)
			}
		})
	}
}

func TestVideoInvalidRequestsNeverReachUpstream(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"id":"unexpected"}`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, body, message string
		replicate           bool
	}{
		{"定时关键帧", `{"mode":"i2v","prompt":"测试","keyframes":[[0,"a"],[3.5,"b"]]}`, "定时关键帧", true},
		{"单个定时关键帧", `{"mode":"i2v","prompt":"测试","keyframes":[0,"a"]}`, "定时关键帧", true},
		{"Replicate草稿增强", `{"mode":"draft_enhance","draft_cache":"cache"}`, "不支持 draft_enhance", true},
		{"草稿缺缓存", `{"mode":"draft_enhance"}`, "draft_cache", false},
		{"草稿不能指定时长", `{"mode":"draft_enhance","draft_cache":"cache","duration":10}`, "只接受", false},
		{"草稿不能重写提示词", `{"mode":"draft_enhance","draft_cache":"cache","prompt":"新提示词"}`, "只接受", false},
		{"超长时长不能截断", `{"mode":"t2v","prompt":"测试","duration":30}`, "duration", true},
		{"过短时长不能延长", `{"mode":"t2v","prompt":"测试","duration":4}`, "duration", true},
		{"小数时长不能截断", `{"mode":"t2v","prompt":"测试","duration":5.9}`, "duration", true},
		{"无效字符串不能变成auto", `{"mode":"t2v","prompt":"测试","duration":"abc"}`, "duration", true},
		{"布尔时长", `{"mode":"t2v","prompt":"测试","duration":true}`, "duration", false},
		{"BFL视频续接最多十五秒", `{"mode":"v2v","prompt":"测试","start_video":"video","duration":16}`, "5–15", false},
		{"图片不能为空", `{"mode":"i2v","prompt":"测试","keyframes":[]}`, "1–10", true},
		{"不能同时使用图片和视频", `{"mode":"i2v","prompt":"测试","keyframes":"image","start_video":"video"}`, "不能同时", true},
		{"图生不能缺失图片", `{"mode":"i2v","prompt":"测试"}`, "keyframes", true},
		{"故事板必须指定时长", `{"mode":"i2v","prompt":"测试","keyframes":["a","b","c"]}`, "整数 duration", true},
		{"草稿只能高清", `{"mode":"t2v","prompt":"测试","draft":true,"resolution":"1080p"}`, "草稿仅支持", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req FluxVideoRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatal(err)
			}
			meta := &util.RelayMeta{BaseURL: server.URL, ActualModelName: "flux-3-video"}
			var message string
			if tc.replicate {
				_, _, err := (&VideoAdaptor{}).submitReplicateVideo(req, meta, &dbmodel.Channel{})
				if err == nil || err.StatusCode != http.StatusBadRequest {
					t.Fatalf("期望提交前返回 400，实际=%v", err)
				}
				message = err.Error.Message
			} else {
				_, err := (&VideoAdaptor{}).submitBFLVideo(req, meta, &dbmodel.Channel{})
				if err == nil || err.StatusCode != http.StatusBadRequest {
					t.Fatalf("期望提交前返回 400，实际=%v", err)
				}
				message = err.Error.Message
			}
			if !strings.Contains(message, tc.message) {
				t.Fatalf("错误应包含 %s，实际=%s", tc.message, message)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("无效请求不应提交上游，实际调用 %d 次", calls)
	}
}

func TestReplicateVideoNormalizesDurationWithoutChangingBillingValue(t *testing.T) {
	for _, duration := range []any{float64(5), "05", float64(20), "20", " AUTO ", nil} {
		req := FluxVideoRequest{Mode: "i2v", Prompt: "测试", Keyframes: json.RawMessage(`["a","b"]`), Duration: duration, Resolution: "1080p"}
		if err := normalizeVideoRequest(&req, true); err != nil {
			t.Fatal(err)
		}
		input, err := buildReplicateVideoInput(req)
		if err != nil {
			t.Fatal(err)
		}
		if input["duration"] != durationToString(req.Duration) || req.Resolution != "fhd" || input["resolution"] != "1080p" {
			t.Fatalf("上游、落库与计费参数不一致: req=%+v input=%+v", req, input)
		}
		if !reflect.DeepEqual(input["images"], []string{"a", "b"}) {
			t.Fatalf("图片没有保留: %+v", input)
		}
	}
}
