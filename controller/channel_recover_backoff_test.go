package controller

import (
	"testing"
	"time"
)

// 这些测试操作包级全局 recoverBackoff 表；Go 同包测试默认串行执行，
// 每个用例前后用 recoverBackoffPrune(空集) 清空，避免相互污染。

func TestRecoverBackoff_FailIncrementsAndBlocks(t *testing.T) {
	recoverBackoffPrune(map[string]struct{}{})
	t.Cleanup(func() { recoverBackoffPrune(map[string]struct{}{}) })

	now := time.Now()

	// 初始未记录 → 不阻塞
	if recoverBackoffBlocked(1, "gpt-5.5", now) {
		t.Fatal("初始不应处于退避")
	}

	// 第一次失败 → 退避首挡 5min
	recoverBackoffOnFail(1, "gpt-5.5", now)
	if !recoverBackoffBlocked(1, "gpt-5.5", now.Add(4*time.Minute)) {
		t.Fatal("首次失败后 4min 内应仍处于退避")
	}
	if recoverBackoffBlocked(1, "gpt-5.5", now.Add(6*time.Minute)) {
		t.Fatal("首次失败后 6min 应已过退避（首挡 5min）")
	}

	// 第二次失败 → 推到 15min 挡
	base := now.Add(6 * time.Minute)
	recoverBackoffOnFail(1, "gpt-5.5", base)
	if !recoverBackoffBlocked(1, "gpt-5.5", base.Add(14*time.Minute)) {
		t.Fatal("第二次失败应退避 15min")
	}
	if recoverBackoffBlocked(1, "gpt-5.5", base.Add(16*time.Minute)) {
		t.Fatal("第二次失败后 16min 应已过退避")
	}
}

func TestRecoverBackoff_SuccessClears(t *testing.T) {
	recoverBackoffPrune(map[string]struct{}{})
	t.Cleanup(func() { recoverBackoffPrune(map[string]struct{}{}) })

	now := time.Now()
	recoverBackoffOnFail(2, "m", now)
	if !recoverBackoffBlocked(2, "m", now) {
		t.Fatal("失败后应处于退避")
	}
	recoverBackoffOnSuccess(2, "m")
	if recoverBackoffBlocked(2, "m", now) {
		t.Fatal("成功后应清除退避记录")
	}
}

func TestRecoverBackoff_PruneRemovesStale(t *testing.T) {
	recoverBackoffPrune(map[string]struct{}{})
	t.Cleanup(func() { recoverBackoffPrune(map[string]struct{}{}) })

	now := time.Now()
	recoverBackoffOnFail(3, "keep", now)
	recoverBackoffOnFail(3, "drop", now)

	// 只保留 keep（模拟 drop 已不在本轮候选集合，如渠道被删/已恢复）
	live := map[string]struct{}{recoverBackoffKey(3, "keep"): {}}
	recoverBackoffPrune(live)

	if !recoverBackoffBlocked(3, "keep", now) {
		t.Fatal("keep 应保留退避")
	}
	if recoverBackoffBlocked(3, "drop", now) {
		t.Fatal("drop 应被清理")
	}
}
