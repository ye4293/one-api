package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

var _ = math.Abs

// 验证 member 编码 → 解码的往返正确性。
func TestParseMetricMember(t *testing.T) {
	cases := []struct {
		name     string
		member   string
		succ     bool
		dur      float64
		fw       float64
		isStream bool
		ok       bool
	}{
		{"成功流式", "1:12.500000:0.250000:1:42", true, 12.5, 0.25, true, true},
		{"成功非流式", "1:2.300000:0.000000:0:7", true, 2.3, 0, false, true},
		{"失败", "0:0.800000:0.000000:0:9", false, 0.8, 0, false, true},
		{"字段不足", "1:2.3:1", false, 0, 0, false, false},
		{"格式错", "1:abc:0.1:0:1", false, 0, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			succ, dur, fw, isStream, ok := parseMetricMember(c.member)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if succ != c.succ || dur != c.dur || fw != c.fw || isStream != c.isStream {
				t.Fatalf("got succ=%v dur=%v fw=%v stream=%v, want succ=%v dur=%v fw=%v stream=%v",
					succ, dur, fw, isStream, c.succ, c.dur, c.fw, c.isStream)
			}
		})
	}
}

// 聚合：成功/失败计数 + 流式/非流式延迟分别累计 + 失败样本不污染延迟。
func TestAggregateSamples(t *testing.T) {
	members := []string{
		"1:12.000000:0.200000:1:1", // 成功流式 dur=12 fw=0.2
		"1:10.000000:0.400000:1:2", // 成功流式 dur=10 fw=0.4
		"1:2.000000:0.000000:0:3",  // 成功非流式 dur=2
		"1:4.000000:0.000000:0:4",  // 成功非流式 dur=4
		"0:0.500000:0.000000:0:5",  // 失败 dur=0.5 —— 不应进延迟
		"0:1.000000:0.000000:0:6",  // 失败
		"garbage",                  // 解析失败，跳过
	}
	s := aggregateSamples(members)

	if s.SuccessCount != 4 {
		t.Fatalf("SuccessCount = %d, want 4", s.SuccessCount)
	}
	if s.FailureCount != 2 {
		t.Fatalf("FailureCount = %d, want 2", s.FailureCount)
	}
	// 流式：2 个成功样本，fw 之和 0.2+0.4=0.6
	if s.StreamCount != 2 {
		t.Fatalf("StreamCount = %d, want 2", s.StreamCount)
	}
	if absf(s.StreamFirstWordSum-0.6) > 1e-9 {
		t.Fatalf("StreamFirstWordSum = %v, want 0.6", s.StreamFirstWordSum)
	}
	// 非流式：2 个成功样本，dur 之和 2+4=6
	if s.NonStreamCount != 2 {
		t.Fatalf("NonStreamCount = %d, want 2", s.NonStreamCount)
	}
	if s.NonStreamDurSum != 6 {
		t.Fatalf("NonStreamDurSum = %v, want 6", s.NonStreamDurSum)
	}
}

// 验证平均延迟计算：StreamFirstWordSum/StreamCount 得到平均首字延迟。
// 对接 buildStatsForModel 的换算：avg_ms = sum/count * 1000。
func TestAggregateSamples_AverageLatency(t *testing.T) {
	members := []string{
		"1:0.000000:0.100000:1:1", // 流式 fw=0.1s
		"1:0.000000:0.300000:1:2", // 流式 fw=0.3s
	}
	s := aggregateSamples(members)
	avgFirstWordMs := s.StreamFirstWordSum / float64(s.StreamCount) * 1000
	want := 200.0 // (0.1+0.3)/2 *1000
	if avgFirstWordMs != want {
		t.Fatalf("avgFirstWordMs = %v, want %v", avgFirstWordMs, want)
	}
}

// 纯失败窗口：延迟维度应为 0，不产生延迟信号。
func TestAggregateSamples_AllFailures(t *testing.T) {
	members := []string{
		"0:1.000000:0.000000:0:1",
		"0:2.000000:0.000000:0:2",
	}
	s := aggregateSamples(members)
	if s.SuccessCount != 0 || s.FailureCount != 2 {
		t.Fatalf("counts wrong: succ=%d fail=%d", s.SuccessCount, s.FailureCount)
	}
	if s.StreamCount != 0 || s.NonStreamCount != 0 {
		t.Fatalf("全失败窗口不应有延迟样本: stream=%d nonstream=%d", s.StreamCount, s.NonStreamCount)
	}
}

func TestAbilityMetricKeyTTL(t *testing.T) {
	oldWindow := config.DynamicPriorityWindowMinutes
	t.Cleanup(func() {
		config.DynamicPriorityWindowMinutes = oldWindow
	})

	config.DynamicPriorityWindowMinutes = 10
	if got := abilityMetricKeyTTL(); got != 30*time.Minute {
		t.Fatalf("default ttl = %s, want 30m", got)
	}

	config.DynamicPriorityWindowMinutes = 60
	if got := abilityMetricKeyTTL(); got != 70*time.Minute {
		t.Fatalf("long-window ttl = %s, want 70m", got)
	}

	config.DynamicPriorityWindowMinutes = 0
	if got := abilityMetricKeyTTL(); got != 30*time.Minute {
		t.Fatalf("fallback ttl = %s, want 30m", got)
	}
}
