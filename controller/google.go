package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/message"
	"github.com/songquanpeng/one-api/model"
)

func GoogleLogin(c *gin.Context) {
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 打印完整的 JSON 数据
	logger.SysLog(fmt.Sprint("Received Google user data: %+v\n", data))
	logger.SysLog(fmt.Sprint("Received Google user data: %+v\n", data))
	logger.SysLog(fmt.Sprint("Received Google user data: %+v\n", data))
}

const (
	GoogleOAuthURL = "https://accounts.google.com/o/oauth2/auth"
	GetTokenUrl    = "https://accounts.google.com/o/oauth2/token"
	GetUserUrl     = "https://www.googleapis.com/oauth2/v1/userinfo"
)

type GoogleTokenResult struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
	IdToken     string `json:"id_token"`
}

type GoogleUser struct {
	GoogleId string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
}

func GoogleOAuth(c *gin.Context) {
	Scope := "https://www.googleapis.com/auth/userinfo.email%20https://www.googleapis.com/auth/userinfo.profile"

	// 从配置中获取重定向URI
	redirectURI := config.GoogleRedirectUri
	//防止CSRF攻击
	state := c.Query("state")

	// 构建OAuth URL，不包含client_secret
	oAuthUrl := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&scope=%s&response_type=code&access_type=offline&state=%s", GoogleOAuthURL, config.GoogleClientId, redirectURI, Scope, state)
	logger.SysLog(fmt.Sprintf("oAuthUrl: %s\n", string(oAuthUrl)))
	// 重定向用户到OAuth URL
	c.Redirect(http.StatusFound, oAuthUrl)
}

func GoogleOAuthCallback(c *gin.Context) {
	session := sessions.Default(c)
	state := c.Query("state")
	if state == "" || session.Get("oauth_state") == nil || state != session.Get("oauth_state").(string) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "state is empty or not same",
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
	tokenResult, err := GetTokenByCode(code)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	googleUser, err := GetGoogleUserInfoByToken(tokenResult.AccessToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	user := model.User{
		GoogleId: googleUser.GoogleId,
	}
	//判断用户是否已经通过此邮箱进行了注册
	if model.IsGoogleIdAlreadyTaken(user.GoogleId) {
		err := user.FillUserByGoogleId()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		if user.Email != "" && user.Email != googleUser.Email {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "User email is different from google email",
			})
			return
		}
	} else {
		if config.RegisterEnabled {
			user.Username = "google" + strconv.Itoa(model.GetMaxUserId()+1)
			user.DisplayName = googleUser.Name
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
			email := googleUser.Email
			subject := fmt.Sprintf("%s's register notification email", config.SystemName)
			content := fmt.Sprintf("<p>hello,You have successfully registered an account in %s, Please update your username and password as well as the warning threshold in your personal settings as soon as possible</p>"+"<p>Congratulations on getting one step closer to the AI world!</p>", config.SystemName)
			err = message.SendEmail(subject, email, content)
			if err != nil {
				return
			}
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "The administrator has closed new user registration",
			})
			return
		}
	}

	//如果是已经被注册过
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{
			"message": "用户已被封禁",
			"success": false,
		})
		return
	}
	user.GoogleId = googleUser.GoogleId
	user.Email = googleUser.Email
	err = user.Update(false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	setupLogin(&user, c)
}

func GetTokenByCode(code string) (*GoogleTokenResult, error) {
	redirect_url := config.GoogleRedirectUri
	data := url.Values{}
	data.Set("client_id", config.GoogleClientId)
	data.Set("client_secret", config.GoogleClientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirect_url)
	response, err := http.PostForm(GetTokenUrl, data)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get token: %d", response.StatusCode)
	}
	getTokenResult, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	logger.SysLog(fmt.Sprintf("getTokenResult: %s\n", string(getTokenResult)))
	var tokenResult GoogleTokenResult
	err = json.Unmarshal(getTokenResult, &tokenResult)
	if err != nil {
		return nil, err
	}
	return &tokenResult, nil
}

func GetGoogleUserInfoByToken(token string) (*GoogleUser, error) {
	req, err := http.NewRequest("GET", GetUserUrl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	client := http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get user info: %d", response.StatusCode)
	}
	userInfo, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	logger.SysLog(fmt.Sprintf("userInfo: %s\n", string(userInfo)))
	var user GoogleUser
	err = json.Unmarshal(userInfo, &user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func GoogleBind(c *gin.Context) {
	if !config.GoogleOAuthEnabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "The administrator has closed new user registration",
		})
		return
	}
	code := c.Query("code")
	tokenResult, err := GetTokenByCode(code)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	googleUser, err := GetGoogleUserInfoByToken(tokenResult.AccessToken)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	user := model.User{
		GoogleId: googleUser.GoogleId,
	}
	if model.IsGoogleIdAlreadyTaken(user.GoogleId) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "This GitHub account has been bound",
		})
		return
	}
	session := sessions.Default(c)
	id := session.Get("id")
	// id := c.GetInt("id")  // critical bug!
	user.Id = id.(int)
	err = user.FillUserById()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if user.Email != "" && user.Email != googleUser.Email {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "User email is different from google email",
		})
		return
	}
	user.Email = googleUser.Email
	user.GoogleId = googleUser.GoogleId
	err = user.Update(false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Bind google successfully",
	})
	return
}
