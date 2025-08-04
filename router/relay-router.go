package router

import (
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/controller"
	"github.com/songquanpeng/one-api/middleware"
)

func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.TokenAuth())
	{
		modelsRouter.GET("", controller.ListModels)
		modelsRouter.GET("/:model", controller.RetrieveModel)
	}
	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.RelayPanicRecover(), middleware.TokenAuth())
	{
		relayV1Router.POST("/files", controller.UploadFile)
		relayV1Router.GET("/video/generations/result", controller.RelayVideoResult)
		relayV1Router.POST("/flux-pro-1.1", controller.RelayDirectFlux)
		relayV1Router.POST("/flux-pro", controller.RelayDirectFlux)
		// Image generation endpoints
	}

	// Create separate router groups for POST and GET
	asyncImagePostRouter := router.Group("/v1/async")
	asyncImagePostRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		asyncImagePostRouter.POST("/images/generations", controller.RelayImageGenerateAsync)
	}

	asyncImageGetRouter := router.Group("/v1/async")
	asyncImageGetRouter.Use(middleware.TokenAuth())
	{
		asyncImageGetRouter.GET("/images/result", controller.RelayImageResult)
	}

	relayV1Router.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
	{
		relayV1Router.POST("/completions", controller.Relay)
		relayV1Router.POST("/chat/completions", controller.Relay)
		relayV1Router.POST("/edits", controller.Relay)
		relayV1Router.POST("/images/edits", controller.Relay)
		relayV1Router.POST("/images/variations", controller.RelayNotImplemented)
		relayV1Router.POST("/embeddings", controller.Relay)
		relayV1Router.POST("/engines/:model/embeddings", controller.Relay)
		relayV1Router.POST("/audio/transcriptions", controller.Relay)
		relayV1Router.POST("/audio/translations", controller.Relay)
		relayV1Router.POST("/audio/speech", controller.Relay)
		relayV1Router.GET("/files", controller.RelayNotImplemented)
		relayV1Router.DELETE("/files/:id", controller.RelayNotImplemented)
		relayV1Router.GET("/files/:id", controller.RelayNotImplemented)
		relayV1Router.GET("/files/:id/content", controller.RelayNotImplemented)
		relayV1Router.POST("/fine_tuning/jobs", controller.RelayNotImplemented)
		relayV1Router.GET("/fine_tuning/jobs", controller.RelayNotImplemented)
		relayV1Router.GET("/fine_tuning/jobs/:id", controller.RelayNotImplemented)
		relayV1Router.POST("/fine_tuning/jobs/:id/cancel", controller.RelayNotImplemented)
		relayV1Router.GET("/fine_tuning/jobs/:id/events", controller.RelayNotImplemented)
		relayV1Router.DELETE("/models/:model", controller.RelayNotImplemented)
		relayV1Router.POST("/moderations", controller.Relay)
		relayV1Router.POST("/assistants", controller.RelayNotImplemented)
		relayV1Router.GET("/assistants/:id", controller.RelayNotImplemented)
		relayV1Router.POST("/assistants/:id", controller.RelayNotImplemented)
		relayV1Router.DELETE("/assistants/:id", controller.RelayNotImplemented)
		relayV1Router.GET("/assistants", controller.RelayNotImplemented)
		relayV1Router.POST("/assistants/:id/files", controller.RelayNotImplemented)
		relayV1Router.GET("/assistants/:id/files/:fileId", controller.RelayNotImplemented)
		relayV1Router.DELETE("/assistants/:id/files/:fileId", controller.RelayNotImplemented)
		relayV1Router.GET("/assistants/:id/files", controller.RelayNotImplemented)
		relayV1Router.POST("/threads", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id", controller.RelayNotImplemented)
		relayV1Router.DELETE("/threads/:id", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id/messages", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/messages/:messageId", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id/messages/:messageId", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/messages/:messageId/files/:filesId", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/messages/:messageId/files", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id/runs", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/runs/:runsId", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id/runs/:runsId", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/runs", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id/runs/:runsId/submit_tool_outputs", controller.RelayNotImplemented)
		relayV1Router.POST("/threads/:id/runs/:runsId/cancel", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/runs/:runsId/steps/:stepId", controller.RelayNotImplemented)
		relayV1Router.GET("/threads/:id/runs/:runsId/steps", controller.RelayNotImplemented)
		relayV1Router.POST("/video/generations", controller.RelayVideoGenerate)
		relayV1Router.POST("/ocr", controller.RelayOcr)
		relayV1Router.POST("/images/imageToImage", controller.RelayRecraft)
		relayV1Router.POST("/images/inpaint", controller.RelayRecraft)
		relayV1Router.POST("/images/replaceBackground", controller.RelayRecraft)
		relayV1Router.POST("/images/vectorize", controller.RelayRecraft)
		relayV1Router.POST("/images/removeBackground", controller.RelayRecraft)
		relayV1Router.POST("/images/crispUpscale", controller.RelayRecraft)
		relayV1Router.POST("/images/creativeUpscale", controller.RelayRecraft)
		relayV1Router.POST("/styles", controller.RelayRecraft)
		relayV1Router.POST("/images/generations", controller.Relay)
	}
	mjModeMiddleware := func() gin.HandlerFunc {
		return func(c *gin.Context) {
			mode := c.Param("mode")

			// 如果 mode 参数为空（对应默认 /mj 路由），设置为 "fast"
			if mode == "" {
				mode = "fast"
			}

			// 验证 mode 是否有效
			switch mode {
			case "fast", "turbo", "relax":
				// 有效的模式
			default:
				// 如果是无效模式，设置为默认的 "fast"
				mode = "fast"
			}

			mjMode := "mj_" + mode
			c.Set("mode", mode)
			c.Set("mj_mode", mjMode)
			c.Next()
		}
	}

	// 定义设置 MJ 路由的函数
	setupMJRoutes := func(group *gin.RouterGroup) {
		group.GET("/image/:id", controller.RelayMidjourneyImage)
		group.POST("/notify", middleware.Distribute(), controller.RelayMidjourney)
		group.Use(middleware.TokenAuth(), middleware.Distribute())
		{
			group.POST("/submit/action", controller.RelayMidjourney)
			group.POST("/submit/shorten", controller.RelayMidjourney)
			group.POST("/submit/modal", controller.RelayMidjourney)
			group.POST("/submit/imagine", controller.RelayMidjourney)
			group.POST("/submit/change", controller.RelayMidjourney)
			group.POST("/submit/simple-change", controller.RelayMidjourney)
			group.POST("/submit/describe", controller.RelayMidjourney)
			group.POST("/submit/blend", controller.RelayMidjourney)
			group.GET("/task/:id/fetch", controller.RelayMidjourney)
			group.GET("/task/:id/image-seed", controller.RelayMidjourney)
			group.POST("/task/list-by-condition", controller.RelayMidjourney)
			group.POST("/insight-face/swap", controller.RelayMidjourney)
		}
	}

	defaultMjRouter := router.Group("/mj", mjModeMiddleware())
	setupMJRoutes(defaultMjRouter)

	// 设置带模式的 MJ 路由 (/mj-:mode/mj)
	modeMjRouter := router.Group("/mj-:mode/mj", mjModeMiddleware())
	setupMJRoutes(modeMjRouter)

	// 现有的路由组
	relaySdRouter := router.Group("/v2beta")
	relaySdRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		relaySdRouter.POST("/stable-image/generate/core", controller.RelaySd)
		relaySdRouter.POST("/stable-image/generate/sd3", controller.RelaySd)
		relaySdRouter.POST("/stable-image/generate/ultra", controller.RelaySd)
		relaySdRouter.POST("/stable-image/upscale/conservative", controller.RelaySd)
		relaySdRouter.POST("/stable-image/upscale/creative", controller.RelaySd)
		relaySdRouter.GET("/stable-image/upscale/creative/result/:generation_id", controller.RelaySd)
		relaySdRouter.POST("/stable-image/edit/erase", controller.RelaySd)
		relaySdRouter.POST("/stable-image/edit/inpaint", controller.RelaySd)
		relaySdRouter.POST("/stable-image/edit/outpaint", controller.RelaySd)
		relaySdRouter.POST("/stable-image/edit/search-and-replace", controller.RelaySd)
		relaySdRouter.POST("/stable-image/edit/remove-background", controller.RelaySd)
		relaySdRouter.POST("/stable-image/control/sketch", controller.RelaySd)
		relaySdRouter.POST("/stable-image/control/structure", controller.RelaySd)
		relaySdRouter.POST("/image-to-video", controller.RelaySd)
		relaySdRouter.GET("/image-to-video/result/:generation_id", controller.RelaySd)
		// relaySdRouter.POST("/3d/stable-fast-3d", controller.RelaySd)
	}

	// 新增的路由组，支持 /sd 开头的所有相同路径
	sdRouter := router.Group("/sd/v2beta")
	sdRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		sdRouter.POST("/stable-image/generate/core", controller.RelaySd)
		sdRouter.POST("/stable-image/generate/sd3", controller.RelaySd)
		sdRouter.POST("/stable-image/generate/ultra", controller.RelaySd)
		sdRouter.POST("/stable-image/upscale/conservative", controller.RelaySd)
		sdRouter.POST("/stable-image/upscale/creative", controller.RelaySd)
		sdRouter.GET("/stable-image/upscale/creative/result/:generation_id", controller.RelaySd)
		sdRouter.POST("/stable-image/edit/erase", controller.RelaySd)
		sdRouter.POST("/stable-image/edit/inpaint", controller.RelaySd)
		sdRouter.POST("/stable-image/edit/outpaint", controller.RelaySd)
		sdRouter.POST("/stable-image/edit/search-and-replace", controller.RelaySd)
		sdRouter.POST("/stable-image/edit/remove-background", controller.RelaySd)
		sdRouter.POST("/stable-image/control/sketch", controller.RelaySd)
		sdRouter.POST("/stable-image/control/structure", controller.RelaySd)
		sdRouter.POST("/image-to-video", controller.RelaySd)
		sdRouter.GET("/image-to-video/result/:generation_id", controller.RelaySd)
		// sdRouter.POST("/3d/stable-fast-3d", controller.RelaySd)
	}

	relayFluxRouter := router.Group("/flux")
	{
		relayFluxRouter.GET("/:id", controller.RelayReplicateImage)
	}
	// 豆包API兼容路由组 - 支持原始豆包API路径格式
	doubaoApiRouter := router.Group("/api/v3/contents/generations")
	doubaoApiRouter.Use(middleware.TokenAuth()).GET("/tasks/:taskid", controller.RelayDouBaoVideoResultById)
	doubaoApiRouter.Use(middleware.TokenAuth(), middleware.Distribute()).POST("/tasks", controller.RelayVideoGenerate)

	// Runway AI 路由组 - 在官方API路径中间插入"runway"
	// Runway API 使用直接代理模式，不需要 Distribute 中间件
	runwayRouter := router.Group("/runway/v1")
	runwayRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
	{
		// 视频生成相关端点
		runwayRouter.POST("/image_to_video", controller.RelayRunway)
		runwayRouter.POST("/video_to_video", controller.RelayRunway)
		runwayRouter.POST("/text_to_image", controller.RelayRunway)
		runwayRouter.POST("/video_upscale", controller.RelayRunway)
		runwayRouter.POST("/character_performance", controller.RelayRunway)
	}

	// 不需要模型分发的查询端点
	runwayResultRouter := router.Group("/runway/v1")
	runwayResultRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth())
	{
		runwayResultRouter.GET("/tasks/:taskId", controller.RelayRunwayResult)
	}

}
