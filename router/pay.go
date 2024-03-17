package router

import (
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/controller"
	"github.com/songquanpeng/one-api/middleware"
)

func SetPayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	apiRouter := router.Group("/")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	
	//apiRouter.Use(middleware.GlobalAPIRateLimit())
	//apiRouter.Use(middleware.CryptCallbackAuth())
	{
		//apiRouter.GET("/order/list",middleware.GlobalAPIRateLimit(), controller.)
		//apiRouter.GET("/order/search", middleware.GlobalAPIRateLimit(),controller.GetSubscription)
		apiRouter.GET("/pay/get_qrcode",middleware.UserAuth(), middleware.GlobalAPIRateLimit(),controller.GetQrcode)
		apiRouter.GET("/pay/get_channel",middleware.UserAuth(), middleware.GlobalAPIRateLimit(),controller.GetPayChannel)
		apiRouter.GET("/crypt/callback",middleware.CryptCallbackAuth(),controller.CryptCallback)
	}
}
