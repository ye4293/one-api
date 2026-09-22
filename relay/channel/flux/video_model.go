package flux

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// FluxVideoRequest BFL FLUX 3 Video 的请求体
// 端点：POST /v1/flux-3-video
// 普通生成需要 mode / prompt；草稿增强只发送该模式允许的字段。
type FluxVideoRequest struct {
	Mode            string          `json:"mode"`                       // t2v / i2v / v2v / draft_enhance，必填
	Prompt          string          `json:"prompt"`                     // 普通生成的文本提示词，草稿增强不下发
	Keyframes       json.RawMessage `json:"keyframes,omitempty"`        // i2v：单图 URL/base64、首尾帧数组、或 [秒,图] 对（最多 10 帧）
	StartVideo      string          `json:"start_video,omitempty"`      // v2v：待续接视频（mp4，URL 或 base64）
	DraftCache      string          `json:"draft_cache,omitempty"`      // draft_enhance：先前草稿返回的 bundle
	Resolution      string          `json:"resolution,omitempty"`       // hd / fhd / qhd / uhd，草稿增强默认 fhd
	Duration        interface{}     `json:"duration,omitempty"`         // 整数秒 5–20 或 "auto"；BFL v2v 最大 15 秒
	AspectRatio     string          `json:"aspect_ratio,omitempty"`     // auto / 21:9 / 16:9 / 1:1 / 9:16 等
	GenerateAudio   *bool           `json:"generate_audio,omitempty"`   // 默认 true，false 输出静音
	SafetyTolerance *int            `json:"safety_tolerance,omitempty"` // 0（最严格）~ 4，默认 2
	Draft           *bool           `json:"draft,omitempty"`            // true 快速出 HD 预览，结果含 draft_cache
	Version         string          `json:"version,omitempty"`          // 默认 latest
	User            string          `json:"user,omitempty"`             // 调用方提供的终端用户标识
}

// FluxVideoSubmitResponse 提交后（POST）的响应。
// 同一结构体既解析 BFL 上游创建响应，又作为 flux 视频端点透传给客户端的响应体。
// cost/input_mp/output_mp 用指针：BFL 提交时算不出（尚未下载/分析素材），三者恒为 null，
// 用 *float64 忠实透传 null，避免退化为 0 被误读为“免费”。真实 cost 仅在完成态
// （get_result Ready 顶层 cost）出现，走完成结算，不在提交响应里。
type FluxVideoSubmitResponse struct {
	ID         string   `json:"id"`          // 任务标识
	PollingURL string   `json:"polling_url"` // 上游轮询地址：BFL 多集群路由，轮询必须原样使用（落库 credentials）
	Cost       *float64 `json:"cost"`        // 上游权威费用：credits 计（1 credit = $0.01 = 1 分）；提交时恒 null
	InputMP    *float64 `json:"input_mp"`    // 输入百万像素；提交时恒 null
	OutputMP   *float64 `json:"output_mp"`   // 输出百万像素；提交时恒 null
}

// FluxVideoPollingResponse 轮询（GET /v1/get_result?id=）的响应
// status 取值：Ready / Error / Request Moderated / Content Moderated / Pending / Processing
type FluxVideoPollingResponse struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Result *FluxVideoResult `json:"result,omitempty"`
	// Detail / Details:出错时上游返回的详情。上游实际字段名是 details(复数,如
	// {"Moderation Reasons":[...]}),历史误用 detail(单数)导致审核原因丢失、
	// message 显示 <nil>。两者都留,取值时优先非空的 Details。
	Detail  interface{} `json:"detail,omitempty"`
	Details interface{} `json:"details,omitempty"`
	// Cost 上游权威费用(美分),BFL get_result 顶层返回,如 85.0=$0.85。
	// 用于完成时按上游 cost 多退少补;缺失(为 0)则保持提交预扣。
	Cost float64 `json:"cost,omitempty"`
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
	"flux-3-video":    "black-forest-labs/flux-3",
	VideoUpscaleModel: "black-forest-labs/flux-video-upscale",
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

// normalizeBillingResolution 统一分辨率别名，保留高分辨率和未知值供校验。
func normalizeBillingResolution(res string) string {
	switch res = strings.ToLower(strings.TrimSpace(res)); res {
	case "", "hd", "720p":
		return "hd"
	case "fhd", "1080p":
		return "fhd"
	default:
		return res
	}
}

// normalizeVideoDuration 只接受整秒或 auto，不截断小数、不钳制越界值。
// 返回的整数用于 BFL、落库和计费；Replicate 下发时再转成数字字符串。
func normalizeVideoDuration(d interface{}, maximum int) (interface{}, error) {
	if d == nil {
		return "auto", nil
	}
	if s, ok := d.(string); ok {
		s = strings.TrimSpace(s)
		if strings.EqualFold(s, "auto") {
			return "auto", nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("duration 必须为 5–%d 的整数或 auto", maximum)
		}
		d = n
	}
	var seconds float64
	switch v := d.(type) {
	case int:
		seconds = float64(v)
	case float64:
		seconds = v
	case json.Number:
		var err error
		seconds, err = v.Float64()
		if err != nil {
			return nil, fmt.Errorf("duration 必须为整数或 auto")
		}
	default:
		return nil, fmt.Errorf("duration 必须为整数或 auto")
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || math.Trunc(seconds) != seconds || seconds < 5 || seconds > float64(maximum) {
		return nil, fmt.Errorf("duration 必须为 5–%d 的整数或 auto", maximum)
	}
	return int(seconds), nil
}

// keyframesToReplicateImages 将 BFL keyframes 转为 Replicate 的 images 数组。
// 定时关键帧无法无损转换，必须报错，避免丢弃素材后变成文生视频。
func keyframesToReplicateImages(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return nil, fmt.Errorf("keyframes 图片不能为空")
		}
		return []string{single}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		if len(arr) < 1 || len(arr) > 10 {
			return nil, fmt.Errorf("keyframes 必须包含 1–10 张图片")
		}
		for _, img := range arr {
			if strings.TrimSpace(img) == "" {
				return nil, fmt.Errorf("keyframes 图片不能为空")
			}
		}
		return arr, nil
	}
	return nil, fmt.Errorf("Replicate 不支持定时关键帧；keyframes 仅支持单张图片或图片字符串数组，定时关键帧请使用 BFL 渠道")
}

// normalizeVideoRequest 在提交前统一校验，确保上游参数、落库和计费使用同一语义。
func normalizeVideoRequest(req *FluxVideoRequest, replicate bool) error {
	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "t2v", "text-to-video":
		req.Mode = "t2v"
	case "i2v", "image-continuation":
		req.Mode = "i2v"
	case "v2v", "video-continuation":
		req.Mode = "v2v"
	case "draft_enhance", "draft-enhance":
		req.Mode = "draft_enhance"
	default:
		return fmt.Errorf("mode 必须为 t2v、i2v、v2v 或 draft_enhance")
	}
	if req.Mode == "draft_enhance" && replicate {
		return fmt.Errorf("Replicate 不支持 draft_enhance 草稿增强，请使用 BFL 渠道")
	}
	if strings.TrimSpace(req.Resolution) == "" && req.Mode == "draft_enhance" {
		req.Resolution = "fhd"
	}
	req.Resolution = normalizeBillingResolution(req.Resolution)
	switch req.Resolution {
	case "hd", "fhd":
	case "qhd", "uhd":
		if replicate {
			return fmt.Errorf("Replicate 仅支持 hd/720p 和 fhd/1080p")
		}
	default:
		return fmt.Errorf("resolution 必须为 hd、fhd、qhd、uhd，或 720p、1080p")
	}
	if req.Mode == "draft_enhance" {
		if strings.TrimSpace(req.DraftCache) == "" {
			return fmt.Errorf("draft_enhance 必须提供 draft_cache")
		}
		if req.Prompt != "" || len(req.Keyframes) > 0 || req.StartVideo != "" || req.Duration != nil || req.AspectRatio != "" || req.GenerateAudio != nil || req.Draft != nil || req.Version != "" {
			return fmt.Errorf("draft_enhance 只接受 mode、draft_cache、resolution、safety_tolerance 和 user，其他生成参数沿用草稿")
		}
		return nil
	}
	if req.DraftCache != "" {
		return fmt.Errorf("draft_cache 仅用于 draft_enhance")
	}
	if req.Draft != nil && *req.Draft && req.Resolution != "hd" {
		return fmt.Errorf("draft 草稿仅支持 hd/720p 分辨率")
	}
	maximum := 20
	if req.Mode == "v2v" && !replicate {
		maximum = 15
	}
	var err error
	req.Duration, err = normalizeVideoDuration(req.Duration, maximum)
	if err != nil {
		return err
	}
	hasKeyframes := len(req.Keyframes) > 0 && string(req.Keyframes) != "null"
	switch req.Mode {
	case "t2v":
		if hasKeyframes || req.StartVideo != "" {
			return fmt.Errorf("t2v 不接受 keyframes 或 start_video")
		}
	case "i2v":
		if !hasKeyframes || req.StartVideo != "" {
			return fmt.Errorf("i2v 必须提供 keyframes，且不能同时提供 start_video")
		}
	case "v2v":
		if strings.TrimSpace(req.StartVideo) == "" || hasKeyframes {
			return fmt.Errorf("v2v 必须提供 start_video，且不能同时提供 keyframes")
		}
	}
	if replicate && hasKeyframes {
		images, err := keyframesToReplicateImages(req.Keyframes)
		if err != nil {
			return err
		}
		if len(images) >= 3 && req.Duration == "auto" {
			return fmt.Errorf("Replicate 使用三张及以上图片时必须指定整数 duration")
		}
	}
	return nil
}

// buildBFLVideoInput 草稿增强使用独立字段集，不能把空 prompt 等字段发给严格校验的上游。
func buildBFLVideoInput(req FluxVideoRequest) any {
	if req.Mode != "draft_enhance" {
		return req
	}
	input := map[string]any{"mode": req.Mode, "draft_cache": req.DraftCache, "resolution": req.Resolution}
	if req.SafetyTolerance != nil {
		input["safety_tolerance"] = *req.SafetyTolerance
	}
	if req.User != "" {
		input["user"] = req.User
	}
	return input
}

// buildReplicateVideoInput 把 BFL 请求字段转成 Replicate predictions 的 input。
// mode 不下发：Replicate 由 images/start_video 是否存在自行推断 t2v/i2v/v2v。
func buildReplicateVideoInput(req FluxVideoRequest) (map[string]any, error) {
	if err := normalizeVideoRequest(&req, true); err != nil {
		return nil, err
	}
	input := map[string]any{
		"prompt":     req.Prompt,
		"resolution": fluxResolutionToReplicate(req.Resolution),
	}
	imgs, err := keyframesToReplicateImages(req.Keyframes)
	if err != nil {
		return nil, err
	}
	if len(imgs) > 0 {
		input["images"] = imgs
	}
	if req.StartVideo != "" {
		input["start_video"] = req.StartVideo
	}
	if req.Duration != nil {
		input["duration"] = durationToString(req.Duration)
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
	return input, nil
}
