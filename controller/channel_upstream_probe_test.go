package controller

import (
	"context"
	"errors"
	"slices"
	"testing"

	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

func TestClassifyProbeError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		apiErr     *relaymodel.Error
		netErr     error
		isMultiKey bool
		want       probeVerdict
	}{
		// ── 明确的「模型不存在」信号 ──
		{
			name:       "404 带可解析错误消息",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "The model does not exist"},
			want:       verdictNotFound,
		},
		{
			name:       "code=model_not_found",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: "model_not_found"},
			want:       verdictNotFound,
		},
		{
			name:       "type=invalid_model",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Type: "invalid_model"},
			want:       verdictNotFound,
		},
		{
			name:       "code 大小写混合仍命中",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: "Model_Not_Found"},
			want:       verdictNotFound,
		},
		{
			name:       "中文「模型不存在」",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "该模型不存在，请检查模型名称"},
			want:       verdictNotFound,
		},
		{
			name:       "消息大小写不敏感",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "UNKNOWN MODEL: foo"},
			want:       verdictNotFound,
		},
		{
			name:       "OpenAI 合并消息（不存在或无权限）按不存在处理",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "The model 'gpt-9' does not exist or you do not have access to it"},
			want:       verdictNotFound,
		},

		// ── 约束 B：裸 404 不能判 not_found（base_url 配错会全量误删）──
		{
			name:       "404 但无可解析错误体（空 body）",
			statusCode: 404,
			apiErr:     nil,
			want:       verdictInconclusive,
		},
		{
			name:       "404 但返回 HTML 错误页（解析失败）",
			statusCode: 404,
			apiErr:     nil,
			want:       verdictInconclusive,
		},
		{
			name:       "404 且错误体存在但消息为空",
			statusCode: 404,
			apiErr:     &relaymodel.Error{},
			want:       verdictInconclusive,
		},
		{
			name:       "404 且消息只有空白字符",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "   "},
			want:       verdictInconclusive,
		},

		// ── 关键词白名单的边界：这两条误命中会导致误删 ──
		{
			name:       "仅无权限不判 not_found",
			statusCode: 403,
			apiErr:     &relaymodel.Error{Message: "You do not have access to this model"},
			want:       verdictInconclusive,
		},
		{
			name:       "上下文超长不判 not_found",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Message: "This model's maximum context length is 4096 tokens"},
			want:       verdictInconclusive,
		},

		// ── 数字类 code 不构成明确信号 ──
		{
			name:       "code 为 float64(404)",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: float64(404)},
			want:       verdictInconclusive,
		},
		{
			name:       "code 为 int",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: 404},
			want:       verdictInconclusive,
		},
		{
			name:       "code 为 nil",
			statusCode: 500,
			apiErr:     &relaymodel.Error{Code: nil},
			want:       verdictInconclusive,
		},

		// ── 各类「与模型存在性无关」的失败 ──
		{
			name:       "401 鉴权失败",
			statusCode: 401,
			apiErr:     &relaymodel.Error{Type: "authentication_error", Message: "Incorrect API key"},
			want:       verdictInconclusive,
		},
		{
			name:       "403 无权限",
			statusCode: 403,
			apiErr:     &relaymodel.Error{Message: "权限不足"},
			want:       verdictInconclusive,
		},
		{
			name:       "429 限流",
			statusCode: 429,
			apiErr:     &relaymodel.Error{Type: "rate_limit_error", Message: "Rate limit reached"},
			want:       verdictInconclusive,
		},
		{
			name:       "402 余额不足",
			statusCode: 402,
			apiErr:     &relaymodel.Error{Type: "insufficient_quota"},
			want:       verdictInconclusive,
		},
		{
			name:       "500 上游内部错误",
			statusCode: 500,
			apiErr:     &relaymodel.Error{Message: "Internal server error"},
			want:       verdictInconclusive,
		},
		{
			name:       "502 网关错误无错误体",
			statusCode: 502,
			apiErr:     nil,
			want:       verdictInconclusive,
		},
		{
			name:       "内容审核拦截",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Type: "content_filter", Message: "内容违规，已拦截"},
			want:       verdictInconclusive,
		},
		{
			name:       "网络超时",
			statusCode: 0,
			apiErr:     nil,
			netErr:     context.DeadlineExceeded,
			want:       verdictInconclusive,
		},
		{
			name:       "网络错误优先于错误体判定",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "model does not exist"},
			netErr:     errors.New("connection reset by peer"),
			want:       verdictInconclusive,
		},

		// ── 决策 2：多 key 渠道降级 ──
		{
			name:       "多 key 渠道的 404 降级为无结论",
			statusCode: 404,
			apiErr:     &relaymodel.Error{Message: "The model does not exist"},
			isMultiKey: true,
			want:       verdictInconclusive,
		},
		{
			name:       "多 key 渠道的 model_not_found 降级为无结论",
			statusCode: 400,
			apiErr:     &relaymodel.Error{Code: "model_not_found"},
			isMultiKey: true,
			want:       verdictInconclusive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyProbeError(tt.statusCode, tt.apiErr, tt.netErr, tt.isMultiKey)
			if got != tt.want {
				t.Errorf("classifyProbeError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsModelNotFoundMessage(t *testing.T) {
	hits := []string{
		"model not found",
		"Model Not Found",
		"error: model_not_found",
		"The model 'x' does not exist",
		"no such model: foo",
		"Unknown model",
		"invalid model name",
		"unsupported model",
		"this model is not supported by the endpoint",
		"模型不存在",
		"不支持该模型",
		"未找到模型",
		"无此模型",
	}
	for _, msg := range hits {
		t.Run("命中/"+msg, func(t *testing.T) {
			if !isModelNotFoundMessage(msg) {
				t.Errorf("isModelNotFoundMessage(%q) = false, want true", msg)
			}
		})
	}

	misses := []string{
		"",
		"   ",
		"You do not have access to this model",
		"This model's maximum context length is 4096 tokens",
		"Rate limit reached for model gpt-4",
		"Incorrect API key provided",
		"insufficient_quota",
		"内容违规，已拦截",
		"upstream timeout",
		"the model is overloaded, please retry",
	}
	for _, msg := range misses {
		t.Run("不命中/"+msg, func(t *testing.T) {
			if isModelNotFoundMessage(msg) {
				t.Errorf("isModelNotFoundMessage(%q) = true, want false", msg)
			}
		})
	}
}

func TestNormalizeErrCode(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"字符串小写化", "Model_Not_Found", "model_not_found"},
		{"字符串去空白", "  model_not_found  ", "model_not_found"},
		{"空字符串", "", ""},
		{"float64", float64(404), "404"},
		{"int", 404, "404"},
		{"bool", true, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeErrCode(tt.in); got != tt.want {
				t.Errorf("normalizeErrCode(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFilterByProbeVerdicts(t *testing.T) {
	// 处置表的全部 8 格
	cells := []struct {
		verdict     probeVerdict
		scene       string
		wantApprove bool
	}{
		{verdictAlive, probeScenePendingAdd, true},
		{verdictNotFound, probeScenePendingAdd, false},
		{verdictInconclusive, probeScenePendingAdd, false},
		{verdictSkipped, probeScenePendingAdd, true},

		{verdictAlive, probeScenePendingRemove, false},
		{verdictNotFound, probeScenePendingRemove, true},
		{verdictInconclusive, probeScenePendingRemove, false},
		{verdictSkipped, probeScenePendingRemove, true},
	}
	for _, c := range cells {
		t.Run(string(c.verdict)+"/"+c.scene, func(t *testing.T) {
			approved, held := filterByProbeVerdicts(
				[]string{"m"}, map[string]probeVerdict{"m": c.verdict}, c.scene,
			)
			if c.wantApprove {
				if !slices.Equal(approved, []string{"m"}) || len(held) != 0 {
					t.Errorf("approved=%v held=%v, want approved=[m] held=[]", approved, held)
				}
			} else {
				if len(approved) != 0 || !slices.Equal(held, []string{"m"}) {
					t.Errorf("approved=%v held=%v, want approved=[] held=[m]", approved, held)
				}
			}
		})
	}

	t.Run("verdicts 中缺失的模型按 inconclusive 暂缓", func(t *testing.T) {
		for _, scene := range []string{probeScenePendingAdd, probeScenePendingRemove} {
			approved, held := filterByProbeVerdicts(
				[]string{"known", "missing"},
				map[string]probeVerdict{"known": verdictSkipped},
				scene,
			)
			if !slices.Equal(approved, []string{"known"}) {
				t.Errorf("scene=%s approved=%v, want [known]", scene, approved)
			}
			if !slices.Equal(held, []string{"missing"}) {
				t.Errorf("scene=%s held=%v, want [missing]", scene, held)
			}
		}
	})

	t.Run("未知 scene 一律暂缓非 skipped 项", func(t *testing.T) {
		approved, held := filterByProbeVerdicts(
			[]string{"a", "b"},
			map[string]probeVerdict{"a": verdictAlive, "b": verdictNotFound},
			"bogus_scene",
		)
		if len(approved) != 0 {
			t.Errorf("approved=%v, want []", approved)
		}
		if !slices.Equal(held, []string{"a", "b"}) {
			t.Errorf("held=%v, want [a b]", held)
		}
	})

	t.Run("空输入返回空切片而非 nil", func(t *testing.T) {
		approved, held := filterByProbeVerdicts(nil, nil, probeScenePendingAdd)
		if approved == nil || held == nil {
			t.Errorf("approved=%v held=%v, want non-nil empty slices", approved, held)
		}
		if len(approved) != 0 || len(held) != 0 {
			t.Errorf("approved=%v held=%v, want empty", approved, held)
		}
	})

	t.Run("保留输入顺序", func(t *testing.T) {
		models := []string{"z", "a", "m"}
		verdicts := map[string]probeVerdict{"z": verdictAlive, "a": verdictAlive, "m": verdictAlive}
		approved, _ := filterByProbeVerdicts(models, verdicts, probeScenePendingAdd)
		if !slices.Equal(approved, models) {
			t.Errorf("approved=%v, want %v（须保留输入顺序）", approved, models)
		}
	})
}
