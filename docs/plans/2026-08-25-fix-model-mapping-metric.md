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

### 三个改动点

#### 1. `relay/controller/helper.go`

在成功路径显式打点，使用原始名 `textRequest.Model`，与 abilities.model 和失败路径对齐：

```go
metrics.RecordAbilityMetric(metrics.AbilityMetric{
    ChannelId:        meta.ChannelId,
    Model:            textRequest.Model,   // 原始名
    Success:          true,
    Duration:         duration,
    FirstWordLatency: firstWordLatency,
    IsStream:         meta.IsStream,
})
```

新增 `github.com/songquanpeng/one-api/common/metrics` 包 import。

**为什么放在这里而不是继续在 log.go**：
- log.go 是通用日志函数，被 midjourney/audio/image/video/chat 等多入口调用
- 各入口传入的 `modelName` 语义不一致（有的是映射后、有的是原始名、有的带箭头组合）
- 打点应该跟"选渠道时用的 model 名"绑定，而 helper.go 是 chat 主路径且能直接访问 `textRequest.Model`（用户请求的原始名）
- 未来其他入口若也支持 model_mapping，各自在入口处复制打点即可，无需改公共 log 函数签名

#### 2. `model/log.go`

删除原来的 `RecordAbilityMetric` 调用及那段误导性注释（"用映射后的 modelName ... 与 abilities 表的 model 字段一致"——注释里的心智模型是错的，abilities 表存的是原始名）。

保留 `metrics.ObserveConsume`（Prometheus 埋点用 `dbModelName`，独立于动态优先级）。

#### 3. `common/metrics/ability_window.go`

`ZAdd` 之后追加 `EXPIRE key 30min`（改用 pipeline 一次 RTT）。

**为什么 30 分钟**：
- 默认 `DynamicPriorityWindowMinutes=10`
- 30 分钟远大于窗口，TTL 到期时 key 里所有 member 的 score 都在窗口外，不会误伤有效数据
- 正常写入的 key 每次 ZADD 都刷新 TTL，永不过期
- **作用**：停止写入的 key（例如映射后名的历史脏数据、渠道下线、model 名字改动）会在 30 分钟后自动过期，无需运维手动 DEL

**为什么之前作者刻意不设 TTL 的担忧不成立**：原注释担心"低频模型几小时才一次请求会被 TTL 提前删掉"——但**只要 TTL >= windowSize**，就没有"窗口内数据被误删"的情况：TTL 到期时，key 里所有 member 的 score 都比 now 早了 30 分钟 >= windowSize，本来就会被 `ZRemRangeByScore` 清理掉。

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

必须在部署后执行一次运维操作，给所有存量 `ability_metrics:*` 打上 30 分钟 TTL：

```bash
# 通过 SSM 在能连 ElastiCache 的节点上执行
$RCLI --scan --pattern 'ability_metrics:*' | \
  awk '{print "EXPIRE", $0, 1800}' | \
  $RCLI --pipe
```

执行后：
- 所有 key 立即拿到 30 分钟 TTL
- **正常被写入的 key**：新代码每次 ZADD 会刷新 TTL → 永不过期
- **停止写入的 key**（映射后名脏数据、已下线渠道、model 改名过的历史遗留）：30 分钟后自动消失
- **低频渠道**：30 分钟内无请求会被清理，但反正 key 里所有 member 的 score 都已在 10 分钟窗口外，评分本来就取不到有效数据，无损

**不做这一步的后果**：Redis 里的映射后名脏 key（`ability_metrics:*:global.anthropic.*` 等）永久驻留，仅增不减。

### 未覆盖的入口
本次只改主 chat 路径（`helper.go` 内的 `postConsumeQuota`）。其他 relay 入口（`relay/controller/{claude.go, gemini.go, audio.go, image.go, video.go, midjourney.go}`）**未添加打点**。

判断依据：
- 这些入口的流量远小于主 chat 路径
- 从 Redis 现存 key 看，主 chat 路径已能产生 13252 条 ability_metrics key，覆盖了绝大多数活跃 (channel, model)
- 若后续观察到这些入口下的渠道 dp 长期为 0，再各自补上 3 行 `RecordAbilityMetric` 即可

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
# 应该看到"映射后名 key"在 30 分钟内数量持续下降（TTL 过期）
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
