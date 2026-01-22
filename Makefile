.PHONY: help swagger-gen swagger-upload swagger-deploy build run clean test

# 默认目标
help:
	@echo "One API - Makefile 命令"
	@echo ""
	@echo "Swagger 文档管理:"
	@echo "  make swagger-gen     - 从 Go 注释生成 Swagger 文档"
	@echo "  make swagger-upload  - 上传 Swagger 文档到 S3"
	@echo "  make swagger-deploy  - 生成并上传 Swagger 文档（推荐）"
	@echo ""
	@echo "项目管理:"
	@echo "  make build           - 编译项目"
	@echo "  make run             - 运行项目"
	@echo "  make clean           - 清理生成的文件"
	@echo "  make test            - 运行测试"
	@echo ""
	@echo "环境变量:"
	@echo "  S3_BUCKET            - S3 存储桶名称 (默认: oneapi-doc)"
	@echo "  S3_REGION            - S3 区域 (默认: us-west-1)"
	@echo "  S3_PATH              - S3 文件路径 (默认: oneapi/swagger.json)"
	@echo ""

# 生成 Swagger 文档
swagger-gen:
	@echo "正在生成 Swagger 文档..."
	@./scripts/generate-swagger.sh

# 上传 Swagger 文档到 S3
swagger-upload:
	@echo "正在上传 Swagger 文档到 S3..."
	@./scripts/upload-swagger-to-s3.sh

# 生成并上传 Swagger 文档（一键操作）
swagger-deploy: swagger-gen
	@echo "正在部署 Swagger 文档到 S3..."
	@./scripts/upload-swagger-to-s3.sh

# 编译项目
build:
	@echo "正在编译项目..."
	@go build -o one-api .

# 运行项目
run:
	@echo "正在启动项目..."
	@go run .

# 清理生成的文件
clean:
	@echo "正在清理文件..."
	@rm -f one-api
	@rm -rf docs/docs.go docs/swagger.json docs/swagger.yaml
	@echo "清理完成"

# 运行测试
test:
	@echo "正在运行测试..."
	@go test ./...

# 安装依赖
deps:
	@echo "正在安装依赖..."
	@go mod download
	@go mod tidy

# 安装 swag 工具
install-swag:
	@echo "正在安装 swag..."
	@go install github.com/swaggo/swag/cmd/swag@latest
	@echo "swag 安装完成"

# 检查代码格式
fmt:
	@echo "正在格式化代码..."
	@go fmt ./...

# 代码检查
lint:
	@echo "正在检查代码..."
	@golangci-lint run
