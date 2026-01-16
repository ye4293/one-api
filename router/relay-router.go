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
	}

	// Sora 视频生成路由 - 需要 Distribute 中间件进行渠道选择
	soraRouter := router.Group("/v1")
	soraRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
	{
		soraRouter.POST("/videos", controller.RelaySoraVideo)
	}

	// Sora 视频 remix 路由 - 不需要 Distribute 中间件，因为必须使用原视频的渠道
	soraRemixRouter := router.Group("/v1")
	soraRemixRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth())
	{
		soraRemixRouter.POST("/videos/:videoId/remix", controller.RelaySoraVideoRemix)
	}

	// Sora 查询路由 - 不需要 Distribute 中间件
	soraResultRouter := router.Group("/v1")
	soraResultRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth())
	{
		soraResultRouter.GET("/videos/:videoId/content", controller.RelaySoraVideoContent)
		soraResultRouter.GET("/videos/:videoId", controller.RelaySoraVideoResult)
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
		relayV1Router.POST("/messages", controller.RelayClaude)
		relayV1Router.POST("/responses", controller.RelayResponse)
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

	// Kling AI 视频生成路由组
	klingRouter := router.Group("/kling/v1/videos")
	klingRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingRouter.POST("/text2video", controller.RelayKlingVideo)
		klingRouter.POST("/omni-video", controller.RelayKlingVideo)
		klingRouter.POST("/image2video", controller.RelayKlingVideo)
		klingRouter.POST("/multi-image2video", controller.RelayKlingVideo)
		klingRouter.POST("/identify-face", controller.DoIdentifyFace)
		klingRouter.POST("/advanced-lip-sync", controller.DoAdvancedLipSync)
	}

	// Kling 查询路由（不需要 Distribute 中间件）
	klingResultRouter := router.Group("/kling/v1/videos")
	klingResultRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth())
	{
		klingResultRouter.GET("/:id", controller.RelayKlingVideoResult)
	}

	// Kling 回调路由（不需要认证）
	klingCallbackRouter := router.Group("/kling/internal")
	{
		klingCallbackRouter.POST("/callback", controller.HandleKlingCallback)
	}

	// // Gemini 原生API透传路由组 - 支持完整的Gemini官方API格式
	// geminiNativeRouter := router.Group("/gemini/v1beta")
	// geminiNativeRouter.Use(middleware.RelayPanicRecover(), middleware.TokenAuth(), middleware.Distribute())
	// {
	// 	// 核心聊天接口
	// 	geminiNativeRouter.POST("/models/:model:generateContent", controller.RelayGeminiNative)
	// 	geminiNativeRouter.POST("/models/:model:streamGenerateContent", controller.RelayGeminiNative)

	// 	// Token计数接口
	// 	geminiNativeRouter.POST("/models/:model:countTokens", controller.RelayGeminiNative)

	// 	// 嵌入接口
	// 	geminiNativeRouter.POST("/models/:model:embedContent", controller.RelayGeminiNative)
	// 	geminiNativeRouter.POST("/models/:model:batchEmbedContents", controller.RelayGeminiNative)

	// 	// 模型管理接口
	// 	geminiNativeRouter.GET("/models", controller.RelayGeminiNative)
	// 	geminiNativeRouter.GET("/models/:model", controller.RelayGeminiNative)

	// 	// 文件上传接口
	// 	geminiNativeRouter.POST("/files", controller.RelayGeminiNative)
	// 	geminiNativeRouter.GET("/files/:name", controller.RelayGeminiNative)
	// 	geminiNativeRouter.DELETE("/files/:name", controller.RelayGeminiNative)
	// 	geminiNativeRouter.GET("/files", controller.RelayGeminiNative)

	// 	// 微调接口
	// 	geminiNativeRouter.POST("/tunedModels", controller.RelayGeminiNative)
	// 	geminiNativeRouter.GET("/tunedModels", controller.RelayGeminiNative)
	// 	geminiNativeRouter.GET("/tunedModels/:name", controller.RelayGeminiNative)
	// 	geminiNativeRouter.PATCH("/tunedModels/:name", controller.RelayGeminiNative)
	// 	geminiNativeRouter.DELETE("/tunedModels/:name", controller.RelayGeminiNative)
	// }

	// Claude 原生API透传路由组 - 支持完整的Anthropic Claude官方API格式

	// Gemini API原生接口路由组
	// 路径格式: /v1beta/models/{model_name}:{action}
	// 支持 generateContent, streamGenerateContent, embedContent, batchEmbedContents 等操作
	geminiRouter := router.Group("/v1beta")
	geminiRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		// 使用通配符捕获 models/ 后的所有内容: gemini-2.0-flash:generateContent
		geminiRouter.POST("/models/*path", controller.RelayGemini)
	}

	// Gemini API v1 版本路由（某些模型使用 v1）
	geminiV1Router := router.Group("/v1")
	geminiV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		geminiV1Router.POST("/models/*path", controller.RelayGemini)
	}

	// Gemini API v1alpha 版本路由（某些项目使用 v1alpha）
	geminiV1AlphaRouter := router.Group("/v1alpha")
	geminiV1AlphaRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		geminiV1AlphaRouter.POST("/models/*path", controller.RelayGemini)
	}
}
