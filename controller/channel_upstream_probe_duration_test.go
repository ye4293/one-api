package controller

import (
	"testing"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

// TestDoProbeChannelModelRecordsDuration 钉死 doProbeChannelModel 必须记录耗时。
//
// 这里守的是一个 Go 语义陷阱：函数用 `defer func(){ res.Duration = ... }()`
// 记录耗时，但如果返回值不是**命名返回值**，`return res` 会先把 res 拷贝到
// 返回槽、之后才执行 defer —— defer 改的是已经无人使用的局部副本，
// Duration 永远是 0。
//
// Duration 落在探针日志里，是判断「是否需要调大
// UpstreamModelProbeTimeoutSeconds」的唯一依据；恒为 0 会让这个指标完全失效。
//
// 用 skipped 路径断言：它在 probeUnsupportedReason 处就 return，不发任何
// 网络请求（测试无外部依赖、无副作用），但同样经过那个 defer。
func TestDoProbeChannelModelRecordsDuration(t *testing.T) {
	ch := &model.Channel{
		Id:   1,
		Name: "probe-duration-test",
		Type: common.ChannelTypeOpenAI,
	}

	// 命中 unsupportedTestModelKeywords 的 "embed" → 走 skipped 分支，不发请求
	res := doProbeChannelModel(ch, "some-embedding-model")

	if res.Verdict != verdictSkipped {
		t.Fatalf("前置条件不成立：期望走 skipped 分支，实际 verdict=%s", res.Verdict)
	}
	if res.Duration <= 0 {
		t.Errorf("Duration = %v，必须 > 0。\n"+
			"defer 里的 res.Duration 赋值没有生效 —— 返回值不是命名返回值时，\n"+
			"return res 先拷贝、defer 后执行，改的是废弃的局部副本。\n"+
			"修法：把签名改成 (res probeResult)。", res.Duration)
	}
}
