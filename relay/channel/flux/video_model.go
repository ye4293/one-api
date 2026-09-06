package flux

import (
	"encoding/json"
	"strconv"
	"strings"
)

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
	PollingURL string `json:"polling_url"` // 上游轮询地址：BFL 多集群路由，轮询必须原样使用（落库 credentials）
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
	// UpstreamStatusTaskNotFound BFL 对已过期/无效任务返回的 status（伴随 HTTP 404）。
	// 归入失败以触发退款，杜绝被 default 分支误判为 processing 而永久卡死。
	UpstreamStatusTaskNotFound = "Task not found"
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

// normalizeBillingResolution 把请求分辨率归一为计费口径 hd/fhd。
// Replicate 原生只认 720p/1080p，若不归一，"1080p" 会匹配不到 fhd 规则、
// 落到兜底通配价（0.17）少收一半。空/未知一律按 hd 兜底。
func normalizeBillingResolution(res string) string {
	switch strings.ToLower(res) {
	case "", "hd", "720p":
		return "hd"
	case "fhd", "1080p":
		return "fhd"
	default:
		return "hd"
	}
}

// normalizeReplicateDuration 把 duration 归一为 Replicate 的字符串枚举（auto / "5"~"20"）。
// Replicate schema 的 duration 是字符串枚举，客户端若按 BFL 习惯传 JSON 数字 5，
// 下发数字会被上游 Pydantic 枚举校验拒绝（422）。返回 "" 表示不下发，交上游默认 auto。
func normalizeReplicateDuration(d interface{}) string {
	s := durationToString(d)
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "auto") {
		return "auto"
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return "" // 无法识别，交上游默认 auto
	}
	// 枚举范围 5~20，越界钳到边界避免 422
	if n < 5 {
		n = 5
	} else if n > 20 {
		n = 20
	}
	return strconv.Itoa(n)
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
		if dur := normalizeReplicateDuration(req.Duration); dur != "" {
			input["duration"] = dur
		}
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
