#!/bin/bash
# rolling-deploy.sh - 全自动 AWS 滚动发布
#
# 用法：
#   1. 填写下方配置区的 TARGET_GROUP_ARN 和 DEPLOY_TARGETS
#   2. chmod +x rolling-deploy.sh
#   3. ./rolling-deploy.sh
#
# 支持中断续跑：进度记录在 /tmp/rolling-deploy-progress.txt
# 若需重新全量发布，删除该文件后重跑即可

set -euo pipefail

# ======================== 配置区 ========================
TARGET_GROUP_ARN="arn:aws:elasticloadbalancing:us-east-1:123456789:targetgroup/my-tg/abc123"
SSH_USER="ec2-user"
DOCKER_COMPOSE_DIR="/opt/app"
DRAIN_WAIT=300          # 摘除后等待排空时间（秒）
HEALTH_CHECK_TIMEOUT=120 # 重新加入后等待健康检查超时（秒）
HEALTH_CHECK_INTERVAL=10 # 健康检查轮询间隔（秒）

# 格式："instance-id|ssh-key-path"，按此顺序依次发布
DEPLOY_TARGETS=(
  "i-0a1b2c3d4e5f|~/.ssh/server1.pem"
  "i-1a2b3c4d5e6f|~/.ssh/server2.pem"
  "i-2a3b4c5d6e7f|~/.ssh/server3.pem"
)

PROGRESS_FILE="/tmp/rolling-deploy-progress.txt"
# ========================================================

log()     { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }
log_ok()  { echo "[$(date '+%Y-%m-%d %H:%M:%S')] ✅ $*"; }
log_err() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] ❌ $*" >&2; }

get_instance_ip() {
  local instance_id=$1
  aws ec2 describe-instances \
    --instance-ids "$instance_id" \
    --query 'Reservations[0].Instances[0].PrivateIpAddress' \
    --output text
}

get_target_state() {
  local instance_id=$1
  aws elbv2 describe-target-health \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --targets "Id=$instance_id" \
    --query 'TargetHealthDescriptions[0].TargetHealth.State' \
    --output text 2>/dev/null || echo "unknown"
}

# 等待摘除完成（状态变为 unused）
# 超过 DRAIN_WAIT 秒后，将 deregistration_delay 临时设为 0 强制排空
wait_for_deregister() {
  local instance_id=$1
  local waited=0

  while [ $waited -lt $DRAIN_WAIT ]; do
    local state
    state=$(get_target_state "$instance_id")
    log "  $instance_id 摘除状态: $state（已等待 ${waited}s）"

    [[ "$state" == "unused" ]] && { log_ok "$instance_id 已完全摘除"; return 0; }

    sleep $HEALTH_CHECK_INTERVAL
    waited=$((waited + HEALTH_CHECK_INTERVAL))
  done

  # 超时：将 deregistration_delay 临时设为 0，强制排空存量连接
  log "⚠️  等待 ${DRAIN_WAIT}s 仍未摘除，执行强制排空..."
  local original_delay
  original_delay=$(aws elbv2 describe-target-group-attributes \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --query 'Attributes[?Key==`deregistration_delay.timeout_seconds`].Value' \
    --output text)

  aws elbv2 modify-target-group-attributes \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --attributes Key=deregistration_delay.timeout_seconds,Value=0 > /dev/null

  # 等待实际生效
  local force_waited=0
  while [ $force_waited -lt 30 ]; do
    local state
    state=$(get_target_state "$instance_id")
    log "  强制排空中，状态: $state"
    [[ "$state" == "unused" ]] && break
    sleep 5
    force_waited=$((force_waited + 5))
  done

  # 恢复原始排空时间
  aws elbv2 modify-target-group-attributes \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --attributes Key=deregistration_delay.timeout_seconds,Value="$original_delay" > /dev/null
  log "  已恢复 deregistration_delay 为 ${original_delay}s"
}

wait_for_healthy() {
  local instance_id=$1
  local waited=0
  while [ $waited -lt $HEALTH_CHECK_TIMEOUT ]; do
    local state
    state=$(get_target_state "$instance_id")
    log "  $instance_id 健康状态: $state"
    [[ "$state" == "healthy" ]] && return 0
    sleep $HEALTH_CHECK_INTERVAL
    waited=$((waited + HEALTH_CHECK_INTERVAL))
  done
  return 1
}

is_processed() { [ -f "$PROGRESS_FILE" ] && grep -qx "$1" "$PROGRESS_FILE"; }
mark_processed() { echo "$1" >> "$PROGRESS_FILE"; }

deploy_on_server() {
  local ip=$1 key=$2
  key="${key/#\~/$HOME}"
  log "SSH 连接 $ip（密钥: $key）..."
  ssh -o StrictHostKeyChecking=no \
      -o ConnectTimeout=30 \
      -i "$key" \
      "$SSH_USER@$ip" \
      "cd $DOCKER_COMPOSE_DIR && \
       docker compose pull && \
       docker compose up -d --remove-orphans && \
       docker image prune -f"
}

# ======================== 主流程 ========================
TOTAL=${#DEPLOY_TARGETS[@]}
log "=========================================="
log "开始滚动发布，共 $TOTAL 台，按配置顺序执行"
log "=========================================="

DONE=0
for entry in "${DEPLOY_TARGETS[@]}"; do
  IFS='|' read -r instance_id ssh_key <<< "$entry"
  DONE=$((DONE + 1))

  if is_processed "$instance_id"; then
    log "⏭️  [$DONE/$TOTAL] $instance_id 已处理，跳过"
    continue
  fi

  # 实时获取私有 IP（避免 IP 变化问题）
  ip=$(get_instance_ip "$instance_id")
  log "------------------------------------------"
  log "[$DONE/$TOTAL] $instance_id | $ip | $ssh_key"
  log "------------------------------------------"

  # 1. 摘除
  log "摘除 $instance_id 从目标组..."
  aws elbv2 deregister-targets \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --targets "Id=$instance_id"

  # 2. 等待连接排空（超时后自动强制排空）
  log "等待排空（最长 ${DRAIN_WAIT}s，超时后强制排空）..."
  wait_for_deregister "$instance_id"

  # 3. SSH 拉取最新镜像并重启
  deploy_on_server "$ip" "$ssh_key"

  # 4. 重新加入目标组
  log "将 $instance_id 加回目标组..."
  aws elbv2 register-targets \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --targets "Id=$instance_id"

  # 5. 等待健康检查通过（失败则终止，防止滚雪球式故障）
  log "等待健康检查（最长 ${HEALTH_CHECK_TIMEOUT}s）..."
  wait_for_healthy "$instance_id" || {
    log_err "$instance_id 健康检查失败，终止发布！"
    log_err "剩余实例未更新，请手动排查后继续（已完成实例记录在 $PROGRESS_FILE）"
    exit 1
  }

  mark_processed "$instance_id"
  log_ok "[$DONE/$TOTAL] $instance_id 完成"
done

rm -f "$PROGRESS_FILE"
log_ok "=========================================="
log_ok "全部 $TOTAL 台实例发布完成！"
log_ok "=========================================="
