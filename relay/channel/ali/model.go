package ali

import (
	"github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
)

type Message struct {
	Content string `json:"content"`
	Role    string `json:"role"`
}

type Input struct {
	//Prompt   string       `json:"prompt"`
	Messages []Message `json:"messages"`
}

type Parameters struct {
	TopP              float64 `json:"top_p,omitempty"`
	TopK              int     `json:"top_k,omitempty"`
	Seed              uint64  `json:"seed,omitempty"`
	EnableSearch      bool    `json:"enable_search,omitempty"`
	IncrementalOutput bool    `json:"incremental_output,omitempty"`
	MaxTokens         int     `json:"max_tokens,omitempty"`
	Temperature       float64 `json:"temperature,omitempty"`
	ResultFormat      string  `json:"result_format,omitempty"`
}

type ChatRequest struct {
	Model      string       `json:"model"`
	Input      Input        `json:"input"`
	Parameters Parameters   `json:"parameters,omitempty"`
	Tools      []model.Tool `json:"tools,omitempty"`
}

type EmbeddingRequest struct {
	Model string `json:"model"`
	Input struct {
		Texts []string `json:"texts"`
	} `json:"input"`
	Parameters *struct {
		TextType string `json:"text_type,omitempty"`
	} `json:"parameters,omitempty"`
}

type Embedding struct {
	Embedding []float64 `json:"embedding"`
	TextIndex int       `json:"text_index"`
}

type EmbeddingResponse struct {
	Output struct {
		Embeddings []Embedding `json:"embeddings"`
	} `json:"output"`
	Usage Usage `json:"usage"`
	Error
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestId string `json:"request_id"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type Output struct {
	//Text         string                      `json:"text"`
	//FinishReason string                      `json:"finish_reason"`
	Choices []openai.TextResponseChoice `json:"choices"`
}

type ChatResponse struct {
	Output Output `json:"output"`
	Usage  Usage  `json:"usage"`
	Error
}

// 阿里云通义万相视频生成请求结构体
type AliVideoRequest struct {
	Model      string              `json:"model"`
	Input      AliVideoInput       `json:"input"`
	Parameters *AliVideoParameters `json:"parameters,omitempty"`
}

type AliVideoInput struct {
	ImgURL   string  `json:"img_url"`             // 输入图像URL
	Text     string  `json:"text"`                // 提示词
	AudioURL *string `json:"audio_url,omitempty"` // 音频URL（可选）
}

type AliVideoParameters struct {
	Resolution *string `json:"resolution,omitempty"` // 分辨率：480P、720P、1080P
	Duration   *string `json:"duration,omitempty"`   // 时长：3、4、5、10（秒）
	Audio      *bool   `json:"audio,omitempty"`      // 是否自动生成音频
	Template   *string `json:"template,omitempty"`   // 视频特效模板
	Watermark  *bool   `json:"watermark,omitempty"`  // 是否添加水印
}

// 阿里云通义万相视频生成响应结构体
type AliVideoResponse struct {
	Output    *AliVideoOutput `json:"output,omitempty"`
	Usage     *Usage          `json:"usage,omitempty"`
	RequestID string          `json:"request_id"`
	Error
}

type AliVideoOutput struct {
	TaskID     string `json:"task_id"`
	TaskStatus string `json:"task_status"`
}

// 查询结果响应结构体
type AliVideoResultResponse struct {
	Output    *AliVideoResultOutput `json:"output,omitempty"`
	Usage     *Usage                `json:"usage,omitempty"`
	RequestID string                `json:"request_id"`
	Error
}

type AliVideoResultOutput struct {
	TaskID        string               `json:"task_id"`
	TaskStatus    string               `json:"task_status"`
	Results       []AliVideoResult     `json:"results,omitempty"`
	TaskMetrics   *AliVideoTaskMetrics `json:"task_metrics,omitempty"`
	SubmitTime    string               `json:"submit_time,omitempty"`
	ScheduledTime string               `json:"scheduled_time,omitempty"`
	EndTime       string               `json:"end_time,omitempty"`
}

type AliVideoResult struct {
	URL string `json:"url"`
}

type AliVideoTaskMetrics struct {
	Total     int `json:"TOTAL"`
	Succeeded int `json:"SUCCEEDED"`
	Failed    int `json:"FAILED"`
}

// 阿里云视频任务查询响应结构体
type AliVideoQueryResponse struct {
	Output    *AliVideoQueryOutput `json:"output,omitempty"`
	Usage     *AliVideoUsage       `json:"usage,omitempty"`
	RequestID string               `json:"request_id"`
	Error
}

type AliVideoQueryOutput struct {
	TaskID        string `json:"task_id"`
	TaskStatus    string `json:"task_status"` // PROCESSING, SUCCEEDED, FAILED, UNKNOWN
	SubmitTime    string `json:"submit_time,omitempty"`
	ScheduledTime string `json:"scheduled_time,omitempty"`
	EndTime       string `json:"end_time,omitempty"`
	OrigPrompt    string `json:"orig_prompt,omitempty"`
	VideoURL      string `json:"video_url,omitempty"`     // 成功时的视频URL
	ActualPrompt  string `json:"actual_prompt,omitempty"` // 实际使用的提示词
	Code          string `json:"code,omitempty"`          // 失败时的错误码
	Message       string `json:"message,omitempty"`       // 失败时的错误信息
}

type AliVideoUsage struct {
	Duration   int `json:"duration"`    // 视频时长（秒）
	VideoCount int `json:"video_count"` // 视频数量
	SR         int `json:"SR"`          // 分辨率数值 480/720/1080
}
