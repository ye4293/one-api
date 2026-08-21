# 接入 AWS SQS：消费日志异步投递（billship）

## 背景与目标

计费系统（charge）需要接收 one-api 每一次"模型使用"的消费日志。charge 团队已封装好生产者发送 SDK `billship`（charge 私有仓库内的独立子 module `github.com/changshiaos/charge/server/shipper`），提供非阻塞热路径 `Ship` + 攒批 + worker 池 + 失败日志 + 优雅停机。

**目标**：在 one-api 每条消费日志写入 `logs` 表**成功后**，把该行 JSON 异步投递到 SQS 队列，**绝不阻塞、绝不影响原业务流程**。

## 关键前提与约束

- `billship.Ship` 本身即非阻塞（buffer 满即丢弃 + 失败日志，永不阻塞/panic）。因此**直接同步调用 `Ship` 即可**，不要自己再包 `go func()`——那样反而丢失 billship 的背压与失败日志语义。
- `billship` 是 charge 私有仓库内的**独立 module**；one-api 的 Dockerfile 通过 BuildKit secret 在依赖下载阶段只读拉取已发布版本，不复制 charge 源码。
- **投递范围**：仅消费日志——`RecordConsumeLogWithOtherAndRequestID`（普通消费）与 `RecordVideoConsumeLog`（视频消费）。**不含**探针（`RecordModelProbeLog`）、错误（`RecordErrorLog`）、系统/注册赠送日志。
- SiteID 为**固定环境变量值**（one-api 单部署 = 一个 site）。
- **无 DB schema 变更**（只读 logs 行做序列化投递）。

### 生产依赖状态

shipper 已发布为 `github.com/changshiaos/charge/server/shipper v0.1.0`。本地与 CI 均直接引用版本，不再使用相邻目录 `replace`；私有仓库认证只通过开发机 Git 凭证或 CI Secret `CHARGE_REPO_READ_TOKEN` 提供。

## 方案设计

### 依赖（go.mod）
- `require github.com/changshiaos/charge/server/shipper v0.1.0`。
- 不添加本地 `replace`，确保本机与镜像构建使用同一个不可变版本。
- 新增间接依赖 `github.com/aws/aws-sdk-go-v2/service/sqs`（主库 aws-sdk-go-v2 one-api 已有）。

### 新增适配层 `common/shipper/`（package `shipper`）
隔离外部 SDK，避免 import 环：**适配层不 import model**，故 `model → common/shipper → 外部 billship` 无环。

- `Init()`：读 config → 构造 `billship.Config` → `billship.Init`。`Logger` 桥接到 `logger.SysError/SysLog`。**init 失败仅告警降级、不 crash 启动**（对齐 `audit` 容错模式）。
- `Ship(logID int64, createdAt int64, modelName string, body []byte)`：填入固定 `SiteID`、`SourceType = "one-api"`，转发 `billship.Ship`。未 Init / 禁用时为安全 no-op。
- `Shutdown(ctx)`：转发 `billship.Shutdown`。

### 埋点（`model/log.go`）
在 `RecordConsumeLogWithOtherAndRequestID`、`RecordVideoConsumeLog` 中，`LOG_DB.Create(log)` **成功后**（此时 `log.Id` 已回填）：
```go
body, err := json.Marshal(log)   // logs 行 JSON；每次新分配 → 满足"Ship 后不得改 Body"契约
if err == nil {
    shipper.Ship(int64(log.Id), log.CreatedAt, log.ModelName, body)
}
```
仅这两处，其余日志入口不动。

### 配置（env，`common/config/config.go`）

| 变量 | 说明 | 默认 |
|---|---|---|
| `BILL_SHIP_ENABLED` | 总开关（灰度可一键关） | false |
| `BILL_SHIP_QUEUE_URL` | SQS 队列 URL | "" |
| `BILL_SHIP_REGION` | AWS region | "" |
| `BILL_SHIP_SITE_ID` | 固定 SiteID | "" |
| `BILL_SHIP_LOG_FAILED_BODY` | 真失败时是否把 Body 打进日志 | false |
| `BILL_SHIP_BUFFER_SIZE` | 内存缓冲记录数，满时非阻塞丢弃 | 10000 |
| `BILL_SHIP_BATCH_SIZE` | 单批记录数，SQS 硬上限 10 | 10 |
| `BILL_SHIP_BATCH_WAIT_MS` | 未满批时最长等待毫秒数 | 200 |
| `BILL_SHIP_SEND_CONCURRENCY` | 并发发送 worker 数 | 8 |
| `BILL_SHIP_SEND_TIMEOUT_SECONDS` | 单次 SQS 请求超时秒数 | 3 |
| `BILL_SHIP_MAX_RETRIES` | 可重试失败的最大重试次数，0 表示不重试 | 3 |

发送参数由 one-api 显式传入 billship，默认值与 SDK 一致。启用时严格校验：所有容量/时间/并发参数必须大于 0，`BatchSize` 必须在 1～10，`MaxRetries` 必须大于等于 0；非法配置会记录错误并禁用投递，不影响 one-api 主业务。

### main.go 接线
- 在 `audit.Start(...)` 附近调 `shipper.Init()`，随后 `defer shipper.Shutdown(ctx)`（5s 超时 ctx）。
- best-effort：与现有 `defer audit.Shutdown()` 同款语义；当前无 SIGTERM 处理、`server.Run` 阻塞下 defer 不保证执行，属既有约定，不在本次改动范围。

## 影响范围

- 涉及文件：`go.mod` / `go.sum`、`common/config/config.go`、`common/shipper/*.go`（新建）、`model/log.go`、`main.go`。
- 跨 3 文件以上 + 新增模块 → 需本计划文档（本文）。
- **无数据迁移、无 schema 变更**。
- 禁用（默认）时对现有行为零影响；启用后热路径只多一次 `json.Marshal` + 非阻塞入队。

## 验证方式

1. `go build ./... && go vet ./...`（带本地 replace）。
2. 适配层单测：禁用时 `Ship` 为 no-op；启用配置解析正确。
3. 手工联调：LocalStack 或真实 SQS 队列，发一次请求 → 确认队列收到消息（属性 `site_id` / `model` / `source_type` 正确、Body 为 logs 行 JSON），且业务响应不受影响。
4. 观察 `billship.Stats()`（可选加监控端点）确认 enqueued / dropped / failed 计数。

## 待发布后收尾

- charge 发布 shipper 后，替换占位 import 路径为真实 module 路径，删除临时 `replace`，`go mod tidy`。
- 完成 commit 后同步更新 `docs/CHANGELOG.md`。
