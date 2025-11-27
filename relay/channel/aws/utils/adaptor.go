package utils

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type AwsAdapter interface {
	ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error)
	DoResponse(c *gin.Context, awsCli *bedrockruntime.Client, meta *util.RelayMeta) (usage *model.Usage, err *model.ErrorWithStatusCode)
}

type Adaptor struct {
	Meta      *util.RelayMeta
	AwsClient *bedrockruntime.Client
}

func (a *Adaptor) Init(meta *util.RelayMeta) error {
	a.Meta = meta

	key := meta.ActualAPIKey
	parts := strings.Split(key, "|")

	var accessKey, secretKey, region string

	if len(parts) == 3 {
		accessKey = parts[0]
		secretKey = parts[1]
		region = parts[2]
	} else {
		// Fallback to legacy config for backward compatibility
		accessKey = meta.Config.AK
		secretKey = meta.Config.SK
		region = meta.Config.Region
	}

	if accessKey == "" || secretKey == "" || region == "" {
		return errors.New("AWS credentials not provided or invalid")
	}

	a.AwsClient = bedrockruntime.New(bedrockruntime.Options{
		Region:      region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	})
	return nil
}

func (a *Adaptor) GetRequestURL(meta *util.RelayMeta) (string, error) {
	return "", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *util.RelayMeta) error {
	return nil
}

func (a *Adaptor) ConvertImageRequest(request *model.ImageRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *util.RelayMeta, requestBody io.Reader) (*http.Response, error) {
	return nil, nil
}
