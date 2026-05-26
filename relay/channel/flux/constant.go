package flux

// Flux 支持的模型列表（以 BFL 官方 API 为准，共 15 个）
var ModelList = []string{
	// FLUX 2 系列
	"flux-2-max",
	"flux-2-pro-preview", // 最新 pro，会随官方更新
	"flux-2-pro",         // 固定快照版，可复现
	"flux-2-flex",
	"flux-2-klein-4b",
	"flux-2-klein-9b-preview", // 最新 9B，带 KV cache
	"flux-2-klein-9b",         // 固定快照版
	// Kontext 编辑系列
	"flux-kontext-max",
	"flux-kontext-pro",
	// FLUX 1.x 系列
	"flux-pro-1.1-ultra",
	"flux-pro-1.1",
	"flux-pro",
	"flux-dev",
	// Fill / Expand 工具（BFL OpenAPI 确认仍有效）
	"flux-pro-1.0-fill",
	"flux-pro-1.0-expand",
}

// 模型端点映射 - 模型名到 BFL API 路径
var ModelEndpoints = map[string]string{
	"flux-2-max":              "/v1/flux-2-max",
	"flux-2-pro-preview":      "/v1/flux-2-pro-preview",
	"flux-2-pro":              "/v1/flux-2-pro",
	"flux-2-flex":             "/v1/flux-2-flex",
	"flux-2-klein-4b":         "/v1/flux-2-klein-4b",
	"flux-2-klein-9b-preview": "/v1/flux-2-klein-9b-preview",
	"flux-2-klein-9b":         "/v1/flux-2-klein-9b",
	"flux-kontext-max":        "/v1/flux-kontext-max",
	"flux-kontext-pro":        "/v1/flux-kontext-pro",
	"flux-pro-1.1-ultra":      "/v1/flux-pro-1.1-ultra",
	"flux-pro-1.1":            "/v1/flux-pro-1.1",
	"flux-pro":                "/v1/flux-pro",
	"flux-dev":                "/v1/flux-dev",
	"flux-pro-1.0-fill":       "/v1/flux-pro-1.0-fill",
	"flux-pro-1.0-expand":     "/v1/flux-pro-1.0-expand",
}

// ReplicateModelMap one-api 模型名 → Replicate 模型 ID
// preview 版本降级到对应正式版（Replicate 无 preview 变体）
var ReplicateModelMap = map[string]string{
	"flux-2-max":              "black-forest-labs/flux-2-max",
	"flux-2-pro-preview":      "black-forest-labs/flux-2-pro", // 降级
	"flux-2-pro":              "black-forest-labs/flux-2-pro",
	"flux-2-flex":             "black-forest-labs/flux-2-flex",
	"flux-2-klein-4b":         "black-forest-labs/flux-2-klein-4b",
	"flux-2-klein-9b-preview": "black-forest-labs/flux-2-klein-9b", // 降级
	"flux-2-klein-9b":         "black-forest-labs/flux-2-klein-9b",
	"flux-kontext-pro":        "black-forest-labs/flux-kontext-pro",
	"flux-kontext-max":        "black-forest-labs/flux-kontext-max",
	"flux-pro-1.1-ultra":      "black-forest-labs/flux-1.1-pro-ultra",
	"flux-pro-1.1":            "black-forest-labs/flux-1.1-pro",
	"flux-pro":                "black-forest-labs/flux-pro",
	"flux-dev":                "black-forest-labs/flux-dev",
}

// ReplicatePriceMap 各模型固定价格（USD/张）
// 数据来源: replicate.com 页面定价，2026-05-14
// Klein 系列为 GPU 时间计费，此处为 p50 中位数估算值
var ReplicatePriceMap = map[string]float64{
	"flux-dev":                0.025,
	"flux-pro":                0.055,
	"flux-pro-1.1":            0.040,
	"flux-pro-1.1-ultra":      0.060,
	"flux-2-pro":              0.015,
	"flux-2-pro-preview":      0.015,
	"flux-2-max":              0.040,
	"flux-2-flex":             0.060,
	"flux-kontext-pro":        0.040,
	"flux-kontext-max":        0.080,
	"flux-2-klein-4b":         0.020,
	"flux-2-klein-9b":         0.005,
	"flux-2-klein-9b-preview": 0.005,
}

// FluxPriceMap BFL 模型固定价格（USD/张），用于 cost=null 时的兜底计费
// 数据来源: BFL 官方价目表，2026-05-14
var FluxPriceMap = map[string]float64{
	"flux-dev":            0.025,
	"flux-pro":            0.055,
	"flux-pro-1.0-fill":   0.055,
	"flux-pro-1.0-expand": 0.055,
	"flux-pro-1.1":        0.040,
	"flux-pro-1.1-ultra":  0.060,
	"flux-2-pro":          0.015,
	"flux-2-pro-preview":  0.015,
	"flux-2-max":          0.040,
	"flux-2-flex":         0.060,
	"flux-kontext-pro":    0.040,
	"flux-kontext-max":    0.080,
	"flux-2-klein-4b":     0.020,
	"flux-2-klein-9b":     0.005,
	"flux-2-klein-9b-preview": 0.005,
}

const (
	// 内部数据库存储状态（修改将破坏历史数据，禁止动）
	TaskStatusPending    = "pending"
	TaskStatusSubmitted  = "submitted"
	TaskStatusProcessing = "processing"
	TaskStatusSucceed    = "success"
	TaskStatusFailed     = "failed"

	// BFL 上游响应/回调状态字面值
	// BFL polling 与 webhook 推送的字段值统一为 "Ready" / "Error"
	UpstreamStatusReady = "Ready"
	UpstreamStatusError = "Error"
)
