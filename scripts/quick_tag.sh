#!/bin/bash

###############################################################################
# 快速打 tag 脚本 (简化版)
#
# 使用方法: ./scripts/quick_tag.sh
###############################################################################

set -e

# 生成 tag
TAG_NAME="alphaas-$(date +%m%d%H%M)"

echo "🚀 开始打 tag: ${TAG_NAME}"

# 切换并拉取
git checkout main
git pull origin main

# 创建并推送 tag
git tag "$TAG_NAME"
git push origin "$TAG_NAME"

echo "✅ 成功! Tag ${TAG_NAME} 已推送"
echo "🔗 GitHub Actions 将自动构建镜像"
