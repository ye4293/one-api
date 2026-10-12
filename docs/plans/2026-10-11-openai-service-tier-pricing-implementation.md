# OpenAI service_tier 分档计费实施计划

- 日期：2026-10-11。
- 状态（2026-10-11 更新）：P00–P05 本地实现与验证完成；P06 专项测试、build、vet、race 通过，全量测试存在已在原始 HEAD 复现的存量失败和环境限制；P07 配置草稿与回滚说明已准备，现场配置核对、真实接口验收及发布未执行。详见[执行证据](../evidence/openai-service-tier/2026-10-11/README.md)。
- 设计依据：[OpenAI 按响应 service_tier 查价设计](2026-10-10-openai-service-tier-pricing-design.md)，采用其 2026-10-11 更新版本。
- 代码基线：分支 `codex/openai-service-tier`，HEAD `fcca90e3b143363f95d8af6e4d7ca421516ef0f0`。
- 执行顺序：`P00 → P01 → P02 → P03 → P04 → P05 → P06 → P07`。每阶段达到验收条件后再进入下一阶段。
- 完成定义：实现响应档位取价与 GPT-6+ 长上下文扩展两项变化；其余既有行为通过基线回归；交付本地验证证据、配置变更清单和回滚说明。

## 1. 目标与范围

在已经完成的请求透传、响应档位采集基础上，让 OpenAI Chat、Completions 和 Responses 按**上游实际响应的 `service_tier`**选择结算价格。继续使用现有价格 map、公式、折扣和扣费流程。

本次有且仅有两类预期计费变化：

1. 响应为非空且非 `default` 的档位，并命中当前入口可用的价格配置时，逐项采用 `基础计费模型_响应档位` 对应价格。
2. 长上下文阈值增加 GPT-6 及后续主版本默认规则；总输入严格超过 `272000` 时，对整次请求的输入及缓存价格乘 `2`、输出价格乘 `1.5`。该规则不依赖档位配置命中。

因此，**未配置档位价格不等于费用完全不变**：GPT-6+ 长上下文请求仍可能因第二项变化增加费用。

本次不增加独立档位价格表、价格版本、配置事务、管理 API、数据库字段或前端页面；不修改预估、预扣、余额检查、重试、流式生命周期，不重构其他供应商的独立计费，不自动写入官网价格。

### 1.1 资料与证据边界

当前设计稿记录了官方模型、档位及价格快照。本文以该设计为需求依据，不将这些快照重新表述成本轮已核实的当前价格。计划制定时访问开发者文档得到 `Forbidden`；本次实施复核时 DNS 失败，提权访问因自动审批 `Quota exceeded` 未执行。官网复核仍未完成，配置草稿仅使用设计快照及本地数学验证。

实施时需要复核的来源沿用设计稿：[Responses service_tier](https://developers.openai.com/api/reference/resources/responses/methods/create)、[Fast mode](https://developers.openai.com/api/docs/guides/fast-mode)、[Flex processing](https://developers.openai.com/api/docs/guides/flex-processing)、[Ultrafast mode](https://developers.openai.com/api/docs/guides/ultrafast-mode)、[价格表](https://developers.openai.com/api/docs/pricing)。档位支持范围与真实价格核对属于 P07 配置准备；不阻塞基于人工价格的本地实现和验收。

第 2 节保留计划制定时的代码现状，第 5–7 节为实施契约。实际完成状态、执行命令和验收矩阵见第 8 节及执行证据，不再以最初的“尚无实现”描述当前工作区。

## 2. 当前能力与改动位置

| 环节 | 已有实现及位置 | 本次处理 |
|---|---|---|
| 请求字段 | `relay/model/general.go`、`relay/channel/openai/model.go` 已有 `ServiceTier` | 复用；补充回归用例，不重写 DTO |
| 请求重序列化与映射 | `relay/controller/text.go`、`relay/controller/opeai_response.go` | 保留透传与映射行为，不用请求档位决定结算 |
| 响应采集 | `relay/channel/openai/main.go`、`doNativeOpenaiResponse()`、`doNativeOpenaiResponseStream()` | 复用 `openai.ResponseServiceTierContextKey`，不重复解析响应 |
| 流式和重试 | 已有最近非空档位更新及旧档位清理逻辑 | 回归验证，`default` 必须覆盖之前的非默认档位 |
| 价格配置 | `controller/pricing.go`、`model/option.go`、`common/model-ratio.go` | 复用现有 options → 内存 map → 同步链路 |
| 精确档位查询 | 尚无独立覆盖项查询 | 新增 `relay/controller/openai_pricing.go` |
| Chat/Completions 结算 | `relay/controller/helper.go:postConsumeQuota()` | 原取价位置应用局部覆盖，保留按次优先级及差额结算 |
| Responses 结算 | `relay/controller/opeai_response.go` | 在 actual quota 计算处接入覆盖，必要时提取内部计算函数 |
| 长上下文 | `common/model-ratio.go:GetLongContextMultipliers()`，目前仅 9 个精确模型阈值 | 保留原表和系数，增加 GPT-6+ 默认阈值 |
| 消费日志 | Chat 的 `UsageDetailsForLog`、Responses 的 `OpenaiReseponseUsageDetails` 已有档位 | 仅命中分支记录实际采用的价格，不新增字段 |
| 测试设施 | `openai_service_tier_test.go` 已有 SQLite 内存库、SSE 构造器和响应处理器测试 | 直接扩展；补充实际扣费断言和入口接线验证 |

配置加载仍由 `InitOptionMap()` / `loadOptionsFromDatabase()` 初始化，通过现有管理接口修改本进程 map，通过 `SyncOptions()` 刷新其他进程。`SYNC_FREQUENCY` 代码默认值为 `600` 秒，实际部署值须现场读取，不承诺多实例立即一致。

`CacheWriteRatio` 当前只有 map/getter，没有接入 options 或价格管理接口；`GetCacheWriteRatio()` 未命中默认返回 `1.25`。本次允许读取该 map 已有的档位项，未配置时回退基础模型，不补做管理功能。

## 3. 必须固定的实施契约

### 3.1 价格选择

| 项目 | 规则 |
|---|---|
| Chat 基础计费模型 | `meta.BillingModelName()`，为空继续回退 `textRequest.Model` |
| Responses 基础计费模型 | 当前入口的 `original_model`，不改成映射后名称或响应中的模型 |
| 档位来源 | 只读取本次响应采集的 context 值；请求档位不参与最终查价 |
| 空值、缺失、null、`default` | 原模型价格；忽略 `_default`、`_standard`、`_Standard` 等配置 |
| 其他响应值 | 原样拼接，不转大小写、不裁剪、不做白名单及别名转换 |
| `priority` / `fast` | 分别只查 `_priority` / `_fast`，不相互兜底 |
| 命中判断 | map 的 `value, exists`；`0` 是已配置，特殊值沿用旧分支含义 |
| 输入回退 | Chat 保留请求前传入的 `modelRatio`；Responses 在原取价位置读取基础模型 |
| 输出/缓存回退 | 在原位置调用基础模型 getter，不对档位键调用带默认逻辑的 getter |
| 按次价格 | Chat 支持 `ModelPrice` 覆盖和回退；Responses 不读取该项、不新增按次计费 |
| 内部价格键 | 只用于查价，不改变请求模型、渠道模型列表、映射结果或日志/账单主体模型名 |

`ModelRatio` 是输入倍率；`CompletionRatio`、`CacheRatio`、`CacheWriteRatio` 是相对输入倍率。只覆盖输入倍率时，其他相对倍率回退基础模型，但对应绝对单价也会随输入倍率变化。这是设计要求，不是逐项回退失效。

### 3.2 长上下文

1. 使用基础计费模型查 `LongContextThreshold`；只要存在精确配置，就完全以该阈值决定是否触发。
2. 无精确配置时，按设计 §2.4 的命名模式解析主版本整数；小写 `gpt-`、主版本 ≥6 才使用默认 `272000`。保留小版本及变体/日期后缀，不用字符串大小比较。
3. 按总输入判断，包含计入总输入的缓存部分，不加输出，不用扣除缓存后的普通输入。
4. `271999 / 272000 / 272001` 分别得到 `1/1、1/1、2/1.5`；超过后全量应用系数。
5. 已选的档位或回退单价各乘一次系数；长上下文分支不得重新查基础价格覆盖它们。
6. 按次计费保持固定价格，不能给整笔固定金额乘 token 长上下文系数。

GPT-7 / GPT-10 等只能作为版本解析测试数据；未来主版本默认阈值是平台策略，不是官方定价承诺。

### 3.3 旧行为与日志

- Chat 保留 `math.Ceil`、`quota - preConsumedQuota`、最低额度判断与零 token 处理顺序。只有档位输入倍率命中时，最低额度条件使用的局部 `ratio` 同步为选定输入倍率 × 原组合折扣。
- Responses 保留原表达式的浮点计算次序、`QuotaPerUnit` 换算及 `int64` 截断，保留原有余额校验、异步全额扣费流程。
- 原导出计算函数与 getter 签名不变。原调用方只通过共用长上下文函数获得 GPT-6+ 扩展，不自动使用档位。
- `usageDetails.service_tier` 保留响应事实：无值不补写，`default` 原样记录。保留原日志出现条件，例如 Chat `quota == 0` 时原本不写消费日志，不能为了档位另写一条。
- 仅命中档位分支把参与计算的实际倍率传给日志；缓存字段仍按原条件出现，保留 `cache_ratio/cache_read_ratio` 与 `cache_write_ratio/cache_creation_ratio` 原有字段。long 系数沿用原文案，不新增 JSON 字段。
- `tier_ratio` 继续表示用户等级折扣，与 `service_tier` 无关。账单发送、数据库日志模型和模型映射处理均不变。
- 无覆盖分支保留原取价位置与日志查价时机；不借机统一热更新语义，不建立全局配置快照或全局锁。

代码当前对异常缓存统计的处理存在差异：Chat 将负普通输入钳为零，Responses 回退到总输入；这与仓库 usage 规范要求的硬校验并不一致。按本次特定设计要求，记录为存量问题并保留差异回归，不在档位功能里改变异常用量策略，也不把旧行为当成正确用量契约。

## 4. 阶段总览

| 阶段 | 交付内容 | 依赖 | 完成关卡 |
|---|---|---|---|
| P00 | 基线与测试夹具准备 | 当前设计稿 | 确认已有能力、原费用与环境状态 |
| P01 | 精确档位覆盖项查询 | P00 | 命中/回退/零值/入口差异测试通过 |
| P02 | GPT-6+ 长上下文默认阈值 | P01 | 新旧模型与显式阈值优先级通过 |
| P03 | Chat/Completions 结算及日志闭环 | P02 | 响应 → 查价 → 差额扣费 → 日志可复算 |
| P04 | Responses 结算及日志闭环 | P03 | 响应 → 查价 → 全额扣费 → 日志可复算 |
| P05 | 流式、重试、兼容性完整回归 | P04 | 三种入口、两种传输形态及回退矩阵通过 |
| P06 | 构建、静态检查与交付审查 | P05 | 必要检查和设计覆盖追踪完成 |
| P07 | 配置准备、环境验收及发布回滚 | P06 | 产出可审查清单；获发布授权后执行上线 |

P03 完成后形成第一个本地可验证闭环；P04 补齐 Responses；不能把其中任一步完成写成全部功能完成。

## 5. 分步骤执行

### P00：建立基线，复用测试设施

**修改范围：**优先扩展 `relay/controller/openai_service_tier_test.go`；必要时新增后续阶段列出的测试文件。本阶段不改生产计费逻辑。

1. 执行 `git status --short`、`git diff --stat`、`git rev-parse HEAD`，记录实际起点。保护现有未跟踪设计文档与其他改动，不重置或清理工作区。
2. 阅读设计稿及 `AGENT.md`、`CLAUDE.md`，核对本文契约；后续若实际代码与本基线不同，先更新影响分析。
3. 运行下面的已有测试，记录命令、退出码和环境问题：

   ```bash
   go version
   go test ./common -run '^TestGPT5Family' -count=1
   go test ./relay/controller -run '^(TestOpenAIServiceTier|TestServiceTierOtherModelUsageLogs|TestAppendUsageDetails|TestExtractOpenaiResponse|TestBuildOpenaiResponse)' -count=1
   ```

4. 复用 `setupServiceTierLogDB()`、`serviceTierRecorder`、`serviceTierSSE()`。扩展用户/token/channel/log 断言需要的测试辅助函数，不新建另一套数据库框架。
5. 为价格 map、`LongContextThreshold`、`QuotaPerUnit` 等测试全局状态保存旧值，并用 `t.Cleanup` 恢复。共享全局状态的测试不要使用 `t.Parallel()`；异步结算完成后才能恢复状态或关库。
6. 在旧实现下采集无档位、`default`、无匹配价格的基线：额度、token 余额变化、用户/渠道用量、日志字段与出现条件。加入固定价格、零 token、nil usage 计算函数、异常缓存和折扣样例。
7. 记录原函数级测试与完整 HTTP 入口测试的区别：现有 Responses 日志测试使用传入的固定 quota，并不能证明 actual quota 已接入档位；P04/P05 必须补齐这层验证。

**验收：**基线测试有实际结果；能复用内存数据库读取扣费和消费日志。原有失败须独立记录，不能算作本功能通过，也不能据此扩大无关修复范围。所有外部依赖/网络阻塞须注明为未运行或未完成。

### P01：实现精确覆盖项查询

**文件：**新增 `relay/controller/openai_pricing.go`、`relay/controller/openai_pricing_test.go`。

1. 实现内部 `lookupOpenAIPriceOverrides(baseModel, responseTier, entry)`，其中入口显式区分 Chat 和 Responses，类型/名称可依仓库风格调整。
2. 返回每项数值及存在标记；字段覆盖输入、输出、缓存读、缓存写和 Chat 按次价格。另提供“当前入口至少命中一项”的判断。
3. 空档位或 `default` 立即返回无覆盖；否则仅使用 `baseModel + "_" + responseTier` 精确读 map。
4. 查询函数不调用原价格 getter、不填回退值、不计算长上下文、不修改 context/模型/配置。回退仍由调用方在原取价位置执行。
5. Responses 查询忽略 `ModelPrice`。仅配置按次价格时，Responses 必须返回无覆盖。
6. 新增表驱动测试 `TestOpenAIPriceOverrides`：完整、部分、完全未命中、显式零值、特殊值、自定义档位、大小写差异、`default`、仅另一种 fast/priority 后缀存在、基础模型包含下划线。
7. 用 `GetModelRatio()` 未命中会返回默认值的模型构造反例，证明档位是否存在是从 map 判断，不会把 getter fallback 当成命中。

**验证命令：**

```bash
go test ./relay/controller -run '^TestOpenAIPriceOverrides' -count=1
```

**验收：**查询只描述覆盖项；不命中、不应读取的按次项和显式 `0` 可准确区分；尚未接入结算时既有行为不变。

### P02：扩展共用长上下文阈值

**文件：**修改 `common/model-ratio.go`；新增 `common/model_ratio_gpt6_test.go`，保留 `common/model_ratio_gpt56_test.go`。

1. 仅调整 `GetLongContextMultipliers()` 内的阈值选择，保持函数签名、返回类型、原 9 项精确配置和 `2/1.5` 系数。
2. 先检查精确阈值是否存在。存在但输入未超过时返回 `1/1`，不能继续走 GPT-6+ 默认分支。
3. 无精确值时，按设计的前缀、主版本整数、小版本及连字符边界识别 GPT-6+；解析失败或整数溢出按不匹配处理。
4. 更新相关注释，让其准确说明精确阈值与 GPT-6+ 默认策略；不要扩充基础模型价格表。
5. 新增 `TestGPT6LongContextMultipliers`，覆盖已列出的 4 个 GPT-6 模型、未来版本测试样例、旧 9 项、未注册旧版本及非法名称。
6. 显式覆盖测试至少有两种：GPT-6 模型阈值改为 `500000`、输入 `300000` 不触发；阈值改为 `1000`、输入 `1001` 触发。测试完成恢复原表。

**验证命令：**

```bash
go test ./common -run '^(TestGPT5Family|TestGPT6LongContextMultipliers)' -count=1
```

**验收：**旧边界不变；新模型的 `271999/272000/272001` 正确；不识别 `gpt6`、`my-gpt-6`、`GPT-6-astra`、`gpt-6foo` 或其他供应商名称；显式阈值拥有完整优先级。

### P03：接入 Chat / Completions，完成最小闭环

**文件：**修改 `relay/controller/helper.go`；新增 `relay/controller/openai_pricing_chat_test.go`；复用或扩展 `openai_service_tier_test.go`。

1. 在 `postConsumeQuota()` 确定原基础计费模型后读取响应 context，查询 Chat 覆盖项。
2. 原有取价位置逐项覆盖局部变量：输入缺失仍用入参 `modelRatio`，输出/缓存/按次缺失仍按原基础模型读取。长上下文始终传基础模型及 `promptTokens`。
3. 保留固定价格优先级：档位按次价存在时采用该值，否则回退基础按次价。原模型按次计费时，仅有档位 token 倍率不能让它自动切换计费方式；显式按次值 `-1` 继续遵守旧分支语义。
4. 保持原 token 算式和有/无缓存分支顺序。仅输入覆盖命中时，同步局部组合 `ratio`；保留最低额度条件及随后零 token 归零逻辑。
5. 在命中覆盖分支保存实际用到的价格局部值，供同次 `billingDetails` 使用；不在日志阶段再次调用 getter 替换它们。无覆盖分支保持原查询位置与次数语义。
6. 原样执行 `quotaDelta = quota - preConsumedQuota` 及后续消费记录/统计。日志主体模型名继续由原映射逻辑决定。
7. `TestOpenAIChatTierPricing` 至少验证第 6 节金额表、输入零值、部分配置、按次零值、按次 `-1`、最低额度、折扣与不命中回归；断言最终额度和日志实际倍率。
8. 使用内存数据库先通过现有预扣函数扣 `100`，再调用 `postConsumeQuota()`：priority 样例最终额度 `2580`，第二次扣费差额应为 `2480`，总扣费应为 `2580`。另设预扣高于实际额度的用例验证退回差额，不只断言日志数字。
9. 验证默认档位即使配置了差异很大的 `_default` 也不使用；流中 priority → default 后，最终按基础价格扣费、日志记录 default。

**验证命令：**

```bash
go test ./relay/controller -run '^(TestOpenAIChatTierPricing|TestOpenAIServiceTier|TestServiceTierOtherModelUsageLogs)' -count=1
```

**验收：**普通和流式 Chat/Completions 的响应采集、实际扣费与日志能够关联复算；固定价格不乘长上下文系数；无覆盖非新增长上下文场景与 P00 一致。

### P04：接入 Responses，复用原计算表达式

**文件：**修改 `relay/controller/opeai_response.go`；新增 `relay/controller/openai_pricing_response_test.go`；必要时扩展 `opeai_response_test.go`。

1. 在 `RelayOpenaiResponseNative()` 成功处理响应后、计算 `actualQuota` 前读取响应档位，按 `modelName`（`original_model`）查询 Responses 覆盖项。
2. 无覆盖时继续调用 `CalculateResponseQuotaFromUsageMetadata()`，保留原异步日志调用路径和取价时机。
3. 为命中分支提取最小内部计算函数，在原 getter 位置替换命中项。原导出函数保持签名，并继续按基础模型调用共用逻辑；不要复制一份新的计费公式。
4. 保留 usage 提取、异常缓存处理、推理 token 字段、原浮点表达式与整数截断；只传递必要价格参数，长上下文仍使用基础模型和总输入。
5. 命中分支把实际计算使用的输入/输出/缓存倍率按值交给异步记录函数；可保留原记录函数作为无覆盖包装，另设内部有价格参数的实现。不能改变原调用方行为或把缺失项提前统一查价。
6. 保留 `recordOpenaiResponseConsumption()` 的全额 `PostConsumeTokenQuota(tokenId, quota)`、统计和日志流程；该入口当前预估只做余额检查，不应减去一个未实际扣除的预估额。
7. `TestOpenAIResponseTierPricing` 覆盖金额表、`QuotaPerUnit` 非默认值、逐项回退、零值、精确模型差异、长上下文；只配置 `ModelPrice[模型_档位]` 时应与旧 token 计费一致。
8. 使用 P00 内存库验证最终 token 余额、用户/渠道额度和消费日志。无缓存时不增添缓存字段，`usageDetails` 保留真实档位。
9. 模拟“计算完成后，异步记录前修改价格 map”的受控顺序：命中分支日志必须保留计算时已用参数；无覆盖分支维持基线读取时机。测试用显式同步点，不能并发读写全局 map 制造无关 data race。

**验证命令：**

```bash
go test ./relay/controller -run '^(TestOpenAIResponseTierPricing|TestOpenAIServiceTier|TestExtractOpenaiResponse|TestBuildOpenaiResponse)' -count=1
```

**验收：**实际 `actualQuota` 已接入覆盖；Responses 仍是 token 计费、原截断和全额扣费；命中分支日志使用实际价格。旧导出函数只产生 GPT-6+ 长上下文这一项允许的新变化。

### P05：补齐链路和兼容性回归

**文件：**扩展前述测试；新增 `relay/controller/openai_pricing_integration_test.go`、`relay/controller/openai_pricing_compatibility_test.go`（仅在现有文件承载不清晰时拆分）。

1. 复用现有 `httptest` 和数据库设施，增加 `TestOpenAITierPricingIntegration`：本地 mock 上游 → `RelayTextHelper()` / `RelayOpenaiResponseNative()` → 实际异步结算 → 数据库消费日志。
2. 覆盖 `/v1/chat/completions`、`/v1/completions`、`/v1/responses` 的普通及 SSE 六种组合。每种至少有一次档位命中、一次回退；检查收到的上游请求模型没有价格后缀，响应 body/SSE 档位未被改写。
3. 等待异步结果使用限定超时的条件等待，并确保结算统计结束后再清理夹具；禁止仅睡固定时间后假设扣费已完成。每个样例检查日志条数和最终余额，防止重复或漏扣。
4. 验证请求 fast → 响应 priority、请求 priority → 响应 flex、请求带档位但响应缺失/default，以及 SSE 档位与 usage 分离、最后 default、null/空字段不覆盖已有非空值。
5. 按现有重试入口复用 context，验证失败尝试旧档位不会进入成功请求；流式错误、中断和零用量按原行为回归，不新增生命周期处理。
6. 模型映射双向测试：为别名和映射后模型配置明显不同价格；Chat 选择原逻辑的映射后基础模型，Responses 保留 `original_model`。阈值选择也保留各自口径，不能为自定义别名偷偷读取响应模型补偿。
7. 扩展 `TestServiceTierOtherModelUsageLogs`：复用 OpenAI 响应处理器的兼容渠道只要采集到档位并命中可进入覆盖；独立响应处理器不能仅凭请求字段、模型前缀或存在档位配置误进入。
8. `TestOpenAITierPricingCompatibility` 比较 P00 基线，覆盖无缓存、有缓存、异常缓存、nil usage 计算函数、零 token、最低额度、固定价格/特殊值、组合折扣、日志条件及配置读取时机。nil usage 的函数级返回值不能替代完整 HTTP 入口的有效 usage 测试。
9. 本阶段不接真实上游、不调用付费模型、不启动生产 shipper。保留现有账单路径，复用现有 shipper 本地测试验证模型与额度传递契约。

**验证命令：**

```bash
go test ./relay/controller -run '^(TestOpenAITierPricingIntegration|TestOpenAITierPricingCompatibility|TestOpenAIServiceTier|TestServiceTierOtherModelUsageLogs)' -count=1
go test ./common/shipper -count=1
```

**验收：**第 7 节矩阵全部有对应测试与结果；完整入口、函数测试、模拟数据库验证分别记录，不能把 mock 测试写成官方接口或生产验证。

### P06：执行检查，形成可审查交付

**文件：**最终业务改动应集中在 `openai_pricing.go`、`helper.go`、`opeai_response.go`、`common/model-ratio.go` 及对应测试。更新本计划执行记录；代码变更实际提交时按项目规范更新 `docs/CHANGELOG.md`。

1. 执行 `gofmt`，只传入本次实际改动的 Go 文件。
2. 按顺序执行必要检查，保存完整输出：

   ```bash
   go test ./common ./relay/controller ./common/shipper -count=1
   go build ./...
   go vet ./...
   go test ./...
   go test -race ./common ./relay/controller -run '^(TestGPT6LongContextMultipliers|TestOpenAIPriceOverrides|TestOpenAIChatTierPricing|TestOpenAIResponseTierPricing|TestOpenAITierPricing)' -count=1
   git diff --check
   ```

3. Race 检查主要验证新增局部价格传递和异步日志路径；不承诺解决既有全局 map 热更新竞争。发现失败时记录原始栈和基线对照，区分本次引入、既有问题和环境限制；新增问题必须修复后重跑相关检查。
4. 审查 diff：getter/导出函数签名不变；旧长上下文 9 项不变；没有 DTO、options、管理接口、迁移、路由和默认价格扩张；无覆盖分支没有提前读取全部价格或改变日志时机。
5. 按设计稿 §7 的 22 项建立第 7 节验收追踪，填入实际测试名、执行结果和证据位置。测试退出 `0` 但显示 `[no tests to run]` 不算验收通过。
6. 交付改动清单、测试结果、基线差异和已知限制。没有提交授权时保留工作区供审查，不自动 commit/push。

**验收：**相关测试、构建和静态检查通过；环境导致未完成的检查明确列出，不以“编译通过”代替扣费闭环。代码和测试没有把第一项档位取价与第二项长上下文扩展混为一项。

### P07：准备配置与环境验收，执行发布回滚

**本阶段分为准备和执行。**P00–P06 的本地开发完成后可以准备可审查的配置增量、发布步骤和回滚材料。真实配置写入、外部付费请求、提交推送和生产部署须在对应操作已获得明确授权后执行；本文不表示已执行或已授权。

1. 只读导出现有相关模型配置作为备份，核对目标实例的实际同步周期、基础计费模型和已有价格键。检查拟用后缀是否与真实模型名冲突；不新增自动冲突检测功能。
2. 逐模型形成“当前值 → 拟增量值”清单。按设计稿换算短上下文价格：`ModelRatio = 输入美元单价/2`，输出/缓存倍率为相对输入价格。不预乘长上下文系数，不写入 `_long` 或 `_default` 价格。
3. 沿用现有 `PUT /api/pricing/model` 或批量接口准备增量请求体；只包含计划修改的字段，不用设计 JSON 整体替换配置，也不要求把价格键加入渠道模型列表。不修改真实前端仓库。
4. `_priority` 与 `_fast` 是否都配置，依据目标渠道实际响应与运营定价决定；两键不会自动互相回退。CacheWriteRatio 无管理入口，应列明基础模型实际回退值；需要不同写入倍率但现有渠道无法配置时，标为本版限制，不声称已可管理。
5. 部署新代码本身即启用 GPT-6+ 默认长上下文规则；已有档位后缀价格也会立即参与新结算。发布前必须清点已有配置，不能把“尚未新增配置”当成纯观察发布。
6. 采用当前部署体系支持的隔离测试环境或小范围流量验证。价格 map 以模型为全局范围，同名模型的共享配置会影响所有使用该配置的实例/渠道；不能承诺通过同名模型价格配置实现渠道级灰度。逐实例核对生效，不能只等待默认 600 秒就宣称全量一致。
7. 在已授权的环境测试普通/流式、priority/fast/flex/ultrafast/default 及请求响应不一致样例；只调用该模型/渠道实际支持的档位。每笔核对原始 usage、响应档位、实际模型口径、额度、日志参数与余额变化。官方不支持的组合标为不适用，不强造响应。
8. 预估仍基于原模型，昂贵档位可能使最终费用高于预估；本版不增加冻结上限。保留既有扣费失败和异步结算限制，上线观察重点为错误日志、扣费与记录差异及异常价格，不新增遥测系统。
9. **价格回滚：**通过现有配置机制恢复备份中本次修改的键；恢复原先不存在状态时移除对应键，不写 `0` 代替删除。保留并发产生的无关配置改动，逐实例核实；删除全部档位项只能回退档位价格，不能关闭 GPT-6+ 长上下文规则。
10. **代码回滚：**需要同时撤销 GPT-6+ 阈值扩展时，恢复已验证的上一发布版本；处理在途请求和异步结算后核对余额/日志。已完成账单不自动重算，不执行数据库结构回滚或批量删除。

**验收：**本地交付与发布状态分别记录；发布后具备目标实例的配置生效证据和实际账单样例。回滚演练明确区分“恢复价格配置”与“恢复旧代码”，不能宣称删除档位键可撤销全部费用变化。

## 6. 可直接用于测试的金额夹具

以下全部为设计稿中的人工价格，不是当前官方价格。设总输入 `P=1000`、缓存读 `R=200`、缓存写 `W=100`、输出 `C=100`；普通输入为 `700`。组合折扣为 `1`，`QuotaPerUnit=500000`，Chat 无有效按次价格。

| 价格/响应 | 输入 M | 输出倍率 | 缓存读倍率 | 缓存写倍率 | Chat quota | Responses quota |
|---|---:|---:|---:|---:|---:|---:|
| 基础模型 | 1 | 3 | 0.1 | 1.25 | 1145 | 1145 |
| priority 全配置 | 2 | 4 | 0.2 | 1.5 | 2580 | 2580 |
| 请求 priority、响应 flex | 0.5 | 2 | 0.1 | 1.25 | 523 | 522 |
| 请求带档位、响应缺失 | 基础 | 基础 | 基础 | 基础 | 1145 | 1145 |
| 响应 default，另有诱饵 `_default` 配置 | 基础 | 基础 | 基础 | 基础 | 1145 | 1145 |
| 非默认档位完全无配置 | 基础 | 基础 | 基础 | 基础 | 1145 | 1145 |
| partial 仅配置输入为 2 | 2 | 3 | 0.1 | 1.25 | 2290 | 2290 |

使用测试专用基础模型临时设置精确长上下文阈值 `999`，保持同样 token：priority / flex / default 的两个入口结果分别为 `4760 / 995 / 2140`。缺失档位与 default 金额相同，日志档位字段不同。

priority 的长上下文复算为：

```text
输入与缓存 = 700×2 + 200×2×0.2 + 100×2×1.5 = 1780
输出       = 100×2×4 = 800
长上下文   = 1780×2 + 800×1.5 = 4760
```

断言使用明确预期数值，不在测试中复制生产公式再与自身比较。增加非整数折扣和 `QuotaPerUnit` 非默认样例时，应分别按两个入口的现有运算/取整方式固定预期。

GPT-6+ 的集成边界另用真实阈值测试：例如 `P=272001、R=1000、W=1000`，普通输入虽低于阈值仍触发；`P=272000、C=1000` 不触发。验证整次用量乘系数，避免仅对超出 `1` 个 token 加价。

## 7. 验收矩阵与设计追踪

| 编号 | 必须覆盖的断言 | 阶段/拟定测试组 | 对应设计稿 §7 |
|---|---|---|---|
| A01 | 缺失/null/空/default/未命中/大小写；忽略 `_default` | P01、P03、P04 | 1、2、13 |
| A02 | 每项独立回退；每项显式零值；自定义档位；请求不参与最终取价 | P01、P03、P04 | 2、3、5 |
| A03 | priority/fast/flex/ultrafast/default 全覆盖；fast/priority 无别名回退 | P01、P05 | 16、18 |
| A04 | Standard/auto 等兼容响应原样查键；priority → default 最后值有效 | P01、P05 | 12 |
| A05 | 三种入口 × 普通/SSE；请求透传；响应档位与 usage 分离 | P05 集成测试 | 4、8 |
| A06 | 固定价格覆盖、回退、0、-1；Responses 忽略按次价 | P03、P04 | 6、21 |
| A07 | 人工金额表、组合折扣、ceil 与截断、QuotaPerUnit 差异 | P03、P04 | 5、7、10 |
| A08 | long 用已选单价；部分回退/default/缺失/无配置仍按基础模型阈值 | P02、P03、P04 | 7、9、10、19 |
| A09 | 旧 9 项不变；GPT-6+ 三个边界；未来版本解析、显式优先、非目标模型 | P02 | 14、15 |
| A10 | 阈值用总输入含缓存、不加输出；超过后全量、各系数只乘一次 | P03、P04 | 11、17 |
| A11 | billingDetails 使用实际参数；usageDetails 原值；模型主体不带价格后缀 | P03、P04、P05 | 11、15、16 |
| A12 | 价格表快照到倍率的数学换算；短价不预乘 long；缓存写回退核对 | P04、P07 | 19 |
| A13 | 无覆盖的额度、预扣差额、统计、日志字段/条件与基线一致 | P00、P05 | 20 |
| A14 | 零 token、nil usage 函数、异常缓存、最低额度、特殊值、映射、流式、重试 | P00、P03–P05 | 21 |
| A15 | 无覆盖读取时机不变；命中分支日志沿用实际计算参数 | P03、P04、P06 审查 | 22 |

对每个编号记录具体测试名、结果和证据；A12 需要区分数学验证与官方现价/环境配置核对，A05 需要区分本地 mock 与真实环境。完整验收不能只执行精确查价单元测试。

## 8. 执行记录（2026-10-11）

实际起点仍为上述 HEAD，工作区已有 P01–P04 主体与部分 P05 测试，但文档未更新。本次保留既有工作并完成缺口核对；命令输出、基线对照及 A01–A15（关联设计 22 项）的实际测试追踪保存在 [docs/evidence/openai-service-tier/2026-10-11/README.md](../evidence/openai-service-tier/2026-10-11/README.md)。全部请求数据均为本地人工夹具，不含真实密钥或用户提示。

| 阶段 | 状态 | 实际改动/证据 | 未完成项 |
|---|---|---|---|
| P00 | 本地通过 | 原始 HEAD 临时副本执行既有测试及无覆盖兼容基线；baseline.log 退出 0 | 无；全量存量失败另记 P06 |
| P01 | 本地通过 | openai_pricing.go；TestOpenAIPriceOverrides | 无 |
| P02 | 本地通过 | GetLongContextMultipliers；TestGPT5Family / TestGPT6LongContextMultipliers | 无 |
| P03 | 本地通过 | Chat 固定价/token、差额扣费、日志、最低额度及长上下文 | 无；真实环境归 P07 |
| P04 | 本地通过 | Responses actualQuota、全额扣费、日志价格传递、公开函数兼容和价格数学换算 | 无；真实环境归 P07 |
| P05 | 本地通过 | 三接口普通/SSE 共 130 个集成用例；兼容、折扣、映射、流错误重试、零用量及 shipper no-op 回归 | 未做真实断连或 SQS 端到端演练 |
| P06 | 专项关卡通过；全量存在限制 | targeted/build/vet/race/diff-check 退出 0；all-tests 退出 1；原始 HEAD 复现相同失败；验收矩阵保留；原始日志与 checks.json 已按用户要求清理 | 存量业务断言失败；外网及本地监听依赖在当前沙箱不能通过 |
| P07 | 配置草稿与回滚说明已准备，未发布 | [增量配置、现场核对与回滚步骤](../evidence/openai-service-tier/2026-10-11/release-preparation.md) | 目标实例/当前配置、官网现价复核、真实验收、授权及发布回滚演练 |

结论：本功能本地专项验收完成，尚未发布。不能把专项通过表述为全仓测试全绿，不能把配置草稿表述为真实配置已生效。未提交、推送、部署或写入生产配置。

接续复核：再次运行计费专项、build、vet，退出码均为 0，保留既有业务实现。本地 `one-api.db` 以只读方式检查后未发现 options/channels 表，不能充当现场配置。官方文档本轮沙箱请求 DNS 失败，经外网读取返回 HTTP 403；没有取得官网价格证据。全量及 race 结果仍引用上一轮证据，本轮未重复执行。真实环境信息尚未提供，P07 现场执行关卡保持未完成。

清理记录（2026-10-11）：按用户要求删除本次测试原始日志、汇总 JSON、人工演练生成物和临时基线/专用缓存；保留上述结果摘要、核心计费 Go 回归测试及发布草稿。历史执行结果不因清理改变，原始输出不再作为现存附件，重跑基线测试须重新准备临时副本。

范围收敛（2026-10-11）：按用户要求移除独立的 Python 配置辅助脚本、对应测试及工具专用说明；保留核心计费功能和 Go 回归测试。价格配置继续使用现有管理接口，发布验收状态不变。

提交前审查（2026-10-12）：相关包测试、专项 race、build、vet 与格式检查通过；全量测试的业务断言失败已在 `0559f72a` 基线复现，DNS/本地监听权限限制保留。发现并复现管理价格接口在部分档位配置下与实际结算不一致的 P2；用户明确决定暂不修复并授权提交现有代码，已同步 CHANGELOG 与执行证据。本次交付包含核心实现、回归测试及已有设计/实施/发布准备文档，业务实现与审查通过版本一致。P07 现场验收和发布仍未执行。
