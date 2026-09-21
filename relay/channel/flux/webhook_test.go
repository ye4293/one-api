package flux

import (
	"testing"

	"github.com/songquanpeng/one-api/common/config"
)

func TestDefaultWebhookURL(t *testing.T) {
	for _, tc := range []struct{ name, address, secret, want string }{
		{"未配置本站地址", "", "secret", ""},
		{"未配置密钥", "https://one-api.example/", "", "https://one-api.example/flux/internal/callback"},
		{"密钥转义", "https://one-api.example/", "test +&secret", "https://one-api.example/flux/internal/callback?key=test+%2B%26secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldAddress := config.ServerAddress
			config.ServerAddress = tc.address
			t.Cleanup(func() { config.ServerAddress = oldAddress })
			t.Setenv("FLUX_WEBHOOK_SECRET", tc.secret)
			if got := defaultWebhookURL(); got != tc.want {
				t.Fatalf("默认回调地址错误：got=%q want=%q", got, tc.want)
			}
		})
	}
}
