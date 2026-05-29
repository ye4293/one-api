package flux

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ReplicateOutput 兼容 Replicate 返回的 string / []string / null 三种 output 形态
// （Klein 系列等会返回数组：output: ["url"]；旧模型返回字符串：output: "url"）
type ReplicateOutput string

func (o *ReplicateOutput) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*o = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*o = ReplicateOutput(s)
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		if len(arr) > 0 {
			*o = ReplicateOutput(arr[0])
		} else {
			*o = ""
		}
		return nil
	}
	return fmt.Errorf("unsupported replicate output: %s", string(data))
}

// FluxRequest 表示 Flux API 的请求结构（透传模式，保留原始字段）
type FluxRequest struct {
	Prompt        string         `json:"prompt"`
	Model         string         `json:"model,omitempty"`
	Width         int            `json:"width,omitempty"`
	Height        int            `json:"height,omitempty"`
	Steps         int            `json:"steps,omitempty"`
	PromptUpscale bool           `json:"prompt_upscale,omitempty"`
	Seed          int64          `json:"seed,omitempty"`
	Guidance      float64        `json:"guidance,omitempty"`
	SafetyCheck   bool           `json:"safety_check,omitempty"`
	OutputFormat  string         `json:"output_format,omitempty"`
	AspectRatio   string         `json:"aspect_ratio,omitempty"`
	// 其他可能的字段可以用 map 接收
	Extra         map[string]any `json:"-"`
}

// FluxResponse 表示 Flux API 的异步响应结构
type FluxResponse struct {
	ID         string  `json:"id"`          // 任务ID
	PollingURL string  `json:"polling_url"` // 轮询URL
	Cost       float64 `json:"cost"`        // 费用（美分）
	InputMP    float64 `json:"input_mp"`    // 输入兆像素
	OutputMP   float64 `json:"output_mp"`   // 输出兆像素
	Error      string  `json:"error,omitempty"`
}

// FluxPollingResponse 表示轮询查询的响应结构
type FluxPollingResponse struct {
	ID     string  `json:"id"`
	Status string  `json:"status"` // pending, processing, succeed, failed
	Result *Result `json:"result,omitempty"`
	Cost   float64 `json:"cost,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Result 表示生成结果
type Result struct {
	TaskId         string   `json:"task_id,omitempty"`         // 任务ID
	Sample         string   `json:"sample,omitempty"`          // 图片URL
	Prompt         string   `json:"prompt,omitempty"`          // 使用的提示词
	Seed           int64    `json:"seed,omitempty"`            // 使用的种子
	Width          int      `json:"width,omitempty"`           // 图片宽度
	Height         int      `json:"height,omitempty"`          // 图片高度
	StartTime      float64  `json:"start_time,omitempty"`      // 开始时间（Unix时间戳）
	GenerationTime float64  `json:"generation_time,omitempty"` // 生成耗时（秒）
	Config         string   `json:"config,omitempty"`          // 配置名称
	ScorerScores   []string `json:"scorer_scores,omitempty"`   // 评分
	PreFiltered    bool     `json:"pre_filtered,omitempty"`    // 是否预过滤
}

// ErrorResponse 表示 Flux API 的错误响应
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// FluxCallbackNotification 表示 Flux API 的回调通知
// BFL 不同事件类型的字段名不一致：Ready/Error 事件用 "id"，processing 事件用 "task_id"。
// 两个 tag 都接，HandleCallback 取第一个非空值。
type FluxCallbackNotification struct {
	TaskId     string  `json:"id"`                        // Ready / Error 事件
	TaskIdAlt  string  `json:"task_id,omitempty"`         // processing 事件
	Status     string  `json:"status"`                    // 上游状态：Ready / Error / processing（参考 isUpstreamReady / isUpstreamFailed）
	Progress   int     `json:"progress,omitempty"`        // 进度（0-100）
	Result     *Result `json:"result,omitempty"`          // 生成结果（Ready 时有值）
	PollingURL string  `json:"polling_url,omitempty"`     // 轮询URL
	Cost       float64 `json:"cost,omitempty"`            // 费用（美分）
	InputMP    float64 `json:"input_mp,omitempty"`        // 输入兆像素
	OutputMP   float64 `json:"output_mp,omitempty"`       // 输出兆像素
	Error      string  `json:"error,omitempty"`           // 错误信息
}

// ReplicateResponse Replicate 预测响应（创建任务和查询结果格式相同）
type ReplicateResponse struct {
	ID          string          `json:"id"`
	Model       string          `json:"model"`
	Status      string          `json:"status"`           // starting / processing / succeeded / failed / canceled
	Output      ReplicateOutput `json:"output"`           // 兼容 string / []string / null
	Error       interface{}     `json:"error"`
	Logs        string          `json:"logs"`
	Metrics     ReplicateMetrics `json:"metrics"`
	URLs        ReplicateURLs   `json:"urls"`
	CreatedAt   string          `json:"created_at"`
	StartedAt   string          `json:"started_at"`
	CompletedAt string          `json:"completed_at"`
}

// ReplicateMetrics Replicate 预测性能指标
type ReplicateMetrics struct {
	PredictTime                float64 `json:"predict_time"`
	TotalTime                  float64 `json:"total_time"`
	ImageOutputCount           int     `json:"image_output_count"`
	ImageOutputMegapixelCount  float64 `json:"image_output_megapixel_count"`
	ImageInputMegapixelCount   float64 `json:"image_input_megapixel_count"`
}

// ReplicateURLs Replicate 预测操作 URL
type ReplicateURLs struct {
	Get    string `json:"get"`
	Cancel string `json:"cancel"`
}
