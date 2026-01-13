package router

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func SetRouter(router *gin.Engine, buildFS embed.FS) {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)

	// 获取 Swagger 文档 URL（优先使用环境变量，否则使用 S3 默认地址）
	swaggerURL := os.Getenv("SWAGGER_JSON_URL")
	if swaggerURL == "" {
		// 默认使用 S3 托管的文档
		swaggerURL = "https://oneapi-doc.s3.us-west-1.amazonaws.com/oneapi/swagger.json"
	}

	// Swagger UI 路由配置
	router.GET("/swagger/*any", ginSwagger.WrapHandler(
		swaggerFiles.Handler,
		ginSwagger.URL(swaggerURL),
	))
	logger.SysLog(fmt.Sprintf("Swagger UI enabled at /swagger/index.html (doc: %s)", swaggerURL))

	// 为了兼容性，也提供 /swagger/doc.json 端点（可选，用于本地开发）
	if os.Getenv("SWAGGER_LOCAL_FILE") == "true" {
		router.StaticFile("/swagger/doc.json", "./docs/swagger.json")
		logger.SysLog("Local swagger.json enabled at /swagger/doc.json")
	}

	// Scalar API 文档路由
	router.StaticFile("/docs", "./static/api-docs.html")
	logger.SysLog("Scalar API documentation enabled at /docs")

	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if config.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		logger.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, buildFS)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		})
	}
}
