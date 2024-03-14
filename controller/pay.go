package controller

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

type PayRequestFront struct {
	Id     int     `json:"id"`
	Chain  string  `json:"chain"`
	Token  string  `json:"token"`
	Amount float64 `json:"amount"`
}

type PayRequestSend struct {
	Ticker        string `json:"ticker"`
	Callback      string `json:"callback"`
	Address       string `json:"address"`
	Pending       int    `json:"pending"`
	Confirmations int    `json:"confirmations"`
	Post          int    `json:"post"`
	Priority      string `json:"priority"`
	MultiToken    int    `json:"multi_token"`
	Convert       int    `json:"convert"`
}

type PaymentCallbackPending struct {
	Uuid          string  `json:"uuid"`
	AddressIn     string  `json:"address_in"`
	AddressOut    string  `json:"address_out"`
	TxidIn        string  `json:"txid_in"`
	Confirmations int     `json:"confirmations"`
	ValueCoin     float64 `json:"value_coin"`
	Coin          string  `json:"coin"`
	Pending       int     `json:"pending"`
}

type PaymentCallbackConfirmation struct {
	Uuid               string  `json:"uuid"`
	AddressIn          string  `json:"address_in"`
	AddressOut         string  `json:"address_out"`
	TxidIn             string  `json:"txid_in"`
	Confirmations      int     `json:"confirmations"`
	ValueCoin          float64 `json:"value_coin"`
	ValueForwardedCoin float64 `json:"value_forwarded_coin"`
	FeeCoin            float64 `json:"fee_coin"`
	Coin               string  `json:"coin"`
	Pending            int     `json:"pending"`
}

func GetPayRequest(c *gin.Context) {

	var payrequestfront PayRequestFront
	err := json.NewDecoder(c.Request.Body).Decode(&payrequestfront)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": "failed to get json",
			"success": false,
		})
		return
	}
	userId := c.GetInt("id")
	var payrequestsend PayRequestSend
	payrequestsend.Ticker = payrequestfront.Chain + "/usdt"
	payrequestsend.Address = "0x936f34289406ACA7F7ebC63AeF1cF16286559b1a"
	payrequestsend.Pending = 1
	payrequestsend.Priority = "default"
	payrequestsend.Post = 1
	payrequestsend.Confirmations = 1
	payrequestsend.Convert = 0
	payrequestsend.MultiToken = 1
	payrequestsend.Callback = GenerateCallbackUrl(userId)

	err = SendPayRequest(payrequestsend)
	if err != nil {
		return
	}
}

func SendPayRequest(payrequestsend PayRequestSend) error {
	// 构建查询参数
	params := url.Values{}
	params.Add("address", payrequestsend.Address)
	params.Add("pending", strconv.Itoa(payrequestsend.Pending))
	params.Add("priority", payrequestsend.Priority)
	params.Add("post", strconv.Itoa(payrequestsend.Post))
	params.Add("confirmations", strconv.Itoa(payrequestsend.Confirmations))
	params.Add("convert", strconv.Itoa(payrequestsend.Convert))
	params.Add("multitoken", strconv.Itoa(payrequestsend.MultiToken))
	params.Add("callback", payrequestsend.Callback)

	// 构建完整的请求URL，将ticker嵌入到路径中
	requestUrl := "https://api.cryptapi.io/" + url.PathEscape(payrequestsend.Ticker) + "/create/?" + params.Encode()

	// 发送GET请求
	response, err := http.Get(requestUrl)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// 这里你可以根据需要处理response
	// 例如检查状态码、读取响应体等

	return nil
}

func GenerateCallbackUrl(userId int) string {
	currentTimestamp := time.Now().Unix()
	userIdStr := strconv.Itoa(userId)
	timestampStr := strconv.FormatInt(currentTimestamp, 10)
	CallbackUrl := "https://api.cryptapi.io/?userid=" + userIdStr + "&timestamp=" + timestampStr
	return CallbackUrl
}
func getAdress() {

}
func GetQrcode() {
	userId := c.GetInt("id")
	order:=Model.CreateOrder()
	if err = Mode.CreateOrder(userId)
	//创建一笔订单
	//构建请求参数
	//获取结果
	//返回结果
}
