// 并发渠道修改压力测试
//
// 用途：验证以下三个并发场景是否安全：
//  1. Lost Update：N 个请求各自禁用不同 key，最终所有 key 应全部写入 DB
//  2. Idempotency：100 个请求同时禁用同一 key，不丢数据不 panic
//  3. Deadlock：HandleKeyError 与 UpdateChannelStatusById 并发，10s 内必须完成
//
// 运行方式（SQLite，无外部依赖）：
//
//	go run ./scripts/concurrent_test/
//
// 运行方式（MySQL）：
//
//	SQL_DSN="user:pass@tcp(127.0.0.1:3306)/oneapi" go run ./scripts/concurrent_test/

package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ─── 颜色输出 ──────────────────────────────────────────────────────────────────

const (
	green  = "\033[32m"
	red    = "\033[31m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	reset  = "\033[0m"
)

func pass(msg string) { fmt.Printf("  %s✓ PASS%s  %s\n", green, reset, msg) }
func fail(msg string) { fmt.Printf("  %sX FAIL%s  %s\n", red, reset, msg) }
func info(msg string) { fmt.Printf("  %s→%s      %s\n", cyan, reset, msg) }
func warn(msg string) { fmt.Printf("  %s!%s      %s\n", yellow, reset, msg) }

// ─── DB 初始化 ────────────────────────────────────────────────────────────────

func initDB() (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	dsn := os.Getenv("SQL_DSN")
	var db *gorm.DB
	var err error
	if dsn != "" {
		fmt.Printf("  Using MySQL: %s\n", dsn)
		common.UsingMySQL = true
		db, err = gorm.Open(mysql.Open(dsn), cfg)
	} else {
		fmt.Println("  Using SQLite (in-memory)")
		common.UsingSQLite = true
		db, err = gorm.Open(sqlite.Open("file:concurrent_test?mode=memory&cache=shared&_busy_timeout=5000"), cfg)
	}
	if err != nil {
		return nil, err
	}
	// 限制连接池，防止并发测试中连接被拒绝（connection reset by peer）。
	// 超出上限的 goroutine 会等待而非报错，保证 DB read 不因连接耗尽而失败。
	if sqlDB, e := db.DB(); e == nil {
		sqlDB.SetMaxOpenConns(50)
		sqlDB.SetMaxIdleConns(10)
	}
	return db, nil
}

// ─── 测试数据构造 ──────────────────────────────────────────────────────────────

func createChannel(db *gorm.DB, keyCount int) (*model.Channel, error) {
	keyStr := ""
	for i := 0; i < keyCount; i++ {
		if i > 0 {
			keyStr += "\n"
		}
		keyStr += fmt.Sprintf("sk-testkey-%04d", i)
	}
	w := uint(1)
	p := int64(0)
	ch := &model.Channel{
		Name:         fmt.Sprintf("test-multikey-%d", time.Now().UnixNano()),
		Type:         1,
		Key:          keyStr,
		Status:       common.ChannelStatusEnabled,
		Weight:       &w,
		Priority:     &p,
		Models:       "gpt-4",
		Group:        "default",
		AutoDisabled: true,
		MultiKeyInfo: model.MultiKeyInfo{
			IsMultiKey:       true,
			KeyCount:         keyCount,
			KeySelectionMode: model.KeySelectionPolling,
			KeyStatusList:    make(map[int]int),
			KeyMetadata:      make(map[int]model.KeyMetadata),
		},
	}
	if err := db.Create(ch).Error; err != nil {
		return nil, err
	}
	ability := model.Ability{
		Group:     "default",
		Model:     "gpt-4",
		ChannelId: ch.Id,
		Enabled:   true,
		Priority:  &p,
	}
	return ch, db.Create(&ability).Error
}

func resetChannel(db *gorm.DB, chId int) {
	db.Model(&model.Channel{}).Where("id = ?", chId).Updates(map[string]interface{}{
		"status": common.ChannelStatusEnabled,
		"multi_key_info": model.MultiKeyInfo{
			IsMultiKey:       true,
			KeyCount:         3,
			KeySelectionMode: model.KeySelectionPolling,
			KeyStatusList:    make(map[int]int),
			KeyMetadata:      make(map[int]model.KeyMetadata),
		},
	})
	db.Model(&model.Ability{}).Where("channel_id = ?", chId).Update("enabled", true)
}

// ─── 测试 1：Lost Update ───────────────────────────────────────────────────────

func testNoLostUpdate(db *gorm.DB) bool {
	fmt.Printf("\n%s[Test 1] Lost Update Prevention%s — 10 keys, 10 concurrent goroutines\n", cyan, reset)

	const keyCount = 10
	ch, err := createChannel(db, keyCount)
	if err != nil {
		fail(fmt.Sprintf("create channel: %v", err))
		return false
	}
	info(fmt.Sprintf("channel id=%d, keys=%d", ch.Id, keyCount))

	start := make(chan struct{})
	var wg sync.WaitGroup
	var errCount int64
	t0 := time.Now()

	for i := 0; i < keyCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := ch.HandleKeyError(i, fmt.Sprintf("error key %d", i), 401, "gpt-4"); err != nil {
				atomic.AddInt64(&errCount, 1)
				warn(fmt.Sprintf("goroutine %d error: %v", i, err))
			}
		}()
	}
	close(start)
	wg.Wait()
	elapsed := time.Since(t0)

	info(fmt.Sprintf("completed in %v, errors=%d", elapsed, errCount))

	// 读最终 DB 状态
	var final model.Channel
	db.First(&final, ch.Id)

	disabledCount := 0
	for i := 0; i < keyCount; i++ {
		if st, ok := final.MultiKeyInfo.KeyStatusList[i]; ok && st == common.ChannelStatusAutoDisabled {
			disabledCount++
		}
	}

	info(fmt.Sprintf("disabled keys in DB: %d / %d", disabledCount, keyCount))
	info(fmt.Sprintf("channel status: %d (want %d=AutoDisabled)", final.Status, common.ChannelStatusAutoDisabled))

	if disabledCount != keyCount {
		fail(fmt.Sprintf("LOST UPDATE: only %d/%d keys in DB", disabledCount, keyCount))
		return false
	}
	if final.Status != common.ChannelStatusAutoDisabled {
		fail(fmt.Sprintf("channel not auto-disabled (status=%d)", final.Status))
		return false
	}
	pass("all keys persisted, channel auto-disabled")
	return true
}

// ─── 测试 2：Idempotency ──────────────────────────────────────────────────────

func testIdempotency(db *gorm.DB) bool {
	fmt.Printf("\n%s[Test 2] Idempotency%s — 100 goroutines targeting same key[0]\n", cyan, reset)

	const concurrency = 100
	ch, err := createChannel(db, 5)
	if err != nil {
		fail(fmt.Sprintf("create channel: %v", err))
		return false
	}
	info(fmt.Sprintf("channel id=%d", ch.Id))

	start := make(chan struct{})
	var wg sync.WaitGroup
	var errCount, successCount int64
	t0 := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := ch.HandleKeyError(0, "quota exceeded", 429, "gpt-4"); err != nil {
				atomic.AddInt64(&errCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	elapsed := time.Since(t0)

	info(fmt.Sprintf("completed in %v, success=%d, errors=%d", elapsed, successCount, errCount))

	var final model.Channel
	db.First(&final, ch.Id)
	status := final.MultiKeyInfo.KeyStatusList[0]

	info(fmt.Sprintf("key[0] status in DB: %d (want %d=AutoDisabled)", status, common.ChannelStatusAutoDisabled))

	if errCount > 0 {
		fail(fmt.Sprintf("%d unexpected errors", errCount))
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		fail("key[0] not disabled after 100 concurrent calls")
		return false
	}
	pass("idempotent: no errors, key status correct")
	return true
}

// ─── 测试 3：Deadlock Detection ───────────────────────────────────────────────

func testDeadlock(db *gorm.DB) bool {
	fmt.Printf("\n%s[Test 3] Deadlock Detection%s — HandleKeyError vs UpdateChannelStatusById (50 rounds, 10s timeout)\n", cyan, reset)

	const rounds = 50
	ch, err := createChannel(db, 3)
	if err != nil {
		fail(fmt.Sprintf("create channel: %v", err))
		return false
	}
	info(fmt.Sprintf("channel id=%d, rounds=%d", ch.Id, rounds))

	done := make(chan struct{})
	var wg sync.WaitGroup
	var handleErrCount, updateErrCount int64
	t0 := time.Now()

	// 一组：HandleKeyError（禁用 key）
	for i := 0; i < rounds; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resetChannel(db, ch.Id)
			if err := ch.HandleKeyError(i%3, "deadlock test", 500, "gpt-4"); err != nil {
				atomic.AddInt64(&handleErrCount, 1)
			}
		}()
	}

	// 另一组：UpdateChannelStatusById（手动 enable / disable）
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := model.UpdateChannelStatusById(ch.Id, common.ChannelStatusEnabled); err != nil {
				atomic.AddInt64(&updateErrCount, 1)
			}
			if err := model.UpdateChannelStatusById(ch.Id, common.ChannelStatusManuallyDisabled); err != nil {
				atomic.AddInt64(&updateErrCount, 1)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(t0)
		info(fmt.Sprintf("completed in %v", elapsed))
		info(fmt.Sprintf("HandleKeyError errors=%d, UpdateStatus errors=%d (some acceptable)", handleErrCount, updateErrCount))
		pass("no deadlock detected")
		return true
	case <-time.After(10 * time.Second):
		fail("DEADLOCK DETECTED — goroutines did not finish within 10s")
		return false
	}
}

// ─── 测试 4：Polling Distribution ────────────────────────────────────────────

func testPollingDistribution(db *gorm.DB) bool {
	fmt.Printf("\n%s[Test 4] Polling Distribution%s — 4 keys, 400 concurrent requests\n", cyan, reset)

	const keyCount = 4
	const requests = 400
	ch, err := createChannel(db, keyCount)
	if err != nil {
		fail(fmt.Sprintf("create channel: %v", err))
		return false
	}
	info(fmt.Sprintf("channel id=%d, keys=%d, requests=%d", ch.Id, keyCount, requests))

	counts := make([]int64, keyCount)
	var wg sync.WaitGroup
	start := make(chan struct{})
	t0 := time.Now()

	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 每次从 DB 重读 channel，模拟真实请求行为
			var c model.Channel
			db.First(&c, ch.Id)
			_, idx, err := c.GetNextAvailableKey()
			if err == nil && idx >= 0 && idx < keyCount {
				atomic.AddInt64(&counts[idx], 1)
			}
		}()
	}

	close(start)
	wg.Wait()
	elapsed := time.Since(t0)
	info(fmt.Sprintf("completed in %v", elapsed))

	fmt.Println("  key distribution:")
	allOK := true
	for i, cnt := range counts {
		pct := float64(cnt) / float64(requests) * 100
		bar := ""
		for j := 0; j < int(pct/2); j++ {
			bar += "█"
		}
		marker := ""
		if pct < 15 || pct > 35 {
			marker = " ← SKEWED"
			allOK = false
		}
		fmt.Printf("    key[%d]: %4d hits (%5.1f%%)  %s%s\n", i, cnt, pct, bar, marker)
	}

	if allOK {
		pass("distribution within expected range [15%%, 35%%] per key")
	} else {
		fail("uneven distribution detected")
	}
	return allOK
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	fmt.Printf("\n%s=== One-API Channel Concurrency Test ===%s\n", cyan, reset)
	fmt.Printf("PID: %d  Time: %s\n\n", os.Getpid(), time.Now().Format("2006-01-02 15:04:05"))

	db, err := initDB()
	if err != nil {
		fmt.Printf("%sERROR: cannot open DB: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	// 建表
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		fmt.Printf("%sERROR: migrate: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	// 注入全局 DB（model 包直接使用 model.DB）
	model.DB = db

	results := []bool{
		testNoLostUpdate(db),
		testIdempotency(db),
		testDeadlock(db),
		testPollingDistribution(db),
	}

	passed := 0
	for _, ok := range results {
		if ok {
			passed++
		}
	}

	fmt.Printf("\n%s=== Summary: %d/%d passed ===%s\n\n", cyan, passed, len(results), reset)
	if passed < len(results) {
		os.Exit(1)
	}
}
