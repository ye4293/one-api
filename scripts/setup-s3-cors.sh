#!/bin/bash

# S3 CORS 配置脚本
# 用途: 为 S3 Bucket 配置 CORS 允许跨域访问

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置
S3_BUCKET="${S3_BUCKET:-oneapi-doc}"

echo -e "${GREEN}=== 配置 S3 CORS ===${NC}"
echo "S3 Bucket: $S3_BUCKET"
echo ""

# 检查 AWS CLI
echo -e "${YELLOW}[1/3] 检查 AWS CLI...${NC}"
if ! command -v aws &> /dev/null; then
    echo -e "${RED}错误: 未找到 aws 命令${NC}"
    exit 1
fi

# 验证 AWS 凭证
if ! aws sts get-caller-identity &> /dev/null; then
    echo -e "${RED}错误: AWS 凭证未配置或已过期${NC}"
    exit 1
fi
echo -e "${GREEN}✓ AWS CLI 验证通过${NC}"

# 创建 CORS 配置
echo -e "${YELLOW}[2/3] 创建 CORS 配置...${NC}"

# 使用 cat 直接写入文件，避免 heredoc 问题
cat > /tmp/s3-cors.json <<'EOFCORS'
{
    "CORSRules": [
        {
            "AllowedHeaders": ["*"],
            "AllowedMethods": ["GET", "HEAD"],
            "AllowedOrigins": ["*"],
            "ExposeHeaders": ["ETag", "Content-Type", "Content-Length"],
            "MaxAgeSeconds": 3000
        }
    ]
}
EOFCORS

echo -e "${GREEN}✓ CORS 配置文件已创建${NC}"
cat /tmp/s3-cors.json

# 应用 CORS 配置
echo ""
echo -e "${YELLOW}[3/3] 应用 CORS 配置到 S3...${NC}"
aws s3api put-bucket-cors \
    --bucket "$S3_BUCKET" \
    --cors-configuration file:///tmp/s3-cors.json

if [ $? -ne 0 ]; then
    echo -e "${RED}错误: CORS 配置失败${NC}"
    echo ""
    echo "可能的原因:"
    echo "  1. 没有 s3:PutBucketCORS 权限"
    echo "  2. Bucket 名称不正确"
    echo ""
    echo "解决方案:"
    echo "  - 通过 AWS 控制台手动配置"
    exit 1
fi

echo -e "${GREEN}✓ CORS 配置成功${NC}"

# 验证配置
echo ""
echo -e "${YELLOW}验证 CORS 配置...${NC}"
aws s3api get-bucket-cors --bucket "$S3_BUCKET" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ CORS 配置已生效${NC}"
    echo ""
    echo "当前 CORS 配置:"
    aws s3api get-bucket-cors --bucket "$S3_BUCKET" | jq '.'
else
    echo -e "${YELLOW}⚠ 无法验证 CORS 配置（可能需要等待几秒钟）${NC}"
fi

# 清理临时文件
rm -f /tmp/s3-cors.json

echo ""
echo -e "${GREEN}=== 完成 ===${NC}"
echo "CORS 已配置，允许所有域名跨域访问"
echo ""
echo "测试 CORS:"
echo "  1. 访问: http://localhost:3000/test-s3.html"
echo "  2. 或在浏览器控制台运行:"
echo "     fetch('https://$S3_BUCKET.s3.us-west-1.amazonaws.com/oneapi/swagger.json').then(r=>r.json()).then(console.log)"
