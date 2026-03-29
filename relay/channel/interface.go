package channel

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor interface {
	Init(meta *util.RelayMeta)
	GetRequestURL(meta *util.RelayMeta) (string, error)
	SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error
	ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error)
	ConvertImageRequest(request *model.ImageRequest) (any, error)
	DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error)
	DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode)
	GetModelList() []string
	GetModelDetails() []model.APIModel
	GetChannelName() string
}

// ErrorHandler 可选接口：支持自定义错误处理的 adaptor 可以实现此接口
type ErrorHandler interface {
	HandleErrorResponse(resp *http.Response) *model.ErrorWithStatusCode
}

type VideoAdaptor interface {
	// 初始化适配器
	Init(meta *util.RelayMeta)

	// 处理完整的请求流程：转换请求、发送 HTTP、解析响应
	HandleVideoRequest(c *gin.Context, videoRequest *model.VideoRequest,
		meta *util.RelayMeta) (*VideoTaskResult, *model.ErrorWithStatusCode)

	// 处理完整的结果查询流程：构建 URL、认证、发送请求、解析响应、映射状态
	HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video,
		channel *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (
		*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode)

	GetProviderName() string
	GetSupportedModels() []string
	GetChannelName() string
	GetPrePaymentQuota() int64
}

// VideoTaskResult 封装视频任务提交后的结果元数据
type VideoTaskResult struct {
	TaskId        string
	TaskStatus    string // "succeed"、"failed"、"processing"
	Message       string
	Mode          string
	Duration      string
	VideoType     string
	VideoId       string
	Quota         int64
	Resolution    string
	Credentials   string  // 用于 VertexAI 保存凭证
	VideoDuration float64 // 输入视频时长（秒），仅编辑/延长时有值
}
