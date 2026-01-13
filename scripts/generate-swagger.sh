#!/bin/bash

# Swagger 文档生成脚本（仅生成，不上传）
# 用途: 本地开发时生成 swagger.json

set -e  # 遇到错误立即退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 获取项目根目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo -e "${GREEN}=== 生成 Swagger 文档 ===${NC}"
echo "项目目录: $PROJECT_ROOT"
echo ""

# 切换到项目根目录
cd "$PROJECT_ROOT"

# 检查 swag 是否已安装
echo -e "${YELLOW}[1/2] 检查 swag 命令...${NC}"
if ! command -v swag &> /dev/null; then
    # 尝试查找 go bin 目录中的 swag
    SWAG_PATH=""
    for GO_BIN in "$HOME/go/bin/swag" "$HOME/go/*/bin/swag"; do
        if [ -f "$GO_BIN" ]; then
            SWAG_PATH="$GO_BIN"
            break
        fi
    done
    
    if [ -z "$SWAG_PATH" ]; then
        echo -e "${RED}错误: 未找到 swag 命令${NC}"
        echo "请先安装: go install github.com/swaggo/swag/cmd/swag@latest"
        exit 1
    fi
    
    SWAG_CMD="$SWAG_PATH"
    echo "使用: $SWAG_CMD"
else
    SWAG_CMD="swag"
    echo "使用: $(which swag)"
fi

# 生成 Swagger 文档
echo -e "${YELLOW}[2/2] 生成 Swagger 文档...${NC}"
$SWAG_CMD init

if [ $? -ne 0 ]; then
    echo -e "${RED}错误: Swagger 文档生成失败${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Swagger 文档生成成功${NC}"
echo ""

# 显示生成的文件信息
if [ -f "docs/swagger.json" ]; then
    FILE_SIZE=$(du -h "docs/swagger.json" | cut -f1)
    echo "生成的文件:"
    echo "  - docs/swagger.json ($FILE_SIZE)"
    echo "  - docs/swagger.yaml"
    echo "  - docs/docs.go"
fi

echo ""
echo -e "${GREEN}=== 完成 ===${NC}"
echo "提示:"
echo "  - 本地开发可直接使用生成的文档"
echo "  - 生产环境请运行 'make swagger-deploy' 上传到 S3"
