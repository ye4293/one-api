# BigQuery 审计查看器

- **日期**：2026-06-22
- **状态**：实现中
- **分支**：`bigQuery`

## 背景与目标

审计模块已完成写入链路（Storage Write API → BigQuery），但缺少**读取/查询**能力。需要一个管理后台页面，允许管理员：
1. 通过 `x_request_id` 精确查找审计记录（与 `model.Log` 表关联排查问题）
2. 按 `event_time` 日期范围分页浏览数据
3. 查看单条记录的完整请求/响应详情

## 方案设计

### 后端 API（Go + Gin）

新增两个端点，均需 AdminAuth：

| 端点 | 说明 |
|------|------|
| `GET /api/audit/logs` | 分页列表（12 个摘要字段，不含 body） |
| `GET /api/audit/detail` | 单条完整记录（含 body 字段） |

核心文件：
- `common/audit/query.go` — BigQuery 参数化查询逻辑
- `controller/audit_viewer.go` — Gin 处理函数（参数校验 + 调用 query）
- `router/api-router.go` — 路由注册

查询成本控制：
- 强制 `start_timestamp` + `end_timestamp`（利用 DAY 分区裁剪）
- 最大跨度 31 天
- 列表查询只 SELECT 12 列摘要字段
- `x_request_id` 精确匹配利用 Clustering 第一列

### 前端页面（Next.js 14 + Shadcn/UI）

新增 `/dashboard/bigquery` 页面，仅管理员可见：
- 日期范围选择器（默认当天）
- 筛选栏：x_request_id、channel_id、actual_model、status_code
- 分页数据表格
- 点击"详情"按钮打开 Dialog，分 tab 展示完整请求/响应

核心文件：
- `app/dashboard/bigquery/page.tsx`
- `sections/bigquery/tables/index.tsx`（主组件）
- `sections/bigquery/tables/detail-dialog.tsx`（详情弹窗）
- `constants/data.ts`（菜单项）

## 影响范围

- 后端新增 2 个只读 API 端点，不影响现有写入链路
- 前端新增独立页面，不影响其他页面
- 不涉及数据库 schema 变更

## 验证方式

1. `go build ./... && go vet ./common/audit/ ./controller/ ./router/`
2. `AUDIT_ENABLED=false` 时 API 返回 "audit is not enabled"
3. 管理员登录后侧边栏可见 "Audit" 菜单
4. 按日期范围搜索、按 x_request_id 精确搜索均正常
5. 详情弹窗正确展示完整请求/响应
