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
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	apiRouter.Use(middleware.CryptCallbackAuth())
	{
		apiRouter.GET("/dashboard/billing/subscription", controller.GetSubscription)
		apiRouter.GET("/v1/dashboard/billing/subscription", controller.GetSubscription)
		apiRouter.GET("/dashboard/billing/usage", controller.GetUsage)
		apiRouter.GET("/v1/dashboard/billing/usage", controller.GetUsage)
	}
}
