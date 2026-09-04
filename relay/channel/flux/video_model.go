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

// ReplicateVideoModelMap one-api 模型名 → Replicate 模型 ID（视频）
// 与图片侧 ReplicateModelMap 同构：baseURL 含 replicate.com 时走此映射拼 predictions URL。
var ReplicateVideoModelMap = map[string]string{
	"flux-3-video": "black-forest-labs/flux-3",
}

// fluxResolutionToReplicate 将 BFL 的 hd/fhd 档位映射为 Replicate 的分辨率字面值。
// 计费仍按 hd/fhd 命中 video-pricing 规则，此处仅转换下发给上游的取值。
func fluxResolutionToReplicate(res string) string {
	switch res {
	case "", "hd":
		return "720p"
	case "fhd":
		return "1080p"
	default:
		return res // 已是 720p/1080p 等则原样透传
	}
}

// keyframesToReplicateImages 将 BFL keyframes 转为 Replicate 的 images 数组。
// 支持：单图 URL/base64（string）、首尾帧数组（[]string）。
// [秒,图] 对等复杂形态本期不支持，返回 nil（不下发 images）。
func keyframesToReplicateImages(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil
		}
		return []string{single}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return arr
	}
	return nil
}

// buildReplicateVideoInput 把 BFL 请求字段转成 Replicate predictions 的 input。
// mode 不下发：Replicate 由 images/start_video 是否存在自行推断 t2v/i2v/v2v。
func buildReplicateVideoInput(req FluxVideoRequest) map[string]any {
	input := map[string]any{
		"prompt":     req.Prompt,
		"resolution": fluxResolutionToReplicate(req.Resolution),
	}
	if imgs := keyframesToReplicateImages(req.Keyframes); len(imgs) > 0 {
		input["images"] = imgs
	}
	if req.StartVideo != "" {
		input["start_video"] = req.StartVideo
	}
	if req.Duration != nil {
		input["duration"] = req.Duration
	}
	if req.AspectRatio != "" {
		input["aspect_ratio"] = req.AspectRatio
	}
	if req.GenerateAudio != nil {
		input["generate_audio"] = *req.GenerateAudio
	}
	if req.SafetyTolerance != nil {
		input["safety_tolerance"] = *req.SafetyTolerance
	}
	if req.Draft != nil {
		input["draft"] = *req.Draft
	}
	return input
}
