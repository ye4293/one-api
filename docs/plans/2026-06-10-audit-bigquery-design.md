# 全链路请求审计 → BigQuery 设计方案

- **日期**：2026-06-10
- **状态**：设计已确认，待实现
- **目标分支**：待定（实现时用 worktree 隔离）

## 1. 背景与目标

在用户调用模型时，记录一次请求的**全链路 6 类数据**，并以**全量、解耦**的方式持久化到 BigQuery，用于审计、问题排查与离线分析。

需记录的 6 类数据：

1. 原始客户端请求头
2. 原始客户端请求体
3. 系统转换后发往上游的请求头
4. 系统转换后发往上游的请求体
5. 上游原始响应
6. 系统转换后返回给客户端的响应

**核心诉求**：与现有业务**相对解耦**——审计是旁路功能，绝不能阻塞或拖慢主请求，故障被完全隔离在审计模块内部。

### 已确认的需求边界（brainstorm 结论）

| 维度 | 决策 |
|---|---|
| 覆盖范围（一期） | **仅 text/chat completions**，跑通骨架后再扩展其他类型 |
| 记录比例 | **全量记录** |
| 量级 | ~2064 req/min ≈ 297 万次/天 ≈ 9000 万次/月 ≈ 34 req/s |
| 数据规模 | 约 2~4 TB/月（逐月累积） |
| 新鲜度 | **可接受分钟~小时级延迟** → 批量缓冲 + GCS load job |
| 数据完整度 | **全部 6 类**，接受在 relay 流程少量埋点 |
| 故障取舍 | **审计绝对让路**，缓冲溢出直接丢弃 + 计数告警，主请求无感 |
| 刷写触发 | batch 大小满 **或** 定时间隔到，先到先触发 |
| 脱敏 | 记录前**自动脱敏凭证类请求头** |
| 保留期 | 按天分区，**不设过期**（保留口子，可选开启） |

> **成本提醒（待决策口子）**：分区不过期 = 存储无上限累积。2-4 TB/月逐月叠加，一年约 25-50 TB，BigQuery 活跃存储约 $0.02/GB/月，一年后单月存储费约 $500-1000 且持续上涨。`AUDIT_PARTITION_EXPIRE_DAYS` 配置项保留此能力，默认 `0`（不过期）。

## 2. 当前请求链路（调研结论）

以 text/chat 为例：

- 入口：`controller/relay.go` → `relay/controller/text.go:RelayTextHelper`
- **原始请求头**：`c.Request.Header`
- **原始请求体**：`common.GetRequestBody(c)`（已缓存于 context `key_request_body`，可重复读）
- **转换后请求体**：`text.go:80-110` 的 `convertedRequest` / `requestBody`
- **转换后请求头**：各 adaptor 的 `SetupRequestHeader` 设置到 `*http.Request`
- **上游原始响应**：`adaptor.DoRequest` 返回的 `resp *http.Response`，body 会被 `DoResponse` 消费
- **返回客户端的响应**：`DoResponse`/`StreamHandler` 内部写入 `c.Writer`（流式 SSE 分块 / 非流式 `IOCopyBytesGracefully`）
- 现有日志：`model.Log` 表 + `postConsumeQuota`（已异步）

**捕获难度分级：**

- ✅ 可在中间件层零侵入捕获：原始请求头、原始请求体、最终返回客户端的响应
- ⚠️ 必须在 relay 流程埋点：转换后请求头、转换后请求体、上游原始响应

## 3. 整体架构与数据流

核心思路：**业务侧只负责"塞数据"，审计模块负责"取、转、投"**，所有复杂度隔离在独立 `audit` 包内。

```
                    ┌─────────────── 主请求链路（同步）───────────────┐
客户端 → [审计中间件] → auth → distributor → RelayTextHelper → adaptor → 上游
              │                                    │埋点(c.Set)
              │ 包装 ResponseWriter                 │
              │ (tee 最终响应)                       ▼
              │                              AuditContext (存于 gin.Context)
              ▼ defer: 请求结束后
        组装 AuditRecord ──非阻塞写──→ [有界 channel]
                                            │
        ┌───────────────────────────────────┘
        ▼ 后台 worker（独立 goroutine，与请求完全解耦）
   累积 batch ──(大小满 或 定时)──→ 写 NDJSON 本地文件 → gzip
        → 上传 GCS → 提交 BigQuery load job (WRITE_APPEND)
        channel 满 → 丢弃 + 计数告警
```

**关键隔离点：**

- 主请求对审计的唯一依赖 = 一次**非阻塞** channel 写（`select { case ch<-r: default: drop }`），纳秒级，永不阻塞。
- 中间件 + worker + BigQuery 客户端全部在 `audit` 包，业务代码只多几行 `c.Set("audit_xxx", ...)`。
- worker 崩溃 / GCS 故障 / 积压都被关在 channel 下游，主链路无感。

**新增模块：**

- `common/audit/`：context、redact、buffer、worker、bigquery client
- `middleware/audit.go`：审计中间件

## 4. 埋点位置与数据捕获

### ① 中间件层（零侵入捕获 3 类）—— `middleware/audit.go`

注册在中间件链最外层（auth 之前）：

- 进入时：读 `c.Request.Header`（原始请求头）、`common.GetRequestBody(c)`（原始请求体，复用已有缓存）。
- 包装 `c.Writer` 为 `auditResponseWriter`：重写 `Write([]byte)`，每次写旁路 tee 一份到内部 buffer（**带上限截断**）。流式 SSE 分块、非流式 JSON 都能完整抓到 → "最终返回客户端的响应"。
- `defer` 中：请求结束后从 `AuditContext` 取齐 6 类数据，组装 `AuditRecord`，非阻塞投递。

### ② relay 流程埋点（拿剩余 3 类）—— 改动收敛到 3 处

| 数据 | 埋点位置 | 调用 |
|---|---|---|
| 转换后请求体 | `text.go` 拿到 `requestBody` 后 | `audit.SetConvertedBody(c, jsonStr)` |
| 转换后请求头 | adaptor `SetupRequestHeader` 后 / `DoRequestHelper` 内 | `audit.SetConvertedHeader(c, req.Header)` |
| 上游原始响应 | `DoRequest` 拿到 `resp` 后，TeeReader 包 `resp.Body` | `audit.WrapUpstreamBody(c, resp)` |

**埋点函数做成"哑操作"：** 每个 `audit.SetXxx` 内部先判断"审计是否开启"，关闭时直接 return，零开销。

**上游响应用 `io.TeeReader`：** `DoResponse` 照常消费 `resp.Body`，tee 旁路复制一份到审计 buffer（带上限截断），不改变现有消费逻辑。

**截断策略：** 每类 payload 设独立上限（请求体默认 256KB、响应默认 1MB），超出截断并在 `truncated_fields` 标记。控制内存与 BigQuery 单行大小（防止 base64 图片撑爆缓冲）。

## 5. 缓冲与投递 worker

`common/audit/buffer.go` + `worker.go`

**投递入口（主请求侧唯一接触点）：**

```go
func Submit(r *AuditRecord) {
    n := r.SizeBytes()
    // 双重判断：条数未满 且 字节未超硬顶
    if atomic.LoadInt64(&bufferedBytes)+int64(n) > maxBufferBytes {
        atomic.AddInt64(&dropped, 1)      // 超字节硬顶，丢弃
        return
    }
    select {
    case recordChan <- r:                 // 成功入队
        atomic.AddInt64(&bufferedBytes, int64(n))
    default:
        atomic.AddInt64(&dropped, 1)      // channel 满，丢弃
    }
}
```

`recordChan` 容量有界（默认 2000 条），同时受 `AUDIT_MAX_BUFFER_MB` 字节硬顶约束（默认 1GB，见 §7 内存边界）。flush 完成后 `bufferedBytes` 扣减对应字节。这是主请求对审计的**全部成本**——一次非阻塞 channel 写 + 一次原子加。

**后台 worker（单 goroutine）：**

```
for {
  select {
  case r := <-recordChan:  batch = append(batch, r)
                           if len(batch) >= maxBatchSize { flush() }
  case <-ticker.C:         if len(batch) > 0 { flush() }   // 定时兜底
  }
}
```

- 触发：batch 达到 N 条（默认 500）**或** 刷写间隔到（默认 10s），先到先触发。
- `flush()`：batch 序列化为 **NDJSON**（每行一条）→ 写本地临时文件 → gzip → 上传 GCS → 提交 BigQuery load job → 删临时文件。

**为什么经 GCS 而非直接 load 本地文件：**

- load job 从 GCS 加载**完全免费**（2-4TB/月省下约 $100-200 写入费）。
- GCS 作为中转，上传失败可重试、文件可留存排查，比 Storage Write API 更解耦、更经济。

**积压自我保护：**

- 上传/load 慢导致 channel 积压 → `Submit` 自动丢弃计数，主请求无感。
- worker 上传失败：重试 N 次仍失败则丢弃这批 + 告警日志，绝不无限堆积。
- `dropped` 计数定期打日志（后续可接监控告警）。

**优雅关停：** 进程退出时 worker 收到信号，带超时尽力 flush 残余 batch，最大努力不丢尾部数据。

## 6. BigQuery 表结构

**数据集/表**：`audit.request_logs`，按 `event_time` 天分区（不设过期，默认；过期为可选配置）。

| 列名 | 类型 | 说明 |
|---|---|---|
| `event_time` | TIMESTAMP | 请求时间（**分区列** `DATE(event_time)`）|
| `x_request_id` | STRING | 关联现有 `model.Log.XRequestID` |
| `user_id` | INT64 | |
| `username` | STRING | |
| `channel_id` | INT64 | |
| `token_name` | STRING | |
| `origin_model` | STRING | 客户请求的模型名 |
| `actual_model` | STRING | 映射后实际调用的模型名 |
| `is_stream` | BOOL | |
| `status_code` | INT64 | 返回客户端的 HTTP 状态码 |
| `duration_ms` | INT64 | 总耗时 |
| `original_req_headers` | STRING(JSON) | **已脱敏** |
| `original_req_body` | STRING | 原始请求体 |
| `converted_req_headers` | STRING(JSON) | **已脱敏** |
| `converted_req_body` | STRING | 转换后请求体（与原始相同时存空，见 `converted_same_as_original`）|
| `converted_same_as_original` | BOOL | 转换体与原始体逐字节相同时为 `true`，去重省内存/存储 |
| `upstream_response` | STRING | 上游原始响应（流式=拼接原始 SSE）|
| `client_response` | STRING | 最终返回客户端的响应 |
| `truncated_fields` | STRING(JSON) | 被截断的字段列表，如 `["upstream_response"]` |
| `dropped_note` | STRING | 预留异常标记 |

**设计要点：**

- 6 类 payload 全部存为 **STRING**（不用 JSON/RECORD 类型）——请求体格式各异、可能非法 JSON、可能被截断，STRING 最稳、load 永不失败。需要时在 BQ 里 `JSON_EXTRACT` 现解析。
- 与现有 `model.Log` 表通过 `x_request_id` 关联：审计表存"全文大字段"，业务日志表存"计费/统计指标"，职责分离。
- 分区 + 后续可加 clustering（按 `actual_model` / `channel_id`）加速查询。
- **建表幂等**：首次启动检测表是否存在，不存在则用固定 schema 建表，无需手工运维。

## 7. 配置项、脱敏与开关

### 配置项（环境变量，均有安全默认值）

| 配置 | 默认 | 说明 |
|---|---|---|
| `AUDIT_ENABLED` | `false` | **总开关**，关闭时埋点哑操作、worker 不启动 |
| `AUDIT_GCP_PROJECT` | - | GCP 项目 ID |
| `AUDIT_BQ_DATASET` | `audit` | BigQuery 数据集 |
| `AUDIT_BQ_TABLE` | `request_logs` | 表名 |
| `AUDIT_GCS_BUCKET` | - | 中转 bucket |
| `AUDIT_CREDENTIALS_FILE` | - | GCP service account JSON 路径 |
| `AUDIT_CHANNEL_SIZE` | `2000` | 有界 channel 容量（条数）|
| `AUDIT_MAX_BUFFER_MB` | `1024` | **【必须项】按字节的内存硬上限**，in-flight 字节超此值即丢弃，防 OOM |
| `AUDIT_BATCH_SIZE` | `500` | 批量条数触发阈值 |
| `AUDIT_FLUSH_INTERVAL` | `10s` | 定时刷写兜底 |
| `AUDIT_MAX_BODY_KB` | `10240` | 请求体单字段上限（10MB，覆盖多模态 base64 图片）|
| `AUDIT_MAX_RESP_KB` | `4096` | 响应单字段上限（4MB，覆盖长文本/推理输出）|
| `AUDIT_REDACT_HEADERS` | `Authorization,Api-Key,X-Api-Key,Cookie,Set-Cookie` | 脱敏头列表 |
| `AUDIT_PARTITION_EXPIRE_DAYS` | `0` | `0`=不过期；>0 自动设分区过期 |

### 内存边界（关键安全设计）

caps 上调到 10MB/4MB 后，**按条数限容量已不足以防 OOM**——多模态最坏单条约 `10MB(原始体) + 10MB(转换体,含同一张图) + 4MB(上游响应) + 4MB(客户响应) ≈ 28MB`，`CHANNEL_SIZE=2000` 按条数最坏可达 56GB。因此引入 `AUDIT_MAX_BUFFER_MB` 作为**真正的兜底**：

- 维护一个原子计数器，累计**所有 in-flight 字节**（channel 中排队 + worker 当前 batch）。
- `Submit` 时双重判断：channel 未满 **且** 累计字节 + 本条字节 ≤ `AUDIT_MAX_BUFFER_MB` → 入队并加计数；否则丢弃 + 计数告警。
- flush 完成后扣减对应字节。
- 内存峰值由此被**钉死在 `AUDIT_MAX_BUFFER_MB`**（默认 1GB），与单条大小无关。
- 正常纯文本流量仅占 ~100-300MB；偶发多模态也够缓冲；只有洪峰超 1GB 才丢弃。

> **取舍说明**：`{全量捕获大请求}`、`{允许大 payload}`、`{内存可控}` 三者最多同时满足两个。当前选择 = 后两者优先（内存硬顶 + 大 payload 上限），代价是多模态洪峰时会丢弃部分记录，符合"审计绝对让路"原则。

### 多模态内存优化：转换体去重

text.go 的 OpenAI 路径在 `shouldResetRequestBody=false` 时 `requestBody = c.Request.Body`（转换体 == 原始体，逐字节相同）。多模态场景下这会让同一张 10MB 图片在内存/BigQuery 里存两份。

- 记录前比对：若 `converted_req_body` 与 `original_req_body` 字节相同，`converted_req_body` 存空 + 置标记列 `converted_same_as_original=true`。
- 节省一半大 payload 的内存与存储；查询时按标记回填即可。

### 脱敏（`common/audit/redact.go`）

- 对 `AUDIT_REDACT_HEADERS` 里的头，值替换为 `***REDACTED***`（保留 key 名）。
- 默认覆盖 `Authorization`（客户 key）+ 转换后发往上游的凭证头。
- **大小写不敏感**匹配，逗号分隔可扩展。

### 开关分级

- `AUDIT_ENABLED=false`：中间件不注册、埋点立即 return、worker 不启动——**完全等于功能不存在**。
- 开启但 GCP 配置缺失：启动校验失败 → 错误日志 + 自动降级为关闭，**绝不影响主服务启动**。

### 依赖

新增 `cloud.google.com/go/bigquery` + `cloud.google.com/go/storage`（GCP 官方 SDK）。

## 8. 影响范围

- 业务代码改动：`text.go` 1 处、adaptor `SetupRequestHeader` 路径 1 处、`DoRequest` 1 处，均为哑操作埋点，关闭时零开销。
- 中间件链：新增 1 个最外层中间件（仅 `AUDIT_ENABLED=true` 时注册）。
- 不涉及现有数据库 schema 变更（审计数据写 BigQuery，非本地 DB）。
- 不改变任何现有请求/响应行为。

## 9. 验证方式

1. `AUDIT_ENABLED=false`（默认）：确认无任何行为变化、无性能影响、worker 未启动。
2. `AUDIT_ENABLED=true` + 测试 GCP 项目：发起 text/chat 流式与非流式请求，确认 BigQuery 表中 6 类字段完整、脱敏生效、`x_request_id` 可与 `model.Log` 关联。
3. 截断验证：构造超大请求体/响应，确认截断 + `truncated_fields` 标记正确。
4. 故障隔离：人为让 GCS 不可达 / channel 打满，确认主请求延迟与成功率无变化，`dropped` 计数与告警日志正常。
5. 优雅关停：发送退出信号，确认残余 batch 被 flush。

## 10. 后续扩展（一期不做）

- 接入 embeddings / image / video（video 为异步两段式，需单独设计）。
- 采样率配置（按比例/按用户/按出错请求）。
- `dropped` 计数接入监控告警系统。
- 分区过期策略落地（`AUDIT_PARTITION_EXPIRE_DAYS > 0`）。
