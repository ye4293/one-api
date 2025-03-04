package channel

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
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

type VideoAdaptor interface {
	// 初始化适配器
	Init(meta *util.RelayMeta)

	// 获取请求URL
	GetRequestURL(meta *util.RelayMeta) (string, error)

	// 设置请求头
	SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error

	// 转换视频生成请求
	ConvertVideoRequest(c *gin.Context, request *model.VideoRequest) (any, error)

	// 执行视频生成请求
	DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error)

	// 处理视频生成响应
	DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (taskId string, err *model.ErrorWithStatusCode)

	// 获取视频生成结果
	GetVideoResult(c *gin.Context, taskId string, meta *util.RelayMeta) (*model.VideoResult, *model.ErrorWithStatusCode)

	// 获取支持的模型列表
	GetSupportedModels() []string

	// 获取渠道名称
	GetChannelName() string
}

// 基础适配器实现，包含共同的功能
type BaseVideoAdaptor struct {
	BaseURL     string
	ChannelType int
	Models      []string
}

// 为不同供应商实现适配器
type MinimaxVideoAdaptor struct {
	BaseVideoAdaptor
}

type PixverseVideoAdaptor struct {
	BaseVideoAdaptor
}
