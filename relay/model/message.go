package model

type Message struct {
	Role             string  `json:"role,omitempty"`
	Content          any     `json:"content,omitempty"`
	Name             *string `json:"name,omitempty"`
	ToolCalls        []Tool  `json:"tool_calls,omitempty"`
	ToolCallId       string  `json:"tool_call_id,omitempty"`
	ReasoningContent string  `json:"reasoning_content,omitempty"`
}

func (m Message) IsStringContent() bool {
	_, ok := m.Content.(string)
	return ok
}

func (m Message) StringContent() string {
	content, ok := m.Content.(string)
	if ok {
		return content
	}
	contentList, ok := m.Content.([]any)
	if ok {
		var contentStr string
		for _, contentItem := range contentList {
			contentMap, ok := contentItem.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == ContentTypeText {
				if subStr, ok := contentMap["text"].(string); ok {
					contentStr += subStr
				}
			}
		}
		return contentStr
	}
	return ""
}

func (m Message) ParseContent() []MessageContent {
	var contentList []MessageContent
	content, ok := m.Content.(string)
	if ok {
		contentList = append(contentList, MessageContent{
			Type: ContentTypeText,
			Text: content,
		})
		return contentList
	}
	anyList, ok := m.Content.([]any)
	if ok {
		for _, contentItem := range anyList {
			contentMap, ok := contentItem.(map[string]any)
			if !ok {
				continue
			}
			switch contentMap["type"] {
			case ContentTypeText:
				if subStr, ok := contentMap["text"].(string); ok {
					contentList = append(contentList, MessageContent{
						Type: ContentTypeText,
						Text: subStr,
					})
				}
			case ContentTypeImageURL:
				if subObj, ok := contentMap["image_url"].(map[string]any); ok {
					contentList = append(contentList, MessageContent{
						Type: ContentTypeImageURL,
						ImageURL: &ImageURL{
							Url: subObj["url"].(string),
						},
					})
				}
			case ContentTypeAudioURL:
				if subObj, ok := contentMap["audio_url"].(map[string]any); ok {
					contentList = append(contentList, MessageContent{
						Type: ContentTypeAudioURL,
						AudioURL: &AudioURL{
							Url: subObj["url"].(string),
						},
					})
				}
			case ContentTypeVideoURL:
				if subObj, ok := contentMap["video_url"].(map[string]any); ok {
					contentList = append(contentList, MessageContent{
						Type: ContentTypeVideoURL,
						VideoURL: &VideoURL{
							Url: subObj["url"].(string),
						},
					})
				}
			case ContentTypeInputAudio:
				if subObj, ok := contentMap["input_audio"].(map[string]any); ok {
					inputAudio := &InputAudio{}
					if data, ok := subObj["data"].(string); ok {
						inputAudio.Data = data
					}
					if format, ok := subObj["format"].(string); ok {
						inputAudio.Format = format
					}
					contentList = append(contentList, MessageContent{
						Type:       ContentTypeInputAudio,
						InputAudio: inputAudio,
					})
				}
			case ContentTypeFileURL:
				if subObj, ok := contentMap["file_url"].(map[string]any); ok {
					contentList = append(contentList, MessageContent{
						Type: ContentTypeFileURL,
						FileURL: &FileURL{
							Url: subObj["url"].(string),
						},
					})
				}
			}
		}
		return contentList
	}
	return nil
}

type ImageURL struct {
	Url    string `json:"url,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type AudioURL struct {
	Url string `json:"url,omitempty"`
}

type VideoURL struct {
	Url string `json:"url,omitempty"`
}

type InputAudio struct {
	Data   string `json:"data,omitempty"`   // base64编码的音频数据
	Format string `json:"format,omitempty"` // 音频格式，如 wav, mp3
}

type FileURL struct {
	Url string `json:"url,omitempty"`
}

type MessageContent struct {
	Type       string      `json:"type,omitempty"`
	Text       string      `json:"text"`
	ImageURL   *ImageURL   `json:"image_url,omitempty"`
	AudioURL   *AudioURL   `json:"audio_url,omitempty"`
	VideoURL   *VideoURL   `json:"video_url,omitempty"`
	InputAudio *InputAudio `json:"input_audio,omitempty"`
	FileURL    *FileURL    `json:"file_url,omitempty"`
}
