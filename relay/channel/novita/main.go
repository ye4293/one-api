package novita

import (
	"fmt"

	"github.com/songquanpeng/one-api/relay/constant"
	"github.com/songquanpeng/one-api/relay/util"
)

func GetRequestURL(meta *util.RelayMeta) (string, error) {
	if meta.Mode == constant.RelayModeChatCompletions {
		return fmt.Sprintf("%s/chat/completions", meta.BaseURL), nil
	}
	return "", fmt.Errorf("unsupported relay mode %d for novita", meta.Mode)
}
