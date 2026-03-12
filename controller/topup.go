package controller

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"
)

type EpayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	ReturnURL     string `json:"return_url"`
}

type AmountRequest struct {
	Amount int64 `json:"amount"`
}

func GetEpayClient() *epay.Client {
	if config.EpayPayAddress == "" || config.EpayId == "" || config.EpayKey == "" {
		return nil
	}
	withUrl, err := epay.NewClient(&epay.Config{
		PartnerID: config.EpayId,
		Key:       config.EpayKey,
	}, config.EpayPayAddress)
	if err != nil {
		return nil
	}
	return withUrl
}

func getEpayAvailability() (bool, string) {
	if !config.EpayPaymentEnabled {
		return false, "管理员未开启易支付"
	}
	if config.EpayPayAddress == "" {
		return false, "当前管理员未配置支付地址"
	}
	if config.EpayId == "" {
		return false, "当前管理员未配置易支付 PID"
	}
	if config.EpayKey == "" {
		return false, "当前管理员未配置易支付密钥"
	}
	return true, ""
}

func getPayMoney(amount int64) float64 {
	return float64(amount) * config.EpayPrice
}

func GetEpayTopUpInfo(c *gin.Context) {
	enabled, reason := getEpayAvailability()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"enable_online_topup": enabled,
			"enable_reason":       reason,
			"min_topup":           config.EpayMinTopUp,
			"price":               config.EpayPrice,
			"quota_per_unit":      config.QuotaPerUnit,
		},
	})
}

func RequestEpay(c *gin.Context) {
	var req EpayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	enabled, reason := getEpayAvailability()
	if !enabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": reason})
		return
	}
	if req.Amount < int64(config.EpayMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("充值数量不能小于 %d", config.EpayMinTopUp)})
		return
	}

	id := c.GetInt("id")
	payMoney := getPayMoney(req.Amount)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额过低"})
		return
	}

	if req.PaymentMethod == "" {
		req.PaymentMethod = "alipay"
	}

	callBackAddress := config.EpayCallbackAddress
	if callBackAddress == "" {
		callBackAddress = config.ServerAddress
	}
	returnURLValue := config.ServerAddress + "/topup"
	if req.ReturnURL != "" {
		parsed, parseErr := url.Parse(req.ReturnURL)
		if parseErr == nil && parsed.Host != "" {
			serverParsed, _ := url.Parse(config.ServerAddress)
			if serverParsed != nil && parsed.Host == serverParsed.Host {
				returnURLValue = req.ReturnURL
			}
		}
	}
	returnUrl, _ := url.Parse(returnURLValue)
	notifyUrl, _ := url.Parse(callBackAddress + "/api/user/epay/notify")
	tradeNo := fmt.Sprintf("USR%dNO%s%d", id, helper.GetRandomString(6), time.Now().Unix())

	client := GetEpayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "当前管理员未配置支付信息"})
		return
	}

	uri, params, err := client.Purchase(&epay.PurchaseArgs{
		Type:           req.PaymentMethod,
		ServiceTradeNo: tradeNo,
		Name:           fmt.Sprintf("充值%d额度", req.Amount),
		Money:          strconv.FormatFloat(payMoney, 'f', 2, 64),
		Device:         epay.PC,
		NotifyUrl:      notifyUrl,
		ReturnUrl:      returnUrl,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "拉起支付失败"})
		return
	}

	topUp := &model.TopUp{
		UserId:        id,
		Amount:        req.Amount,
		Money:         payMoney,
		TradeNo:       tradeNo,
		PaymentMethod: req.PaymentMethod,
		CreateTime:    time.Now().Unix(),
		Status:        "pending",
	}
	err = topUp.Insert()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建订单失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": params, "url": uri})
}

func RequestAmount(c *gin.Context) {
	var req AmountRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	enabled, reason := getEpayAvailability()
	if !enabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": reason})
		return
	}
	if req.Amount < int64(config.EpayMinTopUp) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("充值数量不能小于 %d", config.EpayMinTopUp)})
		return
	}
	payMoney := getPayMoney(req.Amount)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

var orderLocks sync.Map
var createLock sync.Mutex

func LockOrder(tradeNo string) {
	lock, ok := orderLocks.Load(tradeNo)
	if !ok {
		createLock.Lock()
		defer createLock.Unlock()
		lock, ok = orderLocks.Load(tradeNo)
		if !ok {
			lock = new(sync.Mutex)
			orderLocks.Store(tradeNo, lock)
		}
	}
	lock.(*sync.Mutex).Lock()
}

func UnlockOrder(tradeNo string) {
	lock, ok := orderLocks.Load(tradeNo)
	if ok {
		lock.(*sync.Mutex).Unlock()
		orderLocks.Delete(tradeNo)
	}
}

func EpayNotify(c *gin.Context) {
	var params map[string]string

	if c.Request.Method == "POST" {
		if err := c.Request.ParseForm(); err != nil {
			log.Println("易支付回调POST解析失败:", err)
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = lo.Reduce(lo.Keys(c.Request.PostForm), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.PostForm.Get(t)
			return r
		}, map[string]string{})
	} else {
		params = lo.Reduce(lo.Keys(c.Request.URL.Query()), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.URL.Query().Get(t)
			return r
		}, map[string]string{})
	}

	if len(params) == 0 {
		log.Println("易支付回调参数为空")
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	client := GetEpayClient()
	if client == nil {
		log.Println("易支付回调失败 未找到配置信息")
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	verifyInfo, err := client.Verify(params)
	if err != nil || !verifyInfo.VerifyStatus {
		log.Println("易支付回调签名验证失败")
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	if verifyInfo.TradeStatus != epay.StatusTradeSuccess {
		log.Printf("易支付异常回调: %v", verifyInfo)
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	LockOrder(verifyInfo.ServiceTradeNo)
	defer UnlockOrder(verifyInfo.ServiceTradeNo)

	err = model.CompleteTopUpOrder(verifyInfo.ServiceTradeNo)
	if err != nil {
		log.Printf("易支付回调处理订单失败: %v", err)
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	log.Printf("易支付回调处理成功: tradeNo=%s", verifyInfo.ServiceTradeNo)
	_, _ = c.Writer.Write([]byte("success"))
}

func GetUserTopUps(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("pagesize"))
	if pageSize <= 0 {
		pageSize = 10
	}
	userId := c.GetInt("id")
	topups, total, err := model.GetUserTopUps(userId, page, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        topups,
			"currentPage": page,
			"pageSize":    pageSize,
			"total":       total,
		},
	})
}

func GetAllTopUps(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("pagesize"))
	if pageSize <= 0 {
		pageSize = 10
	}
	topups, total, err := model.GetAllTopUps(page, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"list":        topups,
			"currentPage": page,
			"pageSize":    pageSize,
			"total":       total,
		},
	})
}
