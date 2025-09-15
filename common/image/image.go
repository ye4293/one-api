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
		// 音频类型（Gemini支持的格式）
		"audio/wav", "audio/mp3", "audio/aiff", "audio/aac",
		"audio/ogg", "audio/flac", "audio/mpeg", "audio/mp4",
		"audio/webm",
		// 视频类型（Gemini支持的格式）
		"video/mp4", "video/mpeg", "video/mov", "video/quicktime",
		"video/avi", "video/x-msvideo", "video/x-flv", "video/mpg",
		"video/webm", "video/wmv", "video/3gpp", "video/ogg",
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

// IsGeminiSupportedFormat 检查是否为Gemini支持的格式
func IsGeminiSupportedFormat(mimeType string) bool {
	geminiSupportedTypes := []string{
		// Gemini支持的图片格式
		"image/jpeg", "image/jpg", "image/png", "image/gif",
		"image/webp", "image/bmp", "image/tiff",
		// Gemini支持的音频格式
		"audio/wav", "audio/mp3", "audio/aiff", "audio/aac",
		"audio/ogg", "audio/flac",
		// Gemini支持的视频格式
		"video/mp4", "video/mpeg", "video/mov", "video/quicktime",
		"video/avi", "video/x-msvideo", "video/x-flv", "video/mpg",
		"video/webm", "video/wmv", "video/3gpp",
		// Gemini支持的文档格式
		"application/pdf",
	}

	for _, supportedType := range geminiSupportedTypes {
		if strings.HasPrefix(mimeType, supportedType) {
			return true
		}
	}
	return false
}

// GetGeminiMediaInfo 专为Gemini设计的媒体信息获取函数
func GetGeminiMediaInfo(input string) (mimeType string, data string, mediaType string, err error) {
	// 获取媒体信息
	mimeType, data, mediaType, err = GetMediaFromUrl(input)
	if err != nil {
		return "", "", "", err
	}

	// 验证是否为Gemini支持的格式
	if !IsGeminiSupportedFormat(mimeType) {
		return "", "", "", fmt.Errorf("unsupported media type for Gemini: %s", mimeType)
	}

	return mimeType, data, mediaType, nil
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

	// If not a data URL or base64, treat as a regular URL
	isImage, err := IsImageUrl(input)
	if err != nil {
		return "", "", err
	}
	if !isImage {
		return "", "", fmt.Errorf("URL does not point to an image")
	}

	resp, err := http.Get(input)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	buffer := new(bytes.Buffer)
	_, err = buffer.ReadFrom(resp.Body)
	if err != nil {
		return "", "", err
	}

	mimeType = resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(buffer.Bytes())
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
