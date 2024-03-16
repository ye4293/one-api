package router

import (
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/controller"
	"github.com/songquanpeng/one-api/middleware"
)

func SetPayRouter(router *gin.Engine) {
	apiRouter := router.Group("/")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	//apiRouter.Use(middleware.GlobalAPIRateLimit())
	//apiRouter.Use(middleware.CryptCallbackAuth())
	{
		apiRouter.GET("/order/list", controller.GetSubscription)
		apiRouter.GET("/order/search", controller.GetSubscription)
		apiRouter.GET("/pay/get_qrcode", controller.GetQrcode)
		apiRouter.GET("/crypt/callback", controller.CryptCallback)
	}
}
