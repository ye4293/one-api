package replicate

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type Adaptor struct {
	ChannelType int
}

// ConvertRequest implements channel.Adaptor.
func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	panic("unimplemented")
}

// DoRequest implements channel.Adaptor.
func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	panic("unimplemented")
}

// DoResponse implements channel.Adaptor.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	panic("unimplemented")
}

// GetRequestURL implements channel.Adaptor.

// SetupRequestHeader implements channel.Adaptor.
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	panic("unimplemented")
}

func (a *Adaptor) Init(meta *util.RelayMeta) {
	a.ChannelType = meta.ChannelType
}

func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	logger.SysLog(fmt.Sprintf("%s/v1/models/black-forest-labs/%s/predictions", meta.BaseURL, meta.ActualModelName))
	return fmt.Sprintf("%s/v1/models/black-forest-labs/%s/predictions", meta.BaseURL, meta.ActualModelName), nil
}

func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	fluxRequest := FluxReplicate{
		Input: FluxReplicateInput{
			Prompt:        request.Prompt,
			NumOutputs:    request.N,
			Seed:          request.Seed,
			OutputFormat:  request.ResponseFormat,
			OutputQuality: request.OutputQuality,
		}}

	// 设置默认值或处理特殊情况
	if fluxRequest.Input.NumOutputs == 0 {
		fluxRequest.Input.NumOutputs = 1 // 设置默认值为1p
	}

	// 转换 size 到 aspect_ratio
	if request.Size != "" {
		aspectRatio, err := sizeToAspectRatio(request.Size)
		if err != nil {
			return nil, fmt.Errorf("failed to convert size to aspect ratio: %v", err)
		}
		fluxRequest.Input.AspectRatio = aspectRatio
	}
	return fluxRequest, nil
}

func sizeToAspectRatio(size string) (string, error) {
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid size format: %s", size)
	}

	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid width: %s", parts[0])
	}

	height, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid height: %s", parts[1])
	}

	// 计算最大公约数
	gcd := func(a, b int) int {
		for b != 0 {
			a, b = b, a%b
		}
		return a
	}

	divisor := gcd(width, height)
	return fmt.Sprintf("%d:%d", width/divisor, height/divisor), nil
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return "replicate"
}
