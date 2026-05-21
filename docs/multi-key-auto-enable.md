# 多Key渠道自动启用修复方案

## 背景

### 现有状态码定义

| 常量 | 值 | 含义 |
|------|-----|------|
| `ChannelStatusEnabled` | 1 | 正常启用 |
| `ChannelStatusManuallyDisabled` | 2 | 手动禁用 |
| `ChannelStatusAutoDisabled` | 3 | 自动禁用 |

### 渠道有两个独立开关

| 字段 | 含义 |
|------|------|
| `AutoDisabled bool` | 是否允许被系统自动禁用，默认 true |
| `AutoEnabled bool` | 是否允许被系统自动恢复，默认 true |

---

## 问题描述

多Key渠道的Key被逐个自动禁用后，当所有Key都进入 `auto_disabled` 状态，渠道整体也会被置为 `auto_disabled`。

此后定时自动测试（`AutomaticallyTestChannels`）会查出该渠道并尝试恢复，但流程在"选Key"阶段就失败退出，**永远无法自动恢复**。

### 当前失败链路

```
AutomaticallyTestChannels
  └─ testChannels(scope="auto_disabled")
       └─ testChannel(channel, "", true)
            └─ GetNextAvailableKey()
                 └─ enabledIndices 为空 → "no enabled keys available"
                      └─ testChannel 立即返回 err != nil
                           └─ ShouldEnableChannel(err, nil) → false
                                └─ 渠道永远保持 auto_disabled ✗
```

### 根本原因

`testChannel` 的Key选择逻辑只能选 `enabled` 状态的Key。所有Key都是 `auto_disabled` 时，连HTTP请求都不会发出，测试必然失败。

`model/channel.go:1329` 有一行注释明确说明这是已知遗留问题：
```go
// 这里暂时不自动重新启用渠道，需要管理员手动启用
```

---

## 需求

### 期望行为

1. 定时测试发现多Key渠道全部Key为 `auto_disabled` 时，对**所有** `auto_disabled` 的Key**并发**发起测试请求
2. 只将**测试成功**的Key状态重置为 `enabled`
3. 测试失败的Key保持 `auto_disabled` 不变
4. `manually_disabled` 的Key**不参与测试，不受影响**
5. 只要有**至少一个**Key测试成功，渠道恢复 `enabled`，发送通知
6. 全部Key测试失败，渠道保持 `auto_disabled`，等待下一个定时周期重试

### 不在本次范围内

- 正常流量中的Key错误处理逻辑（`HandleKeyError`）不改动
- 单Key渠道逻辑不改动
- 手动测试（`TestChannel` API）行为不改动

---

## 实现方案

### 涉及文件

- `controller/channel-test.go`
- `model/channel.go`

### Step 1：提取 `getChannelTestModel` 函数

**文件**：`controller/channel-test.go`

**目的**：将"确定测试使用的模型名"逻辑从 `testChannel` 中抽出，供并发测试复用。

**签名**：
```go
func getChannelTestModel(channel *model.Channel) (string, error)
```

**逻辑**：
1. 若 `channel.TestModel != ""` → 直接使用
2. 否则取 `channel.Models` 第一个（逗号分隔）
3. 若模型名命中 `isUnsupportedTestModel` → 返回 error（跳过该渠道）
4. 若最终 modelName 为空 → 返回 error

---

### Step 2：提取 `testChannelWithKey` 函数

**文件**：`controller/channel-test.go`

**目的**：将 `testChannel` 中"发HTTP请求并解析结果"的核心逻辑抽出，支持外部传入指定Key，用于并发测试。

**签名**：
```go
func testChannelWithKey(
    channel *model.Channel,
    testKey string,
    keyIndex int,
    modelName string,
) (err error, openaiErr *relaymodel.Error)
```

**逻辑**：等同于现在 `testChannel` 从 `c.Request.Header.Set("Authorization"...)` 开始到函数结束的部分，去掉选Key和确定模型的代码。

**注意**：`auto_enable=true` 时的 `recordChannelTestConsumeLog` 调用保留在此函数内。

---

### Step 3：新增 `testAllAutoDisabledKeys` 函数

**文件**：`controller/channel-test.go`

**目的**：对多Key渠道所有 `auto_disabled` 的Key并发发起测试，返回测试成功的Key索引列表。

**签名**：
```go
func testAllAutoDisabledKeys(
    channel *model.Channel,
    modelName string,
) []int
```

**逻辑**：
```
1. 遍历 channel.MultiKeyInfo.KeyStatusList
   收集所有 status == ChannelStatusAutoDisabled 的 (keyIndex, keyStr) 对

2. 若无 auto_disabled 的Key → 返回空列表

3. 用 sync.WaitGroup 为每个 key 启动一个 goroutine
   每个 goroutine 调用 testChannelWithKey(channel, keyStr, keyIndex, modelName)

4. 用 mutex 保护结果收集，成功的 keyIndex 写入 successIndices

5. WaitGroup.Wait() 等待所有 goroutine 结束

6. 返回 successIndices
```

---

### Step 4：修改 `checkAndUpdateChannelStatus`

**文件**：`model/channel.go:1329`

**目的**：当 `auto_disabled` 渠道中重新出现 `enabled` 的Key时，自动将渠道恢复为 `enabled`。

**改动**：将原来的注释改为实际执行的恢复逻辑：

```go
// 改前：
} else if channel.Status == common.ChannelStatusAutoDisabled && enabledCount > 0 {
    // 这里暂时不自动重新启用渠道，需要管理员手动启用
    logger.SysLog(fmt.Sprintf("Channel %d has %d enabled keys but remains auto-disabled, manual intervention required",
        channel.Id, enabledCount))
}

// 改后：
} else if channel.Status == common.ChannelStatusAutoDisabled && enabledCount > 0 {
    channel.Status = common.ChannelStatusEnabled
    logger.SysLog(fmt.Sprintf("Channel %d auto-re-enabled: %d/%d keys are now enabled",
        channel.Id, enabledCount, totalKeys))
}
```

---

### Step 5：新增 `ReEnableKeysByIndices` 方法

**文件**：`model/channel.go`

**目的**：将指定索引的Key从 `auto_disabled` 恢复为 `enabled`，更新缓存和DB，返回渠道是否因此变为 `enabled`。

**签名**：
```go
func (channel *Channel) ReEnableKeysByIndices(indices []int) (channelNowEnabled bool, err error)
```

**逻辑**：
```
1. 获取 per-channel statusLock（与 HandleKeyError 同一把锁）

2. CacheGetChannelCopy 读取最新状态的深拷贝

3. 对 indices 里的每个 keyIndex：
   a. 若当前状态不是 auto_disabled → 跳过（避免误重置 manually_disabled）
   b. 从 KeyStatusList 中删除该索引（恢复默认 enabled）
   c. 清空 KeyMetadata[keyIndex] 中的禁用信息：
      DisabledReason = nil
      DisabledTime   = nil
      StatusCode     = nil
      DisabledModel  = nil

4. 调用 fresh.checkAndUpdateChannelStatus()
   → enabledCount > 0 且渠道是 auto_disabled → 渠道状态改为 enabled（Step 4 已修改）

5. 记录 channelNowEnabled = (fresh.Status == ChannelStatusEnabled)

6. 更新缓存：CacheUpdateChannelMultiKeyInfo(fresh.Id, fresh.MultiKeyInfo, fresh.Status)

7. DB 事务写入：
   a. 若 channelNowEnabled：UPDATE abilities SET enabled=true WHERE channel_id=?
   b. UPDATE channels SET multi_key_info=?, status=? WHERE id=?

8. 返回 channelNowEnabled, err
```

---

### Step 6：修改 `testChannels`，接入新逻辑

**文件**：`controller/channel-test.go:499`

**目的**：在原有循环中，对"多Key渠道且全部Key为 `auto_disabled`"的情况，走新的并发测试分支。

**新增辅助函数**：
```go
// allKeysAutoDisabled 判断多Key渠道是否所有Key都是 auto_disabled
func allKeysAutoDisabled(channel *model.Channel) bool
```

**循环内改动**（在原有 `testChannel` 调用前插入）：

```go
// 多Key渠道且全部Key都是 auto_disabled：走并发测试分支
if channel.MultiKeyInfo.IsMultiKey && allKeysAutoDisabled(channel) {

    modelName, err := getChannelTestModel(channel)
    if err != nil {
        logger.SysLog(fmt.Sprintf("skip multi-key channel #%d (%s): %v", channel.Id, channel.Name, err))
        time.Sleep(config.RequestInterval)
        continue
    }

    tik := time.Now()
    successIndices := testAllAutoDisabledKeys(channel, modelName)
    milliseconds := time.Since(tik).Milliseconds()
    channel.UpdateResponseTime(milliseconds)

    if len(successIndices) > 0 {
        channelEnabled, err := channel.ReEnableKeysByIndices(successIndices)
        if err != nil {
            logger.SysError(fmt.Sprintf("failed to re-enable keys for channel #%d: %v", channel.Id, err))
        } else if channelEnabled {
            monitor.EnableChannel(channel.Id, channel.Name)
        }
    } else {
        logger.SysLog(fmt.Sprintf("all keys failed for multi-key channel #%d (%s), remaining disabled", channel.Id, channel.Name))
    }

    time.Sleep(config.RequestInterval)
    continue  // 跳过下方原有的 enable/disable 判断
}

// 原有逻辑（单Key渠道 或 多Key渠道仍有可用Key）
tik := time.Now()
err, openaiErr, _, _ := testChannel(channel, "", true)
// ... 后续不变
```

---

## 数据流总览

```
testChannels(scope="auto_disabled")
  │
  └─ for each channel
       │
       ├─ [AutoEnabled=false] → skip
       │
       ├─ [多Key 且 全部Key=auto_disabled] ← 新分支
       │    │
       │    ├─ getChannelTestModel() → modelName
       │    │
       │    ├─ testAllAutoDisabledKeys(channel, modelName)
       │    │    ├─ goroutine per key → testChannelWithKey()
       │    │    └─ 返回 successIndices
       │    │
       │    ├─ [successIndices 为空] → log，保持 auto_disabled
       │    │
       │    └─ [successIndices 非空]
       │         ├─ ReEnableKeysByIndices(successIndices)
       │         │    ├─ 清除成功Key的 auto_disabled 状态
       │         │    ├─ checkAndUpdateChannelStatus → channel.Status=enabled
       │         │    └─ 写缓存 + 写DB
       │         └─ monitor.EnableChannel() → 发飞书/邮件通知
       │
       └─ [其他情况] → 原有逻辑不变
```

---

## 边界情况

| 场景 | 处理 |
|------|------|
| 全部Key均为 `manually_disabled` | `allKeysAutoDisabled` 返回 false，走原有逻辑（`GetNextAvailableKey` 失败），不参与自动测试 |
| 部分Key `auto_disabled`，部分 `manually_disabled` | `allKeysAutoDisabled` 返回 false，不走新分支（`GetNextAvailableKey` 还有 manually_disabled 会报错，但这是存量行为，不在本次修复范围） |
| 所有Key测试均失败 | `successIndices` 为空，渠道保持 `auto_disabled`，下一定时周期重试 |
| 部分Key成功，部分失败 | 只重置成功的Key；失败的Key保持 `auto_disabled`；只要有一个成功，渠道即恢复 |
| 成功Key后续在真实请求中再次出错 | 由 `HandleKeyError` 正常处理，逻辑不变 |
| `ReEnableKeysByIndices` 写DB失败 | 不调 `monitor.EnableChannel`，等下一周期重试 |
| `channel.AutoEnabled = false` | 已在循环入口 `continue`，不进入新分支 |
| 渠道类型不支持聊天测试（Kling/Flux等） | `getChannelTestModel` 中 `isUnsupportedTestChannel` 检查返回 error，直接 skip |

---

## 执行顺序建议

依赖关系决定实现顺序：

```
Step 4（checkAndUpdateChannelStatus）  ← 无依赖，最先改，改动最小
Step 1（getChannelTestModel）          ← 无依赖，纯提取
Step 2（testChannelWithKey）           ← 依赖 Step 1 的提取结果
Step 3（testAllAutoDisabledKeys）      ← 依赖 Step 2
Step 5（ReEnableKeysByIndices）        ← 依赖 Step 4
Step 6（testChannels 修改）            ← 依赖 Step 1/3/5，最后收尾
```

每步完成后建议运行 `go build ./... && go vet ./...` 确认编译通过。
