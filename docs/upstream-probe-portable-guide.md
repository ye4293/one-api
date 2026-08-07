# 上游模型探针系统 — 可移植实现指南

> 目标读者：需要移植到自有供货商系统，一键获取上游真实可用模型的开发者。
> 涵盖平台：OpenAI、Gemini、Claude/Anthropic、AWS Bedrock、Vertex AI、兼容 OpenAI 的中转平台。

---

## 一、系统总览

整个上游模型探针由三层组成：

```
┌─────────────────────────────────────────────────────────┐
│  Layer 1: 模型列表拉取（知道上游有哪些模型）                │
│    - 各平台 API 获取模型列表                               │
│    - 命名空间归一化（Bedrock ID ↔ 本地短名）               │
├─────────────────────────────────────────────────────────┤
│  Layer 2: Diff 计算（本地 vs 上游差异）                    │
│    - pendingAdd: 上游有、本地无                            │
│    - pendingRemove: 本地有、上游无                         │
│    - 忽略列表 + redirect 保护 + 安全兜底                   │
├─────────────────────────────────────────────────────────┤
│  Layer 3: 可用性探针（真实请求验证模型是否可用）             │
│    - 发最小 chat 请求                                     │
│    - 六态判决系统                                         │
│    - 处置矩阵决定自动操作                                  │
└─────────────────────────────────────────────────────────┘
```

---

## 二、各平台模型列表拉取

### 2.1 OpenAI 及兼容平台

```
GET {base_url}/v1/models
Authorization: Bearer {api_key}

响应格式（标准）:
{
  "data": [
    {"id": "gpt-4o", "object": "model", "owned_by": "openai"},
    ...
  ]
}
```

**移植要点**：
- 所有 OpenAI 兼容中转站用同一格式
- `base_url` 末尾需去掉 `/`，再拼 `/v1/models`
- 多 key 渠道取第一个 key 拉列表即可

### 2.2 Gemini (Google AI Studio)

```
GET {base_url}/v1beta/openai/models
Authorization: Bearer {api_key}

或直接用 Gemini 原生 API:
GET https://generativelanguage.googleapis.com/v1beta/models?key={api_key}&pageSize=1000
```

**移植要点**：
- 原生 API 返回 `{"models": [{"name": "models/gemini-2.5-pro"}, ...], "nextPageToken": "..."}`
- 需要剥掉 `models/` 前缀
- 支持分页（`pageToken` 参数）

### 2.3 Claude / Anthropic

```
GET {base_url}/v1/models
x-api-key: {api_key}
anthropic-version: 2023-06-01

备用认证（部分中转站需要）:
Authorization: Bearer {api_key}
```

**移植要点**：
- Anthropic 官方用 `x-api-key` 头，不是 `Authorization: Bearer`
- 返回格式与 OpenAI 一致

### 2.4 AWS Bedrock

```go
// 方式一：AKSK（静态凭证）
client := bedrock.New(bedrock.Options{
    Region:      region,  // "us-east-1"
    Credentials: aws.NewCredentialsCache(
        credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
    ),
})

// 方式二：Bearer Token（API Gateway 代理）
client := bedrock.New(bedrock.Options{
    Region: region,
    BearerAuthTokenProvider: bearer.TokenProviderFunc(func(ctx context.Context) (bearer.Token, error) {
        return bearer.Token{Value: token}, nil
    }),
    AuthSchemePreference: []string{"httpBearerAuth"},
})

// 调用
output, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{
    ByProvider: aws.String("Anthropic"),
})
```

**移植要点**：
- 返回 Bedrock 原生 ID（如 `anthropic.claude-opus-4-6-v1`），非本地短名
- 需跳过 `ModelLifecycle.Status == Legacy` 的条目
- 支持两种凭证格式：`token|region` 或 `accessKey|secretKey|region`
- Mantle 代理端点走 `/v1/models`（OpenAI 格式）

### 2.5 Vertex AI (Google Cloud)

**API Key 模式（Gemini API）**:
```
GET https://generativelanguage.googleapis.com/v1beta/models?key={api_key}&pageSize=1000
```

**Service Account 模式（Vertex AI Platform）**:
```
GET https://{region}-aiplatform.googleapis.com/v1/projects/{project_id}/locations/{region}/publishers/google/models
Authorization: Bearer {oauth_token}
```

**OAuth Token 获取流程**：
```
1. 解析 Service Account JSON → 提取 client_email, private_key, project_id
2. 构造 JWT:
   - iss: client_email
   - scope: "https://www.googleapis.com/auth/cloud-platform"
   - aud: "https://oauth2.googleapis.com/token"
   - iat: now, exp: now + 3600
   - 用 private_key (RSA) 签名
3. POST https://oauth2.googleapis.com/token
   grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion={jwt}
4. 获取 access_token（有效期 ~3600s，建议 5min 提前刷新）
```

**移植要点**：
- Vertex AI 只列出 `publishers/google/models`，不含 Anthropic 模型
- 因此本地的 `claude-*` 模型需要豁免删除保护
- 响应字段根据端点不同：Gemini API 用 `models`，Vertex 用 `publisherModels`
- 模型名需剥掉 `publishers/google/models/` 前缀

---

## 三、命名空间归一化（AWS Bedrock 专属问题）

### 3.1 问题

| 维度 | 本地存储 | 上游返回 |
|------|---------|---------|
| 名称 | `claude-opus-4-6` | `anthropic.claude-opus-4-6-v1` |
| 变体 | `claude-opus-4-6-thinking` | 同一个 `anthropic.claude-opus-4-6-v1` |
| 跨区 | — | `us.anthropic.claude-opus-4-6-v1` |

如果用裸字符串比较，同一模型会同时出现在「新增」和「待删」两侧。

### 3.2 解决方案：Canonical 域

```
CanonicalAwsModelID(name):
  1. 直查 AwsModelIDMap（短名 → Bedrock ID）→ 命中就返回
  2. 剥掉区域前缀（us./eu./apac./ap./global.）
  3. 剥掉 -thinking 后缀
  4. 再查 AwsModelIDMap → 命中就返回
  5. 返回剥掉后的字符串（表外模型原样透出）
```

区域前缀列表：`us.`, `eu.`, `apac.`, `ap.`, `global.`

### 3.3 反向映射（展示名）

pendingAdd 上报时需要从 Bedrock ID 翻译回本地短名：

```
awsCanonicalToDisplay[bedrockID] → 优先非 -thinking 键，同类取字典序最小
```

### 3.4 注意事项

- `-thinking` 变体与基础模型共享同一 Bedrock ID（设计如此）
- `claude-v2` 与 `claude-v2:1` 是不同模型，不能做「剥掉 `:数字` 后缀」的优化
- ARN 格式（`arn:aws:bedrock:...`）只剥 `-thinking`，不剥区域前缀

---

## 四、Diff 计算逻辑

### 4.1 核心算法

```python
def compute_diff(channel_type, local_models, upstream_models, ignored, model_mapping):
    canonical, display = get_canonicalizers(channel_type)
    
    # 1. 覆盖集 = 本地模型的 canonical ∪ redirect target 的 canonical
    covered = {canonical(m) for m in local_models}
    covered |= {canonical(tgt) for tgt in model_mapping.values()}
    
    # 2. 上游 canonical 集合
    upstream_set = {canonical(m) for m in upstream_models}
    
    # 3. pendingAdd: 上游有但本地未覆盖、且未被忽略
    pending_add = []
    for m in upstream_models:
        if canonical(m) not in covered and not is_ignored(m):
            pending_add.append(display(m))
    pending_add = deduplicate(pending_add)
    
    # 4. pendingRemove: 本地有但上游无（排除 redirect source、保护列表、忽略列表）
    pending_remove = []
    for m in local_models:
        if m in redirect_sources: continue
        if removal_protected(channel_type, m): continue
        if is_ignored(m): continue
        if canonical(m) not in upstream_set:
            pending_remove.append(m)  # 保持原始名！
    
    return pending_add, pending_remove
```

### 4.2 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| pendingAdd 用什么名字上报 | display（本地短名） | 管理员看得懂、后续入库是短名 |
| pendingRemove 用什么名字上报 | 原始本地名（不翻译） | 下游 apply 用字面匹配删除，翻译后删不掉 |
| 忽略列表匹配哪些形式 | original + display | 不匹配 canonical，防止 `regex:anthropic\.` 误伤所有模型 |
| redirect source 是否归一化 | 否 | source 是面向客户端的名字，不属于 Bedrock 域 |
| Vertex AI claude-* 模型 | 豁免删除 | 因为列表 API 根本不返回 Anthropic 模型 |

### 4.3 忽略列表

支持两种格式：
- 字面值：`text-embedding-3-small`
- 正则表达式：`regex:^text-embedding-`

同时保护 add 和 remove 两个方向。

---

## 五、可用性探针

### 5.1 探针请求构造

```json
POST /v1/chat/completions
{
  "model": "{mapped_model_name}",
  "messages": [{"role": "user", "content": "hi"}],
  "stream": false,
  "max_tokens": 16
}
```

**特殊 max_tokens**：
- Claude thinking 模型：1200（thinking budget 需 > 1024）
- OpenAI 推理模型（o1/o3 系列）：1000（需覆盖 reasoning tokens）
- 其他模型：16（最小化成本）

**关键**：套用 `model_mapping` 发请求，验证的是用户真实请求路径。

### 5.2 六态判决系统

```
┌──────────────┬────────────────────────────────────────────────────────┐
│ Verdict      │ 触发条件                                               │
├──────────────┼────────────────────────────────────────────────────────┤
│ alive        │ 请求成功，usage.total_tokens > 0                       │
│ not_found    │ 上游明确说模型不存在（见下方分类条件）                    │
│ rate_limited │ HTTP 429                                               │
│ unavailable  │ HTTP 503                                               │
│ inconclusive │ 网络错误/超时/鉴权失败/内容审核/无法判断                  │
│ skipped      │ 模型类型不支持探测（embedding/TTS/image/Codex）          │
└──────────────┴────────────────────────────────────────────────────────┘
```

### 5.3 not_found 判定的严格条件（全部满足才判）

1. `apiErr` 非空 **且** `bodyParsed == true`（上游 body 真的被解析了）
2. 满足以下任一：
   - HTTP 404 + 非空错误消息
   - `error.code`/`error.type` 命中封闭枚举：`model_not_found`, `model_not_exist`, `model_not_support`, `invalid_model`, `modelnotfound`
   - 错误消息命中关键词：`"does not exist"`, `"no such model"`, `"unknown model"`, `"invalid model"`, `"unsupported model"` 等
3. **非多 key 渠道**（多 key 时"模型不存在"与"此 key 无权限"不可区分，降级为 inconclusive）

### 5.4 为什么要自己解析 body

relay 层的 `RelayErrorHandler` 在解析失败时会编造兜底文案，如：
> "资源未找到 (404): 请求的端点或模型不存在"

这个兜底文案含"模型不存在"四字，会命中关键词白名单。如果不做独立解析，base_url 配错（所有请求返回 404）会被误判为所有模型 not_found，一轮删光整个渠道。

**解决**：探针自己用 `parseProbeUpstreamError` 解析上游 body，JSON 解析失败返回 `bodyParsed=false`，强制走 `inconclusive`。

### 5.5 处置矩阵

```
                alive  not_found  unavailable  rate_limited  inconclusive  skipped
pending_add      ✓       ✗          ✗            ✗             ✗           ✓
pending_remove   ✗       ✓          ✓            ✗             ✗           ✓
```

核心原则：
- `inconclusive` / `rate_limited` → 两方向都不动（维持现状最安全）
- `skipped` → 两方向都批准（探针无能力，信任上游）
- `unavailable` 准删：上游 /v1/models 已不列它 + 503，双重证据指向下线
- `rate_limited` 不加也不删：限流可能发生在模型路由之前，不是可用性证据

---

## 六、预算与安全控制

### 6.1 三层预算

| 层级 | 配置项 | 默认值 | 作用 |
|------|--------|--------|------|
| 全局每轮 | `UpstreamModelProbeMaxPerRound` | 200 | 一轮巡检总探测次数上限 |
| 单渠道次数 | `UpstreamModelProbeMaxPerChannel` | 30 | 单渠道每轮探测次数 |
| 单渠道时长 | `UpstreamModelProbeChannelBudgetSecs` | 120s | 单渠道总时长硬切 |
| 单次超时 | `UpstreamModelProbeTimeoutSeconds` | 10s | 单模型探测超时 |

超预算的模型按 `inconclusive` 处理（不动，安全）。

### 6.2 批量删除保护

```go
func shouldBlockBulkRemove(localCount, removeCount int) bool {
    if localCount < MinLocalModels: return false  // 小渠道放行
    return removeCount * 100 / localCount > GuardPercent  // 默认 >50% 拦下
}
```

防止上游 API 临时故障（返回空列表）导致批量误删。小渠道（<5 个模型）不启用保护，因为「全删 → 自动禁用渠道」是正常流程。

### 6.3 手动探针隔离

- 用 Redis 标记防止两人同时探同一渠道
- 手动探针不触碰定时任务预算
- 手动探测期间定时任务暂停对该渠道的自动应用
- 只允许探测 pending 列表中的模型（防止注入任意模型名的付费请求攻击）

---

## 七、定时巡检调度

```
启动 → 立即执行一轮 → 每 15 秒检查是否到达下一轮间隔
                              ↓
                   间隔到达 → 执行一轮
                              ↓
                   分批加载渠道（100 个/批，游标分页）
                              ↓
                   并行处理渠道（默认 5 并发）
                              ↓
                   每渠道：检测 → diff → 探针 → 自动应用
                              ↓
                   刷新缓存 → 记录统计
```

配置：
- 默认间隔：30 分钟
- 最小检测冷却：300 秒（同一渠道不重复检测）
- 仅 master 节点执行
- `atomic.Bool` CAS 防止并发执行

---

## 八、遇到的问题及解决方案

### 问题 1：Bedrock 命名空间不匹配（本次核心修复）

**现象**：`claude-opus-4-6`（本地）与 `anthropic.claude-opus-4-6-v1`（上游）被视为不同模型，每轮巡检都产生虚假的 add + remove。

**根因**：diff 函数做裸字符串比较，不知道 relay 层的 `AwsModelIDMap` 映射。

**解法**：引入 canonical 域归一化。在比较前把两种形式都转为同一 canonical 形式。

### 问题 2：Vertex AI 的 claude 模型每轮被判待删

**现象**：Vertex AI 列表 API 只返回 Google 发布者的模型，不含 Anthropic。本地的 claude 模型每轮都进 pendingRemove。

**解法**：加 `upstreamRemovalProtected(channelType, model)` 安全兜底，Vertex + `claude-*` 前缀 → 豁免删除。代价：真正下线的 claude 模型不会被自动清理，需手工移除。

### 问题 3：探针耗时恒为 0

**现象**：`probeResult.Duration` 永远是 0。

**根因**：Go 的 `defer` 写入无名返回值时，`return res` 先拷贝值到返回槽，defer 改的是已废弃的局部副本。

**解法**：把返回值改为命名返回值 `(res probeResult)`，defer 直接操作返回槽。

### 问题 4：RelayErrorHandler 编造兜底文案导致批量误删

**现象**：base_url 配错时所有请求返回 404 + relay 编造的"模型不存在"文案 → 所有模型判为 not_found → 一轮删光。

**解法**：探针不复用 `RelayErrorHandler`，自己用 `parseProbeUpstreamError` 解析上游 body；JSON 解析失败即 `bodyParsed=false` → 强制 `inconclusive`。

### 问题 5：429 的语义歧义

**现象**：429 到底是"模型可用只是限流"还是"网关在模型路由前就拒了"？

**解法**：
- pendingRemove 方向：不删（限流说明模型被认识，删了是误伤）
- pendingAdd 方向：不加（限流检查可能在模型路由前，不是可用性证据）
- 两方向都暂缓，下轮重试

### 问题 6：多 key 渠道的 not_found 不可信

**现象**：OpenAI 把"模型不存在"和"此 key 无权限"合成同一句错误消息。探针只用了一个 key 就判定 not_found，误删其他 key 能服务的模型。

**解法**：`isMultiKey == true` 时，把 not_found 降级为 inconclusive（不动，下轮重试）。

### 问题 7：渠道级中止的过度杀伤

**早期设计**：遇到 401/403/402 就中止整个渠道的探测。

**问题**：403 经常是模型级权限（某些模型需组织验证），中止会误伤同渠道其他正常模型。

**解法**：彻底移除渠道级中止。资源控制完全交给预算系统（次数 + 时长），极端情况下 key 失效的渠道只浪费 30 次配额（请求立即返回，不占时长），代价可接受。

### 问题 8：`-thinking` 变体保护

**现象**：本地有 `claude-opus-4-6-thinking`、上游返回 `anthropic.claude-opus-4-6-v1`（同一 canonical），若不处理会被判为覆盖。

**解法**：canonical 是"模型"级别，不区分 thinking/非 thinking。`-thinking` 变体与基础模型共享 canonical → 上游有基础 ID 时，thinking 变体也算已覆盖，不被删除。副作用：不会提示新增 `claude-opus-4-6`（thinking 已覆盖了该 canonical 位），这是 feature 不是 bug。

---

## 九、移植检查清单

移植到供货商系统时，按此顺序实现：

### Phase 1：模型列表拉取

- [ ] OpenAI 兼容：`GET /v1/models`
- [ ] Gemini：`GET /v1beta/openai/models` 或原生 API
- [ ] Anthropic：`GET /v1/models` + `x-api-key` 头
- [ ] AWS Bedrock：`ListFoundationModels` SDK 调用
- [ ] Vertex AI：API Key 模式 + Service Account JWT 模式
- [ ] 超时控制（30 秒）

### Phase 2：Diff 计算

- [ ] 归一化函数（至少处理 Bedrock 的短名/原生 ID/区域前缀）
- [ ] 覆盖集构建（本地 + redirect target）
- [ ] 忽略列表（字面 + regex）
- [ ] 保护列表（Vertex claude-*）
- [ ] 批量删除保护阈值

### Phase 3：可用性探针

- [ ] 最小 chat 请求构造（max_tokens 按模型类型适配）
- [ ] 六态判决分类
- [ ] not_found 的严格条件（bodyParsed、封闭枚举、多 key 降级）
- [ ] 独立 body 解析（不复用 relay 层的 fallback）
- [ ] 超时控制（10 秒/单模型）

### Phase 4：预算与安全

- [ ] 三层预算（全局/渠道/单次）
- [ ] 批量删除保护
- [ ] 定时调度 + CAS 互斥
- [ ] 手动/自动隔离

---

## 十、核心数据结构参考

```go
// 渠道配置（存储在 channel.settings JSON 中）
type ChannelOtherSettings struct {
    UpstreamModelUpdateCheckEnabled         bool     // 是否启用同步
    UpstreamModelUpdateAutoSyncEnabled      bool     // 自动新增
    UpstreamModelUpdateAutoDeleteEnabled    bool     // 自动删除
    UpstreamModelUpdateLastCheckTime        int64    // 上次检测时间
    UpstreamModelUpdateLastDetectedModels   []string // 待新增队列
    UpstreamModelUpdateLastRemovedModels    []string // 待删除队列
    UpstreamModelUpdateIgnoredModels        []string // 忽略列表
    UpstreamModelProbeDisabled              bool     // 禁用探针
}

// 探针结果
type ProbeResult struct {
    Model       string  // 本地模型名
    MappedModel string  // 映射后发给上游的名字
    Verdict     string  // alive/not_found/rate_limited/unavailable/inconclusive/skipped
    StatusCode  int     // 上游 HTTP 状态码
    Duration    float64 // 耗时（秒）
    Message     string  // 错误消息
}
```

---

## 十一、源码文件索引

| 文件 | 职责 |
|------|------|
| `controller/channel_upstream_update.go` | diff 计算、自动应用、定时任务调度 |
| `controller/channel_upstream_probe.go` | 探针引擎：verdict 分类、真实请求执行、预算管理 |
| `controller/channel_upstream_probe_manual.go` | 手动探针 API handler |
| `controller/channel_upstream_fetch_providers.go` | AWS Bedrock / Vertex AI 专用拉取 |
| `controller/channel.go` (1570-1834) | 通用模型列表拉取（OpenAI/Gemini/Anthropic/Ali） |
| `controller/channel-test.go` (36-154) | `buildTestRequest`、模型类型判定 |
| `relay/channel/aws/claude/canonical.go` | Bedrock 命名归一化 |
| `relay/channel/aws/claude/main.go` | `AwsModelIDMap`（短名→Bedrock ID 映射表） |
| `common/config/config.go` | 全局配置变量 |
| `common/config/channel_other_settings.go` | 渠道级设置结构体 |
