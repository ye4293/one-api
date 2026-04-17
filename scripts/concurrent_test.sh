#!/usr/bin/env bash
# 并发压测脚本：专项验证 HandleKeyError 并发安全性修复
#
# 被测 Bug：多 goroutine 持有过时 Channel 快照并发写 DB → Lost Update
# 表现：坏 Key 未被正确禁用 → 后续请求继续路由到失效 Key → 返回上游 401/429
#
# 对比流程：
#   LABEL=before ./scripts/concurrent_test.sh   # 更新前跑
#   # 部署新版
#   LABEL=after  ./scripts/concurrent_test.sh   # 更新后跑，自动输出对比报告
#
# 调参：
#   CONCURRENCY=20 TOTAL=100 ./scripts/concurrent_test.sh

set -euo pipefail

# ============ 配置 ============
BASE_URL="${BASE_URL:-https://test2.ezlinkai.com}"
ENDPOINT="${ENDPOINT:-/v1/responses}"
AUTH_TOKEN="${AUTH_TOKEN:-2X8R3tkOLRWvIznmA505B583B5Af475c986a3838Dd30A811}"
MODEL="${MODEL:-gpt-5.4}"
CONCURRENCY="${CONCURRENCY:-20}"
TOTAL="${TOTAL:-60}"
LABEL="${LABEL:-}"
RESULTS_DIR="${RESULTS_DIR:-$(dirname "$0")/results}"
TIMEOUT="${TIMEOUT:-60}"

REQUEST_BODY='{
    "model": "'"$MODEL"'",
    "input": "Write a one-sentence bedtime story about a unicorn."
}'

# ============ 初始化 ============
mkdir -p "$RESULTS_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RUN_LABEL="${LABEL:-run_${TIMESTAMP}}"
OUTPUT_FILE="$RESULTS_DIR/${RUN_LABEL}.jsonl"
SUMMARY_FILE="$RESULTS_DIR/${RUN_LABEL}_summary.json"

echo "========================================"
echo "  HandleKeyError 并发安全性验证"
echo "  URL        : ${BASE_URL}${ENDPOINT}"
echo "  Model      : $MODEL"
echo "  并发数      : $CONCURRENCY"
echo "  总请求数    : $TOTAL"
echo "  Label      : $RUN_LABEL"
echo "========================================"
echo ""

# ============ 单次请求 ============
do_request() {
    local idx=$1
    local start_ns
    start_ns=$(python3 -c "import time; print(int(time.time()*1e9))" 2>/dev/null || date +%s%N)

    local tmp_body
    tmp_body=$(mktemp)

    local http_code
    http_code=$(curl -s \
        --max-time "$TIMEOUT" \
        -w "%{http_code}" \
        -o "$tmp_body" \
        -X POST \
        "${BASE_URL}${ENDPOINT}" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        --data "$REQUEST_BODY" 2>/dev/null) || http_code="000"

    local end_ns
    end_ns=$(python3 -c "import time; print(int(time.time()*1e9))" 2>/dev/null || date +%s%N)
    local elapsed_ms=$(( (end_ns - start_ns) / 1000000 ))

    local body
    body=$(cat "$tmp_body" 2>/dev/null | head -c 4000)
    rm -f "$tmp_body"

    # ── 分类错误（这是与 bug 强相关的核心指标）──
    #
    # 错误类型说明：
    #   upstream_auth    - 上游返回 401，说明坏 Key 仍在被路由（Lost Update 的直接证据）
    #   upstream_ratelimit - 上游返回 429，Key 被限流但未被禁用
    #   no_channel       - 无可用渠道（所有 Key 都坏了，渠道应被禁用）
    #   internal_error   - 服务内部 500，可能是并发写入 panic
    #   timeout          - 请求超时（锁竞争严重时可能出现）
    #   ok               - 正常
    local status="ok"
    local error_type=""
    local error_msg=""

    if [[ "$http_code" == "000" ]]; then
        status="timeout"
        error_type="timeout"
    elif [[ "$http_code" == "500" ]]; then
        status="error"
        error_type="internal_error"
        error_msg=$(echo "$body" | python3 -c "
import sys,json
try:
    d=json.loads(sys.stdin.read())
    print(d.get('error',{}).get('message','')[:100])
except: print('parse_fail')
" 2>/dev/null || echo "")
    elif [[ "$http_code" == "200" ]]; then
        # 检查响应体内是否有错误（API 有时 200 里包 error）
        local api_err
        api_err=$(echo "$body" | python3 -c "
import sys,json
try:
    d=json.loads(sys.stdin.read())
    err = d.get('error',{})
    if err:
        code = err.get('code','')
        msg = err.get('message','')[:80]
        print(f'{code}|{msg}')
    else:
        print('')
except: print('')
" 2>/dev/null || echo "")
        if [[ -n "$api_err" ]]; then
            status="api_error"
            error_type=$(echo "$api_err" | cut -d'|' -f1)
            error_msg=$(echo "$api_err" | cut -d'|' -f2-)
        fi
    elif [[ "$http_code" == "401" || "$http_code" == "403" ]]; then
        status="error"
        error_type="upstream_auth"
        error_msg=$(echo "$body" | python3 -c "
import sys,json
try:
    d=json.loads(sys.stdin.read())
    print(d.get('error',{}).get('message','')[:80])
except: print('')
" 2>/dev/null || echo "")
    elif [[ "$http_code" == "429" ]]; then
        status="error"
        error_type="upstream_ratelimit"
    elif [[ "$http_code" =~ ^4 ]]; then
        # 检查是否是"无可用渠道"类错误
        local msg
        msg=$(echo "$body" | python3 -c "
import sys,json
try:
    d=json.loads(sys.stdin.read())
    print(d.get('error',{}).get('message','')[:100])
except: print('')
" 2>/dev/null || echo "")
        if echo "$msg" | grep -qi "no channel\|no available\|渠道\|channel"; then
            status="error"
            error_type="no_channel"
            error_msg=$msg
        else
            status="error"
            error_type="http_${http_code}"
            error_msg=$msg
        fi
    fi

    # 提取成功时的 output text
    local output_text=""
    if [[ "$status" == "ok" ]]; then
        output_text=$(echo "$body" | python3 -c "
import sys,json
try:
    d=json.loads(sys.stdin.read())
    for item in d.get('output',[]):
        for c in item.get('content',[]):
            if c.get('type')=='output_text':
                print(c.get('text','')[:120])
                sys.exit(0)
    print(d.get('choices',[{}])[0].get('message',{}).get('content','')[:120])
except: print('')
" 2>/dev/null || echo "")
    fi

    # 输出 JSONL
    printf '{"idx":%d,"http_code":"%s","elapsed_ms":%d,"status":"%s","error_type":"%s","error_msg":"%s","output":"%s"}\n' \
        "$idx" "$http_code" "$elapsed_ms" "$status" "$error_type" \
        "$(echo "$error_msg" | sed 's/"/\\"/g; s/\n/ /g')" \
        "$(echo "$output_text" | sed 's/"/\\"/g; s/\n/ /g')" \
        >> "$OUTPUT_FILE"

    # 实时进度：颜色区分错误类型
    if [[ "$status" == "ok" ]]; then
        printf "\033[32m[%3d] ✓\033[0m %4dms  %s\n" "$idx" "$elapsed_ms" \
            "$(echo "$output_text" | cut -c1-50)"
    elif [[ "$error_type" == "upstream_auth" ]]; then
        printf "\033[31m[%3d] ✗ upstream_auth (坏Key仍在路由!)\033[0m  %4dms  HTTP:%s  %s\n" \
            "$idx" "$elapsed_ms" "$http_code" "$(echo "$error_msg" | cut -c1-40)"
    elif [[ "$error_type" == "upstream_ratelimit" ]]; then
        printf "\033[33m[%3d] ✗ upstream_ratelimit\033[0m  %4dms  HTTP:%s\n" \
            "$idx" "$elapsed_ms" "$http_code"
    elif [[ "$error_type" == "internal_error" ]]; then
        printf "\033[35m[%3d] ✗ INTERNAL ERROR\033[0m  %4dms  %s\n" \
            "$idx" "$elapsed_ms" "$(echo "$error_msg" | cut -c1-40)"
    elif [[ "$error_type" == "no_channel" ]]; then
        printf "\033[36m[%3d] ✗ no_channel (渠道已禁用)\033[0m  %4dms\n" \
            "$idx" "$elapsed_ms"
    else
        printf "\033[33m[%3d] ✗ %s\033[0m  %4dms  HTTP:%s  %s\n" \
            "$idx" "$error_type" "$elapsed_ms" "$http_code" \
            "$(echo "$error_msg" | cut -c1-40)"
    fi
}

export -f do_request
export BASE_URL ENDPOINT AUTH_TOKEN REQUEST_BODY TIMEOUT OUTPUT_FILE

# ============ 并发执行 ============
echo "开始并发请求..."
echo ""

seq 1 "$TOTAL" | xargs -P "$CONCURRENCY" -I{} bash -c 'do_request "$@"' _ {}

# ============ 统计分析 ============
echo ""
echo "生成分析报告..."

python3 - "$OUTPUT_FILE" "$RUN_LABEL" "$CONCURRENCY" "$TOTAL" << 'PYEOF'
import sys, json, statistics

fpath, label, concurrency, total = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]

records = []
with open(fpath) as f:
    for line in f:
        line = line.strip()
        if line:
            try:
                records.append(json.loads(line))
            except:
                pass

if not records:
    print("无数据")
    sys.exit(1)

n = len(records)
ok      = [r for r in records if r['status'] == 'ok']
errors  = [r for r in records if r['status'] != 'ok']
elapsed = [r['elapsed_ms'] for r in records]

# 按错误类型分组（核心指标）
err_by_type = {}
for r in errors:
    t = r.get('error_type', 'unknown')
    err_by_type[t] = err_by_type.get(t, 0) + 1

def pct(lst, p):
    if not lst: return 0
    s = sorted(lst)
    return s[min(int(len(s) * p / 100), len(s)-1)]

def bar(v, total, width=20):
    filled = int(v / total * width) if total else 0
    return '█' * filled + '░' * (width - filled)

upstream_auth_count    = err_by_type.get('upstream_auth', 0)
upstream_rl_count      = err_by_type.get('upstream_ratelimit', 0)
internal_err_count     = err_by_type.get('internal_error', 0)
no_channel_count       = err_by_type.get('no_channel', 0)
timeout_count          = err_by_type.get('timeout', 0)

print(f"""
========================================
  测试报告 [{label}]
========================================
总请求数    : {n}
并发数      : {concurrency}
成功        : {len(ok):3d} ({len(ok)/n*100:5.1f}%)  {bar(len(ok), n)}
失败        : {len(errors):3d} ({len(errors)/n*100:5.1f}%)

--- 错误分类（与 Bug 强相关）---""")

def show_err(name, cnt, desc):
    if cnt > 0:
        icon = '⚠ ' if cnt > 0 else '  '
        print(f"  {icon}{name:<20}: {cnt:3d} ({cnt/n*100:4.1f}%)  {desc}")
    else:
        print(f"  ✓ {name:<20}: {cnt:3d}            {desc}")

show_err('upstream_auth',    upstream_auth_count,   '← 坏Key仍在路由，Lost Update 证据')
show_err('upstream_ratelimit', upstream_rl_count,   '← Key被限流未禁用')
show_err('internal_error',   internal_err_count,    '← 服务内部 500，可能有并发 panic')
show_err('no_channel',       no_channel_count,      '← 渠道已禁用（预期行为）')
show_err('timeout',          timeout_count,         '← 超时，可能锁竞争严重')

for t, c in sorted(err_by_type.items(), key=lambda x: -x[1]):
    if t not in ('upstream_auth','upstream_ratelimit','internal_error','no_channel','timeout'):
        print(f"    {t:<22}: {c:3d} ({c/n*100:4.1f}%)")

print(f"""
--- 延迟（毫秒）---
  avg={statistics.mean(elapsed):.0f}  med={statistics.median(elapsed):.0f}  p90={pct(elapsed,90)}  p95={pct(elapsed,95)}  p99={pct(elapsed,99)}  max={max(elapsed)}

--- 问题判断 ---""")

# 判断 bug 是否存在
issues = []
if upstream_auth_count > 0:
    issues.append(f'upstream_auth={upstream_auth_count} 次：坏 Key 仍在被路由，HandleKeyError Lost Update 未修复')
if internal_err_count > 0:
    issues.append(f'internal_error={internal_err_count} 次：存在服务内部错误，可能有并发 panic')
if timeout_count > n * 0.1:
    issues.append(f'timeout={timeout_count} 次（>{n*0.1:.0f} 阈值）：可能存在锁竞争或死锁')

if issues:
    print("  ✗ 检测到潜在问题：")
    for iss in issues:
        print(f"    - {iss}")
else:
    print("  ✓ 未发现 HandleKeyError 并发 Bug 的典型特征")

if ok:
    print(f"\n--- 响应示例 ---")
    print(f"  {ok[0].get('output','')[:120]}")

print("========================================")
PYEOF

# ============ 保存摘要（供 before/after 对比用）============
python3 - "$OUTPUT_FILE" "$RUN_LABEL" > "$SUMMARY_FILE" << 'PYEOF2'
import sys, json, statistics

fpath, label = sys.argv[1], sys.argv[2]
records = []
with open(fpath) as f:
    for line in f:
        line = line.strip()
        if line:
            try:
                records.append(json.loads(line))
            except:
                pass

n = len(records)
ok = [r for r in records if r['status'] == 'ok']
elapsed = [r['elapsed_ms'] for r in records]
err_by_type = {}
for r in records:
    if r['status'] != 'ok':
        t = r.get('error_type', 'unknown')
        err_by_type[t] = err_by_type.get(t, 0) + 1

def pct(lst, p):
    if not lst: return 0
    s = sorted(lst)
    return s[min(int(len(s)*p/100), len(s)-1)]

print(json.dumps({
    "label": label,
    "total": n,
    "success": len(ok),
    "fail": n - len(ok),
    "success_rate": round(len(ok)/n*100, 1) if n else 0,
    "avg_ms": round(statistics.mean(elapsed), 0) if elapsed else 0,
    "p95_ms": pct(elapsed, 95),
    "p99_ms": pct(elapsed, 99),
    "err_by_type": err_by_type,
    # 核心 bug 指标
    "upstream_auth_count": err_by_type.get("upstream_auth", 0),
    "upstream_auth_rate": round(err_by_type.get("upstream_auth", 0)/n*100, 1) if n else 0,
    "internal_error_count": err_by_type.get("internal_error", 0),
    "timeout_count": err_by_type.get("timeout", 0),
    "sample_output": ok[0].get("output", "") if ok else ""
}, ensure_ascii=False, indent=2))
PYEOF2

echo "原始数据: $OUTPUT_FILE"
echo "摘要    : $SUMMARY_FILE"

# ============ before/after 自动对比 ============
BEFORE_SUMMARY="$RESULTS_DIR/before_summary.json"
AFTER_SUMMARY="$RESULTS_DIR/after_summary.json"

if [[ -f "$BEFORE_SUMMARY" && -f "$AFTER_SUMMARY" ]]; then
    echo ""
    python3 - "$BEFORE_SUMMARY" "$AFTER_SUMMARY" << 'PYEOF3'
import sys, json

def load(p):
    with open(p) as f: return json.load(f)

b, a = load(sys.argv[1]), load(sys.argv[2])

def row(name, bv, av, unit="", lower_better=True, highlight_zero=False):
    if not isinstance(bv, (int, float)):
        print(f"  {'':2}{name:<26}: {bv} → {av}")
        return
    delta = av - bv
    if delta == 0:
        arrow, color, rst = "→", "", ""
    elif (lower_better and delta < 0) or (not lower_better and delta > 0):
        arrow, color, rst = "↓" if lower_better else "↑", "\033[32m", "\033[0m"
    else:
        arrow, color, rst = "↑" if lower_better else "↓", "\033[31m", "\033[0m"

    pct_str = f"{abs(delta/bv*100):.1f}%" if bv else "—"
    if highlight_zero and av == 0 and bv > 0:
        flag = "  \033[32m← 问题消失！\033[0m"
    elif highlight_zero and av > 0 and bv == 0:
        flag = "  \033[31m← 新增问题\033[0m"
    else:
        flag = ""
    print(f"  {color}{arrow}{rst} {name:<26}: {bv}{unit} → {av}{unit}  ({color}{arrow}{pct_str}{rst}){flag}")

print(f"""
\033[36m========================================
  Before vs After 对比报告
  Before label: {b['label']}
  After  label: {a['label']}
========================================\033[0m

\033[1m--- 核心 Bug 指标（越低越好）---\033[0m""")

row("upstream_auth 次数",    b['upstream_auth_count'],    a['upstream_auth_count'],    "", lower_better=True, highlight_zero=True)
row("upstream_auth 比率",    b['upstream_auth_rate'],     a['upstream_auth_rate'],     "%", lower_better=True)
row("internal_error 次数",   b['internal_error_count'],   a['internal_error_count'],   "", lower_better=True, highlight_zero=True)
row("timeout 次数",          b['timeout_count'],          a['timeout_count'],          "", lower_better=True)

print(f"\n\033[1m--- 整体质量指标 ---\033[0m")
row("成功率",                b['success_rate'],           a['success_rate'],           "%", lower_better=False)
row("失败次数",              b['fail'],                   a['fail'],                   "", lower_better=True)
row("平均延迟",              b['avg_ms'],                 a['avg_ms'],                 "ms")
row("P95 延迟",              b['p95_ms'],                 a['p95_ms'],                 "ms")
row("P99 延迟",              b['p99_ms'],                 a['p99_ms'],                 "ms")

if b.get('err_by_type') or a.get('err_by_type'):
    print(f"\n\033[1m--- Before 错误分布 ---\033[0m")
    for t, c in sorted(b.get('err_by_type',{}).items(), key=lambda x: -x[1]):
        print(f"  {t:<28}: {c}")
    print(f"\n\033[1m--- After 错误分布 ---\033[0m")
    for t, c in sorted(a.get('err_by_type',{}).items(), key=lambda x: -x[1]):
        print(f"  {t:<28}: {c}")

# 最终判断
print(f"\n\033[1m--- 修复判断 ---\033[0m")
fixed = []
not_fixed = []

if b['upstream_auth_count'] > 0 and a['upstream_auth_count'] == 0:
    fixed.append("upstream_auth 归零：坏 Key 路由问题已修复")
elif b['upstream_auth_count'] > 0 and a['upstream_auth_count'] > 0:
    not_fixed.append(f"upstream_auth 仍有 {a['upstream_auth_count']} 次：Lost Update 问题可能未彻底修复")

if b['internal_error_count'] > 0 and a['internal_error_count'] == 0:
    fixed.append("internal_error 归零：并发 panic 已修复")
elif a['internal_error_count'] > 0:
    not_fixed.append(f"internal_error 仍有 {a['internal_error_count']} 次")

if fixed:
    print(f"  \033[32m✓ 已修复：\033[0m")
    for f in fixed: print(f"    - {f}")
if not_fixed:
    print(f"  \033[31m✗ 未完全修复：\033[0m")
    for nf in not_fixed: print(f"    - {nf}")
if not fixed and not not_fixed:
    if b['upstream_auth_count'] == 0:
        print("  → Before 测试期间未触发 upstream_auth（需更大并发或等待 Key 错误发生）")
    else:
        print("  → 无法判断，请检查原始数据")

print("\033[36m========================================\033[0m")
PYEOF3
fi
