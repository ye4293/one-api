#!/usr/bin/env bash
# 本地启动 Prometheus，抓取跑在宿主机上的 one-api。
#
# 解决两个本地环境的坑：
#
#  1. host.docker.internal 不一定可用。在部分 Docker Desktop 配置下它解析成
#     198.18.0.10 但完全不可达（curl 超时，而 busybox 的 nc -z 会假阳性报"可达"，
#     排查时极易被误导）。本脚本自动探测并回落到宿主机 LAN IP。
#
#  2. Prometheus 不展开配置文件里的 ${ENV_VAR}。写 credentials: '${METRICS_TOKEN}'
#     会把这 17 个字符原样当 token 发出去 → target DOWN + 401，而配置看着完全正确、
#     promtool check config 也通过。所以本脚本直接生成含真实 token 的临时配置。
#
# 用法：
#   # 终端 1：起 one-api
#   METRICS_ENABLED=true METRICS_PORT=9099 METRICS_TOKEN=devtoken go run .
#   # 终端 2：起 Prometheus
#   ./deploy/prometheus/run-local.sh
#
# 然后打开 http://localhost:19090

set -euo pipefail

METRICS_PORT="${METRICS_PORT:-9099}"
METRICS_TOKEN="${METRICS_TOKEN:-devtoken}"
PROM_PORT="${PROM_PORT:-19090}"
PROM_IMAGE="${PROM_IMAGE:-prom/prometheus:v3.7.3}"

# ── 1. 确认 one-api 的 /metrics 在宿主机上活着 ──────────────────────
if ! curl -sf -o /dev/null -H "Authorization: Bearer ${METRICS_TOKEN}" \
      "http://127.0.0.1:${METRICS_PORT}/metrics"; then
  echo "✗ 宿主机 127.0.0.1:${METRICS_PORT}/metrics 不可达或 token 不对。" >&2
  echo "  先启动 one-api：" >&2
  echo "    METRICS_ENABLED=true METRICS_PORT=${METRICS_PORT} METRICS_TOKEN=${METRICS_TOKEN} go run ." >&2
  exit 1
fi
echo "✓ one-api /metrics 就绪（宿主机 127.0.0.1:${METRICS_PORT}）"

# ── 2. 探测容器能用哪个地址访问宿主机 ────────────────────────────────
probe() {
  docker run --rm curlimages/curl:latest -sf -o /dev/null --max-time 4 \
    -H "Authorization: Bearer ${METRICS_TOKEN}" \
    "http://$1:${METRICS_PORT}/metrics" >/dev/null 2>&1
}

HOST_ADDR=""
if probe host.docker.internal; then
  HOST_ADDR="host.docker.internal"
else
  echo "  host.docker.internal 不可达，回落到 LAN IP…"
  for iface in en0 en1 en2; do
    ip="$(ipconfig getifaddr "$iface" 2>/dev/null || true)"
    [ -n "$ip" ] || continue
    if probe "$ip"; then HOST_ADDR="$ip"; break; fi
  done
fi

if [ -z "$HOST_ADDR" ]; then
  echo "✗ 容器无法访问宿主机的 ${METRICS_PORT} 端口。" >&2
  echo "  排查方向：macOS 应用防火墙是否拦了入站连接；Docker Desktop 的网络模式。" >&2
  echo "  替代方案：brew install prometheus，直接在宿主机跑，绕开容器网络。" >&2
  exit 1
fi
echo "✓ 容器可通过 ${HOST_ADDR} 访问宿主机"

# ── 3. 生成临时配置（token 写字面量，因为 Prometheus 不展开环境变量）──
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# 告警与 recording 规则的**唯一真相源在 monitor 仓库**（~/code/monitor/prometheus/），
# 本仓库刻意不保留副本 —— 两边各放一份必然漂移，而漂移的症状是"本地测过的规则
# 上线后行为不同"，极难排查。
#
# 所以这里从 monitor 已发布的镜像里取：拿到的永远是发布过的那一份，物理上无法漂移。
# 拉不到镜像时降级为"只验证 scrape、不加载规则" —— 本地开发多数时候只想看指标有没有上来。
RULES_IMAGE="${RULES_IMAGE:-${DOCKER_USERNAME:-ye4293xx7}/monitor-prometheus:latest}"
RULE_FILES_BLOCK=""
if docker pull -q "$RULES_IMAGE" >/dev/null 2>&1; then
  cid="$(docker create "$RULES_IMAGE")"
  docker cp "$cid:/etc/prometheus/alerts.yml" "$TMPDIR/" 2>/dev/null || true
  docker cp "$cid:/etc/prometheus/rules.yml"  "$TMPDIR/" 2>/dev/null || true
  docker rm -f "$cid" >/dev/null 2>&1 || true
  if [ -f "$TMPDIR/alerts.yml" ] && [ -f "$TMPDIR/rules.yml" ]; then
    # rules.yml 必须排在前面：alerts.yml 里多条告警引用了它定义的 record
    RULE_FILES_BLOCK=$'rule_files:\n  - /etc/prometheus/rules.yml\n  - /etc/prometheus/alerts.yml'
    echo "✓ 规则已从 ${RULES_IMAGE} 取出（唯一真相源：monitor 仓库）"
  fi
fi
if [ -z "$RULE_FILES_BLOCK" ]; then
  echo "⚠ 拉不到 ${RULES_IMAGE}，本次只验证 scrape，不加载告警规则"
  echo "  需要验证规则请在 ~/code/monitor 执行 make prom-check"
fi

cat > "$TMPDIR/prometheus.yml" <<EOF
global:
  scrape_interval: 5s
  evaluation_interval: 5s
${RULE_FILES_BLOCK}
scrape_configs:
  - job_name: one-api
    static_configs:
      - targets: ['${HOST_ADDR}:${METRICS_PORT}']
    authorization:
      type: Bearer
      credentials: '${METRICS_TOKEN}'
  - job_name: prometheus
    static_configs:
      - targets: ['localhost:9090']
EOF

# ── 4. 起容器 ────────────────────────────────────────────────────
docker rm -f one-api-prom-local >/dev/null 2>&1 || true
docker run -d --name one-api-prom-local -p "${PROM_PORT}:9090" \
  -v "$TMPDIR:/etc/prometheus:ro" \
  "$PROM_IMAGE" \
  --config.file=/etc/prometheus/prometheus.yml \
  --storage.tsdb.retention.time=2d >/dev/null

echo "  等待首次抓取…"
for i in $(seq 1 20); do
  sleep 2
  health="$(curl -s "http://127.0.0.1:${PROM_PORT}/api/v1/targets" 2>/dev/null \
    | python3 -c "import json,sys;d=json.load(sys.stdin);print(next((t['health'] for t in d['data']['activeTargets'] if t['labels']['job']=='one-api'),'unknown'))" 2>/dev/null || echo pending)"
  [ "$health" = "up" ] && break
done

echo
if [ "${health:-}" = "up" ]; then
  echo "✓ Prometheus 已就绪，target UP"
else
  echo "✗ target 未 UP（当前 health=${health:-unknown}），看错误："
  curl -s "http://127.0.0.1:${PROM_PORT}/api/v1/targets" \
    | python3 -c "import json,sys;d=json.load(sys.stdin);[print('   ',t['labels']['job'],t['health'],t.get('lastError','')) for t in d['data']['activeTargets']]" 2>/dev/null
fi
echo
echo "  Prometheus UI : http://localhost:${PROM_PORT}"
echo "  Targets       : http://localhost:${PROM_PORT}/targets"
echo "  Alerts        : http://localhost:${PROM_PORT}/alerts"
echo "  停止          : docker rm -f one-api-prom-local"
