#!/bin/bash

###############################################################################
# 自动打 tag 并推送到 main 分支
#
# 功能:
# 1. 检查工作区是否干净
# 2. 切换到 main 分支
# 3. 拉取最新代码
# 4. 创建格式为 alphaas-MMDDHHMM 的 tag
# 5. 推送 tag 到远程仓库
#
# 使用方法:
#   ./scripts/create_and_push_tag.sh
#   ./scripts/create_and_push_tag.sh --message "发布新版本"
#   ./scripts/create_and_push_tag.sh --dry-run  # 模拟运行,不实际推送
###############################################################################

set -e  # 遇到错误立即退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 默认参数
DRY_RUN=false
TAG_MESSAGE=""
FORCE=false

# 解析命令行参数
while [[ $# -gt 0 ]]; do
  case $1 in
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --message|-m)
      TAG_MESSAGE="$2"
      shift 2
      ;;
    --force|-f)
      FORCE=true
      shift
      ;;
    --help|-h)
      echo "使用方法: $0 [选项]"
      echo ""
      echo "选项:"
      echo "  --dry-run          模拟运行,不实际推送"
      echo "  --message, -m      指定 tag 消息"
      echo "  --force, -f        强制创建 tag (如果已存在则删除)"
      echo "  --help, -h         显示帮助信息"
      echo ""
      echo "示例:"
      echo "  $0"
      echo "  $0 --message '发布新版本'"
      echo "  $0 --dry-run"
      exit 0
      ;;
    *)
      echo -e "${RED}错误: 未知参数 $1${NC}"
      exit 1
      ;;
  esac
done

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  自动打 tag 并推送到 main 分支${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# 1. 检查是否在 git 仓库中
if ! git rev-parse --git-dir > /dev/null 2>&1; then
  echo -e "${RED}❌ 错误: 当前目录不是 git 仓库${NC}"
  exit 1
fi

echo -e "${GREEN}✓${NC} 当前在 git 仓库中"

# 2. 检查工作区是否干净
if [[ -n $(git status --porcelain) ]]; then
  echo -e "${YELLOW}⚠️  警告: 工作区有未提交的更改${NC}"
  git status --short
  echo ""
  read -p "是否继续? (y/N) " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${RED}已取消${NC}"
    exit 1
  fi
fi

# 3. 保存当前分支
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
echo -e "${BLUE}ℹ${NC}  当前分支: ${CURRENT_BRANCH}"

# 4. 切换到 main 分支
echo -e "${BLUE}➜${NC} 切换到 main 分支..."
if ! git checkout main; then
  echo -e "${RED}❌ 错误: 切换到 main 分支失败${NC}"
  exit 1
fi
echo -e "${GREEN}✓${NC} 已切换到 main 分支"

# 5. 拉取最新代码
echo -e "${BLUE}➜${NC} 拉取最新代码..."
if ! git pull origin main; then
  echo -e "${RED}❌ 错误: 拉取代码失败${NC}"
  git checkout "$CURRENT_BRANCH"
  exit 1
fi
echo -e "${GREEN}✓${NC} 已拉取最新代码"

# 6. 生成 tag 名称 (格式: alphaas-MMDDHHMM)
TAG_NAME="alphaas-$(date +%m%d%H%M)"
echo -e "${BLUE}ℹ${NC}  生成的 tag: ${TAG_NAME}"

# 7. 检查 tag 是否已存在
if git rev-parse "$TAG_NAME" >/dev/null 2>&1; then
  if [[ "$FORCE" == true ]]; then
    echo -e "${YELLOW}⚠️  tag ${TAG_NAME} 已存在,强制删除...${NC}"
    git tag -d "$TAG_NAME"
    if [[ "$DRY_RUN" == false ]]; then
      git push origin ":refs/tags/$TAG_NAME" 2>/dev/null || true
    fi
  else
    echo -e "${RED}❌ 错误: tag ${TAG_NAME} 已存在${NC}"
    echo -e "${YELLOW}提示: 使用 --force 参数强制覆盖${NC}"
    git checkout "$CURRENT_BRANCH"
    exit 1
  fi
fi

# 8. 创建 tag
echo -e "${BLUE}➜${NC} 创建 tag..."
if [[ -n "$TAG_MESSAGE" ]]; then
  # 带消息的 annotated tag
  git tag -a "$TAG_NAME" -m "$TAG_MESSAGE"
  echo -e "${GREEN}✓${NC} 已创建 tag: ${TAG_NAME} (消息: ${TAG_MESSAGE})"
else
  # 轻量级 tag
  git tag "$TAG_NAME"
  echo -e "${GREEN}✓${NC} 已创建 tag: ${TAG_NAME}"
fi

# 9. 推送 tag
if [[ "$DRY_RUN" == true ]]; then
  echo -e "${YELLOW}🔍 [模拟模式] 将推送 tag: ${TAG_NAME}${NC}"
  echo -e "${YELLOW}   命令: git push origin ${TAG_NAME}${NC}"
else
  echo -e "${BLUE}➜${NC} 推送 tag 到远程仓库..."
  if ! git push origin "$TAG_NAME"; then
    echo -e "${RED}❌ 错误: 推送 tag 失败${NC}"
    echo -e "${YELLOW}提示: 本地 tag 已创建,可以手动推送:${NC}"
    echo -e "${YELLOW}      git push origin ${TAG_NAME}${NC}"
    git checkout "$CURRENT_BRANCH"
    exit 1
  fi
  echo -e "${GREEN}✓${NC} 已推送 tag: ${TAG_NAME}"
fi

# 10. 切换回原来的分支
if [[ "$CURRENT_BRANCH" != "main" ]]; then
  echo -e "${BLUE}➜${NC} 切换回原分支: ${CURRENT_BRANCH}..."
  git checkout "$CURRENT_BRANCH"
  echo -e "${GREEN}✓${NC} 已切换回 ${CURRENT_BRANCH} 分支"
fi

# 11. 显示最近的 tags
echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  最近的 5 个 tags${NC}"
echo -e "${BLUE}========================================${NC}"
git tag --sort=-creatordate | grep "^alphaas-" | head -5 | while read tag; do
  commit_date=$(git log -1 --format=%ai "$tag")
  commit_msg=$(git log -1 --format=%s "$tag")
  echo -e "${GREEN}${tag}${NC}"
  echo -e "  时间: ${commit_date}"
  echo -e "  提交: ${commit_msg}"
  echo ""
done

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  ✅ 完成!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

if [[ "$DRY_RUN" == false ]]; then
  echo -e "${BLUE}Tag 信息:${NC}"
  echo -e "  名称: ${TAG_NAME}"
  echo -e "  提交: $(git rev-parse --short ${TAG_NAME})"
  echo -e "  时间: $(date)"
  echo ""
  echo -e "${YELLOW}提示: 这将触发 GitHub Actions 工作流,开始构建 Docker 镜像${NC}"
else
  echo -e "${YELLOW}🔍 这是模拟运行,未实际推送 tag${NC}"
  echo -e "${YELLOW}   删除本地 tag 命令: git tag -d ${TAG_NAME}${NC}"
fi
