package viggle

import "mime/multipart"

type ViggleRequest struct {
	Image         *multipart.FileHeader `form:"image" json:"image,omitempty"`                     // 上传图片
	Video         *multipart.FileHeader `form:"video" json:"video,omitempty"`                     // 上传视频
	BgMode        string                `form:"bgMode" json:"bgMode,omitempty"`                   // 背景模式 2:original/1:green
	ModelInfoID   string                `form:"modelInfoID" json:"modelInfoID,omitempty"`         // 模型ID
	VideoTempID   string                `form:"video_temp_id" json:"video_temp_id,omitempty"`     // 视频模板id
	VideoAssetsID string                `form:"video_assets_id" json:"video_assets_id,omitempty"` // 视频素材id
	ImgAssetsID   string                `form:"img_assets_id" json:"img_assets_id,omitempty"`     // 图片素材id
	ImageURL      string                `form:"image_url" json:"image_url,omitempty"`             // 图片URL
	VideoURL      string                `form:"video_url" json:"video_url,omitempty"`             // 视频URL
	Type          string                `form:"type" json:"type,omitempty"`                       // 视频URL
}

type ViggleResponse struct {
	Code       int       `json:"code"`           // 响应状态码
	Message    string    `json:"message"`        // 响应信息
	Data       *TaskData `json:"data,omitempty"` // 响应数据
	StatusCode int       `json:"status_code"`    // 响应状态码
}

// 响应体中的 data 结构
type TaskData struct {
	TaskID string `json:"taskID"` // 任务ID
	MqType int    `json:"mqType"` // 消息队列类型
	PlayAd bool   `json:"playAd"` // 是否播放广告
	AdType int    `json:"adType"` // 广告类型
}

type ViggleFinalResponse struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Data    struct {
		Code    int    `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
		Ts      string `json:"ts,omitempty"`
		Reason  string `json:"reason,omitempty"`
		Data    []Task `json:"data,omitempty"`
	} `json:"data,omitempty"`
}

type Task struct {
	TaskID           string   `json:"taskID,omitempty"`
	Name             string   `json:"name,omitempty"`
	Status           int      `json:"status,omitempty"`
	Images           []string `json:"images,omitempty"`
	VideoDuration    float64  `json:"videoDuration,omitempty"`
	ImgBgURL         string   `json:"imgBgURL,omitempty"`
	BgMode           int      `json:"bgMode,omitempty"`
	DrivenType       string   `json:"drivenType,omitempty"`
	ModelInfoID      int      `json:"modelInfoID,omitempty"`
	Optimize         bool     `json:"optimize,omitempty"`
	Watermark        int      `json:"watermark,omitempty"`
	FreeCredits      int      `json:"freeCredits,omitempty"`
	PlanCredits      int      `json:"planCredits,omitempty"`
	PurchasedCredits int      `json:"purchasedCredits,omitempty"`
	MqType           int      `json:"mqType,omitempty"`
	IsBySharing      bool     `json:"isBySharing,omitempty"`
	Result           string   `json:"result,omitempty"`
	ResultCover      string   `json:"resultCover,omitempty"`
	Width            int      `json:"width,omitempty"`
	Height           int      `json:"height,omitempty"`
	CreatedAt        string   `json:"createdAt,omitempty"`
}
