# 审计模块迁移：BigQuery → Firehose + Iceberg + S3 + Athena

- **日期**：2026-06-22
- **状态**：已完成
- **分支**：`AthenaQuery`

## 背景与目标

审计数据从 AWS 跨云传输到 GCP BigQuery 产生 $184-368/月 egress 费用。迁移到 AWS 原生栈消除该成本。

## 方案设计

```
Producer (Go) → Firehose PutRecordBatch (JSON) → Iceberg Table (Glue) → S3
                                                                         ↓
                                               Frontend ← Backend ← Athena Query
```

- **写入**：JSON → Firehose PutRecordBatch（500 条/4MB 分片，部分失败重试一次）
- **查询**：Athena StartQueryExecution → 500ms 轮询 → GetQueryResults（30s 超时）
- **SQL 安全**：严格白名单正则（x_request_id: hex/UUID，model: 字母数字+符号，数字: strconv）
- **Compaction**：每日 Athena OPTIMIZE REWRITE DATA USING BIN_PACK
- **建表**：Glue API CreateDatabase + CreateTable（Iceberg，day 分区，x_request_id 排序）

## 影响范围

- 后端 `common/audit/` 写入和查询层完全重写
- `controller/audit_viewer.go` 和前端页面无需改动（接口层不变）
- 移除 GCP BigQuery 依赖，新增 AWS Firehose/Athena/Glue SDK

## 验证方式

1. `go build ./... && go vet ./common/audit/` 通过
2. `go test ./common/audit/...` 全部 19 个测试通过
3. 集成验证需真实 AWS 环境：PutRecordBatch → 等 90s → Athena 查询可见
