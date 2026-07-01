package audit

import (
	"strings"
	"testing"
)

func TestTruncateUnderLimit(t *testing.T) {
	s, truncated := truncate("hello", 10*1024)
	if truncated {
		t.Errorf("未超限不应截断")
	}
	if s != "hello" {
		t.Errorf("内容应原样返回")
	}
}

func TestTruncateOverLimit(t *testing.T) {
	big := strings.Repeat("a", 2048)
	s, truncated := truncate(big, 1) // 1KB 上限
	if !truncated {
		t.Errorf("超限应标记截断")
	}
	if len(s) > 1024 {
		t.Errorf("截断后长度应 <= 1024, got %d", len(s))
	}
}
