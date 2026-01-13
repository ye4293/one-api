#!/bin/bash

# Swagger 文档生成并上传到 S3 脚本
# 用途: 自动生成 swagger.json 并上传到 AWS S3

set -e  # 遇到错误立即退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置变量
S3_BUCKET="${S3_BUCKET:-oneapi-doc}"
S3_REGION="${S3_REGION:-us-west-1}"
S3_PATH="${S3_PATH:-oneapi/swagger.json}"
SWAGGER_FILE="docs/swagger.json"

# 获取项目根目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo -e "${GREEN}=== Swagger 文档生成并上传到 S3 ===${NC}"
echo "项目目录: $PROJECT_ROOT"
echo "S3 Bucket: $S3_BUCKET"
echo "S3 Region: $S3_REGION"
echo "S3 路径: $S3_PATH"
echo ""

# 切换到项目根目录
cd "$PROJECT_ROOT"

# 1. 检查 swag 是否已安装
echo -e "${YELLOW}[1/5] 检查 swag 命令...${NC}"
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

# 2. 生成 Swagger 文档
echo -e "${YELLOW}[2/5] 生成 Swagger 文档...${NC}"
$SWAG_CMD init
if [ $? -ne 0 ]; then
    echo -e "${RED}错误: Swagger 文档生成失败${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Swagger 文档生成成功${NC}"

# 3. 检查生成的文件
echo -e "${YELLOW}[3/5] 验证生成的文件...${NC}"
if [ ! -f "$SWAGGER_FILE" ]; then
    echo -e "${RED}错误: 未找到 $SWAGGER_FILE${NC}"
    exit 1
fi
echo -e "${GREEN}✓ 文件验证通过: $SWAGGER_FILE${NC}"
FILE_SIZE=$(du -h "$SWAGGER_FILE" | cut -f1)
echo "文件大小: $FILE_SIZE"

# 4. 检查 AWS CLI
echo -e "${YELLOW}[4/5] 检查 AWS CLI...${NC}"
if ! command -v aws &> /dev/null; then
    echo -e "${RED}错误: 未找到 aws 命令${NC}"
    echo "请先安装 AWS CLI: https://aws.amazon.com/cli/"
    exit 1
fi
echo -e "${GREEN}✓ AWS CLI 已安装${NC}"

# 验证 AWS 凭证
if ! aws sts get-caller-identity &> /dev/null; then
    echo -e "${RED}错误: AWS 凭证未配置或已过期${NC}"
    echo "请运行: aws configure"
    exit 1
fi
echo -e "${GREEN}✓ AWS 凭证验证通过${NC}"

# 5. 上传到 S3
echo -e "${YELLOW}[5/5] 上传文件到 S3...${NC}"
aws s3 cp "$SWAGGER_FILE" "s3://$S3_BUCKET/$S3_PATH" \
    --region "$S3_REGION" \
    --content-type "application/json" \
    --cache-control "max-age=300" \
    --metadata-directive REPLACE \
    --acl public-read

if [ $? -ne 0 ]; then
    echo -e "${RED}错误: 文件上传失败${NC}"
    exit 1
fi

echo -e "${GREEN}✓ 文件上传成功${NC}"
echo ""
echo -e "${GREEN}=== 完成 ===${NC}"
echo "文档 URL: https://$S3_BUCKET.s3.$S3_REGION.amazonaws.com/$S3_PATH"
echo ""
echo "提示:"
echo "  - 可以通过浏览器访问上述 URL 验证"
echo "  - 缓存时间设置为 5 分钟（300秒）"
echo "  - 文件已设置为公开可读"
