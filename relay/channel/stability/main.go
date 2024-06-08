package stability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/logger"
	dbmodel "github.com/songquanpeng/one-api/model"
	"github.com/songquanpeng/one-api/relay/channel/openai"
	relayconstant "github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

func GetSdRequestModel(relayModel int) (string, error) {
	modelMap := map[int]string{
		relayconstant.RelayModelGenerateCore:         common.SdActionGenerateCore,
		relayconstant.RelayModelGenerateSd3:          common.SdActionGenerateSd3,
		relayconstant.RelayModelGenerateUltra:        common.SdActionGenerateUltra,
		relayconstant.RelayModeUpscaleConservative:   common.SdActionUpscaleConservative,
		relayconstant.RelayModeUpscaleCreative:       common.SdActionUpscaleCreative,
		relayconstant.RelayModeUpscaleCreativeResult: common.SdActionUpscaleCreativeResult,
		relayconstant.RelayModeEditErase:             common.SdActionEditErase,
		relayconstant.RelayModeEditInpaint:           common.SdActionEditInpaint,
		relayconstant.RelayModeEditOutpaint:          common.SdActionEditOutpaint,
		relayconstant.RelayModeEditSR:                common.SdActionEditSearchReplace,
		relayconstant.RelayModeEditRB:                common.SdActionEditRemoveBackground,
		relayconstant.RelayModeControlSketch:         common.SdActionControlSketch,
		relayconstant.RelayModeControlStructure:      common.SdActionControlStructure,
	}

	modelName, ok := modelMap[relayModel]
	if !ok {
		return "", errors.New("unknown_relay_model")
	}

	modelName = strings.ToLower(modelName)
	return modelName, nil
}

func DoSdHttpRequest(c *gin.Context, timeout time.Duration, fullRequestURL string) (*model.ErrorWithStatusCode, []byte, SdResponse, string, error) {
	var nullBytes []byte
	formData, err := c.MultipartForm()
	if err != nil {
		return openai.ErrorWrapper(err, "parse_multipart_form_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, "", err
	}

	// 构建请求体
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	var modelName string
	// 添加表单字段
	for key, values := range formData.Value {
		for _, value := range values {
			if key == "model" {
				modelName = value
			}
			_ = writer.WriteField(key, value)
		}
	}
	// 添加文件
	for key, fileHeaders := range formData.File {
		for _, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				return openai.ErrorWrapper(err, "open_uploaded_file_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, modelName, err
			}
			defer file.Close()

			part, err := writer.CreateFormFile(key, fileHeader.Filename)
			if err != nil {
				return openai.ErrorWrapper(err, "create_form_file_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, modelName, err
			}
			_, err = io.Copy(part, file)
			if err != nil {
				return openai.ErrorWrapper(err, "copy_file_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, modelName, err
			}
		}
	}

	accept := c.Request.Header.Get("Accept")

	err = writer.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_writer_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, modelName, err
	}

	req, err := http.NewRequest(c.Request.Method, fullRequestURL, body)
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, modelName, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// 设置请求超时
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// 使用带有超时的 context 创建新的请求
	req = req.WithContext(ctx)
	// req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))
	auth := c.Request.Header.Get("Authorization")
	req.Header.Set("Authorization", auth)

	defer cancel()
	resp, err := util.GetHttpClient().Do(req)
	if err != nil {
		logger.SysError("do request failed: " + util.ProcessString(err.Error()))
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError), nullBytes, SdResponse{}, modelName, err
	}
	statusCode := resp.StatusCode
	err = req.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", statusCode), nullBytes, SdResponse{}, modelName, err
	}
	err = c.Request.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", statusCode), nullBytes, SdResponse{}, modelName, err
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", statusCode), nullBytes, SdResponse{}, modelName, err
	}
	err = resp.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_response_body_failed", statusCode), responseBody, SdResponse{}, modelName, err
	}

	if accept == "image/*" {
		return &model.ErrorWithStatusCode{
			StatusCode: statusCode,
		}, responseBody, SdResponse{}, modelName, nil
	} else {
		var sdResponse SdResponse
		err = json.Unmarshal(responseBody, &sdResponse)
		if err != nil {
			return openai.ErrorWrapper(err, "parse_response_body_failed", statusCode), responseBody, SdResponse{}, modelName, err
		}

		return &model.ErrorWithStatusCode{
			StatusCode: statusCode,
		}, responseBody, sdResponse, modelName, nil
	}

}

func DoSdUpscaleResults(c *gin.Context, timeout time.Duration, channel dbmodel.Channel, fullRequestURL string) (*model.ErrorWithStatusCode, []byte, error) {
	var nullBytes []byte
	req, err := http.NewRequest("GET", fullRequestURL, nil)
	if err != nil {
		return openai.ErrorWrapper(err, "create_request_failed", http.StatusInternalServerError), nullBytes, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	accept := c.Request.Header.Get("Accept")
	req = req.WithContext(ctx)
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	resp, err := util.GetHttpClient().Do(req)
	if err != nil {
		return openai.ErrorWrapper(err, "send_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode

	err = c.Request.Body.Close()
	if err != nil {
		return openai.ErrorWrapper(err, "close_request_body_failed", http.StatusInternalServerError), nullBytes, err
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nullBytes, err
	}

	if accept == "image/*" {
		return &model.ErrorWithStatusCode{
			StatusCode: statusCode,
		}, responseBody, nil
	} else {
		var sdResponse SdResponse
		err = json.Unmarshal(responseBody, &sdResponse)
		if err != nil {
			return openai.ErrorWrapper(err, "parse_response_body_failed", http.StatusInternalServerError), responseBody, err
		}

		return &model.ErrorWithStatusCode{
			StatusCode: statusCode,
		}, responseBody, nil
	}
}
