# Gemini 工具 Schema 适配器实现计划（参照 new-api）

日期：2026-06-04
分支建议：从 `dev` 切出 `feat/gemini-schema-adapter`
负责人：待定

---

## 1. 背景与根因

OpenAI 兼容客户端发来的 `tools[].function.parameters` 是**完整 JSON Schema**，而 Gemini 的 `functionDeclarations[].parameters` 只接受 **OpenAPI 3.0 Schema 子集**。

one-api 当前在 `relay/channel/gemini/main.go:300-301` **裸传**该字段，未做任何清洗：

```go
if tool.Function.Parameters != nil {
    funcDecl["parameters"] = tool.Function.Parameters   // ← 根因：直接透传
}
```

导致 Gemini 返回 400，典型报错：

```
Invalid JSON payload received. Unknown name "$schema" ...
Unknown name "exclusiveMinimum" ...
```

## 2. 参照实现（new-api）

文件：`~/code/new-api/relay/channel/gemini/relay-gemini.go`

| 组件 | 位置 | 作用 |
|---|---|---|
| `geminiOpenAPISchemaAllowedFields` | 703–726 | 白名单：仅保留 Gemini 支持的字段 |
| `geminiFunctionSchemaMaxDepth = 64` | 728 | 递归深度上限（防 DoS） |
| `cleanFunctionParameters` / `...WithDepth` | 730–797 | 递归清洗主体 |
| `cleanFunctionParametersShallow` | 799–820 | 超深度兜底，截断 properties/items/anyOf |
| `normalizeGeminiSchemaTypeAndNullable` | 822–882 | 类型规整：小写→大写、`["x","null"]`→`nullable` |
| 调用点 | 387–401 | 清洗后再 append 到 functionDeclarations |

> 注：new-api 另有 `removeAdditionalPropertiesWithDepth`（884–923）用于 `response_format.json_schema`（结构化输出），与 tools 清洗是两条独立链路。本计划**只覆盖 tools 链路**；结构化输出按需另开。

## 3. 目标与范围（B 级 / 实用）

**做：**
- 白名单过滤非法字段（消除 `$schema`/`exclusiveMinimum` 等报错）
- 递归处理 `properties` / `items` / `anyOf`
- 降级转换：`const`→`enum`、`exclusiveMinimum/Maximum`→`minimum/maximum`、`oneOf`→`anyOf`、`type:["x","null"]`→`nullable`
- format 值过滤（丢弃 Gemini 不认的 format）
- 深度上限 + 兜底截断

**不做（明确排除，YAGNI）：**
- `$ref` / `$defs` 内联展开
- `allOf` 合并
- `not` / `patternProperties` / `if-then-else` / `dependencies` → 直接丢弃

## 4. 设计方案

### 4.1 白名单字段

```
anyOf, default, description, enum, example, format, items,
maxItems, maxLength, maxProperties, maximum, minItems, minLength,
minProperties, minimum, nullable, pattern, properties,
propertyOrdering, required, title, type
```

不在表内的键一律丢弃。

### 4.2 降级转换规则

| JSON Schema 输入 | 转换为 | 说明 |
|---|---|---|
| `type: ["string","null"]` | `type:"STRING"` + `nullable:true` | 拆分联合类型 |
| `type: "null"` | 删除 type + `nullable:true` | |
| `const: X` | `enum: [X]` | Gemini 无 const |
| `exclusiveMinimum: N`（数值形式） | `minimum: N` | **损失严格性**，记日志 |
| `exclusiveMaximum: N` | `maximum: N` | 同上 |
| `oneOf: [...]` | `anyOf: [...]` | **损失排他性** |
| `items: [s1,s2,...]`（元组） | `items: s1` | 取首个，Gemini 不支持元组 |
| 不识别的 `format` 值 | 删除该 format | 见 4.4 |

### 4.3 类型规整（决策点 R1，见 §7）

参照 new-api 将 `object`→`OBJECT`、`string`→`STRING` 等大写化。
**风险**：one-api 直接走 Gemini REST，小写 type 当前可能本就可用，大写化属"参照 new-api 的可选项"，需先验证。计划默认**带类型规整**（与 new-api 对齐），但用开关隔离，便于回退。

### 4.4 format 值过滤

Gemini 各类型仅支持有限 format：
- `string`：`enum`、`date-time`
- `number`：`float`、`double`
- `integer`：`int32`、`int64`

其余（如 `email`/`uri`/`uuid`/`date`）一律删除 `format` 字段（保留 type）。

### 4.5 深度与兜底

- 常量 `geminiFunctionSchemaMaxDepth = 64`
- 超过即调用 shallow 版：仅保留白名单标量字段，删除 `properties`/`items`/`anyOf`，数组退化为空，防止恶意深嵌套耗尽栈。

## 5. 落地步骤

1. **新增清洗函数**到 `relay/channel/gemini/main.go`（或新建 `relay/channel/gemini/schema.go` 集中放置，推荐后者，便于测试）：
   - `cleanFunctionParameters(params any) any`
   - `cleanFunctionParametersWithDepth(params any, depth int) any`
   - `cleanFunctionParametersShallow(params any) any`
   - `normalizeGeminiSchemaTypeAndNullable(m map[string]any)`
   - `normalizeGeminiFormat(m map[string]any)`
   - `var geminiOpenAPISchemaAllowedFields map[string]struct{}`
   - `const geminiFunctionSchemaMaxDepth = 64`

2. **改调用点** `main.go:300-301`：
   ```go
   if tool.Function.Parameters != nil {
       funcDecl["parameters"] = cleanFunctionParameters(tool.Function.Parameters)
   }
   ```
   同时对 legacy `textRequest.Functions`（main.go:283-288）评估是否需要同样清洗（决策点 R2）。

3. **类型规整开关**（R1）：用包级常量或 config 控制 `normalizeGeminiSchemaTypeAndNullable` 是否生效，默认开启。

## 6. 测试计划

新建 `relay/channel/gemini/schema_test.go`，覆盖：

| 用例 | 输入要点 | 期望 |
|---|---|---|
| 剔除非法字段 | 含 `$schema`/`additionalProperties`/`exclusiveMinimum` | 输出无这些键 |
| exclusiveMin→min | `exclusiveMinimum:0` | `minimum:0` |
| const→enum | `const:"x"` | `enum:["x"]` |
| 联合类型 | `type:["string","null"]` | `type:STRING`+`nullable:true` |
| 嵌套 properties | 两层对象 | 递归清洗，深层非法键也被剔除 |
| 数组 items | `items` 为对象 / 元组数组 | 对象递归；元组取首个 |
| oneOf→anyOf | `oneOf:[...]` | `anyOf:[...]` |
| format 过滤 | string+`format:"email"` | 删除 format，保留 type |
| 深度兜底 | 构造 >64 层嵌套 | 不崩，深层被截断 |
| 客户真实 schema | 客户报错时实际 JSON（待提供） | Gemini 接受（集成验证） |
| OpenAI 官方样例 | 代表性 tool 定义 | 全部通过 |

**测试优先级高于实现**：先把客户真实 schema 固化为用例，作为回归防线。

## 7. 决策点（需确认）

- **R1 类型大写化**：是否纳入？默认纳入（对齐 new-api），但 one-api 走 REST 可能无需，存在打破现有可用调用的风险。建议先抓包确认现状再定。
- **R2 legacy Functions**：`textRequest.Functions` 路径是否一并清洗？
- **R3 函数放置**：独立 `schema.go`（推荐，可测）vs 内联 `main.go`。
- **R4 日志**：降级转换（exclusiveMin/oneOf）是否打 warn 日志，便于排查客户侧语义损失。

## 8. 验证与提交

```bash
go build ./... && go vet ./...   # 提交前必跑
go test ./relay/channel/gemini/  # 单测
```

提交粒度：①清洗函数+单测 一次 commit；②接入调用点 一次 commit。中文 commit message。

## 9. 风险与回滚

- 清洗逻辑为纯函数、无副作用，回滚只需还原 `main.go:301` 一行。
- 类型大写化若引发回归，关闭 R1 开关即可，无需回退整体。
- 不触碰数据库、不改其它渠道，blast radius 限于 gemini 渠道 tools 路径。

## 10. 工作量

- 实现（白名单+递归+规整+format+深度）：~200–300 行
- 单测：~150 行
- 合计约 **半天–1 天**（不含 `$ref`/`allOf` 的 C 级扩展）
</content>
</invoke>
