# 修复 model_mapping 场景下动态优先级打点 model 名错配

## 背景与目标

### 现象
生产环境 model 视图 API 返回中，AWS Bedrock 渠道（如 `mi-aws-yl-*` 命名的 channel_type=33 渠道）大量出现 `dynamic_priority: 0`，尽管这些渠道在过去 1 小时都有 300+ 次真实请求（log 表可验证），远超 `MinSampleCount=20` 阈值，理应有非零动态优先级分数。

### 诊断证据链

1. **代码证据 A**（`model/ability.go:130`）：`addAbilitiesTx` 里 `Model: model` 直接来自 `channel.Models`，即用户在渠道配置页面填的**原始 model 名**（如 `claude-opus-4-7`）。

2. **代码证据 B**（`relay/util/relay_meta.go:92-97`）：`BillingModelName()` 优先返回 `ActualModelName`，即 **model_mapping 生效后**上游实际请求的模型名（如 `global.anthropic.claude-opus-4-7`）。

3. **代码证据 C**（原 `model/log.go:137-144`）：`RecordAbilityMetric` 的 `Model` 字段是 `RecordConsumeLog*` 函数入参 `modelName`，而 `relay/controller/helper.go:378` 传入的是 `logModelName = BillingModelName()`（映射后名）。

4. **代码证据 D**（`controller/relay.go:181, 287, 295, 299`）：**失败路径** `processChannelRelayError(..., originalModel)` 传入的是原始名，`relay.go:771` 用它写入 Redis。**成功和失败用了不同的 model 名 key**。

5. **代码证据 E**（`controller/dynamic_priority.go:232-246` + `238`）：`loadAbilityCandidates` 从 abilities 表读候选，即拿到原始 model 名，然后用它去 Redis `ability_metrics:{channelId}:{model}` 拉窗口——**读的是原始名 key**。

6. **Redis 实测证据**（生产 ElastiCache Serverless）：
   ```
   ability_metrics:79726:claude-opus-4-7                           ← 原始名，仅存失败样本
   ability_metrics:79726:global.anthropic.claude-opus-4-7           ← 映射后名，存了大量成功样本
   ability_metrics:79866:global.anthropic.claude-opus-4-7           ← 只有映射后名 key（从未失败过）
   ```
   79866 因为从未有过失败请求，原始名 key 完全不存在，评分读到"零数据"→ `HasData=false` → `dp=0`。

### 根因

**同一个渠道同一次请求，成功路径和失败路径在 Redis 里写不同的 key：**

```
成功路径:  helper.go:378  BillingModelName() = ActualModelName（映射后名）
              ↓
           log.go:139     RecordAbilityMetric(Model: modelName)
              ↓
           ZADD ability_metrics:{ch}:{映射后名}

失败路径:  relay.go:181/287/295/299  processChannelRelayError(originalModel)
              ↓
           relay.go:771   RecordAbilityMetric(Model: modelName=originalModel)
              ↓
           ZADD ability_metrics:{ch}:{原始名}

评分链路:  dynamic_priority.go:238  loadAbilityCandidates 读 abilities.model = 原始名
              ↓
           ability_window.go:126    ZRANGEBYSCORE ability_metrics:{ch}:{原始名}
              ↓
           只看到失败样本或空 → dp=0
```

### 目标

- 让**所有渠道**（含 model_mapping 渠道）的动态优先级评分能正确拿到成功样本。
- 修复后 Redis 里遗留的映射后名 key 需能自动清理，避免长期占内存。

---

## 方案设计

### 关键前提（首次实现时忽略、导致 32dc1c4 修错的事实）

- **`relay/controller/text.go:50-52`** 早就把 `textRequest.Model` 从原始名覆写成映射后名：
  ```go
  meta.OriginModelName = textRequest.Model                                      // 原始名保存到 meta
  textRequest.Model, _ = util.GetMappedModelName(textRequest.Model, meta.ModelMapping)   // 覆写！
  meta.ActualModelName = textRequest.Model
  ```
- **`relay/controller/helper.go:324`** 已经在拼 `otherInfo` 时调 `appendModelMappingInfo`，塞入 `origin_model_name:{OriginModelName}`
- **`model/log.go:97-113`** 早就有 `dbModelName` 逻辑，从 `other` 里 `origin_model_name:xxx` 前缀提取原始名（fallback 为 `modelName`）
- **`middleware/metrics.go:47`** 的注释也提到了这套设计（"log.go 会在 other 含 origin_model_name 时回退成 origin"）

### 三个改动点

#### 1. `model/log.go` — 把 `RecordAbilityMetric` 的 `Model` 从 `modelName` 改为 `dbModelName`

```go
metrics.RecordAbilityMetric(ctx, metrics.AbilityMetric{
    ChannelId:        channelId,
    Model:            dbModelName,   // ← 原来是 modelName（可能映射后名），改为 dbModelName（一定原始名）
    Success:          true,
    ...
})
```

**为什么 `dbModelName` 一定是原始名**（三条链路的证据）：
- **chat 主路径**：helper.go 通过 `appendModelMappingInfo` 塞入 `origin_model_name:{OriginModelName}` → dbModelName = OriginModelName（原始名）
- **claude/gemini native 等入口**：传入的 `modelName = c.GetString("original_model")` 本身就是原始名；`other` 无 `origin_model_name:` 前缀 → `dbModelName == modelName == 原始名`
- **无 model_mapping 的普通渠道**：`dbModelName == modelName == 原始名`（本来就一致）

#### 2. `common/metrics/ability_window.go` — 加 ctx 首参数 + `ZAdd` 后设置自适应 TTL

四个导出函数（`RecordAbilityMetric` / `ScanAbilityWindow` / `ScanAbilityWindowBatch` / `DeleteAbilityMetrics`）加 `ctx context.Context` 首参数，内部 `logger.SysError` 改为 `logger.Error(ctx, ...)` / `logger.Errorf(ctx, ...)`。

`RecordAbilityMetric` 内部 `ZAdd` 改为 pipeline，追加 `EXPIRE key abilityMetricKeyTTL()`。

**为什么默认 30 分钟，但跟随长窗口配置**（详见代码内注释）：
- 默认 `DynamicPriorityWindowMinutes=10`
- 30 分钟远大于窗口，TTL 到期时 key 里所有 member 的 score 都在窗口外，不会误伤有效数据
- 若运维把 `DynamicPriorityWindowMinutes` 调到 30 分钟以上，TTL 会变成 `窗口长度 + 10 分钟缓冲`，避免 key 在样本仍属于评分窗口时过期
- 正常写入的 key 每次 ZADD 都刷新 TTL，永不过期
- **作用**：停止写入的 key（历史脏数据、渠道下线、model 名字改动）会在超过评分窗口后自动过期，无需运维手动 DEL

#### 3. `controller/dynamic_priority.go` + `controller/relay.go` — 调用点传 ctx

- `runDynamicPriorityCalcOnce` 内部 `ctx := context.Background()`（定时任务无请求 ctx）
- `buildStatsForModel` 加 ctx 参数，透传给 `ScanAbilityWindowBatch`
- `processChannelRelayError` 里 `RecordAbilityMetric(ctx, ...)` 补 ctx（该函数已有 ctx 入参）

**helper.go 无需改动**——`appendModelMappingInfo` 早就塞好了 `origin_model_name`。

---

## 32dc1c4 里试过、但错的方案（存档说明）

首次实现（commit `32dc1c4`）走的是"把打点从 log.go 挪到 helper.go 用 `textRequest.Model`"，两个致命错误：

1. **`textRequest.Model` 在 `postConsumeQuota` 被调用时已被 `text.go:51` 覆写为映射后名**，跟 `BillingModelName()` 完全等价——dp=0 的根源没修
2. **`log.go` 里的公共埋点被所有 `RecordConsumeLog*` 入口共用**（chat/claude/gemini/audio/image/midjourney/video），其中 chat 之外的入口本来就传原始名，本身是对的；32dc1c4 把公共埋点删掉、只在 helper.go 单点重加，等于把 6 类入口的成功打点全废

代码审计发现后（未上线），后续 commit 采用"用 dbModelName + 保留 log.go 公共埋点"的最小修法（本文档第 2 版方案）。**教训**：字段名 ≠ 变量运行时状态，改动前必须 grep 中途赋值路径。

---

## 影响范围

### 立即受益的场景
- **AWS Bedrock 渠道**（channel_type=33，Inference Profile 命名如 `global.anthropic.*`）
- **Vertex Claude 渠道**（走 `anthropic.claude-*` 内部名）
- **任何配置了 `model_mapping` 的渠道**

修复后，Master 下一轮评分（5 分钟一次）会读取正确 key 的成功样本，动态优先级自然回到非零值。

### 不受影响的场景
- 无 model_mapping 的渠道（成功/失败/评分三处 model 名本来就一致）
- Prometheus 观测指标（`ObserveConsume` 保留原逻辑）
- 静态优先级选渠道（`DynamicPriorityApplyEnabled=false` 时评分不影响选择）

### 遗留数据的处理（**必须一次性运维**）

**盲点**：本次加的 `EXPIRE` 只对新 ZADD 时才生效。**部署前已存在的老 key 没有 TTL**，且映射后名脏 key 修复后不会再被 ZADD → **永远拿不到 TTL → 永远不会自动过期**。

必须在部署后执行一次运维操作，给所有存量 `ability_metrics:*` 打上 TTL。若保持默认 10 分钟窗口，用 30 分钟 TTL：

```bash
# 通过 SSM 在能连 ElastiCache 的节点上执行
$RCLI --scan --pattern 'ability_metrics:*' | \
  awk '{print "EXPIRE", $0, 1800}' | \
  $RCLI --pipe
```

如果 `DynamicPriorityWindowMinutes > 20`，把 `1800` 改成 `(DynamicPriorityWindowMinutes + 10) * 60`，与代码里的 `abilityMetricKeyTTL()` 口径一致。

执行后：
- 所有 key 立即拿到与评分窗口匹配的 TTL
- **正常被写入的 key**：新代码每次 ZADD 会刷新 TTL → 永不过期
- **停止写入的 key**（映射后名脏数据、已下线渠道、model 改名过的历史遗留）：超过评分窗口后自动消失
- **低频渠道**：TTL 内无请求会被清理，但此时 key 里所有 member 的 score 都已在评分窗口外，评分本来就取不到有效数据，无损

**不做这一步的后果**：Redis 里的映射后名脏 key（`ability_metrics:*:global.anthropic.*` 等）永久驻留，仅增不减。

### 覆盖边界
本次最终修法落在 `model/log.go` 的公共消费日志入口，因此所有经过 `RecordConsumeLog*` / `RecordConsumeLogWithOtherAndRequestID` 的成功请求都会写动态优先级成功样本。

对 model_mapping 场景，修复是否能拿到原始 model 名取决于调用方是否传入 `origin_model_name` 到 `other`：
- 主 chat 路径、Claude native、Gemini native、OpenAI Responses、图片等已通过 `appendModelMappingInfo` 传入原始名
- 无 model_mapping 的普通渠道不依赖 `other`，`dbModelName == modelName`
- `RecordVideoConsumeLog` 是独立视频日志入口，不经过 `RecordConsumeLogWithOtherAndRequestID`，本次仍不写动态优先级成功样本；若要让异步视频任务参与动态优先级，需要单独设计成功样本语义

---

## 验证方式

### 编译 & 静态检查
```bash
go build ./... && go vet ./...
```

### 生产验证
1. 部署后**等 5 分钟**（一个评分周期）
2. 观察目标渠道（如 79726, 79805, 79866）的 `dynamic_priority` 值
3. 期望：dp 从 0 变为非零（具体值取决于成功率/延迟/价格）

### Redis 验证
```bash
# 应该看到"映射后名 key"在 TTL 到期后数量持续下降
redis-cli --scan --pattern 'ability_metrics:*:global.anthropic.*' | wc -l

# 应该看到原始名 key 里成功样本出现（之前只有失败样本）
redis-cli --scan --pattern 'ability_metrics:79866:claude-opus-4-7' | xargs -r redis-cli ZRANGE
```

### 回归验证
- 无 model_mapping 的渠道 dp 不应变化
- 失败路径打点仍正常（`processChannelRelayError` 未改）
- Prometheus 指标（`one_api_relay_consume_*` 等）不应有变化

---

## 关联问题 / 后续跟进

### 已发现但本次不处理

1. **`ezlinkai` 老僵尸容器**：ezlink-1 上跑着一个半年前的 `one-api:latest`（2026-02-05 构建）镜像，`NODE_TYPE` 无设置、无 Redis、CPU 0%、几乎无流量。建议关掉释放端口 5000。
2. **其他入口打点缺失**：如上述"未覆盖的入口"列表。
3. **代码里 `RecordAbilityMetric` 的"Model 名"契约未文档化**：应在 `common/metrics/ability_window.go` 顶部注释里明确"传入的 Model 名必须与 abilities.model 一致"。**已在本次修改的注释里补充。**
