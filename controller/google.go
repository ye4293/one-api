package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/songquanpeng/one-api/model"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	googleOauthConfig = &oauth2.Config{
		RedirectURL:  config.GoogleRedirectUri,
		ClientID:     config.GoogleClientId,
		ClientSecret: config.GoogleClientSecret,
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile"},
		Endpoint:     google.Endpoint,
	}
)

type GoogleUser struct {
	GoogleId string `json:"google_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
}

type GoogleOAuthResponse struct {
	AccessToken  string `json:"access_token"`  // 用于访问Google API的令牌
	TokenType    string `json:"token_type"`    // 令牌的类型，通常是"Bearer"
	ExpiresIn    int    `json:"expires_in"`    // 令牌的有效期，单位是秒
	RefreshToken string `json:"refresh_token"` // 用于刷新访问令牌的令牌
	Scope        string `json:"scope"`         // 令牌的权限范围
	IdToken      string `json:"id_token"`      // 包含用户身份信息的JWT
}

func GetGoogleUserInfoByCode(code string) (*GoogleUser, error) {
	if code == "" {
		return nil, errors.New("Invalid parameter")
	}

	values := map[string]string{
		"client_id":     config.GoogleClientId,
		"client_secret": config.GoogleClientSecret,
		"code":          code,
		"grant_type":    "authorization_code",
		"redirect_uri":  config.GoogleRedirectUri, // 你的应用重定向URI
	}
	jsonData, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{
		Timeout: 5 * time.Second,
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接至 Google 服务器，请稍后重试！")
	}
	defer res.Body.Close()

	var oAuthResponse GoogleOAuthResponse
	err = json.NewDecoder(res.Body).Decode(&oAuthResponse)
	if err != nil {
		return nil, err
	}

	userInfoURL := "https://www.googleapis.com/oauth2/v2/userinfo?access_token=" + oAuthResponse.AccessToken
	userInfoRes, err := http.Get(userInfoURL)
	if err != nil {
		return nil, errors.New("无法连接至 Google 用户信息服务器，请稍后重试！")
	}
	defer userInfoRes.Body.Close()

	// 状态码检查
	if userInfoRes.StatusCode != http.StatusOK {
		return nil, errors.New("wrong")
	}

	// 读取和解析响应体
	var googleuser GoogleUser
	if body, err := io.ReadAll(userInfoRes.Body); err != nil {
		return nil, err
	} else if err := json.Unmarshal(body, &googleuser); err != nil {
		return nil, err
	}

	return &googleuser, nil
}

func GoogleOAuth(c *gin.Context) {
	session := sessions.Default(c)
	state := c.Query("state")
	if state == "" || session.Get("oauth_state") == nil || state != session.Get("oauth_state").(string) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "state is empty or not the same",
		})
		return
	}

	username := session.Get("username")
	if username != nil {
		GoogleBind(c)
		return
	}

	if !config.GoogleOAuthEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "管理员未开启通过 Google 登录以及注册",
		})
		return
	}

	code := c.Query("code")
	googleUser, err := GetGoogleUserInfoByCode(code) // 使用前面定义的函数获取Google用户信息
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	user := model.User{
		GoogleId: googleUser.Email, // 假设您使用Google用户的邮箱作为唯一标识
	}
	if model.IsGoogleIdAlreadyTaken(user.GoogleId) {
		err := user.FillUserByGoogleId()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}

	} else {
		if config.RegisterEnabled {
			user.Username = "google_" + strconv.Itoa(model.GetMaxUserId()+1)
			if googleUser.Name != "" {
				user.DisplayName = googleUser.Name
			} else {
				user.DisplayName = "Google User"
			}
			user.Email = googleUser.Email
			user.Role = common.RoleCommonUser
			user.Status = common.UserStatusEnabled

			if err := user.Insert(0); err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "管理员关闭了新用户注册",
			})
			return
		}
	}

	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "用户已被封禁",
			"success": false,
		})
		return
	}

	setupLogin(&user, c) // 假设setupLogin是您处理用户登录的函数
}

func GoogleBind(c *gin.Context) {
	if !config.GoogleOAuthEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "管理员未开启通过 Google 登录以及注册",
		})
		return
	}

	code := c.Query("code")
	googleUser, err := GetGoogleUserInfoByCode(code) // 假设已实现该函数
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	user := model.User{
		GoogleId: googleUser.GoogleId, // 假设使用Google用户的Email作为唯一标识
	}

	if model.IsGoogleIdAlreadyTaken(user.GoogleId) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该 Google 账户已被绑定",
		})
		return
	}

	session := sessions.Default(c)
	id := session.Get("id")
	if id == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "用户未登录",
		})
		return
	}

	user.Id = id.(int)
	err = user.FillUserById() // 假设已实现该方法
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	user.GoogleId = googleUser.Email
	err = user.Update(false) // 假设已实现该方法，其中false可能表示是否更新某些特定字段
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Google 账户绑定成功",
	})
}
