package flux

// Flux 支持的模型列表
var ModelList = []string{
	"flux-2-pro",
	"flux-2-flex",
	"flux-kontext-pro",
	"flux-kontext-max",
	"flux-pro-1.1",
	"flux-dev",
	"flux-pro-1.1-ultra",
	"flux-pro-1.0-fill",
	"flux-pro-1.0-expand",
	"flux-2-max",
}

// 模型端点映射 - 模型名到API端点的映射
var ModelEndpoints = map[string]string{
	"flux-2-pro":          "/v1/flux-2-pro",
	"flux-2-flex":         "/v1/flux-2-flex",
	"flux-kontext-pro":    "/v1/flux-kontext-pro",
	"flux-kontext-max":    "/v1/flux-kontext-max",
	"flux-pro-1.1":        "/v1/flux-pro-1.1",
	"flux-dev":            "/v1/flux-dev",
	"flux-pro-1.1-ultra":  "/v1/flux-pro-1.1-ultra",
	"flux-pro-1.0-fill":   "/v1/flux-pro-1.0-fill",
	"flux-pro-1.0-expand": "/v1/flux-pro-1.0-expand",
	"flux-2-max":          "/v1/flux-2-max",
}

const (
	// 任务状态
	TaskStatusPending    = "pending"
	TaskStatusSubmitted  = "submitted"
	TaskStatusProcessing = "processing"
	TaskStatusSucceed    = "success"
	TaskStatusFailed     = "failed"
)
