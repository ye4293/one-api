package controller

// import (
// 	"bytes"
// 	"encoding/json"
// 	"errors"
// 	"net/http"
// 	"time"

// 	"github.com/gin-gonic/gin"
// 	"github.com/songquanpeng/one-api/common/config"
// 	"golang.org/x/oauth2"
// 	"golang.org/x/oauth2/google"
// )

// var (
// 	googleOauthConfig = &oauth2.Config{
// 		RedirectURL:  "http://localhost:8080/auth/google/callback",
// 		ClientID:     "YOUR_CLIENT_ID",
// 		ClientSecret: "YOUR_CLIENT_SECRET",
// 		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email"},
// 		Endpoint:     google.Endpoint,
// 	}
// )

// type GoogleUser struct {
// 	GoogleId string `json:"google_id"`
// 	Name     string `json:"name"`
// 	Email    string `json:"email"`
// }

// type GoogleOAuthResponse struct {
// 	AccessToken  string `json:"access_token"`  // 用于访问Google API的令牌
// 	TokenType    string `json:"token_type"`    // 令牌的类型，通常是"Bearer"
// 	ExpiresIn    int    `json:"expires_in"`    // 令牌的有效期，单位是秒
// 	RefreshToken string `json:"refresh_token"` // 用于刷新访问令牌的令牌
// 	Scope        string `json:"scope"`         // 令牌的权限范围
// 	IdToken      string `json:"id_token"`      // 包含用户身份信息的JWT
// }

// func GetGoogleUserInfoByCode(code string) (*GoogleUser, error) {
// 	if code == "" {
// 		return nil, errors.New("无效的参数")
// 	}

// 	values := map[string]string{
// 		"client_id":     config.GoogleClientId,
// 		"client_secret": config.GoogleClientSecret,
// 		"code":          code,
// 		"grant_type":    "authorization_code",
// 		"redirect_uri":  config.GoogleRedirectUri, // 你的应用重定向URI
// 	}
// 	jsonData, err := json.Marshal(values)
// 	if err != nil {
// 		return nil, err
// 	}
// 	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return nil, err
// 	}
// 	req.Header.Set("Content-Type", "application/json")
// 	client := http.Client{
// 		Timeout: 5 * time.Second,
// 	}
// 	res, err := client.Do(req)
// 	if err != nil {
// 		return nil, errors.New("无法连接至 Google 服务器，请稍后重试！")
// 	}
// 	defer res.Body.Close()

// 	var oAuthResponse GoogleOAuthResponse
// 	err = json.NewDecoder(res.Body).Decode(&oAuthResponse)
// 	if err != nil {
// 		return nil, err
// 	}
// }

// // func GoogleOAuth(c *gin.Context) {

// // }

// // func GoogleBind(c *gin.Context) {

// // }
