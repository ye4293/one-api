package controller

import (
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

type Pages struct {
	Name     []string
	Settings []string
}

var userPages = Pages{
	Name:     []string{"dashboard", "token", "setting", "topup", "log"},
	Settings: []string{"personalSettings"},
}

var adminPages = Pages{
	Name:     []string{"dashboard", "token", "setting", "topup", "log", "channel", "user"},
	Settings: []string{"personalSettings"},
}

var rootPages = Pages{
	Name:     []string{"dashboard", "token", "setting", "topup", "log", "channel", "user"},
	Settings: []string{"personalSettings", "operateSettings", "systemSettings"},
}

func Getmenus(c *gin.Context) {
	session := sessions.Default(c)
	username := session.Get("username")
	role := session.Get("role")
	status := session.Get("status")
	if username == nil {
		accessToken := c.Request.Header.Get("Authorization")
		if accessToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Not authorized for this operation, not logged in and no access token provided",
			})
			c.Abort()
			return
		}
		user := model.ValidateAccessToken(accessToken)
		if user != nil && user.Username != "" {
			username = user.Username
			role = user.Role
			role = user.Role
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无权进行此操作，access token 无效",
			})
			c.Abort()
			return
		}
	}
	if status.(int) == common.UserStatusDisabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户已被封禁",
		})
		c.Abort()
		return
	}
	if role == 100 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"menus":   rootPages,
		})
		return
	} else if role == 10 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"menus":   adminPages,
		})
		return
	} else if role == 1 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"menus":   userPages,
		})
		return
	} else {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "",
		})
		return
	}
}
