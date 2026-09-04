# 计划：abilities 持久化模型级禁用原因 + 整渠道禁用原因拼接真实错误

- **日期**: 2026-08-31
- **分支**: main（实现时可按需切 feature 分支）
- **类型**: feat / schema 变更

## 背景与目标

当前模型级自动禁用（`AutoDisableModelOnChannel`）只在 abilities 行写
`auto_disabled` / `auto_disabled_time`，上游返回的真实错误只进系统日志，不落库。
后果：

1. 前端「模型自动禁用」视角看不到每个模型被禁的原因，排查只能翻日志；
2. 「最近使用的模型全部被自动禁用」升级为整渠道禁用后，
   `channels.auto_disabled_reason` 只能写通用文案
   `最近使用中的 N 个模型全部被自动禁用`，最后那个模型的真实上游错误已丢失。

**目标**：

- abilities 表新增 `auto_disabled_reason` 列，模型级禁用时持久化上游错误（截断存储）；
- `DisableChannelByRecentUsage` 触发整渠道禁用时，取该渠道**最后一条**被禁模型的
  (model, reason)，拼接成：

  ```
  最近使用中的 N 个模型全部被自动禁用，最后模型禁用原因：<上游错误>（模型：<model>）
  ```

  同时 `channels.auto_disabled_model` 写该模型名（当前为空）。
- 前端零改动（仍读 `auto_disabled_reason`），飞书/邮件通知内容自动受益。

## 方案设计

### 1. `model/ability.go`

- `Ability` 结构体新增字段（AutoMigrate 主节点启动自动加列）：

  ```go
  AutoDisabledReason string `json:"auto_disabled_reason" gorm:"type:varchar(1024);default:''"`
  ```

- `AutoDisableModelOnChannel`：updates map 增加
  `"auto_disabled_reason": truncateReason(reason, 1024)`；reason 为空时写 `""`。
  截断 helper 放本文件（按 rune 截断，避免截出半个中文字符）。

- `EnableModelOnChannel`：恢复的 updates map 增加 `"auto_disabled_reason": ""`，
  与 `auto_disabled_time: 0` 对齐，防止恢复后残留旧原因。

- 新增查询函数：

  ```go
  // GetLatestAutoDisabledModelReason 返回该渠道最近一条被模型级禁用的 (model, reason)。
  // ORDER BY auto_disabled_time DESC LIMIT 1；多分组行的 time 相同，任取一行即可。
  // 无候选返回 ("", "", nil)，调用方回退通用文案。
  func GetLatestAutoDisabledModelReason(channelId int) (modelName, reason string, err error)
  ```

  实现：

  ```sql
  SELECT model, auto_disabled_reason FROM abilities
  WHERE channel_id = ? AND auto_disabled = true AND auto_disabled_time > 0
  ORDER BY auto_disabled_time DESC LIMIT 1
  ```

  走 `channel_id` 索引，单渠道模型数 <100，代价亚毫秒级。

### 2. `monitor/channel.go`

`DisableChannelByRecentUsage(channelId, usedModels)`：

1. 取 channel 后（现有逻辑不变，多 Key 仍跳过）；
2. 调 `model.GetLatestAutoDisabledModelReason(channelId)`；
3. 组装 reason：

   ```go
   reason := fmt.Sprintf("最近使用中的 %d 个模型全部被自动禁用", usedModels)
   if lastModel != "" {
       reason += fmt.Sprintf("，最后模型禁用原因：%s（模型：%s）", lastReason, lastModel)
   }
   ```

   查询失败或无结果（存量数据该列为空）→ 保持现有通用文案，不因新功能失败。
4. `disableChannelInternalWithStatusCode(channel, channelId, channel.Name, reason, modelName, 0)`
   ——把最后被禁模型名传入，落 `channels.auto_disabled_model`。

注意：`auto_disabled_time` 仍由 `AutoDisableChannelById` 在整渠道禁用时统一写，
本计划不改变熔断计数 / 探针退避相关字段的任何现有写入时机。

### 3. 不改动的部分

- 多 Key 渠道（key 级禁用 / all keys disabled）、metric 成功率、filter 巡检、
  上游模型同步禁用：各自的原因来源不变；
- 前端：零改动；
- 恢复探针、usage-based 判定 SQL（`ShouldDisableChannelByRecentUsage`）：不变。

## 影响范围

- **schema 变更**：abilities 加一列 varchar(1024) default ''。主节点启动
  AutoMigrate 自动完成（model/main.go:130）。abilities 行数 = 渠道×模型×分组，
  万级以内，在线 DDL 秒级，无 logs 表式 MDL 锁风险。存量行为空字符串，语义安全。
- **对现有功能**：无行为变更。唯一可见变化是整渠道禁用后的
  `auto_disabled_reason` / `auto_disabled_model` 内容变详细（含通知文案）。
- **数据迁移**：无需手工 SQL。保守起见可在部署前手工执行
  `ALTER TABLE abilities ADD COLUMN auto_disabled_reason varchar(1024) NOT NULL DEFAULT '';`
  由用户决策，不自动执行。

## 验证方式

1. `go build ./... && go vet ./...`
2. 单测：
   - `AutoDisableModelOnChannel` 写入 reason（含超长截断断言）；
   - `EnableModelOnChannel` 清空 reason；
   - `GetLatestAutoDisabledModelReason` 按 time DESC 返回最新一条、无候选返回空；
   - `ShouldDisableChannelByRecentUsage` 既有测试不回归。
   - monitor 层拼接受 `disableChannelByRecentUsageFn` 注入影响，reason 拼接逻辑
     抽小函数后可直接单测（不依赖 DB 的纯字符串组装）。
3. 本地 sqlite 起服务，手动构造：模型级禁用（reason 落 abilities）→ 触发
   usage-based 判定 → 查 channels.auto_disabled_reason 为拼接后文案。
