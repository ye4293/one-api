# 精准巡检：GET /api/channel/test?scope=filter 支持按类型/前缀 + 状态列表巡检 + 自动恢复

## Context

现有健康巡检机制存在盲区：

| 现有机制 | 范围 | 能力 |
|---|---|---|
| `GET /api/channel/test?scope=all` | 所有渠道（几千） | 测失败 → auto_disabled；测通不恢复 disabled |
| `GET /api/channel/test?scope=disabled` | status ∈ {2, 3} | 只测不恢复 |
| `GET /api/channel/test?scope=auto_disabled` | status=3 且 AutoEnabled=true | 只测不恢复 |
| `AutomaticallyTestChannels`（定时） | scope="auto_disabled" | 无 |
| `recoverAutoDisabledModels` | 模型级 auto_disabled | 逐 model 恢复，受 100 上限 + `AutomaticEnableChannelEnabled` 门控 |
| P1 `evaluateUsageBasedChannelDisable` | 有 auto_disabled abilities 且 status=1 | 纯 SQL 判定，不发上游请求 |

**缺失能力**：
1. **精准范围**：按 name 前缀 / type 筛选，不测全站
2. **主动恢复**：当运维明确要求测某状态的渠道时，测通就恢复
3. **auto_disabled 落地语义**：测失败走 auto_disabled=3（可后续被恢复探针救），而不是 manually_disabled=2

**典型场景**：
- 批次事件（如 `mi-tl-oai-*` OpenAI 系列 100% 失效）：精准清算这批，不打扰其他类型
- 供应商恢复后主动救活一批 auto_disabled 渠道
- 定期健康巡检某个 type / prefix，不消耗全站 quota

---

## 决策汇总

| 维度 | 选择 | 备注 |
|---|---|---|
| API 形态 | 扩展 `GET /api/channel/test?scope=filter` | 复用现有路由，只加 scope 值 |
| 筛选参数 | `keyword`（模糊 name）+ `type`（int）+ `status`（CSV） | 至少 keyword 或 type 必填 |
| 无 filter 参数时的行为 | 返回 400 拒绝 | 避免退化成 scope=all 误用 |
| status 参数默认值 | `1`（enabled） | 不传即"只对 enabled 巡检" |
| status 语义 | **参数自表达意图**：用户传什么 status 就代表"愿意对什么状态的渠道做动作" | 不需要额外 `recover_manual` flag |
| 测失败的落点 | `DisableChannelSafelyWithStatusCode` → **status=3 (auto_disabled)** | 与 scope=all 一致 |
| 测通的恢复 | `UpdateChannelStatusById(id, ChannelStatusEnabled)` → **status=1** | 无条件（用户主动触发） |
| 并发控制 | **独立 lock** `filterCheckRunning` | 与 `testAllChannelsRunning` 分离，不互相阻塞 |
| 恢复动作是否受 `AutomaticEnableChannelEnabled` 门控 | **不受** | 主动调接口 = 运维意图明确 |
| 禁用动作是否受 `AutomaticDisableChannelEnabled` 门控 | **不受** | 同上，主动调接口无条件执行；跟现有 `scope=all` 里 ShouldDisableChannel 命中路径也一致（该分支本来就不看这个开关）|

---

## status 语义详解

`status` 参数是**用户意图声明**：想对哪些状态的渠道做主动巡检。

| status 参数 | 候选范围 | 可能触发动作 |
|---|---|---|
| 不传 / `1` | 只测 enabled | 测失败 → auto_disabled；测通 no-op |
| `3` | 只测 auto_disabled | 测通 → enabled；测失败 no-op |
| `2` | 只测 manually_disabled | 测通 → enabled；测失败 no-op（**注意：用户主动传 2 视为明确要救**） |
| `1,3` | 混合 | 测失败的 status=1 → 3；测通的 status=3 → 1 |
| `1,2,3` | 全部三态 | 混合处理 |

**核心规则**（清晰简洁）：
- 测失败：**仅当 status=1 时**走 `DisableChannelSafelyWithStatusCode`
- 测通：**仅当 status ∈ {2, 3} 时**走 `EnableChannel`
- 其他组合 no-op

---

## 方案

### 1. `model/channel.go` `GetAllChannelsForTest` 扩展

签名扩展：

```go
func GetAllChannelsForTest(startIdx int, num int, scope string, keyword string, channelType *int, statusList []int) ([]*Channel, error) {
    ...
    switch scope {
    case "all":         // 现有
    case "disabled":    // 现有
    case "auto_disabled": // 现有
    case "filter":      // 新增
        q := DB.Order("id desc")
        if keyword != "" {
            q = q.Where("name LIKE ?", "%"+keyword+"%")
        }
        if channelType != nil {
            q = q.Where("type = ?", *channelType)
        }
        if len(statusList) > 0 {
            q = q.Where("status IN ?", statusList)
        }
        err = q.Find(&channels).Error
    }
    ...
}
```

**兼容性**：所有现有 caller 只传 scope，其余参数传 `""`, `nil`, `nil` 即可。签名变更需要一次性改所有调用点。

### 2. `controller/channel-test.go` `TestChannels` handler

新增 filter 参数解析 + 参数校验：

```go
func TestChannels(c *gin.Context) {
    scope := c.Query("scope")
    keyword := c.Query("keyword")
    typeStr := c.Query("type")
    statusStr := c.DefaultQuery("status", "1")

    var channelType *int
    if typeStr != "" {
        t, err := strconv.Atoi(typeStr)
        if err != nil || t < 0 {
            c.JSON(400, gin.H{"success": false, "message": "invalid type"})
            return
        }
        channelType = &t
    }

    var statusList []int
    for _, s := range strings.Split(statusStr, ",") {
        s = strings.TrimSpace(s)
        v, err := strconv.Atoi(s)
        if err != nil || v < 1 || v > 3 {
            c.JSON(400, gin.H{"success": false, "message": "invalid status: " + s})
            return
        }
        statusList = append(statusList, v)
    }

    if scope == "filter" {
        if keyword == "" && channelType == nil {
            c.JSON(400, gin.H{"success": false, "message": "filter mode requires keyword or type"})
            return
        }
    }

    err := testChannels(true, scope, keyword, channelType, statusList)
    ...
}
```

### 3. `controller/channel-test.go` `testChannels` 内部函数

新增签名 + 循环内分派：

```go
var filterCheckLock sync.Mutex
var filterCheckRunning bool = false

func testChannels(notify bool, scope, keyword string, channelType *int, statusList []int) error {
    // 独立 lock（filter scope 用 filterCheckLock，其他仍用 testAllChannelsLock）
    var lock *sync.Mutex
    var running *bool
    if scope == "filter" {
        lock = &filterCheckLock
        running = &filterCheckRunning
    } else {
        lock = &testAllChannelsLock
        running = &testAllChannelsRunning
    }
    lock.Lock()
    if *running {
        lock.Unlock()
        return errors.New("test already running")
    }
    *running = true
    lock.Unlock()

    channels, err := model.GetAllChannelsForTest(0, 0, scope, keyword, channelType, statusList)
    ...
    go func() {
        for _, channel := range channels {
            if !channel.AutoEnabled {
                logger.SysLog(...)
                continue
            }
            isChannelEnabled := channel.Status == common.ChannelStatusEnabled
            tik := time.Now()
            err, openaiErr, _, _ := testChannel(channel, "", true)
            tok := time.Now()
            ms := tok.Sub(tik).Milliseconds()

            testFailed := (err != nil) || (isChannelEnabled && ms > disableThreshold) || util.ShouldDisableChannel(openaiErr, -1)

            // 分派：
            //   测失败 && enabled  → auto_disabled
            //   测通  && !enabled  → enabled
            if testFailed && isChannelEnabled {
                if config.AutomaticDisableChannelEnabled {
                    monitor.DisableChannelSafelyWithStatusCode(channel.Id, channel.Name, err.Error(), "N/A (Test)", -1)
                }
            } else if !testFailed && !isChannelEnabled {
                // 主动触发的恢复，不受 AutomaticEnableChannelEnabled 门控
                if e := model.UpdateChannelStatusById(channel.Id, common.ChannelStatusEnabled); e != nil {
                    logger.SysError(fmt.Sprintf("filter check: enable channel %d failed: %s", channel.Id, e.Error()))
                } else {
                    logger.SysLog(fmt.Sprintf("filter check: channel #%d (%s) re-enabled after test success", channel.Id, channel.Name))
                }
            }

            channel.UpdateResponseTime(ms)
            time.Sleep(config.RequestInterval)
        }
        lock.Lock()
        *running = false
        lock.Unlock()
        ...
    }()
    return nil
}
```

**关键点**：
- `if !channel.AutoEnabled continue`：现有跳过条件保留 — 熔断锁死的渠道不参与巡检（即使测通也不救，因为运维明确锁死了）
- **测失败落 auto_disabled=3**（`DisableChannelSafelyWithStatusCode` 内部链路）
- **测通落 enabled=1**（`UpdateChannelStatusById`），无条件（用户主动触发）

### 4. 兼容性适配

`GetAllChannelsForTest` 签名变化，需要修改现有调用点：

- `controller/channel-test.go:540` `testChannels` 内部：修改签名后透传
- 其他调用点搜一下（应该只有此一处）

---

## API 使用示例

```bash
# 场景 1：巡检所有 mi-tl-oai openai 渠道（默认 status=1，只禁失败的）
GET /api/channel/test?scope=filter&keyword=mi-tl-oai&type=1

# 场景 2：救活所有 mi-tl-oai openai 里的 auto_disabled 渠道（信 OpenAI 复活了）
GET /api/channel/test?scope=filter&keyword=mi-tl-oai&type=1&status=3

# 场景 3：混合巡检 —— 禁失效的、救复活的
GET /api/channel/test?scope=filter&keyword=mi-tl-oai&type=1&status=1,3

# 场景 4：明确要救回一批手动禁用的（运维认为可以尝试）
GET /api/channel/test?scope=filter&keyword=mi-tl-oai&type=1&status=2

# 场景 5：所有 Gemini 渠道健康巡检
GET /api/channel/test?scope=filter&type=24&status=1,3
```

---

## 影响范围

**改动文件**：

| 文件 | 改动 |
|---|---|
| `model/channel.go` | `GetAllChannelsForTest` 加参数 + `filter` case |
| `controller/channel-test.go` | `TestChannels` handler 解析 + 校验；`testChannels` 内部签名 + 循环分派 + 独立 lock |
| `controller/channel-test_filter_test.go`（新增） | 单元测试 |

**不改**：
- `router/api-router.go`（复用 `GET /channel/test`）
- 前端（新 scope 值不需要 UI 支持；未来可加"精准巡检"按钮）
- 其他 handler（GetAllChannelsForTest 现有 caller 只有 `testChannels`，签名扩展影响面小）

**兼容性**：
- 现有 `scope=all/disabled/auto_disabled` 语义不变
- 新 filter case 只在明确传 `scope=filter` 时激活
- 没有 schema 变更

---

## 验证

### 编译与静态检查

```bash
go build ./... && go vet ./...
```

### 单元测试（新增 `controller/channel-test_filter_test.go`）

参考 `controller/evaluate_usage_disable_test.go` 用 sqlite + hook `monitor.DisableChannelSafelyWithStatusCode` 观察调用。

| 场景 | 输入 | 预期 |
|---|---|---|
| A 无 keyword + 无 type | scope=filter | 400 |
| B 只有 keyword | scope=filter&keyword=X | 200，只测匹配 X 的 |
| C 只有 type | scope=filter&type=1 | 200，只测 type=1 的 |
| D status 默认 1 | scope=filter&type=1 | 只测 enabled |
| E status=3 | scope=filter&type=1&status=3 | 只测 auto_disabled，测通恢复 |
| F status=1,3 | scope=filter&type=1&status=1,3 | 混合分派 |
| G status 非法值 | status=5 | 400 |
| H status=2 测通 | scope=filter&status=2，channel 测通 | 恢复到 enabled |
| I 并发 lock | 连跑两次 filter | 第二次返回 "test already running" |
| J filter 与 scope=all 并行 | 同时跑 filter 和 all | 两者互不阻塞（独立 lock） |
| K channel.AutoEnabled=false | 熔断锁死的渠道 | 跳过（不测、不禁、不恢复） |
| L 测失败但 status=3 | 已 auto_disabled 且测失败 | no-op（已经禁了）|

### 手工验证（本地 sqlite）

```bash
# 起本地服务，插入几个测试渠道
curl 'http://localhost:3000/api/channel/test?scope=filter&keyword=test&type=1&status=1,3' \
     -H 'Authorization: Bearer $ADMIN_TOKEN'

# 观察日志：
#   "filter check: channel #X re-enabled after test success"
#   "channel #Y has been disabled: ..."
```

### 端到端验证

线上部署后：

```bash
# 巡检 mi-tl-oai 系列，禁 enabled 里失效的
curl -X GET "$EZLINKAI_URL/api/channel/test?scope=filter&keyword=mi-tl-oai&type=1" \
     -H "Authorization: Bearer $EZLINKAI_API_KEY"

# 之后跑救援：测试上一步禁的（现在应该都在 status=3），看能不能自愈
curl -X GET "$EZLINKAI_URL/api/channel/test?scope=filter&keyword=mi-tl-oai&type=1&status=3" \
     -H "Authorization: Bearer $EZLINKAI_API_KEY"
```

---

## 风险与回滚

**风险**：
- 新 `EnableChannel` 路径未经过 `AutomaticEnableChannelEnabled` 门控，运维万一忘了这批渠道有问题手动传 status=2 会把它们救活
- **缓解**：日志会打印 "filter check: channel #X re-enabled"，便于事后审计
- **回滚**：跟前端"批量禁用"或手动 SQL `UPDATE channels SET status=2 WHERE id IN (...)`

**兼容性风险**：`GetAllChannelsForTest` 签名变化，其他包若引用会编译失败
- **缓解**：全项目搜一遍调用点，一次性修完

**回滚方案**：本次改动局限在 filter case，删除 case + 签名回滚即可。

---

## 后续（不在本次范围）

- 前端"精准巡检"按钮，支持 GUI 选择 type/prefix
- 巡检结果邮件/飞书报告（现在只走 log）
- 定期定时巡检（cronjob 化）

---

## CLAUDE.md 强制

- commit 后写 `docs/CHANGELOG.md` 一段（分支、类型、文件、说明、关联本计划）
