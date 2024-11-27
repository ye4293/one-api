package router

import (
	"github.com/songquanpeng/one-api/controller"
	"github.com/songquanpeng/one-api/middleware"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	apiRouter := router.Group("/api")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	{
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/about", controller.GetAbout)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/verification", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), controller.ResetPassword)
		apiRouter.GET("/oauth/state", middleware.CriticalRateLimit(), controller.GenerateOAuthCode)
		apiRouter.GET("/oauth/github", middleware.CriticalRateLimit(), controller.GithubOAuth)
		apiRouter.POST("/github/login", middleware.CriticalRateLimit(), controller.GitHubLogin)
		apiRouter.GET("/oauth/github/callback", middleware.CriticalRateLimit(), controller.GithubOAuthCallback)
		apiRouter.GET("/oauth/google", middleware.CriticalRateLimit(), controller.GoogleOAuth)
		apiRouter.GET("/oauth/google/callback", middleware.CriticalRateLimit(), controller.GoogleOAuthCallback)
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), controller.WeChatAuth)
		apiRouter.GET("/oauth/wechat/bind", middleware.CriticalRateLimit(), middleware.UserAuth(), controller.WeChatBind)
		apiRouter.GET("/oauth/email/bind", middleware.CriticalRateLimit(), middleware.UserAuth(), controller.EmailBind)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.Register)
			userRoute.POST("/login", middleware.CriticalRateLimit(), controller.Login)
			userRoute.GET("/logout", controller.Logout)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.PUT("/self", controller.UpdateSelf)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", controller.GenerateAccessToken)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.POST("/topup", controller.TopUp)
			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.AdminAuth())
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/search", controller.SearchUsers)
				adminRoute.GET("/:id", controller.GetUser)
				adminRoute.POST("/", controller.CreateUser)
				adminRoute.POST("/manage", controller.ManageUser)
				adminRoute.PUT("/", controller.UpdateUser)
				adminRoute.POST("/batchdelete", controller.BatchDelteUser)
				adminRoute.DELETE("/:id", controller.DeleteUser)
			}
		}
		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.PUT("/", controller.UpdateOption)
		}
		channelRoute := apiRouter.Group("/channel")
		channelRoute.Use(middleware.AdminAuth())
		{
			channelRoute.GET("/", controller.GetAllChannels)
			channelRoute.GET("/search", controller.SearchChannels)
			channelRoute.GET("/models", controller.ListModels)
			channelRoute.GET("/types", controller.ListTypes)
			channelRoute.GET("/:id", controller.GetChannel)
			channelRoute.GET("/test", controller.TestChannels)
			channelRoute.GET("/test/:id", controller.TestChannel)
			channelRoute.GET("/update_balance", controller.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", controller.UpdateChannelBalance)
			channelRoute.POST("/", controller.AddChannel)
			channelRoute.PUT("/", controller.UpdateChannel)
			channelRoute.POST("/batchdelete", controller.BatchDelteChannel)
			channelRoute.DELETE("/disabled", controller.DeleteDisabledChannel)
			channelRoute.DELETE("/:id", controller.DeleteChannel)
		}
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", controller.SearchTokens)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.POST("/batchdelete", controller.BatchDeleteToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
		}
		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
		{
			redemptionRoute.GET("/", controller.GetAllRedemptions)
			redemptionRoute.GET("/search", controller.SearchRedemptions)
			redemptionRoute.GET("/:id", controller.GetRedemption)
			redemptionRoute.POST("/", controller.AddRedemption)
			redemptionRoute.PUT("/", controller.UpdateRedemption)
			redemptionRoute.POST("/batchdelete", controller.BatchDeleteRedemption)
			redemptionRoute.DELETE("/:id", controller.DeleteRedemption)
		}
		logRoute := apiRouter.Group("/log")
		logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.DELETE("/", middleware.AdminAuth(), controller.DeleteHistoryLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/search", middleware.AdminAuth(), controller.SearchAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), controller.SearchUserLogs)
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.AdminAuth())
		{
			groupRoute.GET("/", controller.GetGroups)
		}
	}
	cryptoaiRoute := apiRouter.Group("/")
	// cryptoaiRoute.GET("/pay/crypt/get_qrcode", middleware.UserAuth(), middleware.GlobalAPIRateLimit(), controller.GetQrcode)
	cryptoaiRoute.GET("/pay/get_qrcode", middleware.UserAuth(), middleware.GlobalAPIRateLimit(), controller.GetQrcode)
	cryptoaiRoute.GET("/pay/get_channel", middleware.UserAuth(), middleware.GlobalAPIRateLimit(), controller.GetPayChannel)
	cryptoaiRoute.GET("/crypt/callback", middleware.CryptCallbackAuth(), controller.CryptCallback)

	orderRoute := apiRouter.Group("/order")
	orderRoute.GET("/", middleware.AdminAuth(), controller.GetAllOrders)
	orderRoute.GET("/self", middleware.UserAuth(), controller.GetUserOrders)

	dashboardRoute := apiRouter.Group("/dashboard")
	dashboardRoute.GET("/", middleware.AdminAuth(), controller.GetAdminDashboard)
	dashboardRoute.GET("/graph", middleware.AdminAuth(), controller.GetAllGraph)
	dashboardRoute.GET("/self", middleware.UserAuth(), controller.GetUserDashboard)
	dashboardRoute.GET("/graph/self", middleware.UserAuth(), controller.GetUserGraph)

	mjRoute := apiRouter.Group("/mj")
	mjRoute.GET("/self", middleware.UserAuth(), controller.GetUserMidjourney)
	mjRoute.GET("/", middleware.AdminAuth(), controller.GetAllMidjourney)

	videoRoute := apiRouter.Group("/video")
	videoRoute.GET("/self", middleware.UserAuth(), controller.GetUserVideos)
	videoRoute.GET("/", middleware.AdminAuth(), controller.GetAllVideos)

	chargeRoute := apiRouter.Group("/charge")
	chargeRoute.GET("/get_config", middleware.UserAuth(), middleware.GlobalWebRateLimit(), controller.GetChargeConfigs)
	chargeRoute.POST("/create_order", middleware.UserAuth(), middleware.GlobalWebRateLimit(), controller.CreateChargeOrder)
	chargeRoute.GET("/get_order", middleware.UserAuth(), middleware.GlobalWebRateLimit(), controller.GetUserChargeOrders)
	chargeRoute.POST("/stripe_callback", middleware.GlobalWebRateLimit(), controller.StripeCallback)

	fileRoute := apiRouter.Group("/files")
	{
		fileRoute.POST("/", middleware.UserAuth(), middleware.GlobalWebRateLimit(), controller.UploadFile)
		fileRoute.DELETE("/", middleware.UserAuth(), middleware.GlobalWebRateLimit(), controller.DeletiFile)
	}
}
