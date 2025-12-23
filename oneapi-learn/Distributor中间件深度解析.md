# Distributor 中间件深度解析

> One API 核心中间件 - 智能请求分发与渠道选择
> 文件位置: `middleware/distributor.go`
> 最后更新: 2025-12-23

---

## 📋 目录

1. [中间件概述](#中间件概述)
2. [核心功能](#核心功能)
3. [工作流程](#工作流程)
4. [API路径处理](#api路径处理)
5. [渠道选择机制](#渠道选择机制)
6. [多Key聚合机制](#多key聚合机制)
7. [与OpenAI API的对比](#与openai-api的对比)
8. [代码详解](#代码详解)
9. [常见问题](#常见问题)

---

## 中间件概述

### 🎯 核心作用

Distributor 中间件是 One API 的"**交通枢纽**"，负责：
- 从请求中提取模型名称
- 为用户选择合适的AI服务渠道
- 支持多种AI服务商的API格式（OpenAI、Gemini、Midjourney、Stability AI等）
- 设置渠道上下文信息（API Key、Base URL、配置等）
- 支持多Key聚合和故障重试

### 🔄 在中间件链中的位置

```
HTTP请求
  ↓
[Auth中间件] → 验证用户身份
  ↓
[Distributor中间件] → 选择渠道（本文件）
  ↓
[RateLimit中间件] → 速率限制
  ↓
[Controller] → 业务处理
  ↓
[Adaptor] → 调用实际AI服务
  ↓
HTTP响应
```

### 💡 为什么需要这个中间件？

**问题1**: OpenAI API 的标准请求格式
```json
{
  "model": "gpt-4",
  "messages": [...]
}
```

但不同AI服务商有不同的API格式：
- **Gemini**: 路径包含模型 `/v1beta/models/gemini-2.0-flash:generateContent`
- **Midjourney**: 自定义请求格式
- **Stability AI**: 不同的端点结构

**问题2**: 同一个模型可能有多个渠道
- 渠道1: OpenAI官方（优先级高）
- 渠道2: Azure OpenAI（备用）
- 渠道3: 代理服务（价格便宜）

**解决方案**: Distributor 中间件统一处理这些差异！

---

## 核心功能

### 功能1: 模型名称提取

从不同格式的请求中提取模型名称：

| API类型 | 模型来源 | 示例 |
|---------|---------|------|
| **OpenAI标准** | 请求Body的`model`字段 | `{"model": "gpt-4"}` |
| **Gemini API** | URL路径解析 | `/v1beta/models/gemini-2.0-flash:generateContent` |
| **Midjourney** | 自定义请求格式 | MidjourneyRequest |
| **Stability AI** | 根据RelayMode推断 | 根据端点路径 |

### 功能2: 渠道选择

根据以下因素选择最优渠道：
- ✅ 用户组权限
- ✅ 请求的模型
- ✅ 渠道优先级
- ✅ 渠道权重（负载均衡）
- ✅ 渠道可用性

### 功能3: 上下文设置

为选定的渠道设置必要信息：
- API Key (支持多Key轮询)
- Base URL
- 渠道配置（API Version、插件等）
- 模型映射关系

### 功能4: 特定渠道ID支持

如果请求指定了渠道ID（通过`specific_channel_id`），直接使用该渠道，跳过选择逻辑。

---

## 工作流程

### 完整流程图

```
┌─────────────────────────────────────────┐
│  1. 获取用户ID和用户组                    │
│     userId := c.GetInt("id")            │
│     userGroup := CacheGetUserGroup()    │
└──────────────┬──────────────────────────┘
               ↓
┌─────────────────────────────────────────┐
│  2. 检查是否指定特定渠道                  │
│     specific_channel_id 存在？          │
└──────────────┬──────────────────────────┘
               ↓
         ┌─────┴─────┐
         │           │
        是           否
         ↓           ↓
   ┌─────────┐  ┌──────────────────────┐
   │直接使用 │  │ 3. 根据路径识别API类型│
   │该渠道   │  │   - /mj → Midjourney │
   └─────────┘  │   - /v2beta → SD     │
                │   - /v1beta/models → │
                │     Gemini           │
                │   - 其他 → OpenAI    │
                └──────┬───────────────┘
                       ↓
                ┌──────────────────────┐
                │ 4. 提取模型名称       │
                │   - 从Body解析       │
                │   - 从URL路径解析    │
                │   - 从特殊格式解析   │
                └──────┬───────────────┘
                       ↓
                ┌──────────────────────┐
                │ 5. 选择合适的渠道     │
                │   CacheGetRandomSat- │
                │   isfiedChannel()    │
                └──────┬───────────────┘
                       ↓
                ┌──────────────────────┐
                │ 6. 设置渠道上下文     │
                │   - API Key          │
                │   - Base URL         │
                │   - 配置信息         │
                └──────┬───────────────┘
                       ↓
                ┌──────────────────────┐
                │ 7. 调用 c.Next()      │
                │    继续执行后续中间件  │
                └──────────────────────┘
```

### 关键步骤详解

#### 步骤1: 获取用户信息
```go
userId := c.GetInt("id")  // 由Auth中间件设置
userGroup, _ := model.CacheGetUserGroup(userId)
c.Set("group", userGroup)
```

#### 步骤2: 检查特定渠道
```go
channelId, ok := c.Get("specific_channel_id")
if ok {
    // 直接使用指定的渠道，跳过选择逻辑
    channel, err = model.GetChannelById(id, true)
    // ...验证渠道状态
}
```

#### 步骤3: API类型识别
```go
// Midjourney API
if strings.HasPrefix(c.Request.URL.Path, "/mj") {
    relayMode := Path2RelayModeMidjourney(path)
    // ... 处理Midjourney请求
}

// Stability AI
else if strings.HasPrefix(c.Request.URL.Path, "/v2beta") {
    relayMode := Path2RelayModeSd(path)
    // ... 处理SD请求
}

// Gemini API
else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") {
    relayMode := Path2RelayModeGemini(path)
    modelName := extractModelNameFromGeminiPath(path)
    // ... 处理Gemini请求
}

// OpenAI标准格式
else {
    err = common.UnmarshalBodyReusable(c, &modelRequest)
    requestModel = modelRequest.Model
}
```

#### 步骤4: 渠道选择
```go
channel, err = model.CacheGetRandomSatisfiedChannel(
    userGroup,    // 用户组
    requestModel, // 模型名称
    0             // 渠道类型（0表示不限制）
)
```

**选择算法** (在 `model/channel.go` 中实现):
1. 过滤出用户组可用的渠道
2. 过滤出支持该模型的渠道
3. 过滤出状态为"启用"的渠道
4. 按优先级排序
5. 在相同优先级中按权重随机选择

#### 步骤5: 设置上下文
```go
SetupContextForSelectedChannel(c, channel, requestModel)
```

---

## API路径处理

### 1. OpenAI 标准格式

#### 请求格式
```bash
POST /v1/chat/completions
Content-Type: application/json
Authorization: Bearer sk-xxxxx

{
  "model": "gpt-4",
  "messages": [
    {"role": "user", "content": "Hello"}
  ]
}
```

#### 处理逻辑
```go
// 从请求Body中解析模型
err = common.UnmarshalBodyReusable(c, &modelRequest)
requestModel = modelRequest.Model  // "gpt-4"
```

#### 特殊端点处理

**Moderations**:
```go
if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
    if modelRequest.Model == "" {
        modelRequest.Model = "text-moderation-stable"  // 默认模型
    }
}
```

**Embeddings**:
```go
if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
    if modelRequest.Model == "" {
        modelRequest.Model = c.Param("model")  // 从路径参数获取
    }
}
```

**Images**:
```go
if strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations") {
    if modelRequest.Model == "" {
        modelRequest.Model = "dall-e-2"  // 默认模型
    }
}
```

**Audio**:
```go
if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") ||
   strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations") {
    if modelRequest.Model == "" {
        modelRequest.Model = "whisper-1"  // 默认模型
    }
}
```

### 2. Gemini API 格式

#### 请求格式
```bash
POST /v1beta/models/gemini-2.0-flash:generateContent
POST /v1/models/gemini-pro:streamGenerateContent
POST /v1alpha/models/gemini-exp-1206:generateContent
```

#### 模型名称提取

Gemini的模型名称在URL路径中，格式为 `/models/{model_name}:{action}`

```go
func extractModelNameFromGeminiPath(path string) string {
    // 输入: "/v1beta/models/gemini-2.0-flash:generateContent"
    // 或: "/gemini-2.0-flash:generateContent" (通配符参数)

    // 1. 移除开头的 /
    if strings.HasPrefix(path, "/") {
        path = path[1:]
    }

    // 2. 查找 /models/ 位置
    modelsIndex := strings.Index(path, "/models/")
    if modelsIndex != -1 {
        path = path[modelsIndex+8:]  // 跳过 "/models/"
    }

    // 3. 提取 : 之前的模型名称
    colonIndex := strings.Index(path, ":")
    if colonIndex == -1 {
        return path  // 如果没有 :，返回整个字符串
    }

    modelName := path[:colonIndex]  // "gemini-2.0-flash"
    return modelName
}
```

#### 示例

| 输入路径 | 输出模型名称 |
|---------|-------------|
| `/v1beta/models/gemini-2.0-flash:generateContent` | `gemini-2.0-flash` |
| `/v1/models/gemini-pro:streamGenerateContent` | `gemini-pro` |
| `/v1alpha/models/gemini-exp-1206:generateContent` | `gemini-exp-1206` |
| `/gemini-2.0-flash:generateContent` | `gemini-2.0-flash` |

#### 处理逻辑
```go
if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") ||
   strings.HasPrefix(c.Request.URL.Path, "/v1/models/") ||
   strings.HasPrefix(c.Request.URL.Path, "/v1alpha/models/") {

    relayMode := relayconstant.Path2RelayModeGemini(c.Request.URL.Path)
    if relayMode == relayconstant.RelayModeUnknown {
        abortWithMessage(c, http.StatusBadRequest,
            "Invalid gemini request path: " + c.Request.URL.Path)
        return
    }

    modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
    if modelName == "" {
        abortWithMessage(c, http.StatusBadRequest,
            "Invalid gemini request path: " + c.Request.URL.Path)
        return
    }

    modelRequest.Model = modelName
    c.Set("relay_mode", relayMode)
}
```

### 3. Midjourney API 格式

#### 请求格式
```bash
POST /mj/submit/imagine
POST /mj/submit/change
POST /mj/task/{taskId}/fetch
```

#### 处理逻辑
```go
if strings.HasPrefix(c.Request.URL.Path, "/mj") {
    relayMode := relayconstant.Path2RelayModeMidjourney(c.Request.URL.Path)

    // 某些操作不需要选择渠道（如查询任务）
    if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
       relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
       relayMode == relayconstant.RelayModeMidjourneyNotify ||
       relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
        shouldSelectChannel = false
    } else {
        // 解析MJ请求，提取模型信息
        midjourneyRequest := midjourney.MidjourneyRequest{}
        err = common.UnmarshalBodyReusable(c, &midjourneyRequest)

        midjourneyModel, mjErr, success :=
            midjourney.GetMjRequestModel(relayMode, &midjourneyRequest)

        modelRequest.Model = midjourneyModel
    }

    c.Set("relay_mode", relayMode)
}
```

### 4. Stability AI 格式

#### 请求格式
```bash
POST /v2beta/stable-image/generate/sd3
POST /sd/upscale/creative
```

#### 处理逻辑
```go
if strings.HasPrefix(c.Request.URL.Path, "/v2beta") ||
   strings.HasPrefix(c.Request.URL.Path, "/sd") {

    relayMode := relayconstant.Path2RelayModeSd(c.Request.URL.Path)

    // 某些操作不需要选择渠道（如获取结果）
    if relayMode == relayconstant.RelayModeUpscaleCreativeResult ||
       relayMode == relayconstant.RelayModeVideoResult {
        shouldSelectChannel = false
    }

    sdModel, err := stability.GetSdRequestModel(relayMode)
    if err != nil {
        abortWithMessage(c, http.StatusBadRequest, "Invalid request")
        return
    }

    modelRequest.Model = sdModel
    c.Set("relay_mode", relayMode)
}
```

---

## 渠道选择机制

### 渠道数据结构

```go
type Channel struct {
    Id          int       // 渠道ID
    Type        int       // 渠道类型（OpenAI=1, Azure=3, Gemini=15等）
    Name        string    // 渠道名称
    Key         string    // API密钥
    BaseURL     string    // API基础URL
    Models      string    // 支持的模型列表（逗号分隔）
    Priority    int       // 优先级（数字越小越优先）
    Weight      int       // 权重（用于负载均衡）
    Status      int       // 状态（1=启用，0=禁用）
    TestTime    int64     // 最后测试时间
    Config      string    // 渠道配置（JSON）
    MultiKeyInfo MultiKeyInfo // 多Key信息
}
```

### 选择算法详解

**核心函数**: `model.CacheGetRandomSatisfiedChannel(userGroup, model, channelType)`

**选择步骤**:

```
1. 查询数据库/缓存，获取所有渠道
   ↓
2. 第一轮过滤：基本条件
   - Status == 1 (启用状态)
   - 用户组有权限使用该渠道
   - 渠道支持请求的模型
   ↓
3. 第二轮过滤：健康检查
   - TestTime在有效期内
   - 最近没有频繁失败
   ↓
4. 排序：按优先级升序
   Priority=0 > Priority=1 > Priority=2
   ↓
5. 选择：权重随机算法
   相同优先级的渠道，按权重随机选择
   ↓
6. 返回选中的渠道
```

### 权重随机算法

假设有3个渠道，优先级相同：
- 渠道A: 权重=50
- 渠道B: 权重=30
- 渠道C: 权重=20
- 总权重=100

算法：
```go
// 1. 计算总权重
totalWeight := 50 + 30 + 20 = 100

// 2. 生成随机数 [0, 100)
randomValue := rand.Intn(100)  // 例如: 65

// 3. 累加权重，找到对应渠道
cumulative := 0
cumulative += 50  // 0-49 → 渠道A
cumulative += 30  // 50-79 → 渠道B (65落在这里！)
cumulative += 20  // 80-99 → 渠道C

// 4. 返回渠道B
```

**概率**:
- 渠道A: 50%
- 渠道B: 30%
- 渠道C: 20%

### 故障转移

当选中的渠道调用失败时：
1. 在 `relay/` 层捕获错误
2. 标记该渠道为失败（临时降低优先级）
3. 重新调用 Distributor 选择新渠道
4. 最多重试3次

---

## 多Key聚合机制

### 什么是多Key模式？

一个渠道可以配置多个API Key，系统会：
- 轮询使用（Round-robin）
- 失败自动切换
- 提高并发能力

### 数据结构

```go
type MultiKeyInfo struct {
    IsMultiKey  bool     // 是否启用多Key模式
    Keys        []string // Key列表
    CurrentIndex int     // 当前使用的Key索引
}
```

### 配置示例

**单Key模式**:
```json
{
  "key": "sk-xxxxx"
}
```

**多Key模式**:
```json
{
  "key": "sk-key1,sk-key2,sk-key3",
  "is_multi_key": true
}
```

### 工作流程

#### 1. 获取可用Key

```go
// 检查是否有排除的Key索引（用于重试时跳过失败的Key）
excludeIndices := getExcludedKeyIndices(c)

var actualKey string
var keyIndex int
var err error

if channel.MultiKeyInfo.IsMultiKey && len(excludeIndices) > 0 {
    // 多Key模式且有排除列表，使用带重试的方法
    actualKey, keyIndex, err = channel.GetNextAvailableKeyWithRetry(excludeIndices)
} else {
    // 正常获取Key
    actualKey, keyIndex, err = channel.GetNextAvailableKey()
}

if err != nil {
    logger.SysError(fmt.Sprintf("Failed to get available key for channel %d: %s",
        channel.Id, err.Error()))
    actualKey = channel.Key  // 回退到原始Key
    keyIndex = 0
}
```

#### 2. 存储Key信息

```go
// 存储Key信息供后续使用
c.Set("actual_key", actualKey)        // 实际使用的Key
c.Set("key_index", keyIndex)          // Key的索引
c.Set("is_multi_key", channel.MultiKeyInfo.IsMultiKey)  // 是否多Key
```

#### 3. 设置Authorization Header

```go
// 使用实际的Key
c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", actualKey))
```

#### 4. 记录使用的Key（脱敏）

```go
maskedKey := actualKey
if len(actualKey) > 8 {
    maskedKey = actualKey[:4] + "***" + actualKey[len(actualKey)-4:]
}
logger.SysLog(fmt.Sprintf("channel:%d;requestModel:%s;keyIndex:%d;maskedKey:%s",
    channel.Id, modelName, keyIndex, maskedKey))
```

输出示例:
```
channel:5;requestModel:gpt-4;keyIndex:1;maskedKey:sk-a***xyz1
```

### Key重试机制

#### 场景：某个Key失败

当某个Key调用失败（如401错误）时：

**1. 在Relay层检测到错误**
```go
// relay/channel/openai/main.go
if resp.StatusCode == 401 {
    // API Key无效
    if isMultiKey {
        // 标记这个Key为失败
        addExcludedKeyIndex(c, keyIndex)
        // 返回特殊错误，触发重试
        return nil, &ErrorNeedRetry{...}
    }
}
```

**2. Controller层捕获重试错误**
```go
// relay/controller/text.go
err := DoRequest(c, ...)
if err == ErrorNeedRetry {
    // 重新执行Distributor中间件
    // 此时会跳过已失败的Key
    Distribute()(c)
    // 重试请求
    DoRequest(c, ...)
}
```

**3. Distributor使用排除列表**
```go
excludeIndices := getExcludedKeyIndices(c)  // [1] - 跳过索引1的Key
actualKey, keyIndex, err = channel.GetNextAvailableKeyWithRetry(excludeIndices)
// 返回索引2的Key
```

### 辅助函数

#### getExcludedKeyIndices
```go
func getExcludedKeyIndices(c *gin.Context) []int {
    if excludedKeysInterface, exists := c.Get("excluded_key_indices"); exists {
        if excludedKeys, ok := excludedKeysInterface.([]int); ok {
            return excludedKeys
        }
    }
    return []int{}
}
```

#### addExcludedKeyIndex
```go
func addExcludedKeyIndex(c *gin.Context, keyIndex int) {
    excludedKeys := getExcludedKeyIndices(c)

    // 检查是否已经存在
    for _, existingIndex := range excludedKeys {
        if existingIndex == keyIndex {
            return
        }
    }

    // 添加新的索引
    excludedKeys = append(excludedKeys, keyIndex)
    c.Set("excluded_key_indices", excludedKeys)
}
```

---

## SetupContextForSelectedChannel 详解

这个函数负责为选定的渠道设置所有必要的上下文信息。

### 完整代码

```go
func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) {
    // 1. 基本信息
    c.Set("channel", channel.Type)
    c.Set("channel_id", channel.Id)
    c.Set("channel_name", channel.Name)
    c.Set("model_mapping", channel.GetModelMapping())
    c.Set("original_model", modelName)  // 用于重试

    // 2. 获取实际使用的Key（支持多Key聚合）
    var actualKey string
    var keyIndex int
    var err error

    excludeIndices := getExcludedKeyIndices(c)

    if channel.MultiKeyInfo.IsMultiKey && len(excludeIndices) > 0 {
        actualKey, keyIndex, err = channel.GetNextAvailableKeyWithRetry(excludeIndices)
    } else {
        actualKey, keyIndex, err = channel.GetNextAvailableKey()
    }

    if err != nil {
        logger.SysError(fmt.Sprintf("Failed to get available key for channel %d: %s",
            channel.Id, err.Error()))
        actualKey = channel.Key  // 回退
        keyIndex = 0
    }

    // 3. 存储Key信息
    c.Set("actual_key", actualKey)
    c.Set("key_index", keyIndex)
    c.Set("is_multi_key", channel.MultiKeyInfo.IsMultiKey)

    // 4. 记录日志（脱敏）
    maskedKey := actualKey
    if len(actualKey) > 8 {
        maskedKey = actualKey[:4] + "***" + actualKey[len(actualKey)-4:]
    }
    logger.SysLog(fmt.Sprintf("channel:%d;requestModel:%s;keyIndex:%d;maskedKey:%s",
        channel.Id, modelName, keyIndex, maskedKey))

    // 5. 设置Authorization Header
    c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", actualKey))

    // 6. 设置Base URL
    c.Set("base_url", channel.GetBaseURL())

    // 7. 加载渠道配置
    cfg, _ := channel.LoadConfig()

    // 8. 向后兼容处理：某些渠道的配置可能存储在Other字段
    switch channel.Type {
    case common.ChannelTypeAzure:
        if cfg.APIVersion == "" {
            cfg.APIVersion = channel.Other  // 兼容旧版
        }
    case common.ChannelTypeXunfei:
        c.Set(common.ConfigKeyAPIVersion, channel.Other)
    case common.ChannelTypeGemini:
        c.Set(common.ConfigKeyAPIVersion, channel.Other)
    case common.ChannelTypeAIProxyLibrary:
        c.Set(common.ConfigKeyLibraryID, channel.Other)
    case common.ChannelTypeAli:
        c.Set(common.ConfigKeyPlugin, channel.Other)
    }

    // 9. 设置配置对象
    c.Set("Config", cfg)
}
```

### 设置的上下文变量

| 变量名 | 类型 | 说明 | 使用场景 |
|-------|------|------|---------|
| `channel` | int | 渠道类型 | Adaptor选择 |
| `channel_id` | int | 渠道ID | 日志记录、计费 |
| `channel_name` | string | 渠道名称 | 日志记录 |
| `model_mapping` | map | 模型映射 | 模型名称转换 |
| `original_model` | string | 原始模型名 | 重试时使用 |
| `actual_key` | string | 实际使用的Key | 请求发送 |
| `key_index` | int | Key索引 | 多Key管理 |
| `is_multi_key` | bool | 是否多Key | 错误处理 |
| `base_url` | string | API基础URL | 请求构建 |
| `Config` | ChannelConfig | 渠道配置 | 特定渠道逻辑 |

### 模型映射机制

**问题**: 不同渠道可能对同一模型有不同的名称

**示例**:
- 用户请求: `gpt-4`
- Azure渠道: `gpt-4-0613`
- 自定义渠道: `my-gpt4-model`

**解决方案**: 模型映射

```go
// 渠道配置
channel.ModelMapping = map[string]string{
    "gpt-4": "gpt-4-0613",
    "gpt-3.5-turbo": "gpt-35-turbo",  // Azure不支持点号
}

// 在Adaptor中使用
modelMapping := c.Get("model_mapping").(map[string]string)
originalModel := c.GetString("original_model")  // "gpt-4"
if mappedModel, ok := modelMapping[originalModel]; ok {
    actualModel = mappedModel  // "gpt-4-0613"
}
```

---

## 与OpenAI API的对比

### OpenAI官方架构

```
用户 → OpenAI API → GPT模型
```

简单直接，但：
- ❌ 只支持OpenAI模型
- ❌ 无法负载均衡
- ❌ 无法故障转移
- ❌ 无法统一管理

### One API架构（通过Distributor）

```
用户 → One API → Distributor → [渠道1: OpenAI]
                                [渠道2: Azure]
                                [渠道3: Gemini]
                                [渠道4: 自定义]
```

优势：
- ✅ 统一接口，支持多个AI服务商
- ✅ 智能负载均衡
- ✅ 自动故障转移
- ✅ 多Key聚合提高并发
- ✅ 统一认证、计费、监控

### API调用对比

#### OpenAI官方调用

```bash
curl https://api.openai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-xxxxx" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

**特点**:
- 直接调用OpenAI
- 固定端点
- 单一Key

#### One API调用（Distributor处理后）

```bash
# 用户请求（与OpenAI格式相同）
curl http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-oneapi-xxxxx" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

**Distributor内部处理**:
1. 验证 `sk-oneapi-xxxxx`
2. 查询用户组权限
3. 选择最优渠道（可能是Azure而不是OpenAI）
4. 使用渠道的Key: `sk-azure-xxxxx`
5. 转换请求格式（如果需要）
6. 调用实际服务
7. 返回响应

**优势**:
- 用户无感知切换
- 自动选择最优服务
- 统一计费和监控

### Gemini API对比

#### Gemini官方API

```bash
POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=YOUR_API_KEY

{
  "contents": [{
    "parts": [{
      "text": "Hello"
    }]
  }]
}
```

**特点**:
- 独特的URL结构
- 不同的请求格式
- Key在URL参数中

#### One API处理Gemini（Distributor + Adaptor）

**用户请求（OpenAI格式）**:
```bash
POST http://localhost:3000/v1/chat/completions
Authorization: Bearer sk-oneapi-xxxxx

{
  "model": "gemini-2.0-flash",
  "messages": [{"role": "user", "content": "Hello"}]
}
```

**也支持Gemini原生格式**:
```bash
POST http://localhost:3000/v1beta/models/gemini-2.0-flash:generateContent
Authorization: Bearer sk-oneapi-xxxxx

{
  "contents": [...]
}
```

**Distributor处理**:
1. 识别Gemini路径: `/v1beta/models/`
2. 提取模型名称: `gemini-2.0-flash`
3. 选择Gemini渠道
4. 设置Gemini的API Key
5. 交给GeminiAdaptor处理

**GeminiAdaptor处理**:
1. 如果是OpenAI格式，转换为Gemini格式
2. 构建正确的Gemini URL
3. 调用Google API
4. 转换响应为OpenAI格式（如果需要）

---

## 代码详解

### 核心函数分析

#### 1. Distribute() 主函数

```go
func Distribute() func(c *gin.Context) {
    return func(c *gin.Context) {
        // A. 获取用户信息
        userId := c.GetInt("id")
        userGroup, _ := model.CacheGetUserGroup(userId)
        c.Set("group", userGroup)

        var requestModel string
        var channel *model.Channel
        var modelRequest ModelRequest

        // B. 检查是否指定特定渠道
        channelId, ok := c.Get("specific_channel_id")
        if ok {
            // ... 使用指定渠道
        } else {
            // C. 根据路径类型处理
            shouldSelectChannel := true
            var err error

            if strings.HasPrefix(c.Request.URL.Path, "/mj") {
                // Midjourney处理
            } else if strings.HasPrefix(c.Request.URL.Path, "/v2beta") ||
                      strings.HasPrefix(c.Request.URL.Path, "/sd") {
                // Stability AI处理
            } else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") ||
                      strings.HasPrefix(c.Request.URL.Path, "/v1/models/") ||
                      strings.HasPrefix(c.Request.URL.Path, "/v1alpha/models/") {
                // Gemini处理
            } else {
                // OpenAI标准格式
                err = common.UnmarshalBodyReusable(c, &modelRequest)
            }

            // D. 处理默认模型
            if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
                if modelRequest.Model == "" {
                    modelRequest.Model = "text-moderation-stable"
                }
            }
            // ... 其他端点的默认模型

            // E. 设置模型到上下文
            requestModel = modelRequest.Model
            if requestModel == "" {
                requestModel = modelRequest.ModelName
            }
            c.Set("model", requestModel)

            // F. 选择渠道
            if shouldSelectChannel {
                channel, err = model.CacheGetRandomSatisfiedChannel(
                    userGroup, requestModel, 0)
                if err != nil {
                    message := fmt.Sprintf(
                        "There are no channels available for model %s under the current group %s",
                        requestModel, userGroup)
                    abortWithMessage(c, http.StatusServiceUnavailable, message)
                    return
                }
                SetupContextForSelectedChannel(c, channel, requestModel)
            }
        }

        // G. 继续执行后续中间件
        c.Next()
    }
}
```

### 关键点解析

#### A. 用户信息获取
```go
userId := c.GetInt("id")  // 由Auth中间件设置
userGroup, _ := model.CacheGetUserGroup(userId)
```

**为什么需要用户组？**
- 不同用户组有不同的渠道访问权限
- 不同用户组有不同的计费倍率
- 实现租户隔离

#### B. UnmarshalBodyReusable
```go
err = common.UnmarshalBodyReusable(c, &modelRequest)
```

**为什么是"Reusable"？**
- HTTP请求的Body是一个流（io.Reader）
- 读取一次后就不能再读取了
- 但Distributor中间件读取后，后续的Controller还需要读取
- 解决方案：读取后重新写回Body

```go
// common/utils.go
func UnmarshalBodyReusable(c *gin.Context, v any) error {
    // 1. 读取Body
    body, err := io.ReadAll(c.Request.Body)
    if err != nil {
        return err
    }

    // 2. 关闭旧的Body
    c.Request.Body.Close()

    // 3. 解析JSON
    err = json.Unmarshal(body, v)
    if err != nil {
        return err
    }

    // 4. 重新设置Body（供后续使用）
    c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

    return nil
}
```

#### C. 路径前缀匹配顺序

**为什么要按特定顺序检查？**

```go
// 正确的顺序
if strings.HasPrefix(path, "/mj") {
    // Midjourney
} else if strings.HasPrefix(path, "/v2beta") || strings.HasPrefix(path, "/sd") {
    // Stability AI
} else if strings.HasPrefix(path, "/v1beta/models/") {
    // Gemini
} else {
    // OpenAI标准格式（默认）
}
```

如果顺序错误，可能会：
- Gemini请求被误判为OpenAI
- 无法正确提取模型名称

#### D. shouldSelectChannel标志

**某些请求不需要选择渠道**:
```go
// Midjourney任务查询
if relayMode == relayconstant.RelayModeMidjourneyTaskFetch {
    shouldSelectChannel = false
}
```

**原因**:
- 任务查询不需要调用上游API
- 直接从本地数据库查询
- 不消耗上游额度

---

## 常见问题

### Q1: 为什么Gemini的模型名称要从URL提取而不是Body？

**A**: 这是Gemini API的设计决定

Gemini API格式:
```
POST /v1beta/models/{model}:generateContent
```

模型名称是URL路径的一部分，而不是请求Body的字段。这是Google设计的RESTful风格。

One API为了兼容这种设计，必须解析URL路径。

### Q2: 为什么需要 relay_mode？

**A**: 不同的API操作需要不同的处理逻辑

示例：
```go
const (
    RelayModeChatCompletions = 1      // 聊天补全
    RelayModeEmbeddings = 2           // 文本嵌入
    RelayModeImagesGenerations = 3    // 图片生成
    RelayModeMidjourneyImagine = 10   // MJ生图
    RelayModeMidjourneyChange = 11    // MJ变换
)
```

在Adaptor中：
```go
switch relayMode {
case RelayModeChatCompletions:
    return a.doChatCompletions(c, meta)
case RelayModeEmbeddings:
    return a.doEmbeddings(c, meta)
case RelayModeImagesGenerations:
    return a.doImageGeneration(c, meta)
}
```

### Q3: 如果所有渠道都失败了怎么办？

**A**: 返回503错误

```go
channel, err = model.CacheGetRandomSatisfiedChannel(userGroup, requestModel, 0)
if err != nil {
    message := fmt.Sprintf(
        "There are no channels available for model %s under the current group %s",
        requestModel, userGroup)
    abortWithMessage(c, http.StatusServiceUnavailable, message)
    return
}
```

用户会收到：
```json
{
  "error": {
    "message": "There are no channels available for model gpt-4 under the current group default",
    "type": "service_unavailable",
    "code": 503
  }
}
```

### Q4: 多Key模式下，如果所有Key都失败了呢？

**A**: 降级到单渠道失败处理

```go
actualKey, keyIndex, err = channel.GetNextAvailableKeyWithRetry(excludeIndices)
if err != nil {
    // 所有Key都失败了
    logger.SysError(fmt.Sprintf("All keys failed for channel %d", channel.Id))
    actualKey = channel.Key  // 回退到原始Key（会失败）
    keyIndex = 0
}
```

然后会：
1. 尝试使用回退Key（大概率失败）
2. 触发渠道级别的故障转移
3. 选择其他渠道
4. 如果所有渠道都失败，返回503

### Q5: 为什么需要 original_model？

**A**: 用于重试时保持原始模型名称

场景：
1. 用户请求: `gpt-4`
2. 渠道1映射: `gpt-4` → `gpt-4-0613`
3. 渠道1失败
4. 重试渠道2
5. 如果不保存原始模型名，重试时只知道 `gpt-4-0613`
6. 渠道2可能有不同的映射: `gpt-4` → `gpt-4-turbo`

保存 `original_model` 确保每个渠道都基于原始模型名进行映射。

### Q6: CacheGetRandomSatisfiedChannel 的缓存机制是什么？

**A**: 渠道列表缓存 + 实时过滤

```go
// 伪代码
func CacheGetRandomSatisfiedChannel(group, model string, channelType int) (*Channel, error) {
    // 1. 从缓存获取渠道列表（5分钟有效期）
    channels := cache.Get("channels")
    if channels == nil {
        channels = db.GetAllChannels()
        cache.Set("channels", channels, 5*time.Minute)
    }

    // 2. 实时过滤（不缓存）
    satisfied := []Channel{}
    for _, ch := range channels {
        if ch.Status == 1 &&  // 启用
           ch.HasAccess(group) &&  // 用户组有权限
           ch.SupportsModel(model) {  // 支持该模型
            satisfied = append(satisfied, ch)
        }
    }

    // 3. 按优先级和权重选择
    return selectByPriorityAndWeight(satisfied)
}
```

**为什么这样设计？**
- 渠道列表变化不频繁，缓存提高性能
- 过滤条件（用户组、模型）每次请求不同，不适合缓存
- 平衡性能和灵活性

### Q7: 如何测试Distributor中间件？

**A**: 单元测试 + 集成测试

**单元测试**:
```go
func TestExtractModelNameFromGeminiPath(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"/v1beta/models/gemini-2.0-flash:generateContent", "gemini-2.0-flash"},
        {"/v1/models/gemini-pro:streamGenerateContent", "gemini-pro"},
        {"/gemini-2.0-flash:generateContent", "gemini-2.0-flash"},
    }

    for _, tt := range tests {
        result := extractModelNameFromGeminiPath(tt.input)
        if result != tt.expected {
            t.Errorf("input=%s, expected=%s, got=%s",
                tt.input, tt.expected, result)
        }
    }
}
```

**集成测试**:
```bash
# 1. 启动测试服务器
go test -v ./middleware/... -run TestDistributor

# 2. 发送测试请求
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer sk-test-xxxxx" \
  -d '{"model": "gpt-4", "messages": [...]}'

# 3. 验证响应和日志
```

### Q8: 性能优化建议？

**A**: 以下几点可以优化性能

1. **渠道缓存优化**
```go
// 当前：缓存所有渠道，每次请求过滤
// 优化：缓存已过滤的渠道列表（按用户组+模型）
cacheKey := fmt.Sprintf("channels:%s:%s", userGroup, model)
channels := cache.Get(cacheKey)
```

2. **减少数据库查询**
```go
// 使用批量查询和预加载
db.Preload("Config").Find(&channels)
```

3. **并发优化**
```go
// 使用sync.Pool复用对象
var modelRequestPool = sync.Pool{
    New: func() interface{} {
        return &ModelRequest{}
    },
}
```

4. **避免重复解析**
```go
// 缓存解析结果
if modelRequest, ok := c.Get("parsed_model_request"); ok {
    return modelRequest.(*ModelRequest)
}
```

---

## 实践练习

### 练习1: 添加新的API格式支持

**任务**: 添加对Claude API的路径识别

Claude API格式:
```
POST /v1/messages
```

**步骤**:
1. 在Distribute()中添加路径检查
2. 解析请求格式
3. 提取模型名称
4. 设置relay_mode

**提示**:
```go
else if strings.HasPrefix(c.Request.URL.Path, "/v1/messages") {
    // Claude API处理
    relayMode := relayconstant.RelayModeClaude

    var claudeRequest ClaudeRequest
    err = common.UnmarshalBodyReusable(c, &claudeRequest)

    modelRequest.Model = claudeRequest.Model
    c.Set("relay_mode", relayMode)
}
```

### 练习2: 实现渠道健康评分

**任务**: 根据历史成功率选择渠道

**当前**: 只考虑优先级和权重
**目标**: 加入健康评分

```go
type ChannelHealth struct {
    ChannelId      int
    SuccessCount   int
    FailureCount   int
    LastSuccessTime time.Time
}

func calculateHealthScore(ch *Channel) float64 {
    health := GetChannelHealth(ch.Id)
    successRate := float64(health.SuccessCount) /
                   float64(health.SuccessCount + health.FailureCount)

    // 最近1小时没有成功，降低分数
    if time.Since(health.LastSuccessTime) > time.Hour {
        successRate *= 0.5
    }

    return successRate
}
```

### 练习3: 实现智能重试策略

**任务**: 根据错误类型决定是否重试

**策略**:
- 401 Unauthorized → 切换Key，重试
- 429 Rate Limit → 等待后重试，或切换渠道
- 500 Server Error → 立即切换渠道
- 503 Service Unavailable → 标记渠道不健康，切换

```go
func shouldRetry(statusCode int) (bool, time.Duration) {
    switch statusCode {
    case 401:
        return true, 0  // 立即重试，换Key
    case 429:
        return true, 5 * time.Second  // 等待5秒
    case 500, 502, 503:
        return true, 0  // 立即换渠道
    default:
        return false, 0
    }
}
```

---

## 总结

### Distributor中间件的核心价值

1. **统一入口**
   - 支持多种AI服务商的API格式
   - 用户只需使用OpenAI兼容的接口

2. **智能分发**
   - 根据用户、模型、负载选择最优渠道
   - 自动故障转移

3. **高可用**
   - 多Key聚合
   - 自动重试
   - 健康检查

4. **可扩展**
   - 易于添加新的AI服务商
   - 灵活的配置机制

### 学习检验

完成以下问题，验证你的理解：

- [ ] 我能解释Distributor在中间件链中的位置
- [ ] 我能说出4种不同的API路径处理方式
- [ ] 我理解渠道选择的算法（优先级+权重）
- [ ] 我理解多Key聚合和重试机制
- [ ] 我能说出为什么需要extractModelNameFromGeminiPath
- [ ] 我能解释为什么需要UnmarshalBodyReusable
- [ ] 我理解SetupContextForSelectedChannel设置的每个变量的作用
- [ ] 我能画出一个请求从进入Distributor到选择渠道的完整流程图

如果有任何问题答不上来，回去重新阅读对应章节！

---

## 下一步学习

**推荐阅读顺序**:

1. ✅ **Distributor中间件** (当前文档)
2. → **Adaptor接口与实现** (`relay/channel/interface.go`)
3. → **OpenAI Adaptor实现** (`relay/channel/openai/main.go`)
4. → **Gemini Adaptor实现** (`relay/channel/gemini/main.go`)
5. → **渠道管理** (`model/channel.go`)
6. → **计费系统** (`relay/util/billing.go`)

**相关文档**:
- `学习路径.md` - 完整学习路径
- `通义千问配置指南.md` - 渠道配置示例

---

**文档版本**: 1.0
**最后更新**: 2025-12-23
**作者**: One API 学习小组
**需要帮助？** 查看学习路径或提Issue
