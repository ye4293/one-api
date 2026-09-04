package flux

import "encoding/json"

// FluxVideoRequest BFL FLUX 3 Video 的请求体
// 端点：POST /v1/flux-3-video
// mode / prompt 始终必填；其余按模式选填，全部透传给上游。
type FluxVideoRequest struct {
	Mode            string          `json:"mode"`                       // t2v / i2v / v2v / draft_enhance，必填
	Prompt          string          `json:"prompt"`                     // 文本提示词，必填
	Keyframes       json.RawMessage `json:"keyframes,omitempty"`        // i2v：单图 URL/base64、首尾帧数组、或 [秒,图] 对（最多 10 帧）
	StartVideo      string          `json:"start_video,omitempty"`      // v2v：待续接视频（mp4，URL 或 base64）
	DraftCache      string          `json:"draft_cache,omitempty"`      // draft_enhance：先前草稿返回的 bundle
	Resolution      string          `json:"resolution,omitempty"`       // hd（默认）/ fhd
	Duration        interface{}     `json:"duration,omitempty"`         // 整数秒 5–20，或 "auto"
	AspectRatio     string          `json:"aspect_ratio,omitempty"`     // auto / 21:9 / 16:9 / 1:1 / 9:16 等
	GenerateAudio   *bool           `json:"generate_audio,omitempty"`   // 默认 true，false 输出静音
	SafetyTolerance *int            `json:"safety_tolerance,omitempty"` // 0（最严格）~ 4，默认 2
	Draft           *bool           `json:"draft,omitempty"`            // true 快速出 HD 预览，结果含 draft_cache
	Version         string          `json:"version,omitempty"`          // 默认 latest
}

// FluxVideoSubmitResponse 提交后（POST）的响应
type FluxVideoSubmitResponse struct {
	ID         string `json:"id"`          // 任务标识
	PollingURL string `json:"polling_url"` // 轮询地址（本项目用 id 重构 get_result，此字段仅留存）
}

// FluxVideoPollingResponse 轮询（GET /v1/get_result?id=）的响应
// status 取值：Ready / Error / Request Moderated / Content Moderated / Pending / Processing
type FluxVideoPollingResponse struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Result *FluxVideoResult `json:"result,omitempty"`
	Detail interface{}      `json:"detail,omitempty"` // 出错时上游可能返回的详情
}

// FluxVideoResult status=Ready 时的结果载荷
type FluxVideoResult struct {
	Sample string `json:"sample"` // 签名的 .mp4 下载 URL
}

// BFL 视频审核相关的失败状态字面值（与 constant.go 的 UpstreamStatusReady/Error 互补）
const (
	UpstreamStatusRequestModerated = "Request Moderated"
	UpstreamStatusContentModerated = "Content Moderated"
)
