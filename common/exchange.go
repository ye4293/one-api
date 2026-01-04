package common

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// ExchangeRateResponse API响应结构
type ExchangeRateResponse struct {
	Result          string             `json:"result"`
	Provider        string             `json:"provider"`
	Documentation   string             `json:"documentation"`
	TermsOfUse      string             `json:"terms_of_use"`
	TimeLastUpdated int64              `json:"time_last_update_unix"`
	TimeNextUpdate  int64              `json:"time_next_update_unix"`
	Base            string             `json:"base"`
	ConversionRates map[string]float64 `json:"rates"`
}

// ExchangeRateManager 汇率管理器
type ExchangeRateManager struct {
	cnyToUSDRate  float64
	lastUpdate    time.Time
	cacheDuration time.Duration
	mutex         sync.RWMutex
}

// 全局汇率管理器实例
var GlobalExchangeManager = &ExchangeRateManager{
	cacheDuration: 10 * time.Minute, // 缓存10分钟
}

// 默认汇率（当API失败时使用）
const DefaultCNYToUSDRate = 0.14 // 约 1/7.2

// fetchRateFromExchangeRateAPI 从ExchangeRate-API获取汇率
func fetchRateFromExchangeRateAPI() (float64, error) {
	url := "https://api.exchangerate-api.com/v4/latest/CNY"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from ExchangeRate-API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ExchangeRate-API returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %v", err)
	}

	var exchangeRate ExchangeRateResponse
	if err := json.Unmarshal(body, &exchangeRate); err != nil {
		return 0, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	usdRate, exists := exchangeRate.ConversionRates["USD"]
	if !exists {
		return 0, fmt.Errorf("USD rate not found in response")
	}

	return usdRate, nil
}

// GetCNYToUSDRate 获取人民币对美元汇率（带缓存）
func (e *ExchangeRateManager) GetCNYToUSDRate() (float64, error) {
	e.mutex.RLock()
	// 检查缓存是否有效
	if time.Since(e.lastUpdate) < e.cacheDuration && e.cnyToUSDRate > 0 {
		rate := e.cnyToUSDRate
		e.mutex.RUnlock()
		return rate, nil
	}
	e.mutex.RUnlock()

	// 需要更新汇率
	e.mutex.Lock()
	defer e.mutex.Unlock()

	// 双重检查，避免并发时重复请求
	if time.Since(e.lastUpdate) < e.cacheDuration && e.cnyToUSDRate > 0 {
		return e.cnyToUSDRate, nil
	}

	log.Printf("Fetching new exchange rate...")

	// 尝试获取汇率
	rate, err := fetchRateFromExchangeRateAPI()
	if err != nil {
		log.Printf("ExchangeRate-API failed: %v, using fallback rate", err)
		rate = DefaultCNYToUSDRate
	}

	// 更新缓存
	e.cnyToUSDRate = rate
	e.lastUpdate = time.Now()

	log.Printf("Updated exchange rate: %.6f CNY to USD", rate)
	return rate, nil
}

// ConvertCNYToUSD 将人民币转换为美元
func ConvertCNYToUSD(cnyAmount float64) (float64, error) {
	rate, err := GlobalExchangeManager.GetCNYToUSDRate()
	if err != nil {
		// 即使获取失败，也使用默认汇率
		rate = DefaultCNYToUSDRate
	}

	usdAmount := cnyAmount * rate
	return usdAmount, nil
}

// RefreshExchangeRate 手动刷新汇率
func RefreshExchangeRate() error {
	GlobalExchangeManager.mutex.Lock()
	GlobalExchangeManager.lastUpdate = time.Time{} // 重置缓存时间
	GlobalExchangeManager.mutex.Unlock()

	_, err := GlobalExchangeManager.GetCNYToUSDRate()
	return err
}
