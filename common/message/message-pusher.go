package message

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/songquanpeng/one-api/common/config"
)

type request struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
	URL         string `json:"url"`
	Channel     string `json:"channel"`
	Token       string `json:"token"`
}

type response struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func SendMessage(title string, description string, content string) error {
	if config.MessagePusherAddress == "" {
		return errors.New("message pusher address is not set")
	}
	// 在标题前加入系统名称标识，方便区分不同站点
	titleWithSystem := title
	if config.SystemName != "" {
		titleWithSystem = fmt.Sprintf("[%s] %s", config.SystemName, title)
	}
	req := request{
		Title:       titleWithSystem,
		Description: description,
		Content:     content,
		Token:       config.MessagePusherToken,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := http.Post(config.MessagePusherAddress,
		"application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res response
	err = json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		return err
	}
	if !res.Success {
		return errors.New(res.Message)
	}
	return nil
}
