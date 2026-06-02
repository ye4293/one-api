# 上游模型自动同步功能移植总结

> 日期：2026-06-02  
> 涉及仓库：`ezlinkai`（后端）、`ezlinkai-web-next`（前端）  
> 参考来源：`new-api` 项目 `controller/channel_upstream_update.go`

---

## 背景

newapi 具备「渠道上游模型自动同步」功能：定时调用渠道的 `/v1/models` 接口，与本地配置的模型列表对比，检测新增/已删除模型，支持自动或手动应用变更。本次将该功能完整移植到 ezlinkai，同时修复了移植过程中发现的 2 个已有 Bug，并在代码审查阶段修复了 6 个新问题。

---

## 一、Bug 修复（移植前）

### 1.1 多 Key 渠道无法获取上游模型列表

**文件**：`controller/channel.go`（`FetchUpstreamModels`）

**问题**：`IsMultiKey=false` 的旧式换行分隔多 Key 渠道，密钥选择逻辑被跳过，`channel.Key` 原始字符串（含 `\n`）直接作为 API Key 发送，导致认证失败。

**修复**：不再依赖 `IsMultiKey` 标志，改为**始终调用 `ParseKeys()` 解析**，选取第一个启用状态的 Key，所有 Key 均禁用时回退到 `keys[0]`。

覆盖的场景：
- 单 Key 渠道 → 正常
- `IsMultiKey=true` 多 Key → 选取第一个启用 Key
- 旧式 `\n` 分隔、`IsMultiKey=false` → 修复此前的 Bug
- 所有 Key 均被禁用 → 降级使用 `keys[0]`

---

## 二、功能移植

### 2.1 后端（ezlinkai）

#### 新增文件

| 文件 | 内容 |
|------|------|
| `common/config/channel_other_settings.go` | `ChannelOtherSettings` struct，存储 6 个巡检配置字段 |
| `controller/channel_upstream_update.go` | 全部核心逻辑（见下） |

#### 修改文件

| 文件 | 变更 |
|------|------|
| `model/channel.go` | `Channel` 新增 `OtherSettings string`（`gorm:"column:settings"`）字段；新增 `GetOtherSettings()`、`SetOtherSettings()`、`GetModels()` 三个方法 |
| `controller/channel.go` | `updateChannelFields()` 新增 `other_settings` 字段的更新处理 |
| `router/api-router.go` | 注册 4 条新路由 |
| `main.go` | 启动后台定时巡检任务 |

#### 核心逻辑（`channel_upstream_update.go`）

**数据流**：

```
定时任务 / HTTP 触发
    ↓
fetchChannelUpstreamModelList()        调用上游 /v1/models，复用已有的
    → buildModelsURL / getAuthHeader /  buildModelsURL / getAuthHeader
      fetchModelsFromURL
    ↓
upstreamCollectPendingChangesFromModels()  差异计算
    → 新增模型 = 上游有、本地无（排除 redirect target 和忽略列表）
    → 待删除  = 本地有、上游无（排除 redirect source）
    ↓
checkAndPersistUpstreamChanges()       持久化结果
    → 可选自动合入新增模型（auto_sync_enabled=true）
    → 写入 settings 列（JSON）
    → 若模型变更则刷新 abilities 表和内存缓存
```

**API 接口**：

| 路由 | 说明 |
|------|------|
| `POST /api/channel/upstream_updates/detect` | 强制检测单个渠道，body: `{"id": 1}` |
| `POST /api/channel/upstream_updates/apply` | 应用单个渠道变更，body: `{"id":1,"add_models":[...],"remove_models":[...],"ignore_models":[...]}` |
| `POST /api/channel/upstream_updates/detect_all` | 批量检测所有启用巡检的渠道 |
| `POST /api/channel/upstream_updates/apply_all` | 批量应用所有待处理变更 |

**渠道配置字段**（存储于 `channels.settings` JSON 列，AutoMigrate 自动建列）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `upstream_model_update_check_enabled` | bool | 开启定时巡检 |
| `upstream_model_update_auto_sync_enabled` | bool | 自动同步新增模型 |
| `upstream_model_update_last_check_time` | int64 | 上次检测时间（Unix 秒） |
| `upstream_model_update_last_detected_models` | []string | 待加入模型列表 |
| `upstream_model_update_last_removed_models` | []string | 待删除模型列表 |
| `upstream_model_update_ignored_models` | []string | 永久忽略列表 |

**环境变量**：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_ENABLED` | `true` | 关闭定时任务 |
| `CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_INTERVAL_MINUTES` | `30` | 巡检间隔（分钟） |
| `CHANNEL_UPSTREAM_MODEL_UPDATE_MIN_CHECK_INTERVAL_SECONDS` | `300` | 单渠道最小检测冷却（秒） |

**与 newapi 的主要适配差异**：

| newapi | ezlinkai 适配方案 |
|--------|-----------------|
| `GetNextEnabledKey()` | `ParseKeys()` + `GetKeyStatus()` 手动选取 |
| `GetModels()` 方法 | 新增同名方法，分割 `channel.Models` 逗号字符串 |
| `channel.GetSetting().Proxy` | 不需要（复用已有 `fetchModelsFromURL`） |
| `UpdateAbilities(nil)` | `UpdateAbilities()`（无参数） |
| `service.NotifyUpstreamModelUpdateWatchers` | `logger.SysLog` 记录摘要 |
| Ollama 专用抓取 | 跳过（无 ollama relay 包） |
| `common.GetEnvOrDefault` | `os.Getenv` + `sync.Once` 缓存 |

---

### 2.2 前端（ezlinkai-web-next）

#### 新增文件

```
app/api/channel/upstream_updates/
    ├── detect/route.ts
    ├── apply/route.ts
    ├── detect_all/route.ts
    └── apply_all/route.ts
```

#### 修改文件

| 文件 | 变更 |
|------|------|
| `lib/types/channel.ts` | `Channel` 接口新增 `other_settings?: string` |
| `sections/channel/channel-form.tsx` | 渠道编辑页新增「上游模型巡检」配置卡片 |

#### UI 功能

渠道编辑页底部新增「上游模型巡检」卡片：

- **开关 1**：开启上游模型巡检（写入 `upstream_model_update_check_enabled`）
- **开关 2**：自动同步新增模型（写入 `upstream_model_update_auto_sync_enabled`，仅巡检开启时显示）
- **立即检测**（编辑模式）：调用 `detect` 接口，展示新增/待删除模型列表
- **操作按钮**：全部添加 / 全部删除 / 全部应用（新增+删除）
- **状态回显**：上次检测时间、无变更时显示「与上游一致」提示

---

## 三、代码审查修复

代码审查共发现 6 个问题（3 个 Bug / 3 个性能与可维护性），全部已修复：

### Bug 修复

| # | 问题 | 修复方式 |
|---|------|----------|
| 1 | `fetchChannelUpstreamModelList` 缺少 `keys[0]` 兜底：所有 Key 禁用时 raw 多行字符串作为 API Key → 401 | 补充 `if selected == "" { selected = keys[0] }` |
| 2 | 后台任务冷却期未区分「跳过」与「真正执行」，`LastDetectedModels` 旧数据被重复计入 `addedTotal` | `checkAndPersistUpstreamChanges` 新增 `ran bool` 返回值，仅 `ran=true` 时累计指标 |

### 性能优化

| # | 问题 | 修复方式 |
|---|------|----------|
| 3 | `regexp.MatchString` 在 per-model × per-rule 嵌套循环内每次重新编译正则，O(N×K) 次编译 | 循环外预编译成 `[]*regexp.Regexp` 切片，内层 O(1) 调用 |
| 4 | `getUpstreamMinCheckInterval()` 每渠道调用 `os.Getenv`（1000 渠道 = 1000 次系统调用） | `sync.Once` 进程级缓存，整个生命周期仅读一次 |

### 可维护性优化

| # | 问题 | 修复方式 |
|---|------|----------|
| 5 | `ApplyChannelUpstreamModelUpdates` 对同一对象调用 `GetOtherSettings()` 两次，冗余 JSON 反序列化 | `doApplyChannelUpstreamModelUpdates` 改为接收 `settings` 参数，上层传入 |
| 6 | `runTaskOnce` / `DetectAll` / `ApplyAll` 三处包含完全相同的 20 行分页查询循环 | 提取 `queryUpstreamChannelBatch()` 公共函数 |

---

## 四、分支与推送记录

| 仓库 | 操作 | 结果 |
|------|------|------|
| ezlinkai | `ins` ← fast-forward merge `main` | ✅ |
| ezlinkai | `origin/ins` push | ✅ |
| ezlinkai | `origin/main` ← merge remote + push | ✅（顺带合入远端 Gemini bugfix） |
| ezlinkai-web-next | `ins` ← merge `develop` + merge `github/ins` | ✅ 无冲突 |
| ezlinkai-web-next | `github/ins` push | ✅ |
| ezlinkai-web-next | `github/main` ← `develop` push | ✅ |
