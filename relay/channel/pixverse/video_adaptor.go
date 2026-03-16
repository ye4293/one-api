package pixverse

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/helper"
	dbmodel "github.com/songquanpeng/one-api/model"
	relaychannel "github.com/songquanpeng/one-api/relay/channel"
	openaiAdaptor "github.com/songquanpeng/one-api/relay/channel/openai"
	"github.com/songquanpeng/one-api/relay/model"
	"github.com/songquanpeng/one-api/relay/util"
)

type VideoAdaptor struct {
	relaychannel.BaseVideoAdaptor
}

func (a *VideoAdaptor) GetProviderName() string      { return "pixverse" }
func (a *VideoAdaptor) GetChannelName() string        { return "Pixverse" }
func (a *VideoAdaptor) GetSupportedModels() []string { return []string{"v3.5"} }

func (a *VideoAdaptor) HandleVideoRequest(c *gin.Context, req *model.VideoRequest, meta *util.RelayMeta) (*relaychannel.VideoTaskResult, *model.ErrorWithStatusCode) {
	ch, err := dbmodel.GetChannelById(meta.ChannelId, true)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "get_channel_error", http.StatusInternalServerError)
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_request_error", http.StatusBadRequest)
	}

	var imageCheck struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(bodyBytes, &imageCheck); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
	}

	var fullRequestUrl string
	var jsonData []byte

	if imageCheck.Image != "" {
		// Image-to-video: upload image first
		uploadUrl := meta.BaseURL + "/openapi/v2/image/upload"

		buf := &bytes.Buffer{}
		writer := multipart.NewWriter(buf)
		part, err := writer.CreateFormFile("image", "image.png")
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_create_form", http.StatusInternalServerError)
		}

		if strings.HasPrefix(imageCheck.Image, "data:") {
			b64Data := imageCheck.Image
			if i := strings.Index(b64Data, ","); i != -1 {
				b64Data = b64Data[i+1:]
			}
			imgData, err := base64.StdEncoding.DecodeString(b64Data)
			if err != nil {
				return nil, openaiAdaptor.ErrorWrapper(err, "invalid_base64_image", http.StatusBadRequest)
			}
			if _, err = part.Write(imgData); err != nil {
				return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_write_image", http.StatusInternalServerError)
			}
		} else {
			if !strings.HasPrefix(imageCheck.Image, "http://") && !strings.HasPrefix(imageCheck.Image, "https://") {
				return nil, openaiAdaptor.ErrorWrapper(fmt.Errorf("invalid URL format"), "invalid_url", http.StatusBadRequest)
			}
			imgResp, err := http.Get(imageCheck.Image) //nolint:noctx
			if err != nil {
				return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_download_image", http.StatusBadRequest)
			}
			defer imgResp.Body.Close()
			if _, err = io.Copy(part, imgResp.Body); err != nil {
				return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_copy_image", http.StatusInternalServerError)
			}
		}
		writer.Close()

		uploadReq, err := http.NewRequest(http.MethodPost, uploadUrl, buf)
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_create_request", http.StatusInternalServerError)
		}
		uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
		uploadReq.Header.Set("API-KEY", ch.Key)
		uploadReq.Header.Set("AI-trace-id", helper.GetUUID())

		uploadResp, err := http.DefaultClient.Do(uploadReq)
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_upload_image", http.StatusInternalServerError)
		}
		defer uploadResp.Body.Close()

		var uploadResponse UploadImageResponse
		if err := json.NewDecoder(uploadResp.Body).Decode(&uploadResponse); err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_parse_upload_response", http.StatusInternalServerError)
		}
		if uploadResponse.ErrCode != 0 {
			return nil, openaiAdaptor.ErrorWrapper(
				fmt.Errorf("image upload failed: %s", uploadResponse.ErrMsg),
				"image_upload_failed", http.StatusBadRequest)
		}

		var originalBody PixverseRequest2
		if err := json.Unmarshal(bodyBytes, &originalBody); err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}
		convertDurationField(&originalBody.Duration)
		originalBody.ImgId = uploadResponse.Resp.ImgId
		originalBody.Image = ""

		jsonData, err = json.Marshal(originalBody)
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
		}
		fullRequestUrl = meta.BaseURL + "/openapi/v2/video/img/generate"
	} else {
		var textRequest PixverseRequest1
		if err := json.Unmarshal(bodyBytes, &textRequest); err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "invalid_request_body", http.StatusBadRequest)
		}
		convertDurationField(&textRequest.Duration)
		jsonData, err = json.Marshal(textRequest)
		if err != nil {
			return nil, openaiAdaptor.ErrorWrapper(err, "failed_to_marshal_request", http.StatusInternalServerError)
		}
		fullRequestUrl = meta.BaseURL + "/openapi/v2/video/text/generate"
	}

	postReq, err := http.NewRequest(http.MethodPost, fullRequestUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "create_request_error", http.StatusInternalServerError)
	}
	postReq.Header.Set("Ai-trace-id", helper.GetUUID())
	postReq.Header.Set("API-KEY", ch.Key)
	postReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "read_response_error", http.StatusInternalServerError)
	}

	var pixverseResp PixverseVideoResponse
	if err := json.Unmarshal(body, &pixverseResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "response_parse_error", http.StatusInternalServerError)
	}
	pixverseResp.StatusCode = resp.StatusCode

	if pixverseResp.ErrCode != 0 || pixverseResp.StatusCode != 200 || pixverseResp.Resp == nil {
		return nil, openaiAdaptor.ErrorWrapper(
			fmt.Errorf("pixverse error: %s", pixverseResp.ErrMsg),
			"api_error", http.StatusInternalServerError)
	}

	videoId := strconv.Itoa(pixverseResp.Resp.VideoId)
	return &relaychannel.VideoTaskResult{
		TaskId:     videoId,
		TaskStatus: "succeed",
		VideoId:    videoId,
		Quota:      45,
	}, nil
}

func (a *VideoAdaptor) HandleVideoResult(c *gin.Context, videoTask *dbmodel.Video, ch *dbmodel.Channel, cfg *dbmodel.ChannelConfig) (*model.GeneralFinalVideoResponse, *model.ErrorWithStatusCode) {
	taskId := videoTask.TaskId
	url := fmt.Sprintf("%s/openapi/v2/video/result/%s", *ch.BaseURL, taskId)

	headers := map[string]string{
		"API-KEY":     ch.Key,
		"Ai-trace-id": "aaaaa",
	}
	_, body, err := relaychannel.SendVideoResultQuery(url, headers)
	if err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "request_error", http.StatusInternalServerError)
	}

	log.Printf("Pixverse response body: %s", string(body))

	var pixverseResp PixverseFinalResponse
	if err := json.Unmarshal(body, &pixverseResp); err != nil {
		return nil, openaiAdaptor.ErrorWrapper(err, "json_parse_error", http.StatusInternalServerError)
	}

	videoIdStr := taskId
	if pixverseResp.Resp != nil {
		videoIdStr = strconv.Itoa(pixverseResp.Resp.Id)
	}

	generalResponse := &model.GeneralFinalVideoResponse{
		TaskId:     videoIdStr,
		VideoId:    videoIdStr,
		TaskStatus: "succeed",
		Message:    pixverseResp.ErrMsg,
		Duration:   videoTask.Duration,
	}

	if pixverseResp.Resp != nil && pixverseResp.Resp.Url != "" {
		generalResponse.VideoResult = pixverseResp.Resp.Url
		generalResponse.VideoResults = []model.VideoResultItem{{Url: pixverseResp.Resp.Url}}
	}

	if pixverseResp.ErrCode != 0 {
		generalResponse.TaskStatus = "failed"
	}

	return generalResponse, nil
}

func convertDurationField(d *interface{}) {
	if d == nil {
		return
	}
	switch v := (*d).(type) {
	case float64:
		*d = int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			*d = n
		}
	}
}
