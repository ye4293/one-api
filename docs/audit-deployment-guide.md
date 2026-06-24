# 审计模块部署指南 (Firehose + Iceberg + Athena)

分支: `AthenaQuery`

---

## 一、AWS 前置资源准备

### 1. S3 Bucket

创建一个 S3 Bucket（或复用已有的），用于存储 Iceberg 数据文件和 Athena 查询结果。

```bash
# 示例：在 us-west-2 创建
aws s3 mb s3://your-audit-bucket --region us-west-2
```

规划好两个路径前缀：
- **数据存储**: `s3://your-audit-bucket/audit/data/` — Iceberg 表文件
- **查询结果**: `s3://your-audit-bucket/audit/athena-output/` — Athena 查询临时结果

### 2. Firehose Delivery Stream

创建 Direct PUT → Iceberg 的 Delivery Stream。

> 注意：Firehose 直接写入 Iceberg 需要在 Firehose 控制台（或 CloudFormation/Terraform）中配置 Iceberg 作为目标，指定 Glue Database/Table。应用代码会自动通过 Glue API 创建 Database 和 Table（见下方），所以**先部署应用让它建表，再创建 Firehose Stream 指向该表**，或者手动先建好 Glue 资源。

**推荐流程**：
1. 先通过应用启动一次（会自动调 `ensureGlueResources` 创建 Glue Database + Iceberg Table）
2. 再在 AWS 控制台创建 Firehose Delivery Stream，目标选 Iceberg Table

**Firehose 配置要点**：
- Source: Direct PUT
- Destination: Apache Iceberg Tables
- Database: `audit`（对应 `AUDIT_ATHENA_DATABASE`）
- Table: `request_logs`（对应 `AUDIT_ATHENA_TABLE`）
- S3 bucket: `s3://your-audit-bucket/audit/data/`
- Buffer: 128 MB / 900 秒（按量调整）
- Compression: 不用额外设（Iceberg 层已配 zstd）

### 3. IAM 用户/角色

创建专用 IAM 用户（或角色），赋予以下权限：

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Firehose",
      "Effect": "Allow",
      "Action": [
        "firehose:PutRecord",
        "firehose:PutRecordBatch"
      ],
      "Resource": "arn:aws:firehose:REGION:ACCOUNT:deliverystream/YOUR_STREAM_NAME"
    },
    {
      "Sid": "Athena",
      "Effect": "Allow",
      "Action": [
        "athena:StartQueryExecution",
        "athena:GetQueryExecution",
        "athena:GetQueryResults",
        "athena:StopQueryExecution"
      ],
      "Resource": "arn:aws:athena:REGION:ACCOUNT:workgroup/*"
    },
    {
      "Sid": "Glue",
      "Effect": "Allow",
      "Action": [
        "glue:CreateDatabase",
        "glue:CreateTable",
        "glue:GetDatabase",
        "glue:GetTable",
        "glue:UpdateTable"
      ],
      "Resource": [
        "arn:aws:glue:REGION:ACCOUNT:catalog",
        "arn:aws:glue:REGION:ACCOUNT:database/audit",
        "arn:aws:glue:REGION:ACCOUNT:table/audit/*"
      ]
    },
    {
      "Sid": "S3",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": [
        "arn:aws:s3:::your-audit-bucket",
        "arn:aws:s3:::your-audit-bucket/audit/*"
      ]
    }
  ]
}
```

记录下 Access Key ID 和 Secret Access Key。

### 4. Athena Workgroup（可选）

默认使用 `primary` workgroup。如需隔离查询成本或设置单独限额：

```bash
aws athena create-work-group \
  --name audit-workgroup \
  --configuration ResultConfiguration={OutputLocation=s3://your-audit-bucket/audit/athena-output/}
```

---

## 二、环境变量配置

### 必填项（缺少任一审计模块自动降级关闭）

| 变量 | 示例值 | 说明 |
|------|--------|------|
| `AUDIT_ENABLED` | `true` | 总开关 |
| `AUDIT_AWS_REGION` | `us-west-2` | AWS 区域 |
| `AUDIT_AWS_ACCESS_KEY` | `AKIA...` | IAM Access Key |
| `AUDIT_AWS_SECRET_KEY` | `wJal...` | IAM Secret Key |
| `AUDIT_FIREHOSE_STREAM` | `audit-request-logs` | Firehose Delivery Stream 名称 |
| `AUDIT_S3_OUTPUT_LOCATION` | `s3://your-audit-bucket/audit/athena-output/` | Athena 查询结果输出路径 |
| `AUDIT_S3_DATA_LOCATION` | `s3://your-audit-bucket/audit/data/` | Iceberg 表数据存储路径 |

### 可选项（有合理默认值）

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `AUDIT_ATHENA_DATABASE` | `audit` | Glue/Athena 数据库名 |
| `AUDIT_ATHENA_TABLE` | `request_logs` | Glue/Athena 表名 |
| `AUDIT_ATHENA_WORKGROUP` | `primary` | Athena workgroup |
| `AUDIT_CHANNEL_SIZE` | `2000` | 内存 channel 缓冲大小 |
| `AUDIT_BATCH_SIZE` | `500` | 每批发送记录数上限 |
| `AUDIT_FLUSH_INTERVAL_SEC` | `10` | 批次刷新间隔（秒） |
| `AUDIT_MAX_BUFFER_MB` | `1024` | 内存缓冲上限（MB） |
| `AUDIT_DISK_BUFFER_DIR` | `./data/audit_spill` | 磁盘溢出缓冲目录 |
| `AUDIT_DISK_BUFFER_MAX_GB` | `40` | 磁盘溢出缓冲上限（GB） |
| `AUDIT_MAX_BODY_KB` | `10240` | 请求体最大采集大小（KB） |
| `AUDIT_MAX_RESP_KB` | `4096` | 响应体最大采集大小（KB） |
| `AUDIT_REDACT_HEADERS` | `Authorization,Api-Key,...` | 需脱敏的 Header（逗号分隔） |
| `AUDIT_RETENTION_DAYS` | `0`（禁用） | 数据保留天数，>0 时 compaction 自动清理过期数据 |

### Compaction 定时任务

Compaction（BIN_PACK 合并小文件 + 可选的数据留存清理）每 24 小时执行一次，**复用已有的 poller 开关保证只在一台机器上跑**：

| 变量 | 值 | 说明 |
|------|-----|------|
| `ENABLE_VIDEO_TASK_POLLER` | `true` | **仅在一个实例上设为 true**，该实例会同时运行视频任务轮询和审计 compaction |

---

## 三、部署检查清单

### 首次部署

- [ ] S3 Bucket 已创建，两个路径前缀确认
- [ ] IAM 用户已创建，权限策略已附加，AK/SK 已记录
- [ ] 环境变量已配置（至少 7 个必填项）
- [ ] 部署应用 → 观察日志确认 `audit: Glue resources ensured` 打印
- [ ] Firehose Delivery Stream 已创建，目标指向 Glue Database/Table
- [ ] 发一条测试请求 → 观察日志确认无 `putRecordBatch 失败` 报错
- [ ] Athena 控制台手动执行 `SELECT * FROM audit.request_logs LIMIT 5` 验证数据可查
- [ ] 调用 `GET /api/audit/logs` 验证查询 API 正常返回
- [ ] 确认**一个且仅一个**实例设置了 `ENABLE_VIDEO_TASK_POLLER=true`

### 数据留存（可选）

- [ ] 决定保留天数 → 设置 `AUDIT_RETENTION_DAYS`（例如 `90`）
- [ ] 等待一次 compaction 周期后检查日志确认 `retention cleanup done`

---

## 四、测试环境最小配置模板

```env
# === 审计模块 ===
AUDIT_ENABLED=true
AUDIT_AWS_REGION=us-west-2
AUDIT_AWS_ACCESS_KEY=AKIA_YOUR_KEY
AUDIT_AWS_SECRET_KEY=YOUR_SECRET
AUDIT_FIREHOSE_STREAM=audit-request-logs-test
AUDIT_S3_OUTPUT_LOCATION=s3://your-test-bucket/audit/athena-output/
AUDIT_S3_DATA_LOCATION=s3://your-test-bucket/audit/data/

# 可选覆盖
AUDIT_ATHENA_DATABASE=audit_test
AUDIT_ATHENA_TABLE=request_logs
AUDIT_RETENTION_DAYS=30

# 测试环境缩小缓冲
AUDIT_CHANNEL_SIZE=500
AUDIT_DISK_BUFFER_MAX_GB=5

# 单机执行 compaction（测试环境只有一个实例时直接开）
ENABLE_VIDEO_TASK_POLLER=true
```

---

## 五、故障排查

| 日志关键字 | 含义 | 处理方式 |
|-----------|------|----------|
| `缺少 AWS 凭证配置，自动降级为关闭` | AK/SK/Region 未配置 | 检查环境变量 |
| `缺少 AUDIT_FIREHOSE_STREAM` | Stream 名称未配置 | 设置 `AUDIT_FIREHOSE_STREAM` |
| `Glue 建表失败，降级为关闭` | IAM 权限不足或区域不对 | 检查 Glue 权限和 Region |
| `putRecordBatch 失败，转落盘` | Firehose 写入失败 | 检查 Stream 是否存在、IAM firehose 权限 |
| `磁盘缓冲已满，丢弃 N 条记录` | 持续写入失败 + 磁盘也满了 | 排查 Firehose 连通性，清理磁盘 |
| `compaction failed` | OPTIMIZE 执行失败 | 检查 Athena 权限和 S3 写权限 |
| `athena query timed out` | 查询超时 | 数据量过大或 Athena 资源不足 |
| `retention delete failed` | 留存清理失败 | 检查 Iceberg 表 S3 写/删权限 |
