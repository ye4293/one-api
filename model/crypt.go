package model

import (
	// "errors"
	// "fmt"
	// "strings"

	"encoding/json"
	"fmt"

	// "github.com/songquanpeng/one-api/common"
	// "github.com/songquanpeng/one-api/common/blacklist"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/model"

	// "github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	// "gorm.io/gorm"
	"io/ioutil"
	"net/http"
)

var pubkey = "-----BEGIN PUBLIC KEY-----\nMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC3FT0Ym8b3myVxhQW7ESuuu6lo\ndGAsUJs4fq+Ey//jm27jQ7HHHDmP1YJO7XE7Jf/0DTEJgcw4EZhJFVwsk6d3+4fy\nBsn0tKeyGMiaE6cVkX0cy6Y85o8zgc/CwZKc0uw6d5siAo++xl2zl+RGMXCELQVE\nox7pp208zTvown577wIDAQAB\n-----END PUBLIC KEY-----"
var cryptHost = "https://api.cryptapi.io/"

// User if you add sensitive fields, don't forget to clean them in setupLogin function.
// Otherwise, the sensitive information will be saved on local storage in plain text!
type CreateResponse struct {
	AddressIn   string `json:"address_in"`
	AddressOut  string `json:"address_out"`
	CallbackUrl string `json:"callback_url"`
	Priority    string `json:"priority"`
	Status      string `json:"status"` // admin, util
}
type QrcodeResponse struct {
	QrCode     string `json:"qr_code"`
	PaymentUri string `json:"payment_uri"`
	Status     string `json:"status"` // admin, util
}
type CryptCallbackResponse struct {
	UserId             int     `json:"user_id"`
	OrderId            string  `json:"order_id"`
	Uuid               string  `json:"uuid"`
	AddressIn          string  `json:"address_in"`
	AddressOut         string  `json:"address_out"`
	TxidIn             string  `json:"txid_in"`
	Confirmations      int     `json:"confirmations"`
	Value              int     `json:"value"`
	ValueCoin          float64 `json:"value_coin"`
	ValueForwarded     float64 `json:"value_forwarded"`
	ValueForwardedCoin float64 `json:"value_forwarded_coin"`
	Fee                float64 `json:"fee"`
	FeeCoin            float64 `json:"fee_coin"`
	Coin               string  `json:"coin"`
	Price              float64 `json:"price"`
	Result             string  `json:"result"`
	Pending            int     `json:"pending"`
}

var CryptResponseResult = map[string]int{
	"pending":  1,
	"received": 2,
	"sent":     3,
	"done":     4,
}

func CryptGetRequest(url string, params map[string]string) []byte {
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		panic(err)
	}

	//请求头部信息
	//Set时候，如果原来这一项已存在，后面的就修改已有的
	//Add时候，如果原本不存在，则添加，如果已存在，就不做任何修改
	//最终服务端获取的应该是token2
	// request.Header.Set("User-Agent", "自定义浏览器1...")
	// request.Header.Set("User-Agent", "自定义浏览器2...")
	// request.Header.Add("Host", "www.xxx.com")

	// //header:  map[User-Agent:[自定义浏览器2...]]
	// request.Header.Add("name", "alnk")
	// request.Header.Add("name", "alnk2")

	// //header:  map[Name:[alnk alnk2] User-Agent:[自定义浏览器2...]]
	// request.Header.Add("Authorization", "token1...") //token

	// fmt.Println("header: ", request.Header)

	//url参数
	query := request.URL.Query()
	for key, value := range params {
		query.Add(key, value)
	}

	// query.Add("id", "1")
	// query.Add("id", "2")
	// query.Add("name", "wan")
	request.URL.RawQuery = query.Encode()
	logger.SysLog(fmt.Sprintf("request.URL: %s", request.URL)) //request.URL:  https://xxx/instance?id=1&id=2&name=wan

	//发送请求给服务端,实例化一个客户端
	client := &http.Client{}
	res, err := client.Do(request)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	//服务端返回数据
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}
	logger.SysLog(fmt.Sprintf("url=%s;result: %s", url, string(b)))
	return b
}
func GetAddress(ticker string, params map[string]string) *CreateResponse {
	callback := config.OptionMap["CryptCallbackUrl"]
	address := config.OptionMap["AddressOut"]
	params["multi_token"] = "1"
	params["callback"] = callback
	params["address"] = address
	params["email"] = "ye4293@gmail.com"
	url := cryptHost + ticker + "/create/?"
	response := CryptGetRequest(url, params)
	var addressInfo CreateResponse
	err := json.Unmarshal(response, &addressInfo)
	if err != nil {
		panic(err)
	}
	return &addressInfo
}
func GetQrcode(ticker string, userId int) *QrcodeResponse {
	orderId := helper.GetUUID()
	params := map[string]string{
		"order_id": orderId,
		"user_id":  fmt.Sprintf("%d", userId),
	}
	addressInfo := GetAddress(ticker, params)
	url := cryptHost + ticker + "/qrcode/?"
	response := CryptGetRequest(url, map[string]string{"address": addressInfo.AddressIn})
	var qrcodeInfo QrcodeResponse
	err := json.Unmarshal(response, &qrcodeInfo)
	if err != nil {
		panic(err)
	}

	return &qrcodeInfo
}
func HandleCryptCallback(respons CryptCallbackResponse) {
	orderId := CreateOrUpdateOrder(userId, ticker, params)
}
