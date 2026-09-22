# Provider 与 Responses 状态绑定：可行性评估及实施计划

- 评估日期：2026-09-21
- 后端基线：`fix/azure-responses-encrypted-content`，`a308b5a`
- 实际前端：`/Users/yueqingli/code/ezlinkai-web`
- 输入：`v0.1.32-provider-state-binding.md`
- 本次修订：渠道详情支持创建/编辑 Provider；未指定时按渠道类型名称取默认值；兼容现有重试次数、优先级和候选轮转机制。
- 状态：核心功能已在后端及 ezlinkai-web 工作区实现；实施与验证记录见第 9 节，尚未发布或部署。第 2–8 节保留原基线评估及验收要求。

## 1. 结论与适用范围

建议采用方案的核心设计：渠道增加可选的字符串 Provider 配置，把可携带的加密历史限制在同一有效 Provider 内，把服务端资源引用固定到原渠道及凭证；首次选渠和失败重试使用同一约束。这样能够阻止跨 Provider 回放，并避免通过删除历史获得一个上下文不完整的成功响应；同 Provider 下实际资源是否互通仍需验证。

现有渠道 config、分发中间件、候选筛选、Responses 处理器和 Redis 客户端足以承载实现，无需增加数据库表。工作量主要集中在路由边界、响应输出顺序和故障处理，前端字段属于小部分工作。

Provider 是渠道级供应方标识，使用 `OpenAI`、`Anthropic Claude`、`Azure OpenAI` 等文字字符串。渠道详情的创建、编辑表单提供 Select 下拉选择；未指定或清空时，默认使用当前渠道类型对应的名称字符串。`channel.Type` 仍是决定协议适配的数字字段，Provider 可以独立指定，不改变适配器。例如 OpenAI 类型的转发渠道可以指定 Provider 为 `Anthropic Claude`。

本计划在现有 config 中新增 `provider` 来存储显式配置，通过统一函数计算有效 Provider：非空显式值优先，否则取渠道类型名称。旧渠道无需批量回填即可获得默认值。字符串同时作为 Responses 的候选分组依据；同名并不自动证明跨账户密文互通，存在实际隔离需求时可使用不同字符串标识，但不要求每个渠道配置自定义资源标识。

Provider 字段对所有渠道类型开放。首期状态绑定和 Provider 重试约束限定 `/v1/responses`、`/v1/responses/compact`，包括这两个接口的纯文本请求：无历史及请求级 Provider 约束时，先按原规则选首渠，再用首渠的有效 Provider 固定本次重试边界。共享选渠函数接受可选约束，但只在上述入口设置约束。扩展到 Chat Completions、Claude thinking/signature 和视频等其他协议需要各自设计与验收，不应因修改公共重试函数而顺带改变行为。

文档列出的后端提交 `43dd736`、`6ecd949`、`28a3c75` 在当前本地对象库无法解析，因此无法核验其实现或直接据此认定可 cherry-pick。其“已通过测试”“已发布 v0.1.32”是来源文档的陈述，不是本仓库验收结果。本文未核实远端提交、镜像或生产部署。

## 2. 当前已有能力及代码差异

以下行号以本次评估基线为准。

| 模块 | 已有能力 | 实施时的差异 |
| --- | --- | --- |
| `model/channel.go:183` | `ChannelConfig` 已有 JSON 扩展字段 | 新增 `Provider string`、有效值解析和校验，无需 schema 迁移 |
| `common/model-provider.go:6` | 用于模型供应商推断的映射把 Azure 归为 OpenAI、Claude 写为 Anthropic | 不能直接用于本功能默认值；新增与渠道类型名称一致的映射，保留现有模型供应商统计口径 |
| `middleware/distributor.go:93` | previous_response_id、X-Response-ID 和规则亲和选渠 | 当前只是优先命中，失败会重新随机选择；compact 没有同样的 previous_response_id 入口 |
| `model/cache.go:379` | 权重、静态优先级、动态优先级、失败排除 | 每条候选路径都需要硬约束；动态优先级必须在分池及计算比例前过滤 |
| `controller/retry_policy.go:24` | 从最高优先级开始排除失败渠道；选渠报错后清空失败列表再选 | 区分候选耗尽与查询故障；Provider 模式保留同域轮转，每次查询携带相同约束 |
| `controller/relay.go:3271` | Responses 使用 `RetryTimes`、`shouldRetry()`、最后渠道回退、聚合日志及自动禁用 | 保留次数和上游错误规则；回退也验证约束；内部状态错误不进入上游失败处理 |
| `controller/relay.go:188` | 通用 Relay 检查亲和 `skip_retry_on_failure` | 当前 `RelayResponse` 未检查；接入时补齐，不能因 Provider 允许同域重试而忽略亲和禁止重试 |
| `middleware/distributor.go:348` | 按缓存 key 索引选密钥 | 索引失效自动换 key，选 key 失败还会回退原始 `channel.Key` |
| `model/cache.go:1093` | response ID 到渠道/key 索引缓存 | 无用户维度，24 小时 TTL，失败只记日志，不能作为新功能的权威索引 |
| `relay/controller/opeai_response.go:403` | 普通响应、SSE、usage 和计费 | 普通响应先输出后缓存；SSE 先发送事件，只在 completed 写 response ID |
| `relay/controller/azure_responses.go:42` | Azure invalid_encrypted_content 降级处理 | 会删除 reasoning 密文/ID 和 compaction 项，直接违反新方案完整性要求 |
| `relay/controller/opeai_response.go:67` | 请求模型映射 | 重新序列化固定结构体，可能丢未知字段、false/0，并改变 interface{} 中的大整数 |
| `relay/channel/openai/adaptor.go:44` | Azure Responses 路径 | 把 compact 也映射到 `/openai/responses`，丢失 compact 后缀 |
| `relay/channel/common.go:93` | 上游响应前发送 SSE ping | 可提前提交 HTTP 200，导致后续无法返回真实 JSON 错误状态 |
| `controller/channel.go:426` | 更新渠道 config | 当前整段覆盖，不具备文档要求的字段合并语义 |
| `ezlinkai-web/sections/channel/channel-form.tsx:1243` | 表单构建 config、批量提交 | 增加 Provider Select、默认值提示和回显；从原始 config 合并，保留未知字段；实际仓库不是 linkinfra-web |
| `ezlinkai-web/constants/index.ts:2` | `CHANNEL_OPTIONS` 的 `text` 为名称、`value` 为数字类型 | Provider 选项使用名称字符串作 value，不能直接复用数字 value |

## 3. 上线前必须解决的问题

### 1）历史完整性与现有降级冲突

**P1，置信度 10/10。** `azure_responses.go:42` 会清理历史再向同一渠道请求。compaction 可能承载已经从客户端删除的原始历史，删除后无法恢复。

建议在新 Responses 路径中移除该降级行为，并将现有“清理后成功”测试改成“原始历史保持完整、明确返回失败”。复用请求读取等工具即可，不保留第二套隐式清理策略。模型映射采用 `map[string]json.RawMessage`，仅替换顶层 model。

### 2）Provider 默认值、字符串值与实际兼容性的边界

**P1，置信度 9/10，属于方案前提。** Provider 的产品要求是可下拉选择的供应方文字，未指定时继承渠道类型；原计划禁止默认值并强制使用细粒度兼容域的表述不准确。尚无真实 OpenAI/Azure 联调证据。

默认值按渠道类型名称计算，例如类型 1 → `OpenAI`、类型 14 → `Anthropic Claude`、类型 3 → `Azure OpenAI`；不能写入数字 1/14/3，也不能复用把 Azure 合并到 OpenAI 的模型供应商推断。默认名称不同的渠道类型可以通过显式配置进入同一个 Provider 候选集合，且仍须满足接口能力、模型和权限要求。

允许上述常规名称作为 Provider，同时以真实 reasoning/compaction 回放确认组内资源兼容性。若同品牌资源实际不互通，应拆分 Provider 字符串或固定原资源；不能声称仅靠品牌字符串已解决全部密文兼容问题，也不能因此取消按类型默认的产品行为。

### 3）硬约束不能被候选捷径、配置变更和 key 回退绕过

**P1，置信度 10/10。** 当前缓存、亲和、普通候选、动态优先级和重试回退是多条分支；亲和与 response ID 快捷路径仅检查渠道的模型列表、分组及状态，未统一验证对应 `abilities.enabled`。

建议统一候选资格校验：当前用户/令牌权限、分组、模型 ability、渠道状态、有效 Provider 或固定资源约束全部通过才能入选。明确指定渠道也必须与状态约束求交集。Provider 模式区分“本轮未失败候选耗尽”和“约束内已无可用候选”：前者可在剩余重试预算内清空失败列表，继续同 Provider 轮转；后者结束。最后渠道回退必须重新校验资格，不能绕过约束；精确资源模式禁止通过清空失败列表或回退来换渠道、换 key。详见 4.3。

固定资源至少验证渠道 ID、key 索引、实际 key 指纹。还应保存资源身份指纹，覆盖规范化端点及影响资源命名空间的配置；同渠道同 key 改了 BaseURL 或项目后，也不应继续视为原资源。索引保存签发时的有效 Provider 快照，不能读取渠道当前 Provider 来重解释旧状态。编辑 Provider、清空覆盖值或修改默认模式下的渠道类型，都可能改变有效 Provider；更新渠道缓存后，新请求使用新值，旧状态继续按原快照校验，不自动迁移到新 Provider。

每次上游尝试的 key 必须只选择一次；重试日志和状态索引都记录实际发送使用的密钥。当前 `RelayResponse` 为日志调用一次 `GetNextAvailableKey()`，随后 SetupContext 再选一次，不能沿用这套方式构造指纹。Provider 模式允许在合规候选内按现有多 key 策略选 key，精确资源模式只允许绑定 key。首期按文档保守处理：key 重排导致原索引对应指纹改变时拒绝，不静默寻找替代 key。

### 4）需要明确引用解析与冲突合并规则

**P1，置信度 9/10，属于实现合同缺口。** “多份历史冲突则拒绝”正确，但需要明确如何识别可携带项、如何合并以及并发写入时谁有权改变来源。

建议规则如下：

- reasoning/compaction 存在非空合法密文时，按密文指纹恢复签发时的有效 Provider；同一项附带的 ID 不应再额外强制固定 key，否则会把可携带历史错误降级为固定资源。
- previous_response_id、conversation 引用、item_reference、只有 ID 的 reasoning/compaction 按服务端资源处理。`call_id` 等工具关联字段不能当作上游资源引用。
- 按协议位置读取字段，覆盖 conversation 的受支持字符串/对象形式及普通和 SSE 响应载体，不递归扫描任意工具参数中的 id。
- 各项约束取交集：同 Provider 的密文可合并；密文加资源引用要求资源属于该 Provider；多个资源引用必须指向同一精确资源；无交集返回 409。
- 未知密文只允许请求级显式 Provider 收紧路由，不能凭渠道默认值猜测来源，也不能覆盖已知冲突；未知服务端资源即使有 Provider 头也拒绝。
- 索引键带版本、认证用户、引用种类及指纹，输入编码必须无歧义。只有上游实际输出的资源才自动登记，不把客户端声明的未知 ID 直接写成可信来源。
- 重复写入相容来源可续期；不相容写入必须报冲突，禁止普通 SET 后写覆盖先写。对密文比较有效 Provider，对服务端引用比较精确资源，避免误把同 Provider 跨渠道输出认作冲突。

### 5）SSE 和内部故障需要独立错误策略

**P1，置信度 10/10。** `shouldRetry()` 对 5xx/429 以及多数 4xx 默认允许重试；Responses 最终错误直接写 JSON；状态登记若套用此路径，会重放已经执行成功的请求，还可能污染渠道评分或触发禁用。

建议给错误增加稳定分类，而非仅靠 HTTP 状态码和文本关键词：客户端状态冲突、索引服务故障、上游错误、流写出错误分别处理。索引故障不参与上游自动禁用、key 禁用和动态优先级失败评分，也不触发重新请求上游。

必须在发送状态所在的响应体/事件前确认索引写入成功。上游请求前关闭 ping；首个可输出事件登记成功后才允许提交 SSE 响应。任何响应字节或 ping 已发出后禁止重放。`c.Writer.Written()` 可作为额外保护，但不能替代上游是否已执行及错误来源判断。

流式终止必须区分 completed、incomplete、failed、读取异常和客户端取消。当前 `bufferStreamPrefix()` 限制 64 KiB 且未返回 scanner.Err；公共 scanner 的 timeout/EOF 状态也需传回 Responses 层。应覆盖大型加密事件和断流，不能只有未解析 JSON 才被视为错误。

首事件前失败清除 SSE 头并返回 JSON；流已开始返回协议兼容的错误事件，客户端断开则记录状态并结束。已获得 usage 后索引失败，仍需进入一次性用量结算，避免因提前 return 漏记已经发生的上游消耗；没有 usage 时按现有估算策略单独标识，不能假称未执行。网关无法保证上游调用的全局恰好一次语义。

### 6）config 合并和清空语义必须前后端一致

**P1，置信度 10/10。** 后端整段覆盖、前端从空对象重建，都会让已有 api_version 等字段丢失。

建议契约：未传 config 保持原值；传 JSON 对象字符串时合并字段；对象内 `provider:""` 仅清空显式覆盖值，随后按当前渠道类型计算有效 Provider；显式空 config 字符串沿用原有整段清空语义，同时恢复默认 Provider。前端编辑从保存的原始 config 构建，保留未知字段；清空 Provider 必须提交 `provider:""`，只省略字段会被后端理解为保留原值。其他受管字段的清空也必须采用接口约定的显式值，不能只在前端删除属性。后端写接口同样验证 Provider 类型、长度和规范化，不能只依靠前端。完整字段合同见 4.1。

### 7）缓存容量、故障边界与升级需要量化

**P2，置信度 9/10，容量为公式推导，未做负载实测。** 7 天 TTL 不代表本地 10 万项能保存 7 天。假设每请求产生 4 个独立索引，1 请求/秒持续产生新状态，7 天约 242 万项；10 万项约 6.9 小时就达到容量，且命中续期会改变实际分布。

建议复用现有 Redis 客户端，新建独立命名空间、短超时的批量查询/写入、请求内去重和原子冲突检查。pipeline 只减少往返，不提供比较后写入的原子性，需要 Lua 或等价条件写入。普通 token delta 不应产生重复索引写入。

多实例上线以共享 Redis 为前提，并核对当前初始化同时要求 `REDIS_CONN_STRING` 和 `SYNC_FREQUENCY`。本地模式仅面向明确接受重启/淘汰后历史不可恢复的单实例。Redis 故障不得降级到本地；查询未命中与基础设施不可用必须区分，建议 409 表示状态不可恢复/冲突，503 表示索引不可用或没有兼容候选。

必须考虑滚动升级中新旧实例混跑：旧实例不写新索引，新实例就无法接续；旧实例也不会执行新边界。建议新池统一升级并接入共享 Redis 后再切流，不将同一严格路由流量分配到未升级实例。旧密文通过每次携带请求级显式 Provider 过渡，旧服务端引用不能靠该头恢复。

## 4. 推荐实现设计

### 4.1 渠道 Provider 字段与表单合同

在 `ChannelConfig` 中增加 `Provider string`，JSON 字段名为 `provider`。外层渠道 API 的 `config` 继续使用 JSON 对象字符串，不新增另一套顶层 Provider 存储字段。例如 OpenAI 类型的渠道显式指定 Provider 为 Anthropic Claude：

```json
{
  "type": 1,
  "config": "{\"provider\":\"Anthropic Claude\"}"
}
```

此时协议适配仍按 `type: 1`，有效 Provider 为 `Anthropic Claude`。`config.provider` 存储文字，不存渠道类型数字、数字字符串或 Select 选项对象。

统一定义 `EffectiveProvider(channel)`：先读取并 trim `config.provider`，非空即使用该字符串；缺失、空字符串或纯空白时返回 `ChannelTypeDefaultProvider(channel.Type)`。默认映射采用稳定的渠道类型名称，覆盖系统支持的全部类型，与前端选项对齐；不根据请求模型名、BaseURL 或 key 猜测 Provider。未知旧类型使用包含类型 ID 的独立兜底字符串，例如 `Channel Type 999`，不能全部并入 `Other`。旧渠道无需回填 config；新建不指定 Provider 时也保持默认模式。

| 场景 | 保存及有效值行为 |
| --- | --- |
| 创建时不指定 Provider | 不写显式覆盖值，按所选渠道类型默认；类型 1 为 `OpenAI` |
| 创建/编辑时选择 `Anthropic Claude` | 保存 `provider: "Anthropic Claude"`，即使渠道类型为 1 也使用该值 |
| 编辑时 Provider 字段未提交 | 保留原显式覆盖值；不存在覆盖值则继续默认模式 |
| 选择“默认（跟随渠道类型）”或清空 | 编辑提交 `provider: ""`，保留其他 config 字段；重新按当前类型取默认值 |
| 默认模式下修改渠道类型 | 有效 Provider 跟随新类型，例如 1 → 3 时由 `OpenAI` 变为 `Azure OpenAI` |
| 显式模式下修改渠道类型 | 保留显式 Provider，除非用户同时修改或清空 |
| 显式选择与默认值相同的文字 | 仍视为显式覆盖，后续修改渠道类型时不自动跟随 |
| 批量创建 | 每条渠道使用相同的显式选择；未指定时分别按各自类型计算默认值 |

渠道详情、创建和编辑表单在渠道类型附近展示 Provider。复用现有 `Select`，选项为 `{ label: 'OpenAI', value: 'OpenAI' }`、`{ label: 'Anthropic Claude', value: 'Anthropic Claude' }` 等；由渠道类型名称列表生成并去重，不能复用 `CHANNEL_OPTIONS` 的数字 `value`。另设“默认（跟随渠道类型）”选项，提示当前有效值，例如“当前：Azure OpenAI”。Select 若不接受空 option value，可使用仅存在于前端的默认占位值，提交时转换为空字符串，不能把占位值存到后端。

编辑回显必须区分显式值与默认模式，不能把计算出来的默认值自动写成显式配置。详情页显示有效 Provider，并标注来自类型默认或显式指定。已有非预置字符串应作为当前选项保留并可回显，避免一次编辑将它覆盖；预置选项不构成后端枚举限制。中英文界面只翻译标签和帮助文字，存储值保持稳定。

创建和更新接口统一校验：只接受字符串，trim 后允许为空；非空建议限制为 1–128 个 Unicode 字符，禁止控制字符，允许空格及中文，不使用只允许 slug 的正则，也不自动转小写。规范化后的值按字符串精确匹配。`null`、数字、数组、对象等非法 Provider 值返回 400。非空 config 必须解析为 JSON 对象，读取或编辑遇到损坏的 config 应明确报错，不能悄悄当作空对象再保存；空 config 按前述清空合同处理。合并时保留未知字段，不能通过固定结构体重序列化丢失它们。

后端以同一有效值函数服务于详情展示、首次分发、亲和命中、重试和状态快照；前端默认提示使用同一名称映射的对应数据，并以契约测试避免漂移。请求级 Provider 头只用于额外收紧路由或旧密文过渡，不能修改渠道配置，也不要求普通调用方额外传头才能使用渠道默认 Provider。

### 4.2 数据与模块边界

在 `model/` 定义无 service 依赖的路由约束及来源快照，在 `service/` 实现 Responses 解析、交集合并和索引存储。这样不会形成 model → service → model 的循环依赖。

路由约束包含三种模式：无约束、Provider 约束、资源约束。资源约束包含 channel ID、key 索引、key 指纹和资源身份指纹，并保存有效 Provider 快照；Provider 约束保存规范化后的有效字符串。保存约束来源以便区分客户端显式指定、历史恢复和首次选渠生成，同时标明渠道 Provider 来自显式配置还是类型默认。

约束通过带类型键的 request context 传入候选选择器；Gin context 只保存必要的处理状态。选出渠道和 key 后创建本次尝试的不可变快照。重试可以更换候选，但不能覆盖最初确定的边界；索引必须使用产生该输出的那次尝试快照。

建议存储接口表达批量查找、相容写入、续期和可区分错误；Redis 与本地实现共享同一契约测试。本地缓存需要 TTL、明确容量和并发安全，避免无界 map 或每次请求全表扫描。

### 4.3 与现有重试逻辑的衔接

当前 `RelayResponse` 首次调用失败后，最多再尝试 `config.RetryTimes` 次；`selectRetryChannel()` 每次以 `skipPriorityLevels=0` 开始，通过失败渠道排除和选择器内部 fallback 自然降级，动态优先级模式则使用现有评分、分池、探索及权重逻辑。当前选渠报错就清空 `failedChannelIds` 再选，仍失败时上层直接取最后渠道。实现应在这些机制上加入约束，不把重试改成另一套优先级算法。

| 重试环节 | 计划行为 |
| --- | --- |
| 边界生成 | 有历史或请求级 Provider 时先解析约束；否则首渠选定后用有效 Provider 冻结边界。未配置 `config.provider` 的旧渠道同样有默认 Provider，不存在因此放开重试的例外 |
| 次数及错误判断 | 保留 `RetryTimes`，0 表示仅首次调用。每次真实上游重试消耗一次预算，清空失败列表不重置计数；优先处理内部错误、取消和已输出状态，再对真实上游错误执行 `shouldRetry()` |
| 优先级与权重 | 首选、低优先级 fallback、动态主池/探索池都先过滤有效 Provider，再沿用原算法；同 Provider 可以跨渠道类型，但接口能力、权限、分组和模型 ability 必须满足 |
| 失败排除及轮转 | Provider 模式继续按渠道 ID 排除本轮失败渠道；只有明确的候选耗尽可清空失败列表，第二次选渠仍携带同一 Provider。数据库或索引故障直接结束，不能当作耗尽重轮。约束内仍无可用候选则返回无兼容渠道错误 |
| 最后渠道回退 | Provider 模式若保留该路径，必须重新读取并校验渠道状态、权限、ability、有效 Provider 和可用 key，仅在剩余预算内执行；不能直接使用未验证的旧指针。精确资源模式不走该通用回退 |
| 多 key | Provider 模式沿用可用 key 的轮询/随机及禁用排除，选 key 失败则该候选不可用；每次尝试只选一次 key，日志和索引共用实际 key，不 fallback 到原始 `channel.Key`。精确资源模式校验原索引及指纹，失效即结束，不能换替代 key |
| 显式渠道与亲和 | 保留 `specific_channel_id` 禁止重试；补齐 `RelayResponse` 对亲和 `skip_retry_on_failure` 的检查，并清除失败亲和上下文。Provider 相同不覆盖这些禁止重试条件 |
| 日志及健康 | 保留聚合重试明细、渠道历史和真实上游错误的禁用/评分处理；记录冻结的 Provider、实际渠道及 key 索引以便核对。状态冲突、索引故障和候选耗尽不伪装成上游渠道故障 |

精确资源绑定首期只向原渠道、原 key 发起一次调用，失败后不进入跨渠道循环；未来如支持同资源原位重试，需单独定义规则。Provider 模式在明确可重试的上游错误且尚未输出时，可以有限次重访同一候选；它与“已经收到成功输出后因索引失败重放”不同，后者始终禁止。所有重试开始前检查请求取消；已有响应字节、索引写失败或来源冲突时立即停止。

例如候选 A、B 的有效 Provider 都为 `OpenAI`，C 为 `Azure OpenAI`，首次选 A：A 失败后按原优先级规则在 OpenAI 组内选 B；若 A、B 都失败但仍可用且预算未耗尽，可以在同组开启下一轮，不能转到 C。若 B 未配置 Provider 但类型为 1，也属于该组；若 B 类型为 3 但显式配置 `OpenAI`，则在满足接口能力后同样属于该组。若携带绑定 A/key-0 的 `previous_response_id`，即使 B 的 Provider 相同也不能换到 B 或 A 的其他 key。

无约束的范围外接口保留现有重试行为。不要全局删除公共函数的轮转机制，也不要让它在带约束时丢掉过滤条件。新增查询故障分类和严格资源处理可以通过可选约束分支接入，并分别覆盖有约束与无约束的回归测试。

### 4.4 请求与输出流程

```text
认证完成，取得用户 ID
  │
  ├─ 解析原始请求中的状态引用和显式 Provider
  │    ├─ 格式错误 → 400
  │    ├─ 索引不可用 → 503
  │    ├─ 未知/过期/不相容 → 409
  │    └─ 合并得到本请求边界
  │
  ├─ 候选资格 = 权限 ∩ ability ∩ 渠道可用 ∩ 路由边界
  │    ├─ 无候选 → 503
  │    └─ 在候选内应用亲和、优先级与权重，精确选 key
  │
  ├─ 无历史/请求级约束 → 取首渠有效 Provider（含类型默认），固定重试边界
  ├─ 只替换 model，关闭上游响应前 ping，发送请求
  │    └─ 可重试上游错误且尚未输出 → 按 4.3 在同一边界及预算内重试
  │
  └─ 普通响应 / 每个完整 SSE 事件
       ├─ 提取上游状态 → 写索引 → 成功后输出原始内容
       ├─ 写索引失败 → 不重放、不误禁用；JSON 503 或 SSE 错误
       └─ 终态/断流 → 正确标记结果与用量结算
```

重试边界在首次上游调用前确定，每次重新选渠道只更新尝试快照，不能用新渠道的 Provider 覆盖边界。配置变更后不再满足原边界的候选必须排除。

### 4.5 错误合同

| 条件 | 建议 HTTP / 流行为 | 自动重试及渠道健康处理 |
| --- | --- | --- |
| 请求字段或 Provider 头格式错误 | 400 | 不重试 |
| 未知/过期状态、来源冲突、资源身份改变 | 409，稳定业务错误码 | 不重试，不禁用渠道 |
| 索引读取不可用 | 503 | 未请求上游，不禁用渠道 |
| 约束内无可用候选或固定渠道不可用 | 503 | 结束候选选择，不越界；与本轮失败列表耗尽区分 |
| 上游可重试错误且未输出 | 原有上游错误策略 | Provider 模式按 4.3 在约束及预算内重试；精确资源模式首期不重试 |
| 重试预算耗尽且最后一次为上游错误 | 保留最后上游错误状态及内容 | 不改写成 503 无候选错误；保留聚合重试日志 |
| 上游已返回但索引写失败 | 输出前 503，输出后 SSE 错误 | 不再调用上游，不禁用渠道 |
| 流开始后上游失败或读取异常 | SSE 错误并结束 | 不重放，按真实上游故障处理 |

建议业务错误码包括 `responses_state_unknown`、`responses_state_conflict`、`responses_resource_changed`、`responses_state_store_unavailable`、`responses_no_compatible_channel`。文档中“索引查询统一 409”建议改为区分未命中和 Redis 故障，便于客户端采取正确恢复动作。

## 5. 实施阶段、依赖与验收

| 阶段 | 工作内容 | 模块 | 依赖 | 完成标准 |
| --- | --- | --- | --- | --- |
| S0 | 确定协议范围、错误合同、渠道类型默认名称映射和真实兼容矩阵 | 文档、测试夹具 | 无 | 字符串选项、默认语义、重试规则和旧历史过渡规则明确 |
| S1 | Provider 字符串、有效值解析、合并更新、缓存刷新、不可变约束和来源快照 | `common/`、`model/`、`controller/` | S0 | 旧渠道自动取默认；显式覆盖及清空正确；配置往返不丢字段；资源身份变化可检测 |
| S2 | 引用解析、交集算法、Redis/本地索引 | `service/` | S1 的类型合同 | 跨用户隔离、冲突写入、TTL、容量和故障测试通过 |
| S3 | 首次分发、全部候选过滤、精确 key、同 Provider 有限轮转及回退验证 | `middleware/`、`model/`、`controller/` | S1、S2 | 默认/显式 Provider 均不越界；轮转不重置重试预算；精确资源不换渠道/key；范围外重试回归通过 |
| S4 | 完整请求保留、Azure compact、普通/SSE 登记和错误处理 | `relay/controller/`、`relay/channel/`，必要时 `relay/helper/` | S2、S3 | 输出前登记、断流不重放、计费和健康状态正确 |
| S5 | 渠道详情及创建/编辑 Select、字符串选项、默认提示、批量创建与恢复默认 | `ezlinkai-web/sections/channel/`、`constants/`、`lib/types/` | S1 配置合同 | 选项 value 是字符串；显式/默认模式回显正确；保留 config 未知字段，前端类型及 lint 通过 |
| S6 | 端到端、真实双向联调、容量测量、灰度与操作文档 | 测试、部署、文档 | S3、S4、S5 | 达到下列验收矩阵后再发布 |

后端建议拆成配置/索引、路由、响应处理三个可审查变更；在全链路完成前不启用生产严格流量。S1 完成后，S2 与 S5 可独立开展；S3 与 S4 按依赖顺序接入。S3 和 S4 都可能修改 controller 或公共流处理，避免同时修改共享目录。最终 S6 汇合验证。

估算：熟悉仓库的一名工程师约 5–8 个工作日完成开发和自动化验证，真实上游联调及灰度再预留 1–2 天；这是基于当前代码差异的计划估算，不是排期承诺。AI 可协助实现和测试生成，但兼容域确认、真实账号联调及生产观测仍需实际完成。若拿到来源提交，应先比较差异再决定哪些部分可移植。

## 6. 测试与验收矩阵

现有测试使用 Go testing、testify 和 httptest。以下是新功能验收要求，不能用来源文档中的测试清单代替执行结果。

```text
Responses / compact 用户流程                         测试层级
├─ 文本首轮 → 常规选渠 → 首渠有效 Provider 重试边界      单元 + 中间件集成
├─ 同 Provider 重试 → 优先级降级 / 有限轮转 / 回退校验   单元 + 中间件集成
├─ 密文下一轮 → 原 Provider → 同域其他候选             单元 + 中间件集成 + 真实联调
├─ 服务端 ID 下一轮 → 原渠道、原 key                    单元 + 中间件集成
├─ 混合历史 → 交集 / 冲突 / 未知 / 用户隔离             单元 + 中间件集成
├─ JSON 输出 → 先登记全部状态 → 交付                    httptest
├─ SSE created / item / completed → 分事件登记           httptest
│  ├─ 首事件登记失败 → JSON 503，无任何 SSE 字节          故障注入
│  └─ 流中失败/EOF/取消 → 错误结束、不重放                故障注入
├─ Redis 断开 / 并发冲突 / TTL / 淘汰                   存储契约 + 实例集成
└─ Select 新建 / 编辑 / 批量 / 恢复默认 → 字符串及 config 完整  前端检查 + UI/API 验收
```

| 组别 | 必须断言的行为 |
| --- | --- |
| Provider 默认与覆盖 | 类型 1/14/3 未配置时分别为 `OpenAI` / `Anthropic Claude` / `Azure OpenAI`；显式值优先；缺失/空白回退；默认模式修改类型跟随，显式模式保持；旧渠道不回填也生效 |
| Provider 数据合同 | 名称字符串含空格、中文可往返；trim、长度边界及非法类型校验正确；不把数字类型、数字字符串、默认占位值或 Select 对象写入 provider；前后端名称映射一致，Azure 不自动并入 OpenAI |
| 普通请求回归 | 无历史、无头的首次选渠规则不变，随后按首渠有效 Provider 重试；未配置 Provider 也按类型默认受约束；范围外协议无约束时行为不变 |
| 重试轮转 | `RetryTimes=0` 仅首次调用，总调用次数不超过 `1 + RetryTimes`；静态高到低优先级、动态评分/探索行为保留；同 Provider 本轮耗尽可重轮，重轮不刷新预算、不跨 Provider |
| 重试停止与回退 | 约束内无可用候选即停止；查询/索引故障不触发清空失败列表；最后渠道禁用、Provider 改变、ability 失效或无可用 key 时不可回退；预算耗尽返回最后上游错误 |
| 指定渠道与亲和 | 指定渠道仍不重试；Responses 补齐亲和命中且 `skip_retry_on_failure=true` 时禁止重试，亲和未命中仍可正常重试；失败亲和不写回成功缓存 |
| 多 key 与精确绑定 | Provider 模式沿用可用 key 选择，每次尝试只选择一次，发送/log/索引一致；精确资源模式只调用原渠道原 key 一次，不通过同 Provider、key 轮询或原始 Key 回退绕过绑定 |
| 候选覆盖 | 静态多优先级、动态分池/探索、亲和、X-Response-ID、指定渠道均受约束；模型 ability 禁用不能从快捷路径绕过 |
| 双向绑定 | 默认 OpenAI 与 Azure OpenAI 不混选；不同类型显式设为相同 Provider 后可进入同组且不改变协议适配；同 Provider 的实测兼容渠道可以接续，服务端 ID 仍固定精确资源 |
| 状态类型 | reasoning/compaction 密文与纯 ID 分别处理；密文附 ID 不过度绑定；工具 call_id 不误判；字符串/对象 conversation 形态 |
| 多份状态 | 同域合并、不同域拒绝、密文与精确引用求交；显式头不能覆盖已知来源或未知服务端引用 |
| 身份变化 | key 禁用、更换、重排、渠道禁用、BaseURL/资源配置变化都不会静默切到新资源；修改 Provider、清空覆盖或默认模式改类型后缓存及时刷新，旧状态仍使用签发快照 |
| 请求完整性 | model 映射前后除 model 外语义不变；未知字段、false、0、9007199254740993、工具调用及结果、全部密文保留 |
| Azure 路径 | compact 后缀、配置 api_version、鉴权和请求长度正确；实际版本确实支持对应接口 |
| 普通响应 | 输出前索引已可见；写失败不泄出成功响应、不再次请求上游；无 usage 的响应不触发空指针 |
| SSE | created、output_item.added/done、completed、incomplete 中可用来源及时登记；大事件、分片、多行 data、错误帧和突然 EOF 正确处理 |
| 故障副作用 | 索引失败不触发渠道/key 禁用或评分失败；已产生 usage 不因索引故障丢失结算；流已提交时上游调用次数不增加 |
| 存储 | 用户隔离、引用类型隔离、命中续期、到期/淘汰、并发相容写与冲突写、Redis 故障不降级本地 |
| 升级 | 旧密文按显式头连续携带可用，去掉头且索引未知时明确失败；未知服务端引用拒绝；跨新实例接续成功 |
| 前端 | 详情显示有效值及来源；新建/编辑 Select 的 value 为名称字符串；批量、恢复默认和类型切换正确；显式选择等于默认值也保持显式模式；已有非预置字符串可回显；只更新 Provider 时保留 api_version 和未知 config 字段 |
| config 更新 | 未传 config 保留原值；对象内省略 provider 保留旧覆盖，`provider:""` 恢复类型默认；空 config 清空整段并恢复默认；非法 JSON 明确失败；创建和更新均执行后端校验 |

实施后的检查命令：

```bash
go test ./service ./model ./controller ./middleware ./relay/controller ./relay/channel/openai
go test -race ./service ./middleware ./relay/controller
go build ./...
go vet ./...
```

模型缓存/密钥代码若改动并发逻辑，需要加跑对应 `model` 的 race 用例。集成测试使用隔离数据库和测试 Redis，不连接生产数据。前端在 ezlinkai-web 执行项目 TypeScript 和目标文件 ESLint 检查。

真实联调必须完成普通响应及 SSE、reasoning→下一轮、compact→下一轮、同域不同 key/渠道回放，并保留脱敏的资源、模型、API 版本和结果矩阵。确认 `/openai/responses/compact?api-version=...` 在所用 Azure 版本可用；修 URL 本身不能证明上游支持。

## 7. 发布、观测与回退

1. 盘点渠道类型与显式 Provider，确认旧渠道使用类型名称默认值、创建/编辑下拉保存字符串且可恢复默认；再验证同 Provider 下实际 OpenAI/Azure 资源及多 key 的互通情况。需要隔离的资源使用不同 Provider 字符串或固定资源，默认映射不自动合并 Azure 与 OpenAI。
2. 共享 Redis 配置齐备后先部署完整新版本到隔离流量池；开始记录新索引，明确旧历史过渡规则。
3. 按用户或独立流量池灰度完整新逻辑。不要用不完整的单个过滤器作为中间生产版本，也不要让严格用户流量混入旧实例。
4. 监测索引命中/未知/冲突、Redis 延迟与失败、兼容候选耗尽、流式提交后错误、重试次数、内存及费用记录；标签避免包含用户原始引用、密文或未限制的 Provider 输入。
5. 本仓库已有 tag 触发的 amd64 和 arm64 构建工作流，但应以实际产物确认架构和镜像摘要。源码 tag、镜像发布、容器升级和前端部署分别验收，不能直接沿用来源文档的发布结论。
6. 回退不应恢复“删除历史继续执行”。保留索引，优先退回兼容的已验证版本或固定到明确资源；无法保证状态来源时明确拒绝有状态请求。不要在事故中把路由约束全量放开作为恢复手段。

首事件前不发送 ping 后，需核对反向代理等待上游/首字节超时。延长合理的代理超时或使用完整事件的提交策略，不能为了保活重新提前发送 200。

## 8. 原评估及计划修订记录

计划修订阶段仅修改本文，没有修改业务代码、前端代码或部署配置。该阶段对照后端 `a308b5a` 的渠道 config、候选选择、Responses 重试、多 key 和亲和逻辑，以及 ezlinkai-web 的渠道表单与类型选项，补充字符串 Provider、类型默认值和现有重试衔接合同。后续代码实现记录见第 9 节。

原评估保留的测试记录（本次文档修订未重新执行）：

```bash
go test ./relay/controller -run 'Test(AzureResponses|SanitizeAzureResponsesInput|ExtractOpenaiResponseNativeUsageDetailsIncludesCacheWriteTokens|BuildOpenaiResponse)' -count=1
```

原记录结果：`ok github.com/songquanpeng/one-api/relay/controller 1.062s`。这些用例验证当前 Azure 降级边界及 Responses 部分计费逻辑；其中清理历史的预期需要随新方案调整。此记录不代表新增 Provider 或重试设计已通过测试。本次仅做文档一致性与差异检查，未运行新功能测试、全量 build/vet、前端检查、真实上游联调及负载测试。

评审覆盖：架构、代码质量、测试和性能；列出 7 组上线问题，其中前 6 组为 P1。核心范围保留，建议限定 Responses 首期协议边界。独立第二评审未执行。没有把任何待验证的跨账户互通假设标为已通过。

实施前需落实的事实：同 Provider 渠道的真实互通结果、生产实例与 Redis 拓扑、旧历史需要维持的时间窗口。渠道级 Provider 字符串、创建/编辑 Select、未指定按类型名称默认，以及约束内保留既有有限重试的行为已在本文明确；真实互通结果决定需要额外隔离的资源和生产发布节奏。

## 9. 2026-09-21 代码实施与验证记录

### 已实现

- 渠道 `config.provider` 字符串配置、128 字符校验、按类型名称默认、创建/更新时按字段合并、显式清空及缓存刷新。48 个渠道类型的默认名称已与实际前端常量逐项比对。
- ezlinkai-web 渠道详情的创建/编辑 Select、默认来源提示、显式选择回显、类型切换及批量提交；保留 `api_version` 和未知 config 字段。前端不会把渠道类型的数字 value 当作 Provider 提交。
- Responses / compact 在首次选渠前解析历史及请求级 `X-Provider`；无历史约束时，以首渠的有效 Provider 冻结本次重试边界。适配器仍由渠道类型决定，当前 Responses 候选限定为 OpenAI 兼容适配器及 xAI 适配器。
- 复用静态/动态优先级和权重选择；失败候选在同 Provider 内有限轮转，不重置 `RetryTimes`。Responses 移除了未经验证的最后渠道回退，精确资源绑定不进入通用重试；补齐指定渠道、亲和禁止重试、取消及已输出检查。
- 用户隔离的状态索引、密文 Provider 约束、服务端引用的渠道/key/资源指纹、交集及冲突检查。共享 Redis 采用原子批量比较后写入；本地采用 10 万项 LRU，TTL 为 7 天且命中续期；Redis 故障不回退本地。
- 每次尝试只选一次实际 key，清空前次尝试残留的自定义请求头；快照、发送和重试日志使用同一 key。资源指纹规范化默认端点及空配置，避免普通表单保存造成虚假的资源变化。
- 移除删除 reasoning 密文/ID、删除 compaction 项后再次请求的 Azure 降级；模型映射仅替换顶层 model，保留未知字段、false/0 和大整数；修复 Azure compact 后缀。
- 普通响应和完整 SSE 事件在状态登记成功后输出；关闭 Responses 上游首字节前的 ping，响应已输出后不重放。SSE 支持多行 data、超过 64 KiB 的事件、断流及超时，单事件上限为 16 MiB。
- 索引/路由故障不触发渠道或 key 禁用及失败评分；已获得 usage 的失败仍结算，缺失 usage 的估算单独标记。失败消费记录不同时生成成功评分样本。

### 自动化与本地交互验证

以下新增及相关回归用例通过，包括隔离 SQLite 中间件/API 测试、独立本地 Redis 契约测试和 httptest 上游测试：

```bash
go test ./service ./model ./controller ./middleware ./relay/controller ./relay/channel/openai \
  -run 'Test(ChannelProvider|ProviderChannel|ChannelConfigClear|Responses|AzureResponses)' -count=1
go test -race ./service ./model ./controller ./middleware ./relay/controller \
  -run 'Test(ChannelProvider|ProviderChannel|ChannelConfigClear|Responses|AzureResponses)' -count=1
go build ./...
go vet ./...
```

相关包完整测试中有两个既有失败：`TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered`、`TestEvaluateUsageBasedChannelDisable_MixedChannels`。已在独立、未修改的 `a308b5a` 工作树复现相同结果；其余相关包用例使用下列命令检查，不将完整测试报告为全部通过：

```bash
go test ./service ./model ./controller ./middleware ./relay/controller ./relay/channel/openai \
  -skip '^TestEvaluateUsageBasedChannelDisable_(SingleChannelTriggered|MixedChannels)$'
```

前端执行 `tsc --noEmit --incremental false` 和渠道表单的 Next ESLint 检查。表单原有的 console、Hook 依赖警告仍保留；修复了该文件中阻止 ESLint 通过的 JSX 引号转义错误。

通过 browser-harness 对真实渠道表单使用模拟 API 做本地交互验收：编辑回显默认 Provider、选择 `Anthropic Claude`、修改渠道类型后保留显式值、恢复默认后显示 `Azure OpenAI`、保存字符串/空值且保留 `api_version` 和未知字段，以及创建页面展示默认选项。临时验收路由、浏览器标签和开发服务已清理。

### 发布前仍需验证

尚未执行真实 OpenAI/Azure 跨资源互通联调、负载与容量测量、滚动升级或生产部署。`X-Provider` 仅能帮助未知旧密文收紧路由，不能恢复未知的服务端 response/item/conversation 引用；上线须遵守第 7 节的共享索引和切流要求。同 Provider 下的实际兼容性仍由真实资源联调确认。
