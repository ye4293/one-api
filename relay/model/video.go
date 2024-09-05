package model

type VideoRequest struct {
	Model    string `json:"model,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type VideoRequestMinimax struct {
	Model  string `json:"model,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

type VideoRequestZhipu struct {
	Model    string `json:"model,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	UserId   string `json:"user_id,omitempty"`
}

type VideoResponse struct {
	// 通用字段
	StatusCode int `json:"status_code,omitempty"`
	// minimax
	TaskID   string   `json:"task_id,omitempty"`
	BaseResp BaseResp `json:"base_resp,omitempty"`

	// 智谱
	RequestID  string `json:"request_id,omitempty"`
	ID         string `json:"id,omitempty"`
	Model      string `json:"model,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`

	// 智谱特别的错误处理字段
	ZhipuError *ZhipuError `json:"error,omitempty"`
}

// minimax的字段
type BaseResp struct {
	StatusCode int    `json:"status_code,omitempty"`
	StatusMsg  string `json:"status_msg,omitempty"`
}

// 智谱的错误结构
type ZhipuError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type FinalVideoResponse struct {
	// 智谱
	Model        string        `json:"model,omitempty"`        // 模型名称
	VideoResults []VideoResult `json:"video_result,omitempty"` // 视频生成结果
	TaskStatus   string        `json:"task_status,omitempty"`  // 处理状态：PROCESSING（处理中），SUCCESS（成功），FAIL（失败）
	RequestID    string        `json:"request_id,omitempty"`   // 用户在客户端请求时提交的任务编号或者平台生成的任务编号
	ID           string        `json:"id,omitempty"`           // 智谱 AI 开放平台生成的任务订单号，调用请求结果接口时请使用此订单号

	// Minimax 特有字段
	TaskID   string   `json:"task_id,omitempty"`   // 此次被查询的任务ID
	Status   string   `json:"status,omitempty"`    // 任务状态：Queueing-队列中, Processing-生成中, Success-成功, Failed-失败
	FileID   string   `json:"file_id,omitempty"`   // 任务成功后，该字段返回生成视频对应的文件ID
	BaseResp BaseResp `json:"base_resp,omitempty"` // 状态码及状态详情
}

type VideoResult struct {
	URL           string `json:"url,omitempty"`             // 视频url
	CoverImageURL string `json:"cover_image_url,omitempty"` // 视频封面url
}
