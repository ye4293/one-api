# one-api 计费生产者生产部署手册

本文说明如何把 one-api 中已经接入的 `billship` 生产者构建成生产镜像，配置运行环境，并确认消息成功发送到 charge 使用的 AWS SQS 队列。

适用版本：

```text
one-api 分支：billship-sqs
shipper module：github.com/changshiaos/charge/server/shipper v0.1.0
消息来源标识：source_type=one-api
```

## 1. 最终交付形态

生产部署分成构建与运行两个阶段。shipper 源码已随 one-api 仓库保存，因此构建阶段不再需要 charge 私库凭证：

| 阶段 | 需要的凭证 | 用途 | 保存位置 |
|---|---|---|---|
| 构建镜像 | Docker Hub/GHCR 凭证 | 下载其他公开 Go 依赖并推送 one-api 镜像；shipper 从仓库内 `third_party` 读取 | GitHub Actions Repository Secret |
| 运行容器 | IAM Role 或 `AWS_ACCESS_KEY_ID` 等 | one-api 向目标 SQS 队列发送消息 | AWS 实例角色或部署系统 Secret/服务器 `.env` |

镜像中不写 Queue URL、Site ID、GitHub token 或 AWS 密钥。同一个镜像可以部署到不同站点，每个站点通过环境变量配置自己的身份和目标队列。

数据链路：

```text
真实模型请求
  → one-api 成功写入 logs 表
  → billship 非阻塞入队
  → 批量 SendMessageBatch
  → AWS SQS
  → charge consumer 消费并落库
```

只有成功写入 `logs` 表的普通消费日志和视频消费日志会投递。错误日志、系统日志和模型探针日志不在本次投递范围内。

## 2. 上线前总检查表

没有全部勾选前，不要把 `BILL_SHIP_ENABLED` 设置为 `true`：

- [ ] one-api 当前改动已经评审、提交并推送到用于构建镜像的远端分支或 tag。
- [ ] `go.mod` 固定 `require github.com/changshiaos/charge/server/shipper v0.1.0`，并 `replace` 到 `./third_party/charge-shipper-v0.1.0`。
- [ ] `third_party/charge-shipper-v0.1.0` 已随本次 one-api 代码提交并进入 Docker 构建上下文。
- [ ] Docker Hub/GHCR 构建凭证可用。
- [ ] 已确认生产服务器 CPU 架构是 `amd64` 还是 `arm64`。
- [ ] 已创建或确认目标 SQS Standard Queue。
- [ ] 已记录 Queue URL、Queue ARN 和 Region。
- [ ] one-api 运行身份具有目标队列的 `sqs:SendMessage` 权限。
- [ ] one-api 服务器能通过 HTTPS 443 访问对应的 AWS SQS endpoint。
- [ ] 每个部署实例已经分配唯一且稳定的 `BILL_SHIP_SITE_ID`。
- [ ] 部署机 `.env` 已设置 `chmod 600`，且不会提交到 Git。
- [ ] 已准备快速关闭开关 `BILL_SHIP_ENABLED=false`。
- [ ] 已安排一笔真实模型请求用于上线后的端到端验证。

## 3. 当前代码已经完成的内容

当前 one-api 代码侧已经具备：

- 固定引用 `shipper v0.1.0`，源码快照位于 `third_party/charge-shipper-v0.1.0`，不依赖本机相邻目录或远程 charge 仓库。
- one-api 启动时初始化 shipper，退出时限时排空在途消息。
- 只有日志写库成功后才投递，消息 Body 带数据库回填后的日志 ID。
- `Ship` 为非阻塞调用，SQS 异常不会卡住用户请求。
- 启用时校验 Queue URL、Region 和 Site ID，缺少配置会明确记录错误并禁用投递。
- Dockerfile 先复制本地 shipper 模块，再用普通 `go mod download` 下载其他公开依赖。
- GitHub Actions 不需要 `CHARGE_REPO_READ_TOKEN`，Docker 构建也不接收 charge Token。
- `BILL_SHIP_ENABLED=false` 时可以使用同一镜像快速关闭计费投递。

因此，正式镜像构建完成后，不需要再修改 Go 代码。上线差异通过 IAM/SQS 权限和容器环境变量提供。

## 4. 本地 shipper 快照管理

当前固定关系：

```text
require github.com/changshiaos/charge/server/shipper v0.1.0
replace github.com/changshiaos/charge/server/shipper => ./third_party/charge-shipper-v0.1.0
```

升级 shipper 时，在有权读取 charge 的开发机复制新版本到新的版本目录，更新 `require` 与 `replace`，执行 `go mod tidy` 和测试后，把新目录随 one-api 一起提交。不要直接在 one-api 中零散修改快照源码，否则会失去与上游版本的对应关系。

## 5. 准备 SQS 队列

### 5.1 队列类型

当前 shipper 应使用 **Standard Queue**。不要使用名称以 `.fifo` 结尾的 FIFO Queue，因为当前消息没有设置 FIFO 必需的 `MessageGroupId`。

建议配置：

| 配置 | 建议值 |
|---|---|
| Queue type | Standard |
| Message retention period | 14 天，给 charge 停机或积压恢复留足时间 |
| Encryption | SSE-SQS，或按公司要求使用 KMS |
| Visibility timeout | 由 charge 消费侧处理时长决定，与生产者发送无关 |
| Dead-letter queue | 由 charge 消费侧重试与 DLQ 方案决定 |

### 5.2 记录三个值

在 AWS SQS 控制台进入目标队列，记录：

```text
Queue URL: https://sqs.us-east-1.amazonaws.com/123456789012/charge
Queue ARN: arn:aws:sqs:us-east-1:123456789012:charge
Region: us-east-1
```

注意：

- `BILL_SHIP_QUEUE_URL` 填 Queue URL，不是 ARN。
- IAM Policy 的 `Resource` 填 Queue ARN，不是 URL。
- `BILL_SHIP_REGION` 必须与 Queue URL 中的 Region 一致。

## 6. 配置运行期 AWS 权限

### 6.1 推荐：EC2/ECS IAM Role

如果 one-api 运行在 AWS EC2 或 ECS，优先给 EC2 Instance Profile 或 ECS Task Role 绑定最小权限：

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SendChargeUsageMessages",
      "Effect": "Allow",
      "Action": "sqs:SendMessage",
      "Resource": "arn:aws:sqs:us-east-1:123456789012:charge"
    }
  ]
}
```

`SendMessageBatch` 使用的也是 `sqs:SendMessage` 权限，不需要额外增加名为 `sqs:SendMessageBatch` 的 Action。

使用 IAM Role 时，不要在部署机 `.env` 中设置 `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY` 和 `AWS_SESSION_TOKEN`，AWS SDK 会自动读取角色凭证。

如果队列使用客户管理的 KMS Key，还需要由 AWS 管理员根据 Key Policy 增加相应的 KMS 权限；使用默认 SSE-SQS 时通常不需要额外 KMS 权限。

### 6.2 非 AWS 服务器

如果 one-api 运行在阿里云、其他云或自建服务器：

1. 创建一个专用于 one-api SQS 发送的 IAM 身份。
2. 只绑定上一节的目标队列最小权限。
3. 生成 Access Key，优先使用可定期轮换的临时凭证。
4. 把凭证保存到部署平台 Secret 或服务器 `.env`，不要写死在 Compose 文件。

长期凭证：

```dotenv
AWS_ACCESS_KEY_ID=<运行期AccessKey>
AWS_SECRET_ACCESS_KEY=<运行期SecretKey>
```

临时凭证还必须增加：

```dotenv
AWS_SESSION_TOKEN=<运行期SessionToken>
```

只填写 `AWS_ACCESS_KEY_ID` 而缺少 `AWS_SECRET_ACCESS_KEY` 会导致认证失败。

## 7. 配置 one-api 运行环境变量

在生产服务器 `docker-compose.yml` 所在目录创建或修改 `.env`：

```dotenv
# 首次部署先 false，完成镜像和权限检查后再改 true。
BILL_SHIP_ENABLED=false

# SQS 控制台显示的完整 Queue URL，不是 ARN。
BILL_SHIP_QUEUE_URL=https://sqs.us-east-1.amazonaws.com/123456789012/charge

# 必须与 Queue URL 所在区域一致。
BILL_SHIP_REGION=us-east-1

# 当前部署实例唯一且长期稳定的站点标识。
BILL_SHIP_SITE_ID=one-api-prod-us-01

# 生产建议 false，避免原始 Body 中的敏感信息进入应用日志。
BILL_SHIP_LOG_FAILED_BODY=false

# 有界内存缓冲记录数；满时不阻塞请求，而是记录 reason=dropped。
BILL_SHIP_BUFFER_SIZE=10000

# 每次批量发送的记录数，只允许 1～10。
BILL_SHIP_BATCH_SIZE=10

# 一批不满时最长等待时间，单位毫秒。
BILL_SHIP_BATCH_WAIT_MS=200

# 并发执行 SendMessageBatch 的 worker 数。
BILL_SHIP_SEND_CONCURRENCY=8

# 单次 SQS API 请求超时，单位秒。
BILL_SHIP_SEND_TIMEOUT_SECONDS=3

# 可重试错误的最大重试次数；0 表示不重试。
BILL_SHIP_MAX_RETRIES=3
```

非 AWS 服务器继续在同一安全配置来源中添加运行期 AWS 凭证。使用 IAM Role 时不要添加：

```dotenv
AWS_ACCESS_KEY_ID=<仅非IAM-Role环境填写>
AWS_SECRET_ACCESS_KEY=<仅非IAM-Role环境填写>
AWS_SESSION_TOKEN=<仅临时凭证填写>
```

设置文件权限并确认不会被 Git 跟踪：

```bash
chmod 600 .env
git check-ignore .env
```

### 7.1 Site ID 命名规则

建议格式：

```text
one-api-<环境>-<区域>-<序号>
```

示例：

```text
one-api-prod-us-01
one-api-prod-sg-01
one-api-staging-us-01
```

Site ID 必须长期稳定，不能使用容器 ID、随机 UUID 或每次部署变化的值。上线前还要与 charge 侧确认该 Site ID 对应的映射项目已经配置并发布。

### 7.2 失败日志 Body

`BILL_SHIP_LOG_FAILED_BODY=false` 时，失败日志仍会记录：

```text
reason, source_type, site_id, model, log_id, created_at, attempts, err
```

运维可以使用 `source_type=one-api + log_id` 回查 one-api 的 `logs` 表。只有设置为 `true` 才会把完整 Body 写入失败日志，生产默认保持 `false`。

### 7.3 发送参数说明与调优原则

| 环境变量 | 默认值 | 合法范围 | 说明 |
|---|---:|---:|---|
| `BILL_SHIP_BUFFER_SIZE` | 10000 | `> 0` | 本地内存缓冲记录数；越大越能吸收短时抖动，但占用更多内存 |
| `BILL_SHIP_BATCH_SIZE` | 10 | 1～10 | 单次批量条数；SQS `SendMessageBatch` 硬上限为 10 |
| `BILL_SHIP_BATCH_WAIT_MS` | 200 | `> 0` | 低流量下半满批的最长等待时间 |
| `BILL_SHIP_SEND_CONCURRENCY` | 8 | `> 0` | 并发发送 worker 数；提高会增加 SQS 请求并发 |
| `BILL_SHIP_SEND_TIMEOUT_SECONDS` | 3 | `> 0` | 单次 SQS API 请求超时 |
| `BILL_SHIP_MAX_RETRIES` | 3 | `>= 0` | 可重试错误的最大重试次数；0 为快速失败且记录失败日志 |

推荐首次生产上线保持默认值，不要同时调整多个参数。只有监控出现明确证据时再修改：

- 持续出现 `reason=dropped`：先排查 SQS 网络、权限、限流和发送失败；确认只是短时尖峰后再考虑增加 Buffer 或并发。
- 低流量消息延迟过高：可以适当减小 `BILL_SHIP_BATCH_WAIT_MS`，代价是 SQS 请求数增加。
- SQS 限流或网络压力明显：先降低 `BILL_SHIP_SEND_CONCURRENCY`，不要增加重试次数放大故障。
- 单条或整批消息接近 256KB 时，SDK 会自动提前切批，实际批量条数可能小于 `BILL_SHIP_BATCH_SIZE`。

这些参数只在 one-api 启动时读取。修改 `.env` 后必须重建容器：

```bash
docker compose up -d --no-deps --force-recreate ezlinkai
```

启动成功日志会打印最终生效值，但不会打印 Queue URL、AWS 密钥或消息 Body：

```text
bill shipper initialized, site=one-api-prod-us-01 buffer_size=10000 batch_size=10 batch_wait=200ms send_concurrency=8 send_timeout=3s max_retries=3
```

任一发送参数非法时，shipper 会记录 `bill shipper config invalid` 并禁用投递，one-api 主业务仍继续运行。

## 8. 生产配置安全要求

当前仓库的 `docker-compose.yml` 仍存在历史遗留的明文数据库、Redis 和 Metrics 配置。正式生产部署前应把实际值迁移到服务器 `.env`、Docker Secret 或部署平台 Secret，Compose 中只保留变量引用。

推荐形态：

```yaml
environment:
  - SQL_DSN=${SQL_DSN:?SQL_DSN is required}
  - REDIS_CONN_STRING=${REDIS_CONN_STRING:?REDIS_CONN_STRING is required}
  - METRICS_TOKEN=${METRICS_TOKEN:?METRICS_TOKEN is required}
```

如果这些明文值已经进入远端 Git 历史，仅从当前文件删除还不够，应立即轮换相关凭证。

## 9. 构建和发布镜像

### 9.1 构建前验证

```bash
go mod verify
go build ./...
go vet ./...
go test ./common/shipper/... -race -count=1
go list -m github.com/changshiaos/charge/server/shipper
```

最后一条命令预期输出：

```text
github.com/changshiaos/charge/server/shipper v0.1.0
```

确认 shipper 只替换到仓库内固定快照：

```bash
go list -m -f '{{if eq .Path "github.com/changshiaos/charge/server/shipper"}}{{.Path}} => {{.Replace.Path}}{{end}}' all
# 应输出：github.com/changshiaos/charge/server/shipper => ./third_party/charge-shipper-v0.1.0
```

### 9.2 GitHub Actions 构建

当前流水线行为：

| Workflow | 触发方式 | 主要镜像 |
|---|---|---|
| `docker-dev.yml` | push 到 `dev` 或手动触发 | `ye4293xx7/one-api-test:*` |
| `docker-image-amd64.yml` | push tag 或手动触发 | `ye4293xx7/one-api:*`，amd64 |
| `docker-image-arm64.yml` | push 非 alpha tag 或手动触发 | `ye4293xx7/one-api:*`，amd64+arm64 |

注意：当前两个生产 workflow 都会被普通 tag 触发，并可能向相同镜像 tag 推送结果。正式发版前必须明确服务器架构并决定使用哪一条生产流水线，避免两个 workflow 并发覆盖同名镜像。amd64 服务器只需要 amd64 流水线；需要多架构镜像时应使用多架构流水线，并避免重复发布同一 tag。

推荐先验证开发镜像：

1. 把确认后的代码推送到 `dev`。
2. 打开 GitHub **Actions → Build Dev Image**。
3. 确认 checkout 后存在 `third_party/charge-shipper-v0.1.0/go.mod`。
4. 确认普通 `go mod download`、前端构建和 Go build 通过；无需配置 charge 私库 Secret。
5. 使用生成的不可变版本 tag 部署测试环境，不要只依赖 `latest`。
6. 测试通过后再按团队发布规范创建生产 tag。

不要在未评审和未验证的本地工作区直接创建生产 tag。

### 9.3 本地构建镜像

```bash
docker buildx build \
  --load \
  -t one-api:billship-v0.1.0 .
```

该构建只需要访问公开的 Go/npm 镜像源和目标镜像仓库，不访问 charge 私库。

## 10. 灰度部署步骤

推荐先关闭投递部署镜像，再打开开关，便于区分“镜像升级问题”和“SQS 接入问题”。

### 10.1 记录当前版本

```bash
docker compose ps
docker inspect ezlinkai2 --format '{{.Config.Image}}'
```

记录当前可回滚镜像 tag，不要只记录 `latest`。

### 10.2 先以关闭状态部署新镜像

在 `.env` 设置：

```dotenv
BILL_SHIP_ENABLED=false
```

执行：

```bash
docker compose pull ezlinkai
docker compose up -d --no-deps --force-recreate ezlinkai
docker compose ps
curl -fsS http://127.0.0.1:5001/api/status
docker compose logs --since=10m ezlinkai
```

此时应看到：

```text
bill shipper disabled
```

### 10.3 打开计费投递

确认镜像本身正常后，把 `.env` 改为：

```dotenv
BILL_SHIP_ENABLED=true
```

重建容器：

```bash
docker compose up -d --no-deps --force-recreate ezlinkai
docker compose logs --since=5m ezlinkai
```

成功日志：

```text
bill shipper initialized, site=one-api-prod-us-01
```

以下日志表示没有启用成功：

```text
bill shipper config invalid, shipping disabled: ...
bill shipper init failed, shipping disabled: ...
```

## 11. 上线后端到端验证

只看到初始化成功还不够，必须验证真实消息。

### 11.1 发起真实请求

用生产允许的最小测试账号发送一笔真实模型请求，记录请求时间、`x-request-id`、模型、one-api `logs.id` 和 Site ID。

### 11.2 检查 one-api

确认：

1. 用户请求正常完成，没有因计费投递增加明显延迟。
2. one-api `logs` 表存在对应的成功消费日志。
3. 应用日志没有以下内容：

```text
billship ship failed
AccessDenied
QueueDoesNotExist
InvalidClientTokenId
ExpiredToken
bill shipper shutdown timeout
```

### 11.3 检查 SQS 和 charge

不要给 one-api 运行身份增加 `ReceiveMessage` 权限；生产者只需要发送权限。使用具备队列查看权限的运维身份核对消息：

```text
MessageAttribute.site_id     = BILL_SHIP_SITE_ID
MessageAttribute.model       = 本次请求使用的模型名
MessageAttribute.source_type = one-api
Body                         = 写入 logs 表后的行 JSON
Body.id                      = one-api logs.id
```

如果 charge consumer 正在运行，消息可能被迅速消费。此时应在 charge 消费日志或最终明细表中按 `site_id`、模型、请求时间和请求 ID 核对。

### 11.4 观察 15～30 分钟

持续观察：

- one-api 请求成功率和延迟；
- `billship ship failed` 数量；
- `reason=dropped`：本地 buffer 满；
- `reason=invalid`：消息属性或 Body 不合法；
- `reason=send_failed`：SQS 发送最终失败；
- SQS `NumberOfMessagesSent` 和 `ApproximateNumberOfMessagesVisible`；
- charge consumer 消费成功率、DLQ 和数据库写入情况。

## 12. 快速关闭和回滚

### 12.1 只关闭计费投递

这是首选方式，不影响 one-api 主业务：

```dotenv
BILL_SHIP_ENABLED=false
```

```bash
docker compose up -d --no-deps --force-recreate ezlinkai
docker compose logs --since=5m ezlinkai
```

确认日志出现 `bill shipper disabled`。

### 12.2 回滚整个镜像

如果新镜像影响 one-api 其他功能，把 Compose 镜像改回部署前记录的不可变 tag：

```yaml
image: ye4293xx7/one-api:<previous-version>
```

```bash
docker compose pull ezlinkai
docker compose up -d --no-deps --force-recreate ezlinkai
```

不要把 `latest` 当作可靠回滚版本，因为它可能已经被新的构建覆盖。

### 12.3 数据补偿边界

billship 是“尽力发送”模式：SQS 暂时失败会自动重试，最终失败会记录 `source_type`、`site_id` 和 `log_id`；进程异常崩溃仍可能丢失尚在内存中的消息。需要补数据时，用 `source_type=one-api + log_id` 回查 one-api `logs` 表，再按运维补投流程处理。

## 13. 常见故障排查

### CI 报本地 shipper 目录缺失

确认 `third_party/charge-shipper-v0.1.0` 已提交并推送，且 `.dockerignore` 没有忽略 `third_party`。

### 构建仍然访问 charge 私库

确认 `go.mod` 中的 `replace` 路径正确，Dockerfile 在 `go mod download` 前已经复制该目录，并检查构建使用的是包含这些修改的新镜像上下文。

### 启动日志显示配置缺失

```bash
docker compose config | grep 'BILL_SHIP_'
```

确认 `.env` 与 Compose 文件在同一部署目录，并用 `--force-recreate` 重建容器。

### `AccessDenied`

检查 IAM Policy 是否使用目标 Queue ARN、跨账号 Queue Policy 是否允许发送身份，以及 KMS Key Policy 是否满足要求。

### `QueueDoesNotExist`

检查 Queue URL、Region、AWS 账号和环境是否正确。

### `InvalidClientTokenId` 或 `ExpiredToken`

检查 Access Key 是否错误、临时凭证是否过期、是否遗漏 `AWS_SESSION_TOKEN`。使用 IAM Role 时确认容器能访问实例元数据或 ECS credential endpoint。

### `reason=dropped`

表示内存 buffer 已满，为避免阻塞用户请求而丢弃本次投递。先排查 SQS 网络、权限和持续限流，再使用失败日志中的 `log_id` 补投。

### 初始化成功但看不到消息

依次确认：产生的是成功消费日志、日志已写入 `logs` 表、charge consumer 没有快速消费掉消息、Queue URL 指向正确环境，并按 `logs.id` 在 charge 侧核对最终明细。

## 14. 凭证与依赖维护

### shipper 版本升级

1. 从 charge 对应发布 tag 获取完整 shipper 模块源码。
2. 新建 `third_party/charge-shipper-vX.Y.Z`，不要覆盖旧目录，以便回滚和审计。
3. 同步修改 `go.mod` 中的 `require` 版本和 `replace` 目录。
4. 执行 `go mod tidy`、`go test ./third_party/charge-shipper-vX.Y.Z/...`、`go test ./common/shipper` 和完整构建。
5. 将新快照、`go.mod`、`go.sum` 和相关代码一起提交；确认新镜像后再删除不再使用的旧快照。

### AWS Access Key

1. 创建新 Access Key。
2. 更新部署系统 Secret 或服务器 `.env`。
3. 重建 one-api 容器。
4. 发一笔测试请求确认 SQS 成功。
5. 禁用并删除旧 Access Key。

推荐逐步迁移到 IAM Role 或短期凭证。

## 15. 上线记录模板

```text
部署时间：
执行人：
one-api Git commit：
one-api 镜像完整 tag/digest：
shipper 版本：v0.1.0
部署环境：
服务器架构：amd64 / arm64
BILL_SHIP_SITE_ID：
SQS Queue ARN：
SQS Region：
AWS 身份名称（不要记录密钥）：
灰度测试 logs.id：
灰度测试 x-request-id：
charge 侧核对结果：
回滚镜像 tag：
观察结束时间：
异常与处理：
```

完成这份记录，才算完成一次可审计、可回滚的生产上线。
