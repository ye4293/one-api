package doubao

// DoubaoVideoRequest 豆包视频生成请求结构
type DoubaoVideoRequest struct {
	Model       string          `json:"model"`                  // 必选，模型ID
	Content     []DoubaoContent `json:"content"`                // 必选，输入内容数组
	CallbackURL string          `json:"callback_url,omitempty"` // 可选，回调通知地址
}

// DoubaoContent 输入内容结构，支持文本和图片
type DoubaoContent struct {
	Type     string          `json:"type"`                // "text" 或 "image_url"
	Text     string          `json:"text,omitempty"`      // 文本内容（包含参数）
	ImageURL *DoubaoImageURL `json:"image_url,omitempty"` // 图片URL
	Role     string          `json:"role,omitempty"`      // 文本内容（包含参数）
}

// DoubaoImageURL 图片URL结构
type DoubaoImageURL struct {
	URL string `json:"url"` // 图片URL或base64数据
}

//DoubaoVideoResponse 豆包视频生成响应结构
type DoubaoVideoResponse struct {
	ID         string            `json:"id"`                    // 任务ID
	StatusCode int               `json:"status_code,omitempty"` // 错误码
	Error      *DoubaoVideoError `json:"error,omitempty"`       // 错误信息，成功时为null
}

// 用于解析文本命令参数的结构
type DoubaoVideoParams struct {
	Resolution     string `json:"resolution,omitempty"`     // 分辨率：480p, 720p, 1080p
	Ratio          string `json:"ratio,omitempty"`          // 宽高比
	Duration       int    `json:"duration,omitempty"`       // 时长（秒）
	FramePerSecond int    `json:"framepersecond,omitempty"` // 帧率
	Watermark      bool   `json:"watermark,omitempty"`      // 是否包含水印
	Seed           int    `json:"seed,omitempty"`           // 种子值
	CameraFixed    bool   `json:"camerafixed,omitempty"`    // 是否固定摄像头
}

// DoubaoVideoResult 豆包视频查询结果响应 - 完全匹配实际API响应格式
type DoubaoVideoResult struct {
	ID        string              `json:"id"`                   // 任务ID
	Model     string              `json:"model,omitempty"`      // 模型名称
	Status    string              `json:"status"`               // 任务状态：queued, running, cancelled, succeeded, failed
	Error     *DoubaoVideoError   `json:"error,omitempty"`      // 错误信息，成功时为null
	CreatedAt int64               `json:"created_at,omitempty"` // 任务创建时间的Unix时间戳（秒）
	UpdatedAt int64               `json:"updated_at,omitempty"` // 任务更新时间的Unix时间戳（秒）
	Content   *DoubaoVideoContent `json:"content,omitempty"`    // 任务完成时的内容对象
	Usage     *DoubaoVideoUsage   `json:"usage,omitempty"`      // Token使用统计
}

// DoubaoVideoError 豆包视频生成错误信息结构
type DoubaoVideoError struct {
	Code    string `json:"code"`    // 错误码
	Message string `json:"message"` // 错误提示信息
}

// DoubaoVideoContent 豆包视频生成完成时的内容结构
type DoubaoVideoContent struct {
	VideoURL string `json:"video_url"` // 生成视频的URL，24小时后会被清理
}

// DoubaoVideoUsage 豆包视频生成Token使用统计
type DoubaoVideoUsage struct {
	CompletionTokens int `json:"completion_tokens"` // 模型生成的token数量
	TotalTokens      int `json:"total_tokens"`      // 总token数量，视频生成模型不统计输入token，故等于completion_tokens
}
