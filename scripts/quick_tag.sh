#!/bin/bash

###############################################################################
# 快速打 tag 脚本 (简化版)
#
# 使用方法: ./scripts/quick_tag.sh
###############################################################################

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "🚀 开始打 tag"

# 检查是否在 git 仓库中
if ! git rev-parse --git-dir > /dev/null 2>&1; then
  echo -e "${RED}❌ 错误: 当前目录不是 git 仓库${NC}"
  exit 1
fi

# 检查工作区是否干净
if [[ -n $(git status --porcelain) ]]; then
  echo -e "${RED}❌ 错误: 工作区有未提交的更改${NC}"
  echo ""
  echo "未提交的文件:"
  git status --short
  echo ""
  echo -e "${YELLOW}请先提交或暂存更改:${NC}"
  echo "  git add ."
  echo "  git commit -m \"your message\""
  echo ""
  echo "或者使用 git stash 暂存:"
  echo "  git stash"
  echo "  ./scripts/quick_tag.sh"
  echo "  git stash pop"
  exit 1
fi

# 保存当前分支
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# 切换到 main 分支
echo "🔄 切换到 main 分支..."
git checkout main

# 拉取最新代码
echo "⬇️  拉取最新代码..."
git pull origin main

# 生成 tag (基于 main 分支的最新代码)
TAG_NAME="alphaas-$(date +%m%d%H%M)"
echo "📦 Tag 名称: ${TAG_NAME}"

# 检查 tag 是否已存在
if git rev-parse "$TAG_NAME" >/dev/null 2>&1; then
  echo -e "${RED}❌ 错误: tag ${TAG_NAME} 已存在${NC}"
  echo -e "${YELLOW}提示: 请等待1分钟后重试,或手动删除 tag:${NC}"
  echo "  git tag -d ${TAG_NAME}"
  git checkout "$CURRENT_BRANCH"
  exit 1
fi

# 创建并推送 tag
echo "🏷️  创建 tag..."
git tag "$TAG_NAME"

echo "⬆️  推送 tag..."
git push origin "$TAG_NAME"

# 切换回原分支
if [[ "$CURRENT_BRANCH" != "main" ]]; then
  echo "🔙 切换回 ${CURRENT_BRANCH} 分支..."
  git checkout "$CURRENT_BRANCH"
fi

echo ""
echo -e "${GREEN}✅ 成功! Tag ${TAG_NAME} 已推送${NC}"
echo "🔗 GitHub Actions 将自动构建镜像"
echo ""
echo "监控构建进度:"
echo "  https://github.com/ye4293/one-api/actions"
