package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"
)

// Regex to match data URL pattern (支持所有媒体类型)
var dataURLPattern = regexp.MustCompile(`data:([^;]+);base64,(.*)`)

// isBase64 检测字符串是否为有效的base64编码
func isBase64(s string) bool {
	// 基本长度检查：base64编码长度应该是4的倍数
	if len(s) == 0 || len(s)%4 != 0 {
		return false
	}

	// 检查字符集：只允许A-Z, a-z, 0-9, +, /, =
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '+' || c == '/' || c == '=') {
			return false
		}
	}

	// 尝试解码验证
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}

// wrapBase64WithType 检测base64数据的媒体类型并包装成data URL
func wrapBase64WithType(base64Data string) (string, error) {
	// 解码base64数据
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %v", err)
	}

	// 检测媒体类型
	contentType := http.DetectContentType(decoded)

	// 验证是否为支持的媒体类型
	if !isSupportedMediaType(contentType) {
		return "", fmt.Errorf("unsupported media type: %s", contentType)
	}

	// 构造data URL
	dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64Data)
	return dataURL, nil
}

// isSupportedMediaType 检查是否为支持的媒体类型
func isSupportedMediaType(contentType string) bool {
	supportedTypes := []string{
		// 图片类型
		"image/jpeg", "image/jpg", "image/png", "image/gif",
		"image/webp", "image/bmp", "image/tiff", "image/svg+xml",
		// 音频类型（Gemini支持的格式，包括常见变体）
		"audio/wav", "audio/x-wav", "audio/wave", // WAV格式变体
		"audio/mp3", "audio/mpeg", "audio/x-mp3", // MP3格式变体
		"audio/aiff", "audio/x-aiff", // AIFF格式变体
		"audio/aac", "audio/x-aac", // AAC格式变体
		"audio/ogg", "audio/x-ogg", // OGG格式变体
		"audio/flac", "audio/x-flac", // FLAC格式变体
		"audio/mp4", "audio/webm",
		// 视频类型（Gemini支持的格式，包括常见变体）
		"video/mp4", "video/x-mp4", // MP4格式变体
		"video/mpeg", "video/x-mpeg", // MPEG格式变体
		"video/mov", "video/quicktime", "video/x-quicktime", // MOV格式变体
		"video/avi", "video/x-msvideo", "video/msvideo", // AVI格式变体
		"video/x-flv", "video/flv", // FLV格式变体
		"video/mpg", "video/x-mpg", // MPG格式变体
		"video/webm", "video/x-webm", // WebM格式变体
		"video/wmv", "video/x-wmv", // WMV格式变体
		"video/3gpp", "video/x-3gpp", // 3GPP格式变体
		"video/ogg", "video/x-ogg", // OGG视频格式变体
		// 文档类型（Gemini支持的格式）
		"application/pdf",
	}

	for _, supportedType := range supportedTypes {
		if strings.HasPrefix(contentType, supportedType) {
			return true
		}
	}
	return false
}

// GetMediaTypeFromBase64 从base64数据中获取媒体类型（新增的辅助函数）
func GetMediaTypeFromBase64(base64Data string) (string, error) {
	// 解码base64数据
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %v", err)
	}

	// 检测并返回媒体类型
	contentType := http.DetectContentType(decoded)
	return contentType, nil
}

// IsImageType 检查是否为图片类型
func IsImageType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// IsAudioType 检查是否为音频类型
func IsAudioType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "audio/")
}

// IsVideoType 检查是否为视频类型
func IsVideoType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "video/")
}

// IsDocumentType 检查是否为文档类型
func IsDocumentType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "application/pdf") ||
		strings.HasPrefix(mimeType, "text/") ||
		strings.HasPrefix(mimeType, "application/msword") ||
		strings.HasPrefix(mimeType, "application/vnd.openxmlformats")
}

// getMimeTypeFromURL 根据URL的文件扩展名推断MIME类型
func getMimeTypeFromURL(rawURL string) string {
	// 解析 URL，去掉查询参数，只检查路径部分
	parsedURL, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := strings.ToLower(parsedURL.Path)

	// 音频格式
	if strings.HasSuffix(path, ".wav") {
		return "audio/wav"
	} else if strings.HasSuffix(path, ".mp3") {
		return "audio/mp3"
	} else if strings.HasSuffix(path, ".aac") {
		return "audio/aac"
	} else if strings.HasSuffix(path, ".ogg") {
		return "audio/ogg"
	} else if strings.HasSuffix(path, ".flac") {
		return "audio/flac"
	} else if strings.HasSuffix(path, ".aiff") || strings.HasSuffix(path, ".aif") {
		return "audio/aiff"
	}

	// 视频格式
	if strings.HasSuffix(path, ".mp4") {
		return "video/mp4"
	} else if strings.HasSuffix(path, ".webm") {
		return "video/webm"
	} else if strings.HasSuffix(path, ".mov") {
		return "video/quicktime"
	} else if strings.HasSuffix(path, ".avi") {
		return "video/x-msvideo"
	} else if strings.HasSuffix(path, ".wmv") {
		return "video/wmv"
	}

	// 图片格式
	if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
		return "image/jpeg"
	} else if strings.HasSuffix(path, ".png") {
		return "image/png"
	} else if strings.HasSuffix(path, ".gif") {
		return "image/gif"
	} else if strings.HasSuffix(path, ".webp") {
		return "image/webp"
	}

	// 文档格式
	if strings.HasSuffix(path, ".pdf") {
		return "application/pdf"
	}

	return ""
}

// GetMediaFromUrl 通用媒体处理函数，支持图片、音频、视频（为Gemini设计）
func GetMediaFromUrl(input string) (mimeType string, data string, mediaType string, err error) {
	// 获取媒体信息
	mimeType, data, err = GetImageFromUrl(input)
	if err != nil {
		return "", "", "", err
	}

	// 确定媒体类型
	switch {
	case IsImageType(mimeType):
		mediaType = "image"
	case IsAudioType(mimeType):
		mediaType = "audio"
	case IsVideoType(mimeType):
		mediaType = "video"
	case IsDocumentType(mimeType):
		mediaType = "document"
	default:
		mediaType = "unknown"
	}

	return mimeType, data, mediaType, nil
}

// GetGeminiMediaInfo 获取Gemini媒体信息
func GetGeminiMediaInfo(input string) (mimeType string, data string, mediaType string, err error) {
	return GetMediaFromUrl(input)
}

func IsImageUrl(url string) (bool, error) {
	// 创建一个自定义的 HTTP 客户端
	client := &http.Client{}

	// 创建一个新的 GET 请求
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}

	// 设置自定义的 User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// 读取响应体的前 512 字节
	buffer := make([]byte, 512)
	_, err = io.ReadFull(resp.Body, buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, err
	}

	// 使用 http.DetectContentType 检测内容类型
	contentType := http.DetectContentType(buffer)
	return strings.HasPrefix(contentType, "image/"), nil
}

func GetImageSizeFromUrl(url string) (width int, height int, err error) {
	isImage, err := IsImageUrl(url)
	if !isImage {
		return
	}
	resp, err := http.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	img, _, err := image.DecodeConfig(resp.Body)
	if err != nil {
		return
	}
	return img.Width, img.Height, nil
}

func GetImageFromUrl(input string) (mimeType string, data string, err error) {
	// Check if the input is a data URL
	matches := dataURLPattern.FindStringSubmatch(input)
	if len(matches) == 3 {
		// Input is a data URL
		mimeType = matches[1] // 现在包含完整的媒体类型，如 image/png, audio/mpeg 等
		data = matches[2]
		return mimeType, data, nil
	}

	// Check if the input is pure base64
	if isBase64(input) {
		// 检测媒体类型
		detectedType, err := GetMediaTypeFromBase64(input)
		if err != nil {
			return "", "", fmt.Errorf("failed to detect media type from base64: %v", err)
		}

		// 验证是否为支持的媒体类型
		if !isSupportedMediaType(detectedType) {
			return "", "", fmt.Errorf("unsupported media type: %s", detectedType)
		}

		return detectedType, input, nil
	}

	// If not a data URL or base64, treat as a regular URL - download and detect media type
	resp, err := http.Get(input)
	if err != nil {
		return "", "", fmt.Errorf("failed to download from URL: %v", err)
	}
	defer resp.Body.Close()

	buffer := new(bytes.Buffer)
	_, err = buffer.ReadFrom(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response body: %v", err)
	}

	// 从响应头获取媒体类型
	mimeType = resp.Header.Get("Content-Type")
	
	// 如果 Content-Type 为空或不可靠（octet-stream），则从内容检测
	if mimeType == "" || mimeType == "application/octet-stream" || mimeType == "binary/octet-stream" {
		// 优先使用内容检测
		detectedType := http.DetectContentType(buffer.Bytes())
		if detectedType != "" && detectedType != "application/octet-stream" {
			mimeType = detectedType
		}
	}

	// 如果还是无法检测到正确的MIME类型，尝试根据URL扩展名推断
	if mimeType == "" || mimeType == "application/octet-stream" || mimeType == "binary/octet-stream" {
		mimeType = getMimeTypeFromURL(input)
	}

	// 验证是否为支持的媒体类型
	if !isSupportedMediaType(mimeType) {
		return "", "", fmt.Errorf("unsupported media type: %s for URL: %s", mimeType, input)
	}

	data = base64.StdEncoding.EncodeToString(buffer.Bytes())

	return mimeType, data, nil
}

var (
	reg = regexp.MustCompile(`data:([^;]+);base64,`) // 更新为支持所有媒体类型
)

var readerPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Reader{}
	},
}

func GetImageSizeFromBase64(encoded string) (width int, height int, err error) {
	decoded, err := base64.StdEncoding.DecodeString(reg.ReplaceAllString(encoded, ""))
	if err != nil {
		return 0, 0, err
	}

	reader := readerPool.Get().(*bytes.Reader)
	defer readerPool.Put(reader)
	reader.Reset(decoded)

	img, _, err := image.DecodeConfig(reader)
	if err != nil {
		return 0, 0, err
	}

	return img.Width, img.Height, nil
}

func GetImageSize(image string) (width int, height int, err error) {
	if strings.HasPrefix(image, "data:") {
		return GetImageSizeFromBase64(image)
	}

	// 检测纯base64字符串
	if isBase64(image) {
		// 检测媒体类型并构造data URL
		dataURL, err := wrapBase64WithType(image)
		if err != nil {
			return 0, 0, err
		}
		return GetImageSizeFromBase64(dataURL)
	}

	return GetImageSizeFromUrl(image)
}
