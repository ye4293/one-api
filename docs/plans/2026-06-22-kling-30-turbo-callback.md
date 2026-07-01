# Kling 3.0 Turbo 回调处理（v2）

## 背景与目标

Kling 3.0 Turbo 的回调格式与 v1 完全不同（字段名、状态值、结构全部改变），无法复用现有 `HandleKlingCallback`。需要新增独立的 v2 回调端点，使 3.0 Turbo 及后续新模型走 `/kling/internal/callback/v2`，老模型继续走 `/kling/internal/callback`。

## 方案设计

### 核心改动

1. **新增 v2 回调结构体** (`relay/channel/kling/model.go`)
   - `Callback30TurboNotification`：对应 3.0 Turbo 回调 JSON
   - `Callback30TurboOutput`：outputs 数组项
   - `Callback30TurboBilling`：billing 数组项

2. **新增 `succeeded` 状态常量** (`relay/channel/kling/constants.go`)
   - 3.0 Turbo 用 `succeeded` 表示成功，内部映射为 `succeed`

3. **修改 `buildCallbackURL`** (`controller/kling_video.go`)
   - 增加 `requestType` 参数，3.0 Turbo 类型返回 `/callback/v2` 路径

4. **新增 `HandleKling30TurboCallback`** (`controller/kling_video.go`)
   - 解析 v2 格式回调
   - 通过 `external_id` 或 `id` 查找任务
   - 从 `billing[]` 累加计费金额
   - 复用 `NotifyUserCallback` 转发给用户

5. **注册路由** (`router/relay-router.go`)
   - `POST /kling/internal/callback/v2`

## 影响范围

- 仅影响 3.0 Turbo 新模型的回调处理
- 现有 v1 回调逻辑完全不变
- `buildCallbackURL` 签名变更，但所有调用点已同步更新

## 验证方式

```bash
go build ./... && go vet ./...
```

功能验证：
1. 创建 3.0 Turbo 任务，确认 `options.callback_url` 指向 `/callback/v2`
2. 模拟发送 v2 格式回调，确认任务状态正确更新和扣费
3. v1 回调回归测试
