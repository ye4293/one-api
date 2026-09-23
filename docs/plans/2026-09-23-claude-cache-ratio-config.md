# Claude 缓存倍率改为前端可配

## 背景与目标

`relay/controller/claude.go` 中的三个缓存倍率是编译期常量，无法在前端调整：

```go
claudeCache5mRatio   = 1.25  // 5 分钟缓存创建
claudeCache1hRatio   = 2.0   // 1 小时缓存创建
claudeCacheReadRatio = 0.1   // 缓存读取
```

目标：改为**前端可配置**，优先级 **配置 > 代码写死默认**；且**绝不影响其他模型（Gemini / OpenAI 等）的既有计费**。

## 关键约束与结论

1. **不能复用 `GetCacheRatio` 直接取值**：它未命中时 fallback 到 `GetCompletionRatio`，而后者对 `claude-` 前缀硬编码返回 3~5，会导致未配置的 Claude 模型缓存读被按 3~5 倍计费，永远到不了 0.1。
2. **`CacheWriteRatio` 尚未接入 option/前端体系**（仅 map + Get，无 JSON/Update/Default/pricing），且为 OpenAI Responses 专用。为不影响 OpenAI，Claude 的 5m/1h 写倍率**新建独立 map**，不复用它。
3. **读倍率复用 `CacheRatio` 存储**（已可前端配置），但取值走 Claude 专用函数，未命中固定回退 0.1（不经过补全倍率）。

## 方案设计

### 取值函数（`common/model-ratio.go`）

- `GetClaudeCacheReadRatio(name)`：查 `CacheRatio` / `DefaultCacheRatio`，未命中 → `0.1`
- 新增 `ClaudeCacheCreation5mRatio` map + `DefaultClaudeCacheCreation5mRatio`：`GetClaudeCacheCreation5mRatio(name)`，未命中 → `1.25`
- 新增 `ClaudeCacheCreation1hRatio` map + `DefaultClaudeCacheCreation1hRatio`：`GetClaudeCacheCreation1hRatio(name)`，未命中 → `2.0`
- 两张新 map 各配套：`init` 拷贝 Default、`XxxRatio2JSONString`、`UpdateXxxRatioByJSONString`、`AddNewMissingXxxRatio`

### option 接入（`model/option.go`）

- `InitOptionMap()`：新增 `ClaudeCacheCreation5mRatio`、`ClaudeCacheCreation1hRatio` 两个 OptionMap 键
- `updateOptionMap()`：新增两个 `case`，调用对应 `UpdateXxxByJSONString`
- `loadOptionsFromDatabase()`：新增两个 `AddNewMissing` 兼容行

### 前端接口（`controller/pricing.go`）

- `ModelPriceInfo`：新增 `ClaudeCache5mRatio`、`ClaudeCache1hRatio`（读倍率复用现有 `CacheRatio` 字段）
- 单模型 & 批量更新入口 struct：新增两个 `*float64` 字段
- 保存逻辑：新增两段（带 `> 0` 校验，挡误配 0/负值）
- `getAllModelPrices`：回填两个新字段；Claude 模型的 `CacheRatio` 展示改用 `GetClaudeCacheReadRatio`，保证展示与计费一致

### 计费与展示（`relay/controller/claude.go`）

- 删除三个 const
- `CalculateClaudeQuotaByRatio`：5m/1h/read 三处改调新取值函数（模型名已在函数内）
- `recordClaudeConsumption`：billingDetails 四处展示倍率改调同一取值函数（保证展示 == 计费）
- 修正硬编码 `×1.25/×2.0/×0.1` 的日志文案

## 影响范围

- **其他模型**：零影响。老 `GetCacheRatio` / `GetCacheWriteRatio` 签名与行为不变；新 map 仅 Claude 使用。
- **存量数据**：仅新增 options 行，靠 Default + `AddNewMissing` 兜底，无 schema 迁移。
- **未配置时**：三档 fallback 精确等于原常量（0.1 / 1.25 / 2.0），行为与改造前逐位一致。
- **前端仓库**：真实前端在 `~/code/ezlinkai-web`，需另行同步新增字段的编辑 UI（本仓库仅提供后端接口）。

## 验证方式

1. `go build ./... && go vet ./...`
2. 构造 仅读 / 仅 5m / 仅 1h / 混合 usage，断言未配置时配额与旧常量一致
3. 断言 billingDetails 展示倍率 == 计费实际倍率
4. 验证配置某模型后取值随之变化、其他模型不受影响
