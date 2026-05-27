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
// Deprecated: 使用 FluxPriceMap，两张表价格一致，统一维护

// FluxMPPricingTier flux-2-* 系列分级 MP 计费定义
type FluxMPPricingTier struct {
	FirstMPPrice      float64 // 首个 1MP（USD）
	SubsequentMPPrice float64 // 后续每 MP（USD）
	RefMPPrice        float64 // 参考图每 MP（USD）
}

// FluxMPPricingMap flux-2-* 系列按 MP 分级计费
// 数据来源: bfl.ai/pricing，2026-05-27
var FluxMPPricingMap = map[string]FluxMPPricingTier{
	"flux-2-max":              {FirstMPPrice: 0.07, SubsequentMPPrice: 0.03, RefMPPrice: 0.015},
	"flux-2-pro":              {FirstMPPrice: 0.03, SubsequentMPPrice: 0.015, RefMPPrice: 0.015},
	"flux-2-pro-preview":      {FirstMPPrice: 0.03, SubsequentMPPrice: 0.015, RefMPPrice: 0.015},
	"flux-2-flex":             {FirstMPPrice: 0.05, SubsequentMPPrice: 0.05, RefMPPrice: 0.05},
	"flux-2-klein-9b":         {FirstMPPrice: 0.015, SubsequentMPPrice: 0.002, RefMPPrice: 0.002},
	"flux-2-klein-9b-preview": {FirstMPPrice: 0.015, SubsequentMPPrice: 0.002, RefMPPrice: 0.002},
	"flux-2-klein-4b":         {FirstMPPrice: 0.014, SubsequentMPPrice: 0.001, RefMPPrice: 0.001},
}

// FluxPriceMap BFL 模型固定价格（USD/张），用于 cost=null 时的兜底计费
// 数据来源: bfl.ai/pricing，2026-05-27
// flux-2-* 系列按 MP 计费，此处取首个 1MP 的价格作为兜底估算值
var FluxPriceMap = map[string]float64{
	// FLUX 1.x 系列（固定价/张）
	"flux-dev":            0.025,
	"flux-pro":            0.050,
	"flux-pro-1.0-fill":   0.050,
	"flux-pro-1.0-expand": 0.050,
	"flux-pro-1.1":        0.040,
	"flux-pro-1.1-ultra":  0.060,
	// FLUX Kontext 系列（固定价/张）
	"flux-kontext-pro": 0.040,
	"flux-kontext-max": 0.080,
	// FLUX.2 系列（按 MP 计费，首 MP 价作为 1MP 兜底估算）
	"flux-2-pro":              0.030,
	"flux-2-pro-preview":      0.030,
	"flux-2-max":              0.070,
	"flux-2-flex":             0.050,
	"flux-2-klein-4b":         0.014,
	"flux-2-klein-9b":         0.015,
	"flux-2-klein-9b-preview": 0.015,
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
