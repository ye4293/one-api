# OpenAI 缓存创建价格与响应计费

日期：2026-11-06。根据用户补充要求恢复缓存价格配置，同时保留原请求、流式、重试和估算流程。代码不提交、不部署。

## 实现

- `common/openai_cache_pricing.go`：两档价格的配置校验、默认值和读取函数。新增 30m 覆盖表与历史倍率表分离；12h 默认 2.5，均支持显式零。
- `model/openai_cache_option.go`：单模型更新在数据库事务内锁定对应配置行并合并最新内容，提交成功后更新运行时。新配置的整表保存和同步共享本实例锁。
- `model/option.go`：注册两个缓存选项，对它们使用独立的保存/刷新流程；其他选项沿用原流程。
- `controller/pricing.go`：恢复原价格列表中的 GPT 两档字段及单模型保存入口，调用数据库合并方法。
- `relay/controller/openai_cache_pricing.go`：缓存单价覆盖解析。
- `relay/controller/openai_pricing.go`：缓存新配置独立携带，不参与原 `any/applies`。即使只配置 `_priority` 的 30m 或 12h 倍率，普通请求仍使用原默认 2 倍。
- `relay/model/general.go`：新增三个可选 RawMessage 字段，保证 Chat 缓存参数在原请求重建过程中保留，未传入时不注入。
- Chat/Responses 只在原流程取得的响应 usage 上计算缓存创建费用。两档明细完整且总量一致时，直接使用响应的 30m/12h 分项；明细缺失或不一致时，单独回退旧总量计费。2026-11-07 按用户确认调整日志：usageDetails 的旧创建字段改为记录 30m，仅新增 12h 和总量两个字段；无有效分项时，旧总量按 30m 口径记录。计费拆分器与费用公式不变。

前端现有 `ezlinkai-web/sections/setting/view/pricingPage.tsx` 保留 JSON 配置和 GPT 价格编辑字段；后续增加未编辑缓存价格不提交覆盖的保护。日志详情同步三字段展示，见下文。

## 保持原样

`common/model-ratio.go`、`relay/channel/openai/adaptor.go`、`relay/channel/openai/main.go`、`relay/controller/text.go` 与 HEAD 一致。`relay/model/general.go` 仅新增三个可选缓存请求字段。Responses 请求构造、流式和非流式响应处理保持原样；仅结算分支接入缓存价格覆盖。

不恢复请求 JSON patch、主动补取/过滤 usage、缓存保护估算条件或 incomplete 事件采集。原创建倍率字段使用实际应用的 30m 价格；2026-11-07 按用户补充要求追加两档结算快照，见下文。

## 验证

本轮验证日志保存在 `/private/tmp/one-api-cache-price-restore-20261106`。同目录保留恢复配置前的响应计费版本。

- 默认值、零值、删除配置、非法配置、价格列表回显、保存持久化和数据库失败保护。
- 24 个模型、两档共 48 次并发保存，验证每项均持久化且运行时一致。
- 模拟运行时配置落后于数据库，验证单模型保存保留数据库中的其他模型。
- 只配置档位缓存价格时，普通请求仍使用原输入/输出/读取价格及默认 Priority 倍率；混合缓存请求按配置结算。
- 原缓存/价格/服务档位回归及全项目构建、静态检查、相关 race 检查。

### 价格配置恢复验证结果

- `go test ./common ./model ./controller ./relay/util ./relay/controller -run 'TestOpenAI|TestPricing' -count=1` 通过；其中 model 无匹配用例。
- `go test -race ./common ./controller ./relay/util ./relay/controller -run 'TestOpenAI|TestPricing' -count=1` 通过。
- common、relay/util、relay/controller 完整包测试通过；relay/model、relay/channel/openai 无测试文件，编译通过。
- model 完整包测试仅 `TestShouldDisableChannelByRecentUsage_WindowConfigurable` 失败。用 HEAD 源码覆盖层单独运行同一测试复现失败：40 分钟前的时间向下取整到小时后，可能落到 90 分钟窗口外。该测试及对应逻辑不属于本次修改。
- `go build ./...`、`go vet ./...` 和 `git diff --check` 通过。
- 原请求/流式关键文件与 HEAD 一致；未提交、未部署。

### 30m 分项修正

- `SplitOpenAICacheWriteTokens` 仅在两档字段均存在、非负、未溢出、总量等于分项之和且不超过输入余额时，返回可用于分项计费的结果。缺总量时允许由完整分项补全总量。
- 旧响应或无效明细的回退结果只包含原总量，不再把总量赋给 30m，也不根据总量减去 12h 反推 30m。
- Chat/Responses 使用同一校验结果决定分项计费及 usageDetails 新字段是否写入；完整分项直接乘对应配置倍率，回退使用原总量算式。
- 回归覆盖部分明细、单档数量恰好等于总量、null、空明细、明确零值、分项之和小于/大于总量、溢出及旧响应；两个入口均覆盖流式和非流式的费用与日志落库。
- 30m 保留 `tokens × ModelRatio × CacheWriteRatio` 的乘法顺序；新增旧总量与完整 30m 明细的取整边界回归，保护 Chat 向上取整和 Responses 截断结果。
- 最终 `go test ./relay/util ./relay/controller -count=1`、`go test -race ./common ./controller ./relay/util ./relay/controller -run 'TestOpenAI|TestPricing' -count=1`、`go build ./...` 及 `git diff --check` 通过。日志位于 `/private/tmp/one-api-cache-direct-30m-20261106`。

### Chat 显式缓存参数透传

- 功能代码仅在 `GeneralOpenAIRequest` 增加 `prompt_cache_options`、`prompt_cache_key`、`prompt_cache_retention` 三个可选 RawMessage 字段；保留完整 JSON，不恢复先前的请求 JSON patch 或强制 usage 获取逻辑。
- `TestOpenAIChatCacheParametersPassthrough` 使用真实 Chat 入口和模拟 HTTP transport，覆盖 7 条路径 × 6 种参数组合 × 流式/非流式，共 84 个场景。
- 路径包括原样转发、模型映射、max_tokens、max_completion_tokens、映射加 token 转换、音频及重试后映射。校验缓存参数、内容块断点、数字精度、显式空值、未传时不添加、原 token 转换和流式 usage 规则。
- 同一响应用量在各请求缓存选项下仍结算为同一额度，验证请求 TTL 不参与用量推断，也不改变原档位计费。
- 专项透传测试已通过；本轮日志位于 `/private/tmp/one-api-chat-cache-params-20261106`。
- `go test -race ./common ./controller ./relay/util ./relay/controller -run 'TestOpenAI|TestPricing' -count=1`、`go build ./...`、`go vet ./...` 和 `git diff --check` 通过。
- `go test ./common ./relay/... -count=1` 中 common、relay/controller、relay/util 及其余有测试的适配器通过；Anthropic 的 `TestFilterBetaFlags/vertex_allows_files-api` 和 Kling 的 `TestGetModelNameFromRequest/有model` 失败。使用 HEAD 源码覆盖层运行同样测试，复现相同失败，确认是既有问题。本次未修改这两处逻辑。

### 2026-11-07 日志一致性修正

本节的独立 30m 字段及总量去重展示已被下方“旧字段复用为 30m”方案替代，验证结果保留为历史记录。

- Responses 记录日志时复用 `usageDetails` 中已经校验或补全的缓存创建总量，补齐原始响应未提供总量时的 `billingDetails` 数量和原有倍率字段；扣费计算及价格获取时机保持原样。
- 前端为 `cache_creation_30m_tokens` 增加正确标签；四处桌面/移动日志共用显示筛选，隐藏与新总量相同的旧总量，保留新缓存分项的明确零值。旧日志与 Claude 字段继续使用原筛选规则。
- 新增 `TestOpenAICacheResponseDerivedWriteLogs`，覆盖流式/非流式及带/不带价格快照的四条日志路径；先确认旧实现失败，再验证修复通过。
- `go test -race ./common ./controller ./relay/util ./relay/controller -run 'TestOpenAI|TestPricing|TestGPT5Family|TestServiceTier' -count=1` 通过。前端 TypeScript、三个修改文件的 lint 和 26 个中英文分项/旧日志兼容场景通过。验证日志位于 `/private/tmp/one-api-cache-fix-20261107`。

### 2026-11-07 缓存价格快照

- Chat 的有效缓存分项按 token 计费后，将实际使用的 30m/12h 倍率写入 `billingDetails.cache_write_30m_ratio`、`cache_write_12h_ratio`；读取倍率也使用本次计算值。固定价格及其他 API 不增加分项价格快照。
- Responses 在原计算函数中保存 12h 相对倍率，使用可空指针区分无有效分项和明确零价；有效分项的基础档位也向异步日志传入完整的原有价格快照，不在记录时用新价格覆盖。
- 保留原创建倍率别名、无分项的旧日志路径、公式及取整顺序。前端两处账单详情读取保存的快照，展示两档倍率；旧日志不补现价，明确零价显示 `0×`。
- `TestOpenAICachePriceSnapshots` 通过真实 Chat/Responses 入口、流式/非流式与异步结算，在实际扣款时修改运行配置，覆盖 32 个场景。验证基础默认价、基础覆盖、自动档位、档位缓存覆盖、零价、补全总量及固定价格；用落库明细复算测试账单，保留 Responses 原浮点截断边界。
- 原双入口分项测试同步验证：有效分项记录两档倍率，无效/部分分项及旧响应不新增快照。原 `TestOpenAIResponseTierPricingLegacyLogTiming` 仍通过，旧总量路径的行为未改变。
- 后端相关 `-race` 回归、前端 TypeScript 和三个日志文件的 lint 均通过；lint 仅有原有警告。24 个真实 JSX 渲染场景和 26 个旧日志兼容场景通过。日志与修改前备份位于 `/private/tmp/one-api-cache-snapshot-20261107`。
- 仅改代码及文档，未回填历史数据、提交代码或重新部署。

### 2026-11-07 旧字段复用为 30m

- `cache_creation_input_tokens` 保存 30m 创建量，仅新增 `cache_creation_12h_tokens` 与 `cache_creation_total_tokens`；移除独立的 `cache_creation_30m_tokens` 输出。旧账单按原字段为 30m 解释，不修改历史记录。
- 有效分项直接记录上游 30m/12h 数量；旧响应或无效分项仍按原公式扣费，日志中将原总创建量归到 30m，12h 记 0，总量保留原值。没有创建信息时不新增字段。
- 两入口在写入 `other` 时保留明确为 0 的 30m 字段，包括仅 12h 和两档均为零的场景；非 OpenAI 日志继续使用原省略规则。`billingDetails.cache_write_tokens` 及两档价格快照保持原口径。
- 前端删除独立 30m 字段定义和旧总量去重逻辑，分别显示旧字段代表的 30m、12h 和总量；可识别的 GPT/o 系列历史记录按 30m 标注，Claude 等其他模型的旧展示保持原样。
- 原双入口测试更新日志预期，并新增明确零分项、无创建信息的流式/非流式用例；原金额断言不变。改动前已复现旧字段记录 7000 而预期 30m 为 3000 的失败。
- 专项后端竞态回归通过；前端 36 个字段展示场景、24 个倍率快照渲染场景和 31 个价格编辑场景通过。验证日志与修改前备份位于 `/private/tmp/one-api-cache-legacy-30m-20261107`。
- 前端 TypeScript 和三个日志文件的 lint 通过，lint 仅保留原有警告；后端、前端 `git diff --check` 通过。未提交、未部署。
