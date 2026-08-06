# 更新记录 (CHANGELOG)

所有通过 Claude Code 辅助完成的代码变更必须记录在此文件中。

格式要求：每条记录包含日期、分支、变更类型、涉及文件和简要说明。

---

## 2026-08-06

### fix(upstream): 429 / 503 改为模型级判定，不再中止渠道探测
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `controller/channel_upstream_probe.go`, `controller/channel_upstream_probe_test.go`, `controller/channel_upstream_update.go`, `common/config/config.go`, `model/option.go`
- **说明**: 原实现把 429 归为「上游整体不可用」，连续 2 次就中止**整个渠道**的剩余探测；503 则完全没处理。这两个判断都错了 —— 它们是**模型级**信号，渠道本身是好的，不该拖累同渠道其他模型的探测。
- **新增两个 verdict**:
  - `rate_limited`(429) —— 上游能对这个模型做限流，说明它认识该模型并愿意服务，**是模型可用的证据**
  - `unavailable`(503) —— 该模型当前无可用后端，模型级信号，渠道正常
- **处置**：
  - `429` 两个方向都「不动」。pendingRemove 不删是因为模型可用，删了是误伤；pendingAdd 不加是因为 —— **很多网关的限流检查发生在模型路由之前，对不存在的模型也会返回 429**，直接当 alive 会把不存在的模型加进列表，不加的代价只是延迟一轮。
  - `503` **pendingRemove 准予删除**，pendingAdd 不加。准删的依据是双重信号：能进 pendingRemove 就意味着上游 `/v1/models` 已不返回它，503 是第二个独立证据，两者都指向「这个模型没了」。留着反而更糟 —— 请求会路由到它然后失败；删掉后路由能找到其他仍提供该模型的渠道。**「503 只是临时抖动会导致误删」的担心不成立**：临时抖动时 models 接口通常仍列着该模型，根本不会进 pendingRemove，探针也就不会被触发。批量误删仍有比例保护（>50% 且本地 ≥5 个模型）兜底。
- **为什么 unavailable 不并入 not_found（处置相同）/ rate_limited 不并入 inconclusive（处置相同）**: 运维含义完全不同 —— 「模型下架了」和「模型还在但后端全挂了」需要的动作不一样，后者通常意味着要联系上游。上线看日志时「限流 45 个 + 无结论 5 个」和「无结论 50 个」是两回事，前者说明该渠道正被限流、探测结果基本有效，后者说明探针真的没拿到信息。轮末统计日志已加上这两个计数。
- **彻底移除「渠道级中止」机制**: 原实现对连续 429 中止，中途改为对 401/403/402 中止，最终**全部删掉**。确立的原则是：**探针只回答「这个模型怎么样」，不从单个模型的失败去推断「整个渠道都完了」**。逐条否掉中止理由 ——
  - `403` 经常是**模型级权限**（OpenAI 部分模型需组织验证、Azure deployment 权限、中转站按套餐分组），中止会误伤同渠道其他完全正常的模型
  - `402` 语义各家网关不统一，有些平台按模型分配额度
  - `401` 虽是 key 级，但探针只用了一个 key（`GetNextAvailableKey`），**多 key 渠道下不能代表其他 key** —— 这一点在 `classifyProbeError` 里已为 `not_found` 做了降级，中止逻辑却漏了
  - 收益也比预估的小：这几类都是**立即返回**，只消耗次数配额、不占用时长预算
  
  资源控制全部交给预算：单渠道次数（30）、单渠道时长（120s）、全局每轮次数（200）。代价是 key 整体失效的渠道会吃掉 30 次全局配额，可接受。`probeBudget` 因此简化掉 `aborted` / `abortReason` 两个字段和整个 `noteResult` 方法。
- **原实现的两个缺陷随之消失**: (1) 超时路径 `StatusCode=0` 会让 `note429(false)` **重置连续计数**，而超时恰恰是限流最常见的表现形式，导致 `429 → 超时 → 429` 永远触发不了中止；(2)「连续」定义过严，`429 → 成功 → 429` 这种限流边缘状态永远累计不到阈值。既然中止机制整个删掉，这两个缺陷不复存在。
- **移除配置项** `UpstreamModelProbeConsecutive429`（连续 429 中止阈值已无意义）。该功能尚未上线，options 表中不存在此 key，无迁移成本。
- **顺带修正一处测试断言**: `TestRelayErrorHandlerFallbackMessagesNeverCauseNotFound` 原本断言 `== inconclusive`，但它的真实意图是「不得判为 not_found」。断言写窄会在新增 verdict 时误报，已改为 `!= verdictNotFound`。
- **验证**: `controller` + `model` 两包 **216 个用例全绿、0 失败**。新增 429/503 各 2 组判定用例；中止相关用例改为锁死「任何状态码都不中止渠道」这条不变式（覆盖 401/403/402/429/503/500/502/504/404/超时共 10 个码）+「预算耗尽是唯一停止条件」。**实测有效性**：临时去掉 429 判定分支 → 相关用例立刻 FAIL。`go build ./...`、`go vet ./...`（退出码 0）通过。

### fix(stats): Dashboard 与曲线图统一按可计费日志类型过滤
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `model/log.go`
- **说明**: `GetAllGraph`（`:451`）、`GetUserGraph`（`:500`）、`getDashboardMetrics` 的三条查询（`:603/616/626`）此前对 logs 表**不做任何 type 过滤**，`COUNT(*)` / `SUM(quota)` / `SUM(prompt_tokens+completion_tokens)` 会把非用户流量的日志行一并算进去。新增 `applyBillableLogTypes` helper（`type IN (LogTypeConsume, LogTypeError)`），口径与 `model_metrics.go:200` 的 `AggregateLogsForHour` 对齐 —— 此前同一个仓库里两套统计口径并存。
- **发现方式**: 代码审计。这是我在 commit `8957594` 引入探针日志时**声称已验证、实际漏掉**的问题。当时只查了 `SumUsedQuota`（它确实按 `LogTypeConsume` 过滤）就下了「不会污染统计」的结论，没有穷举所有聚合路径。教训：验证「新数据不污染统计」时必须枚举**所有**读取路径，单点验证不足以支撑全称结论。
- **为什么探针触发了这个既有缺陷**: 既有的 `LogTypeSystem` 行只有注册赠送日志，经 `RecordLog` 写入且**不设 `Quota`/`PromptTokens`/`CompletionTokens`**（`log.go:62-77`），金额全为 0，只贡献 `count`。探针日志是**第一批带非零金额的 system 行**，会同时污染请求数、token 数、quota 消耗和 Top-5 模型榜。所以缺陷是既有的，探针是把它暴露出来的触发条件。
- **顺带修正的既有偏差**: 注册赠送日志此前会被计入 Dashboard 的请求数与曲线图的 count（虽不贡献金额）。修复后这部分也被正确排除 —— 统计口径更准，但**已部署实例升级后 Dashboard 的历史请求数会略微下降**，属预期内。
- **影响面**: 修复前，探针开启时污染管理员 Dashboard 的 RPM/TPM/QuotaPM/今日请求数/今日消耗/Top-5 模型榜。营收统计（`SumUsedQuota`/`SumUsedToken`）与模型性能指标（`AggregateLogsForHour`）本就有正确过滤，未受影响。
- **验证**: `go build ./...`、`go vet ./...`（退出码 0）、`go test ./model/ ./controller/` 全绿。

### fix(upstream): 探针改为套用 model_mapping，与真实请求路径对齐
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `controller/channel_upstream_probe.go`, `docs/plans/2026-08-05-上游模型同步真实请求探针.md`
- **说明**: 探针原本刻意跳过 `util.GetMappedModelName`，用上游原名请求。这是设计上的错误 —— 它只回答了「上游有没有这个名字」，而探针真正要回答的是「**这个模型加进本地列表后，用户请求它能不能成功**」。用户请求走的就是映射后的名字，探针不走同一条链路，就可能出现「探针报 alive、用户实际调用失败」，比不探更糟。另一层理由：管理员显式配置 `model_mapping` 已经表达了「该模型要用映射名调上游」，这个显式意图应优先于 `/v1/models` 的自动发现结果。现与 `testChannel`（`channel-test.go:325-327`）保持一致。
- **不会双重映射**: `GetRelayMeta`（`relay_meta.go:164-170`）内部也会应用映射，但它基于 `meta.OriginModelName`，而探针给 `SetupContextForSelectedChannel` 传的是空字符串，那里是空转。已确认。
- **日志新增映射信息**: `probeResult` 加 `MappedModel` 字段，仅在与原名不同时写入日志 `映射后=xxx`。否则排查时会困惑「我探的是 gpt-4，为什么错误里说 gpt-4-turbo 不存在」。
- **计费口径不变**: `calcProbeQuota` 仍用原名而非映射名，与 `recordChannelTestConsumeLog`（`channel-test.go:361` 传的也是原名）的既有惯例一致 —— 按请求的模型名计费，而非上游实际用的名字。
- **验证**: `go build ./...`、`go vet ./...`（退出码 0）、`go test ./controller/ ./model/` 全绿。映射逻辑复用 `util.GetMappedModelName`（对不在映射表中的模型是恒等变换），未新增单测。

### fix(model): AddAbilities 对 models / groups 去重，从源头消除主键冲突
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `model/ability.go`, `model/ability_test.go`
- **说明**: 新增 `normalizeAbilityKeys`（trim + 丢空项 + 保序去重），`addAbilitiesTx` 改为先规范化再做笛卡尔积。此前 `AddAbilities` 直接 `strings.Split` 后逐项 trim，重复模型名会构造出主键 `(group, model, channel_id)` 相同的记录，导致整批插入失败。管理员从 UI 手工编辑模型列表时没有任何去重保护，一次手滑就会让该渠道的 abilities 无法重建。
- **与上一条的关系是互补而非重复**: 事务保证「失败时不损坏已有数据」，去重保证「不失败」。两者都要 —— 事务是安全网，去重是治本。
- **行为变更**: 重复输入从「报主键冲突错误」变为「静默去重后正常工作」。这与上游同步路径（`upstreamNormalizeModelNames` 早就在去重）的行为一致，也比报错对管理员更友好。副作用是 UI 上填重复模型名不再有任何提示，保存后列表里仍是原样字符串、只有 abilities 被去重。
- **顺带简化**: 内层循环不再需要 trim 和空值判断，规范化统一在入口完成。
- **测试改造**: 去重后「重复模型名」不再是有效的插入失败源，上一条的事务回归测试会失效。改用 SQLite 触发器（`RAISE(ABORT)`）制造插入失败 —— 这样回归测试只验证「INSERT 失败就回滚」这一个属性，不再耦合任何具体失败原因。注意 SQLite 触发器体内不允许绑定变量（`trigger cannot use variables`），只能拼字符串。
- **验证**: 新增 `TestAddAbilitiesDeduplicates`（7 组：模型重复/带空格重复/分组重复/两者都重复/混入空项/无重复不受影响）与 `TestNormalizeAbilityKeys`（7 组含保序、空串、全分隔符）。改造后重新验证事务回归测试仍然有效：临时去掉事务 → 立刻 FAIL 并报 `失败前 3 条 ability，失败后 0 条`。`model` 包 **24 个用例全绿**，`go build ./...`、`go vet ./...`（退出码 0）通过。

### fix(model): UpdateAbilities 的先删后插改为事务，防止渠道模型被静默清空
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `model/ability.go`, `model/ability_test.go`(新增)
- **说明**: `UpdateAbilities` 一直是「先 `DeleteAbilities` 全删、再 `AddAbilities` 全建」且**没有事务包裹**。INSERT 失败时 DELETE 已经提交，该渠道的 abilities 变成 **0 条 —— 等价于所有模型不可路由**，且调用方只看到一个主键冲突错误，完全联想不到"模型已经全没了"。抽出 `addAbilitiesTx` / `deleteAbilitiesTx` 接受 `*gorm.DB`，`UpdateAbilities` 用 `DB.Transaction` 包裹；`AddAbilities` / `DeleteAbilities` 的公开签名保持不变（`model/channel.go:494/558/649` 三处独立调用不受影响）。已确认 `channel.Update()`（`channel.go:619`）用的是全局 `DB` 而非事务句柄，不会产生嵌套事务。
- **影响面比看起来大**: `UpdateAbilities` 在**任何** `channel.Update()` 时都会执行 —— 管理员在 UI 编辑渠道（哪怕只改个备注）、批量导入 Key、上游同步 apply，全都走这条路。
- **触发条件在生产中很可能已经发生过**: `AddAbilities` 对 `channel.Models` **不做去重**，重复模型名会构造出主键 `(group, model, channel_id)` 相同的记录导致插入失败。而管理员从 UI 手工编辑模型列表时没有任何去重保护。表现为「编辑渠道后该渠道所有模型突然不可用」，排查时只会看到一条主键冲突报错。
- **验证**: 新增 `model` 包的**第一个测试文件**（此前该包零测试），用 SQLite 内存库。核心用例 `TestUpdateAbilitiesRollsBackOnInsertFailure` 用重复模型名触发真实的插入失败。**实测有效性**：临时去掉事务后该测试立刻 FAIL，且报出 `失败前 3 条 ability，失败后 0 条` —— 直接复现了"所有模型不可路由"。另有跨渠道隔离、Enabled 跟随渠道状态、清空模型列表三组用例。`go build ./...`、`go vet ./...`（退出码 0）、`go test ./model/ ./controller/` 全绿。
- **未做（待确认）**: ~~`AddAbilities` 仍不对 models/groups 去重~~ → 已在下一条补上。

### chore(upstream): 探针单次超时默认值 20s → 10s
- **分支**: `upstream-model-probe`
- **类型**: 优化
- **涉及文件**: `common/config/config.go`
- **说明**: 探针发的是 `max_tokens=16` 的最小请求，正常 1-3s 返回；超过 10s 基本是模型有问题或上游过载，继续等没有信息量。20s 超时会让单个卡住的模型白白吃掉 1/6 的单渠道时长预算（默认 120s）。按实测口径：平均响应 ≤4s 时瓶颈是 `MaxPerChannel=30` 而非时长预算；调低超时主要是压缩异常样本的尾部开销。该项可后台热改，无需发版。
- **关联计划**: `docs/plans/2026-08-05-上游模型同步真实请求探针.md`

---

## 2026-08-05

### feat(upstream): 上游模型同步接入真实请求探针
- **分支**: `upstream-model-probe`
- **类型**: 新功能
- **涉及文件**: `controller/channel_upstream_probe.go`, `controller/channel_upstream_probe_test.go`, `controller/channel_upstream_update.go`, `model/log.go`, `model/option.go`, `common/config/config.go`, `common/config/channel_other_settings.go`
- **说明**: 给巡检的 diff 结果加一道真实请求验证门 —— pendingAdd 探测通过才加入、pendingRemove 探测确认「上游明确说不存在」才删除。`UpstreamModelProbeEnabled` 默认关闭。探测量等于 diff 大小，平时接近 0，只在上游列表变动时产生调用。**手动 apply 路径不走探针**，管理员保留最终决定权（否则渠道持续限流时探测全是 `inconclusive`，管理员会被锁死）。只在「该方向真的会被自动应用」时才探 —— 结果只进人工队列的话探测是白花钱。
- **🔴 实现中发现并修掉的一个致命缺陷**: 原设计打算用 `util.RelayErrorHandler` 拿上游错误。读代码发现它在 body 解析失败时会**编造兜底文案**（`relay/util/common.go:182-202`），其中 404 那条是 `"资源未找到 (404): 请求的端点或模型不存在"` —— **含「模型不存在」四字，正好命中关键词白名单**，且同时命中「404 + Message 非空」信号，属双重命中。真实后果：base_url 配错或上游反代挂掉 → 所有模型返回 404 + 该文案 → 全部判 `not_found` → 一轮删光整个渠道。实测 8 条兜底文案里只有 404 那条会命中，而 404 恰恰是配置出错时最典型的返回。**修法有两层**：(1) 探针改为自己解析上游 body（`parseProbeUpstreamError`），不经过会编造文案的 `RelayErrorHandler`；(2) 恢复 `bodyParsed` 参数作为 `not_found` 的硬前置条件，拿不到上游原话时一律 `inconclusive`。
- **教训（值得记住）**: 这个缺陷我的单测原本抓不到 —— 因为测试里手动构造了 `apiErr = nil`，而真实调用路径下 `RelayErrorHandler` 永远返回非 nil 且 Message 永远非空。**纯函数的测试用例必须来自真实调用路径的可能输入，而不是想象的输入。** 现已加 `TestRelayErrorHandlerFallbackMessagesNeverCauseNotFound` 把全部 8 条兜底文案钉死，并做了反向断言（若 404 文案不再命中白名单则测试主动失败并提示重新评估门禁必要性）。实测有效性：临时去掉 `bodyParsed` 门禁后该测试立刻 FAIL。
- **超时必须外层自己包**: `relay/channel/common.go:36-38` 明确「不绑定客户端 context」，超时只由全局 `HTTPClient.Timeout` 控制（默认 **5 分钟**）。串行探 30 个模型最坏 2.5 小时，而巡检本身是串行遍历所有渠道的。解法是 `goroutine + buffered chan + select`。`done` 必须带 buffer，否则超时返回后发送方永久阻塞、goroutine 泄漏；带 buffer 时泄漏的 goroutine 会在 HTTPClient 超时后自行退出，同时存在上限等于探测预算。
- **日志复用 logs 表，零前端改动**: 新增 `model.RecordModelProbeLog`（`Type=LogTypeSystem`、`TokenName=model-probe`）。**不能复用** `RecordConsumeLogWithOtherAndRequestID` —— 它在 `LogConsumeEnabled` 早退之前无条件调用 `metrics.ObserveConsume`（该埋点位置是 P1 时有意为之），复用会让探针流量污染 `oneapi_llm_*` 指标；且它硬编码 `LogTypeConsume`。quota 仅记录不扣费；`SumUsedQuota`/`SumUsedToken` 都按 `LogTypeConsume` 过滤，`LogTypeSystem` 不进营收统计。前端日志页筛「类型=系统」+「令牌名称=model-probe」即可按渠道/模型检索。**⚠️ 此处的验证不完整 —— 只查了 `SumUsedQuota` 就下了结论，遗漏了 `GetAllGraph` / `GetUserGraph` / `getDashboardMetrics` 三条不过滤 type 的聚合路径。已由 2026-08-06 的审计发现并修复，见下方条目。**
- **不能复用 `testChannel`**: `channel-test.go:294` 有 `strings.Contains(channel.Models, specifiedModel)` 白名单检查，而 pendingAdd 的模型压根不在 `channel.Models` 里，必然返回 `not supported by this channel`。故新写 `doProbeChannelModel`，`channel-test.go` **零改动**（它挂着管理员测活和自动启停渠道两条命脉）。另刻意不套 `util.GetMappedModelName`：pendingAdd 是上游真名（映射会反向搞错），pendingRemove 已排除 redirect source（映射对它是恒等变换），两者用原名都正确。
- **settings 回填以原始 pending 为基准**: 被探针暂缓的模型必须留在 `LastDetectedModels`/`LastRemovedModels` 里，管理员才能在 UI 上看到并手动决策。`approved == pending` 时等价于原来的清空行为。
- **成本控制**: 每渠道 30 次 + 全局每轮 200 次 + 单渠道 120s 时长预算 + 单次 20s 超时 + 连续 2 次 429 中止本渠道（限流下结果全是 `inconclusive`，继续探纯烧钱）。刻意保持串行不引入渠道内并发 —— 同一个 key 并发打上游更容易触发 429，而 429 恰恰是中止条件。全局预算用包级 `atomic.Int64`，安全前提是 `upstreamUpdateTaskRunning` 的 CAS 已保证同一时刻只有一轮巡检；因此 `checkAndPersistUpstreamChanges` **签名完全不变**，三个 HTTP handler 调用点无需改动。
- **配置解析**: 新增 `setPositiveIntOption` helper。超时/预算/上限这类项取 0 的语义是"立即失败/永不执行"，沿用 `config.X, _ = strconv.Atoi(value)` 会让一次脏数据静默瘫痪功能。每渠道逃生舱 `UpstreamModelProbeDisabled` 用**负极性 + omitempty**，现有渠道的 settings JSON 一个字节都不变，零迁移风险。
- **未做（有意）**: 僵尸模型（上游列表虚报、两边都有 → diff 为空 → 探针不触发）探不到；`not_found` 的 pendingAdd 每轮会重探（上游一直虚报就一直有成本，可选缓解是进程内 `sync.Map` 缓存 + TTH，待观察真实成本后再定）；Codex 模型判 `skipped`（复用 `testChannelViaResponses` 会连带写 `LogTypeConsume` 污染渠道测试记录）。
- **验证**: `controller` 包累计 **183 个用例全绿、0 失败**。新增 `parseProbeUpstreamError` 的 20 个用例（HTML/纯文本/截断 JSON/空对象一律 false，标准 OpenAI 与顶层平铺形态一律 true）、`probeBudget` 的 6 个用例（单渠道上限、全局跨渠道共享、时间预算、连续 429、非 429 重置计数、中止后一律失败）、`truncateProbeMessage` 的多字节安全、`upstreamProbeEnabledFor` 与 `probeUnsupportedReason` 的矩阵。`go build ./...` 与 `go vet ./...`（退出码 0）通过。
- **尚未验证（需真实上游）**: 各 adaptor 的 alive 判定准确性、探测 quota 数值、429 中止的实际触发、单轮巡检总耗时、`oneapi_llm_*` 指标未被污染。上线须按计划文档的灰度步骤逐项核对。
- **关联计划**: `docs/plans/2026-08-05-上游模型同步真实请求探针.md`

### feat(upstream): 模型探针的结论分类器（纯函数层，尚未接入）
- **分支**: `upstream-model-probe`
- **类型**: 新功能
- **涉及文件**: `controller/channel_upstream_probe.go`(新增), `controller/channel_upstream_probe_test.go`(新增)
- **说明**: 上游模型探针的第一层 —— 四态结论（`alive` / `not_found` / `inconclusive` / `skipped`）与判定规则，全部为纯函数、零 IO、无运行时副作用，本 commit 尚无调用方。核心设计是**只有上游明确说「这个模型不存在」才是 `not_found`，其余一切失败都是 `inconclusive`**：把 `inconclusive` 误判成 `not_found` 的后果是批量误删模型，比漏删严重得多。`not_found` 需命中三个明确信号之一：404 且带非空错误消息、`error.code`/`error.type` 命中封闭枚举、错误消息命中关键词白名单。
- **两条最容易翻车的边界（已用单测固化）**: (1) `"You do not have access to this model"` —— 无权限不等于不存在；(2) `"This model's maximum context length is 4096"` —— 含 `model` 字样但模型确实存在。用关键词白名单而非"400 就算不存在"正是为了挡住这两类。反过来，OpenAI 的 `"The model 'x' does not exist or you do not have access to it"` 会命中 `does not exist` 判为 `not_found` —— 对单 key 渠道这是**正确**的（这个 key 服务不了它），多 key 渠道则降级为 `inconclusive`。
- **数字类 code 自动排除**: `error.code` 在 JSON 里可能是 string / number / null。`normalizeErrCode` 归一化后 `float64(404)` 变成 `"404"`，不在封闭枚举里，等于自动被排除 —— 数字 code 不构成「模型不存在」的明确信号。
- **对计划的一处偏离**: `classifyProbeError` 去掉了原计划的 `bodyParsed` 参数，改用 `apiErr == nil` 表达"没解析出上游错误体"。一个参数能表达的事不用两个，否则调用方可能传出 `apiErr != nil && bodyParsed == false` 这种自相矛盾的组合。
- **`skipped` 的语义是「探针无能力」而非「探测失败」**: 因此它在两个场景下都批准执行（信任上游）。这保证对 embedding/tts/视频类模型完全不改变现有行为，上线不引入回归。而 `inconclusive` 在两个场景下都暂缓 —— 保持现状最安全，且下轮 diff 从零重算会自然重试。
- **验证**: 新增 `channel_upstream_probe_test.go`，含 `classifyProbeError` 的 28 个用例、`isModelNotFoundMessage` 的命中/不命中各 10+ 项、`normalizeErrCode` 的类型矩阵、`filterByProbeVerdicts` 处置表的全部 8 格 + 缺失键/未知 scene/空输入/顺序保持。`controller` 包累计 127 个用例全绿、0 失败。`go build ./...` 与 `go vet ./...`（退出码 0）通过。
- **关联计划**: `docs/plans/2026-08-05-上游模型同步真实请求探针.md`

### feat(upstream): 自动删除模型加比例保护，防上游返回不相干列表导致一轮删光
- **分支**: `upstream-model-probe`
- **类型**: 新功能
- **涉及文件**: `controller/channel_upstream_update.go`, `controller/channel_upstream_update_test.go`, `common/config/config.go`, `model/option.go`
- **说明**: 巡检自动删除此前的防误删保护只有两道 —— "上游返回空列表则拒绝整轮"和"ModelMapping 的 redirect source 豁免"。前者只挡得住 `len(upstream)==0`，**挡不住上游返回一个无关模型**（换了 API 版本、路径语义变化、返回空壳列表如 `{"data":[{"id":"default"}]}`）。这种情况下 `len(upstream)=1` 通过检查，本地全部模型被判 pendingRemove，一轮删光，进而触发 `allModelsRemoved` → `AutoDisableChannelById` → **整个渠道被自动禁用**。新增 `shouldBlockBulkRemove` 纯函数 + 两个可后台热改的配置项：`UpstreamRemoveGuardPercent`(默认 50) 与 `UpstreamRemoveGuardMinLocalModels`(默认 5)。触发时不删，转 `LastRemovedModels` 供人工审核并打 `upstreamError`。
- **MinLocalModels 是必需的，不是可选项**: 本地只有 1-3 个模型的渠道，任何删除都 ≥50%。若无下限，比例保护会把现有的"模型全删 → 自动禁用渠道 → 上游恢复后自动重启用"整条链路**静默关掉**（功能看起来还在，实际永不触发）。测试里为此专门写了 4 个回归用例。
- **配置解析上的一个刻意偏离**: 这两项没沿用仓库里 `config.X, _ = strconv.Atoi(value)` 的惯例。那个写法在解析失败时静默得 0，而这里 0 的语义是"关闭保护"/"对所有渠道启用" —— 一次脏数据就会让保护消失或误伤小渠道。改为解析失败或负数时保持默认值。
- **代价**: 比例保护是"宁可漏删不可误删"的取舍。上游真的大批量下线模型时（比如一次下线 60%），自动删除会被拦下、需要人工在 UI 确认。这是有意的 —— 大批量删除本就该有人看一眼。
- **说明**: `shouldBlockBulkRemove` 放在 `channel_upstream_update.go` 而非计划里写的 probe 文件 —— 它完全独立于探针（探针关闭时也生效），代码位置也应独立。
- **验证**: 15 个表驱动用例，覆盖阈值严格大于语义、约束下限的 4 个回归用例、percent=0/负数关闭、local=0/remove=0 边界。`go build ./...`、`go vet ./...`（退出码 0）、`go test ./controller/` 全绿。
- **关联计划**: `docs/plans/2026-08-05-上游模型同步真实请求探针.md`

### fix(upstream): 忽略列表（IgnoredModels）现在同时拦截自动删除
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `controller/channel_upstream_update.go`, `controller/channel_upstream_update_test.go`(新增)
- **说明**: `upstreamCollectPendingChangesFromModels` 里 pendingAdd 有 `isIgnored` 过滤，pendingRemove 没有 —— 导致 `IgnoredModels` 只拦新增、不拦删除。翻车场景很实际：上游 `/v1/models` 不暴露但实际可调用的模型（相当常见），管理员手动加进渠道并写入忽略列表以为受保护，结果每轮 diff 它都进 pendingRemove，开了 AutoDelete 就被删掉。从 `ignored` 的字面语义看应该是"巡检别碰这个模型"，而非"别新增但可以删"。现给 pendingRemove 补上同一个 `isIgnored` 闭包。
- **行为变更（必须知道）**: `regex:` 规则从此同时具备"永不自动删除"语义。用宽规则（如 `regex:.*`）的用户会感知 —— 这些模型将不再被自动同步删除。这是本次有意选择的方向：忽略列表本就是"保护手工维护模型"的天然机制。
- **顺带**: 这是 `controller/` 包的**第一个测试文件**。此前该包零测试覆盖，`upstreamCollectPendingChangesFromModels` 这个纯函数一直没有任何保护。新增 18 个表驱动用例，覆盖基础 diff、去重/trim、ModelMapping 的 redirect target/source 语义、忽略列表对两个方向的作用、非法 regex 不 panic（`:241` 的 err 被吞掉）。
- **验证**: 按 TDD 先写测试 —— 改动前 3 个 pendingRemove 忽略用例 FAIL、其余 15 个 PASS，改动后全绿。`go build ./...` 与 `go vet ./...` 通过。
- **关联计划**: `docs/plans/2026-08-05-上游模型同步真实请求探针.md`

### fix(vet): 修复 11 处非常量格式串导致的二次格式化
- **分支**: `upstream-model-probe`
- **类型**: 修复
- **涉及文件**: `controller/relay.go`(9 处), `relay/controller/image.go`(2 处)
- **说明**: `go vet ./...` 在 main 分支即失败 11 处，意味着 CLAUDE.md 要求的"提交前必跑 `go vet`"实际从未通过；且 `go test` 默认执行 vet 子集（含 printf 检查），导致 `controller` 包根本无法运行测试 —— 这也是该包长期零测试覆盖的一个隐性原因。两类问题都是真实缺陷：(1) `controller/relay.go` 的 `retryLog` 已是 `formatRetryLog` 的产物，再传给 `logger.Infof` 会走 `fmt.Sprintf(retryLog)` 二次格式化，而 `retryLog` 内含 `retryReason`（上游错误原因），一旦含 `%` 就输出 `%!q(MISSING)` 类垃圾 → 改为 `logger.Info`；(2) `relay/controller/image.go` 的 `fmt.Errorf(errorMsg)`，其 `errorMsg` 拼了 `fluxError.Detail[0].Msg`，**直接来自上游 API 响应体且该错误会返回给最终用户** → 改为 `fmt.Errorf("%s", errorMsg)`，刻意不新增 `errors` import 以缩小改动面。
- **发现方式**: 为上游探针功能给 `controller` 包写第一个测试时被 `go test` 挡住而暴露。
- **验证**: `go build ./...` 通过；`go vet ./...` 退出码 0、零输出。
- **注意**: `go test ./...` 全量仍有一处失败 —— `common/image/image_test.go` 的 `TestDecode` 从 Wikipedia `http.Get` 下载图片，下载失败后 `img` 为 nil 直接 panic。这是预先存在的外网依赖测试，与本次改动无关，未处理。

---

## 2026-07-31

### refactor(observability): 指标链路改为推送式，告警规则移交 monitor 仓库
- **分支**: `prometheus-p0`
- **类型**: 重构
- **涉及文件**: `deploy/prometheus/run-local.sh`, `deploy/prometheus/docker-compose.agent.yml`(原 `docker-compose.monitoring.yml` 改名), `deploy/prometheus/README.md`, `.github/workflows/guard-metrics-rules.yml`(新增)；**删除** `deploy/prometheus/{alerts,rules,alerts_test,prometheus}.yml`
- **说明**: 把 Prometheus 接入 `~/code/monitor`（监控服务器侧的 Loki+Grafana+Nginx 栈）时确定的分工落地。核心决定：**指标改用推送式**（业务服务器跑 Prometheus agent 抓本机后 `remote_write` 到 nginx 网关 → 监控服务器的 Prometheus），与现有 Loki 日志链路同构，业务服务器只需出站连接、不必开入站端口改安全组。**告警与 recording 规则按部署位置划归 monitor 仓库**（它们在接收端评估）—— 本仓库物理删除副本，`run-local.sh` 改为从 monitor 发布的镜像 `docker cp` 取规则，物理上无法漂移；新增 `guard-metrics-rules.yml` 把这条约定变成 CI 门禁（检测规则文件重现、检测从本地 cp 规则，正则排除 `docker cp`）。`docker-compose.monitoring.yml` 改名为 `docker-compose.agent.yml` 并把 prometheus service 改成 **agent 模式**（`--agent` + `--storage.agent.path`，去掉 retention flag —— 已实测 agent 模式带 `--storage.tsdb.retention.time` 会启动失败）。
- **代价（必须知道）**: 本仓库失去"改指标代码时同一个 PR 里改告警规则"的能力。加一个 `oneapi_llm_*` 指标要开两个 PR、两次发布，且**有顺序依赖：先发指标，后发规则**（反了会让告警引用不存在的指标）。
- **monitor 侧同期改动**: 新增 `prometheus/` 目录（第 5 个镜像）+ nginx `/api/v1/write` 入口（独立 `PROM_BEARER_TOKEN` 与限流 zone）+ Grafana Prometheus 数据源与两个 dashboard + `validate-rules.yml`（PR 阶段跑 promtool）。规则迁移时做了 5 处 remote_write 适配：severity 归一到 `critical/high/medium/low`、补 `category`、新增 `MetricsAgentGone`、`container` label 改 `name`、全局聚合加 `site` 维度。
- **验证**: `go build ./...` 与 `go test -race`（metrics + middleware）通过；守卫断言双向验证（正常通过 + 插入违规能拦住）。monitor 侧 `make prom-check` 全绿（34 告警 + 15 recording rule + promtool 单测 SUCCESS），nginx 路由实测 401/415/400/403 四态正确且 Loki 链路无回归，Grafana provisioning 零错误加载。
- **关联计划**: `docs/plans/2026-07-31-prometheus-p1-业务与主机指标.md`

### feat(observability): AI 业务指标 + 主机指标（Prometheus P1）
- **分支**: `prometheus-p0`
- **类型**: 新功能
- **涉及文件**: `common/metrics/{llm,channel,classify,cardinality}.go`(新增), `common/metrics/metrics_test.go`(新增), `middleware/metrics.go`(新增), `middleware/metrics_test.go`(新增), `common/metrics/registry.go`, `common/config/config.go`, `main.go`, `model/log.go`, `middleware/distributor.go`, `controller/relay.go`, `controller/retry_log.go`, `router/relay-router.go`, `deploy/prometheus/{rules.yml,alerts.yml,prometheus.yml,docker-compose.monitoring.yml,alerts_test.yml,README.md}`
- **说明**: 覆盖用户提出的 12 项监控需求。其中 7 项主机指标（CPU/内存/磁盘/IO/网络/Load/文件系统）由 node_exporter + cAdvisor 提供，**one-api 零代码**；TPM/RPM 不建独立指标，用 `rate(counter[5m])*60` 派生。业务侧 8 处埋点全部"加一行"，`relay/controller/text.go` 零改动、12 个重试循环一行不动。三个关键设计决定：(1) **Group A（模型维度）与 Group B（渠道维度）严格不交叉** —— `model`(388) × `channel_id`(百级) × 直方图 14 序列 ≈ 110 万条会打爆 Prometheus，`model_metrics` 表能承受三元组是因为有小时聚合和唯一索引，不能照搬 DB 的维度设计；(2) **两种错误率的指标名前缀彻底分开并用 recording rule 固化公式** —— `oneapi_llm_*` 是用户请求级（SLO，重试不计）、`oneapi_channel_*` 是渠道调用级（含重试），一次请求重试 3 次全失败前者记 1 次后者记 3 次，规则是任何除法的分子分母不得跨前缀；(3) **埋点在 `LogConsumeEnabled` 早退之前** —— 那是可后台动态关闭的开关，运维关掉它的场景（DB 压力大）恰好最需要监控，放在后面则 tokens 曲线掉零与"真的没流量"完全无法区分。渠道失败埋在 `processChannelRelayError` **函数体内**而非调用点：该文件有 12 个独立重试循环、30 个调用点，在调用点埋只能覆盖 1/12。处理了 **SSE 流式 200 陷阱**（响应头写出后中途失败状态码仍是 200，只看状态码会系统性低估错误率），解法是 `recordFinalErrorLog` 写 context 标记、中间件优先读它 —— 副产品是 Prometheus 失败计数与 `logs` 表 `LogTypeError` 行数由构造保证一致。`Error.Type` 不能当 label（`relay/util/common.go` 会用上游 JSON 整体覆盖它，基数无界），改为以状态码为主键的 9 值封闭枚举并有单测守住封闭性。延迟桶前 7 个与 `model.LatencyBoundaries` 逐值对齐（有单测），后 4 个扩到 600s（生产 `STREAMING_TIMEOUT=600`，DB 侧上界 30s 使 40s 与 25 分钟的请求同桶，P95 失真）。新增 31 条告警 + 14 条 recording rule。序列数实测 ~1780（含 P0 的 108）。
- **验证**: `go build ./...`、改动范围 `go vet`、`go test -race`（metrics + middleware）全部通过；`promtool check rules` 31+14 条合法；`promtool test rules` SUCCESS（专门为 `LLMTrafficDroppedToZero` 与 `ChannelDegraded` 流量下限写了单测，前者是最容易写成永不触发的规则）。运行时实测：503 无渠道路径三个指标齐全；基数守卫在上限 50 + 并发 200 个随机模型名下 distinct label 精确停在 51（50+`__other__`）、`label_overflow_total`=302；关掉两个开关后业务指标序列数归零而 P0 的 15 条完好。
- **单测抓到并修复的两个真实缺陷**: (1) 基数守卫原用"先 Load 检查再 Add"，两步间有窗口，50 goroutine 并发下上限 100 冲到 113 —— 改为用 `LoadOrStore` + `Add` 返回值做原子闸门（超限回退），硬上限成立；(2) 503 请求原本完全不进 `llm_requests_total`（distributor 在 `c.Set("model")` 之前 abort 且不走 `recordFinalErrorLog`），后果是"某模型丢光全部渠道"在 SLO 上显示零影响 —— 已补 `CtxModelKey` 兜底键。
- **未验证项**: token 精确性、独立于 `LogConsumeEnabled`、两种错误率的 1:3:1 不变式、长请求落桶、与 DB 对账、主机/容器指标 —— 需 mock 上游或 Linux 环境（本地渠道指向真实外部 API，不能压测）。
- **关联计划**: `docs/plans/2026-07-31-prometheus-p1-业务与主机指标.md`

### feat(observability): 引入 Prometheus 指标导出与 pprof（P0：进程与依赖健康度）
- **分支**: `prometheus-p0`
- **类型**: 新功能
- **涉及文件**: `common/metrics/registry.go`(新增), `common/metrics/collectors.go`(新增), `common/metrics/server.go`(新增), `common/config/config.go`, `main.go`, `go.mod`, `go.sum`, `deploy/prometheus/{prometheus.yml,alerts.yml,servicemonitor.yaml,README.md}`(新增)
- **说明**: 补齐三块此前完全观测不到的盲区：Go runtime（goroutine/GC/内存/fd/调度延迟）、DB 连接池（生产 `SQL_MAX_OPEN_CONNS=300` 跨公网连 RDS，`wait_count` 此前无任何观测手段）、Redis 连接池（AWS serverless ElastiCache 有 scale 抖动）。新增 leaf 包 `common/metrics`（只依赖 config/env/logger，禁止 import model 等以避免未来循环依赖），私有 Registry 不用 `DefaultRegisterer`；DB 指标复用官方 `collectors.NewDBStatsCollector`（导出 `go_sql_*`，带 `db_name` label），Redis 因 go-redis v8 无官方实现故自写 collector。`/metrics` 与 `/debug/pprof/*` 挂在**独立端口** + Bearer token（常量时间比较），刻意不复用 `middleware.RootAuth()`——它每次 scrape 打一次 DB，且 DB 挂掉时监控端点会一起挂。pprof 五个 handler 显式注册而非 `import _ "net/http/pprof"`，避免污染 `http.DefaultServeMux`。`LOG_DB == DB` 时只注册一次，避免导出两份相同数字误导告警。顺带移除 `monitorGoroutines()` 里每 30s 一次的 `runtime.ReadMemStats`（stop-the-world 操作，GoCollector 已基于 `runtime/metrics` 覆盖）。约 110 条固定小基数序列，无基数爆炸风险。**零业务代码侵入**，未改动 `relay/`、`controller/`、`model/`、`middleware/`；`METRICS_ENABLED=false`（默认）即完全消失。附 11 条告警规则与 K8s ServiceMonitor/NetworkPolicy 清单。业务指标（请求量/延迟/tokens/quota）继续由 `model_metrics` 与 `logs` 表承担，本次刻意不复制，避免两套数字对不上。
- **验证**: `go build ./...` 通过；改动范围内 `go vet` 通过。本地实测：401/200 鉴权正确；`SQL_MAX_OPEN_CONNS=1` + 60 并发下 `go_sql_wait_count_total` 0→120、`wait_duration_seconds_total` 0→1.29s（证明 collector 在 `Collect()` 时实时取快照而非注册时定格）；Redis `pool_hits_total` 1→354、`pool_connections` 1→8；主端口 `/metrics` 与 `/debug/pprof/` 均返回 404，证明 `DefaultServeMux` 未被污染。
- **注意**: `go get` 顺带升级了 5 个 `golang.org/x/*` 包（crypto/net/sync/sys/text），已验证编译通过。`docker-compose.yml` 未改动——启用需手动加 `METRICS_*` 环境变量（token 建议走 `.env` 引用，不要写入该文件）。
- **关联计划**: `docs/plans/2026-07-31-prometheus-p0-可观测性.md`

---

## 2026-07-13

### feat(billing): 接入 gpt-5.6-sol/terra/luna，支持 cache_write 与 long-context 计费
- **分支**: `dev-gpt-5.6`
- **类型**: 新功能
- **涉及文件**: `relay/model/misc.go`, `relay/channel/openai/model.go`, `common/model-ratio.go`, `relay/channel/openai/constants.go`, `relay/controller/helper.go`, `relay/controller/opeai_response.go`, `common/model_ratio_gpt56_test.go`
- **说明**: 新增三个 OpenAI 兼容模型。为 `Usage.PromptTokensDetails` 与 `InputTokensDetails` 增加 `cache_write_tokens` 字段；`model-ratio.go` 配置三模型的 Model/Completion/Cache 倍率，并新增 `CacheWriteRatio`（写入 1.25x）+ `GetCacheWriteRatio` 与 `LongContextThreshold`（>272000 触发）+ `GetLongContextMultiplier`（2x）。chat 与 responses 两条计费链路均加入 cache_write 分项计费与 long-context 乘子。long 档全列 2x 通过翻倍 modelRatio 实现，无需第二套价格表。附单测覆盖倍率表、阈值边界与真实样本 quota。
- **关联计划**: `docs/plans/2026-07-13-gpt-5.6-sol-luna-terra.md`

### fix(billing): 修正 gpt-5.6 long-context 输出倍率错误（×1.5 而非 ×2）
- **分支**: `dev-gpt-5.6`
- **类型**: 修复
- **涉及文件**: `common/model-ratio.go`, `relay/controller/helper.go`, `relay/controller/opeai_response.go`, `common/model_ratio_gpt56_test.go`
- **说明**: 官方定价截图确认：long 档输出 ×1.5（而非统一 ×2），输入×2。首次实现统一翻倍 `modelRatio` 导致 output 超收 33%。修正为 `GetLongContextMultipliers` 返回结构体，chat 与 responses 计费分别应用 InputMultiplier（2.0）与 OutputMultiplier（1.5）。单测验证新倍率。
---

## 2026-07-01

### feat(audit): 为 /v1/messages (Claude 原生) 添加 audit 埋点
- **分支**: `AthenaQuery`
- **类型**: 新功能
- **涉及文件**: `relay/controller/claude.go`
- **说明**: 引入 `common/audit` 包，在 `RelayClaudeNative` 中调用 `SetMeta`（记录 isStream 和实际模型名）和 `SetConvertedBody`（记录转发给上游的请求体）；非流式路径在 `doNativeClaudeResponse` 中调用 `SetUpstreamResponse`；流式路径在 `doNativeClaudeStreamResponse` 中调用 `WrapUpstreamBody`，通过 TeeReader 透明捕获上游 SSE 数据。

---

## 2026-06-29

### feat(audit): 审计配置热重载，保存后无需重启服务
- **分支**: `AthenaQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/manager.go`, `model/option.go`, `common/audit/worker_test.go`
- **说明**: `manager.go` 用 mutex+`hasStarted` 替换 `sync.Once`，提取 `doStart`/`doStop`，新增 `Reload()`。`model/option.go` 在 `updateOptionMap` 里对 key `auditConfig` 触发 `go audit.Reload()`（goroutine 避免锁重入）。保存配置后审计模块自动停止旧实例并以新配置重启，无需重启进程。

### feat(audit): 将 9 个性能配置字段纳入 auditConfig JSON，支持前端覆盖
- **分支**: `AthenaQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/config.go`, `d:/my/ezlinkai-web/sections/setting/view/settingPage.tsx`
- **说明**: `loadConfig()` 新增解析 `channelSize`、`maxBufferMB`、`diskBufferDir`、`diskBufferMaxGB`、`batchSize`、`flushIntervalSec`、`maxBodyKB`、`maxRespKB`、`retentionDays` 9 个字段；整数 > 0 / 字符串非空时覆盖环境变量默认值。前端新增"高级性能配置"区块，留空则沿用默认值。



### fix(audit): 修复代码审查发现的 7 项数据完整性、竞态和性能问题
- **分支**: `AthenaQuery`
- **类型**: 修复
- **涉及文件**: `common/audit/awsclient.go`, `common/audit/compaction.go`, `common/audit/config.go`, `common/audit/query.go`, `common/audit/spill.go`, `common/audit/worker.go`, `main.go`
- **说明**: 代码审查后集中修复：(1) putRecordBatch 部分成功时只 spill 未发送记录防重复写入；(2) compaction goroutine 改用可取消 context；(3) XRequestID 正则放宽兼容非十六进制 ID；(4) spillStore 加 mutex + 原子 rename 防竞态；(5) AthenaDatabase/Table 标识符校验防 SQL 注入；(6) QueryLogs 用 COUNT(*) OVER() 合并为单次查询减半延迟；(7) 新增 AUDIT_RETENTION_DAYS 数据留存策略。

### fix(audit): 修复审计中间件吞没 panic 及 compaction 30 秒超时必败
- **分支**: `AthenaQuery`
- **类型**: 修复
- **涉及文件**: `middleware/audit.go`, `common/audit/athena.go`
- **说明**: (1) 审计中间件 defer 中捕获 panic 后未 re-panic，导致 Gin 的 RelayPanicRecover 无法触发——改为先完成审计采集再 re-panic；(2) compaction OPTIMIZE 查询需数分钟但 Athena 硬编码 30s 超时——改为从 context deadline 取值，compaction 传入 15 分钟超时。

### refactor(audit): compaction 从进程内定时器改为外部调度
- **分支**: `AthenaQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/compaction.go`, `common/audit/manager.go`, `common/audit/config.go`, `main.go`
- **说明**: 将 Iceberg BIN_PACK compaction 从 audit 模块内部 goroutine 剥离，改为在 main.go 中由 `ENABLE_VIDEO_TASK_POLLER` 环境变量守卫的独立定时任务。保证多实例部署时只在一台机器上执行，消除 `AUDIT_COMPACTION_ENABLED` 配置项。
- **关联计划**: `docs/plans/2026-06-23-audit-compaction-externalize.md`

## 2026-06-22

### refactor(audit): 写入/查询层从 BigQuery 迁移到 Firehose + Iceberg + Athena
- **分支**: `AthenaQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/config.go`、`common/audit/awsclient.go`（新）、`common/audit/athena.go`（新）、`common/audit/compaction.go`（新）、`common/audit/query.go`、`common/audit/serialize.go`、`common/audit/worker.go`、`common/audit/manager.go`、`go.mod`、`go.sum`；删除 `bqclient.go`、`bqclient_test.go`
- **说明**: 将审计模块整体从 GCP BigQuery 迁移到 AWS 原生栈（Firehose PutRecordBatch → Iceberg Table → S3 → Athena），消除跨云 egress 费用（~$184-368/月）。写入改用 JSON + Firehose PutRecordBatch（自动分片 500 条/4MB，部分失败重试）；查询改用 Athena 异步轮询（500ms 间隔，30s 超时）；SQL 注入防护用严格白名单正则校验；新增 Glue 自动建表（Iceberg 格式，day 分区 + x_request_id 排序）；新增每日 OPTIMIZE compaction。移除全部 GCP/BigQuery/protobuf 依赖。
- **关联计划**: `docs/plans/2026-06-22-audit-athena-migration.md`

### perf(audit): Clustering 首列改为 x_request_id
- **分支**: `bigQuery`
- **类型**: 性能优化
- **涉及文件**: `common/audit/bqclient.go`
- **说明**: 将 BigQuery 表 Clustering 字段顺序调整为 `[x_request_id, actual_model, channel_id, user_id]`，x_request_id 排首位以优化按请求 ID 精确查询的性能。

### feat(audit): 新增 BigQuery 审计查看器（后端 API + 前端页面）
- **分支**: `bigQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/query.go`、`controller/audit_viewer.go`、`router/api-router.go`、前端 `~/code/ezlinkai-web` 下 `app/dashboard/bigquery/`、`sections/bigquery/`、`constants/data.ts`、`components/icons.tsx`、`lib/searchparams.ts`
- **说明**: 新增管理后台审计查看页面。后端提供 `GET /api/audit/logs`（分页列表）和 `GET /api/audit/detail`（完整详情）两个 API，通过 BigQuery 参数化查询实现，强制日期范围（≤31天）确保分区裁剪控制成本。前端新增 Audit 页面（仅管理员可见），支持按 x_request_id 精确搜索、按日期范围分页浏览、查看完整请求/响应详情。
- **关联计划**: `docs/plans/2026-06-22-audit-bigquery-viewer.md`

### refactor(audit): 写入层从 GCS Load Job 迁移到 Storage Write API
- **分支**: `bigQuery`
- **类型**: 重构
- **涉及文件**: `common/audit/bqclient.go`、`common/audit/worker.go`、`common/audit/config.go`、`common/audit/manager.go`、`common/audit/bqclient_test.go`、`common/audit/worker_test.go`、`go.mod`、`go.sum`、`docs/plans/2026-06-10-audit-bigquery-design.md`、`docs/plans/2026-06-10-audit-bigquery-implementation.md`
- **说明**: 将审计数据写入通道从 GCS 上传 + BigQuery Load Job 替换为 BigQuery Storage Write API（`managedwriter` 包 DefaultStream + `EnableWriteRetries`），消除 Load Job 1,500 次/天/表的硬性配额限制和 `job.Wait()` 同步阻塞瓶颈。新增 protobuf 序列化（`dynamicpb` + `adapt` 包，无需 .proto codegen）；spill 落盘仍为 gzip NDJSON（调试友好），重放时转 proto→AppendRows。移除 GCS 直接依赖（`cloud.google.com/go/storage` 降级为 indirect）。建表新增 Clustering（`actual_model`、`channel_id`、`user_id`）免费优化查询。新增 4 个测试覆盖 proto 序列化和 spill 重放。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-design.md`

## 2026-06-11

### fix(audit): Shutdown 等待 flush + 透传埋点改用 bytes 缓冲
- **分支**: `bigQuery`
- **类型**: fix
- **涉及文件**: `common/audit/manager.go`、`common/audit/worker_test.go`、`relay/controller/text.go`
- **说明**: 集成层评审遗留项收口。`Shutdown()` 关闭 `recordChan` 后阻塞等待 `ingestLoop` flush 完成（新增 `ingestDone` 信号），避免将来真有 graceful shutdown 时丢失内存尾批。经评估，审计为 best-effort 旁路功能，未引入进程级 SIGTERM 处理（blast radius 过大，与收益不匹配）——重启丢失最多 ~10s（默认 FlushInterval）内存尾批属可接受。`text.go` 透传分支改为仅在 `audit.Enabled()` 时读取原始体并用 `bytes.NewBuffer(raw)` 下发，保持审计关闭时零开销，且不依赖 `c.Request.Body` 读取后的状态。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-implementation.md`

## 2026-06-10

### feat(audit): 模型调用全链路审计 → BigQuery
- **分支**: `bigQuery`
- **类型**: 新功能
- **涉及文件**: `common/audit/*`（config.go、record.go、redact.go、truncate.go、context.go、serialize.go、spill.go、bqclient.go、manager.go、worker.go、assemble.go 及对应测试）、`middleware/audit.go`、`middleware/recover.go`、`relay/controller/text.go`、`relay/channel/common.go`、`main.go`、`router/relay-router.go`
- **说明**: 新增与主业务解耦的审计模块，记录模型调用 6 类全链路数据（原始请求头/体、转换后请求头/体、上游响应、返回客户端响应），经脱敏（Authorization/Api-Key 等凭证）、截断（请求 10MB/响应 4MB）后批量写入 BigQuery。两级缓冲：内存（默认 1GB）满则落盘 NDJSON gzip（默认 40GB）经 GCS load job 入库。全程非阻塞 channel 投递 + 哑操作埋点，审计未启用（`AUDIT_ENABLED` 默认关闭）时零开销，任何初始化/运行失败自动降级，绝不阻断主请求。一期仅覆盖 `/completions`、`/chat/completions`。顺带修复 `middleware/recover.go` 既有的 non-constant format string vet 报错。
- **关联计划**: `docs/plans/2026-06-10-audit-bigquery-design.md`、`docs/plans/2026-06-10-audit-bigquery-implementation.md`

## 2026-07-01

### feat(gemini): Omni 视频结果接口透传上游 usage

- **分支**: `gemini-omini`
- **类型**: feat
- **涉及文件**:
  - `relay/model/general.go`
  - `relay/channel/gemini/video_adaptor.go`
- **说明**: `/v1/video/generations/result` 对 gemini-omni 的响应新增 `usage` 字段，透传上游 Interactions API 返回的完整 token 用量（total_tokens / input / output / cached / thought / tool_use 及按模态拆分明细）。扩展 `VideoUsage` 结构承载 token 字段；`HandleVideoResult` 成功路径与缓存命中路径（`buildCachedVideoResponse`）均从 `result` JSON 还原 usage，保持响应格式一致。

### feat(gemini): Omni 视频改为按真实 token 用量计费

- **分支**: `gemini-omini`
- **类型**: feat
- **涉及文件**:
  - `common/video-pricing.go`
  - `relay/channel/gemini/video_adaptor.go`
  - `relay/channel/gemini/billing.go`（新增）
  - `relay/model/general.go`
  - `model/video.go`
  - `relay/controller/video.go`
  - `controller/gemini_video_poller.go`
- **说明**: 废弃创建时固定扣 $0.20 的占位计费，改为按 Gemini 官方 token 定价（输入 $1.50/M、输出文本 $9/M、输出视频 $17.50/M）。创建任务不扣费不记消费 log，任务成功完成后从上游 `usage` 解析真实 token 计数、计算 quota 并异步扣费记 log。并发安全参考 flux：`Video.UpdateIfNotTerminal` CAS（`WHERE status NOT IN ('succeed','failed')`）保证后台 poller 与用户主动查询两条路径只扣一次；CAS 赢得竞争后 goroutine 异步执行 `PostConsumeTokenQuota` 扣费（记入 Token 维度，token_id 来自 `videos.token_id`，创建时落库）并记消费 log（真实 PromptTokens/CompletionTokens）。`videos.quota` 记总费用、`videos.result` 记完整上游 JSON。失败任务不扣不退。创建时保留 $0.2 最低余额门槛（`GetPrePaymentQuota` 返回 $0.2 用于余额校验但不实际预扣）防透支；无 TokenId 时降级为只扣用户余额。
- **关联计划**: `docs/plans/2026-07-01-gemini-omni-usage-based-billing.md`
- **运维提示**: 已部署实例 DB 中旧的 `gemini-omni-flash-preview` fixed $0.20 定价规则不会自动删除（虽不再生效），建议在管理后台清理。

### fix(gemini): Omni 视频任务正确保存 prompt 至 videos 表
- **分支**: `gemini-omini`
- **类型**: fix
- **涉及文件**:
  - `relay/channel/interface.go`
  - `relay/channel/gemini/video_adaptor.go`
  - `relay/controller/video.go`
  - `relay/controller/directvideo.go`
  - `relay/controller/directvideo_xai.go`
- **说明**: `VideoTaskResult` 新增 `Prompt` 字段，Gemini adaptor 提交任务时回填 `req.Prompt`，`invokeVideoAdaptorRequest` 将其透传给 `CreateVideoLog`，使 `videos.prompt` 列记录真实用户输入（此前硬编码为字面量 `"prompt"`）。其余 9 处非 adaptor 调用点补 `""` 占位以修复因 `CreateVideoLog` 签名变更导致的编译失败。
- **关联计划**: `docs/plans/2026-07-01-gemini-prompt-persist.md`

### feat(gemini): Omni 视频查询结果落库到 videos.result 字段
- **分支**: `gemini-omini`
- **类型**: feat
- **涉及文件**:
  - `model/video.go`
  - `relay/channel/gemini/video_adaptor.go`
  - `controller/gemini_video_poller.go`
- **说明**: `FetchAndStoreVideoResult` 增加返回上游完整响应体 `rawJSON`；用户主动查询（`HandleVideoResult`）与后台 poller 各自将其写入 `videos.result` 字段。新增 `model.UpdateVideoResult` 方法。每次查询覆盖为最新一次上游响应。
- **关联计划**: `docs/plans/2026-07-01-gemini-prompt-persist.md`

---

## 2026-06-25

### fix(stream): 修复 wg.Add 竞态、跳过空 EndReason、补充 None 测试
- **分支**: `stream-status-port`
- **类型**: fix
- **涉及文件**:
  - `relay/helper/stream_scanner.go`
  - `relay/util/stream_status.go`
  - `relay/util/stream_status_test.go`
- **说明**: 将 `wg.Add(1)` 移至 `RelayCtxGo` 调用前，消除调度延迟导致的竞态；
  `AppendStreamStatusOther` 在 `EndReason == ""` 时提前返回，避免写入误导性 `"status":"error"` 记录；
  测试新增 `StreamEndReasonNone` 用例及 `TestAppendStreamStatusOther_NoneReasonSkipped`。

### feat(stream): 移植 StreamStatus 机制，持久化流式结束原因
- **分支**: `stream-status-port`
- **类型**: 新功能
- **涉及文件**:
  - `relay/util/stream_status.go`（新建）
  - `relay/util/stream_status_test.go`（新建）
  - `relay/util/relay_meta.go`
  - `relay/helper/stream_scanner.go`
  - `relay/controller/helper.go`
- **说明**: 从 new-api 完整移植 StreamStatus 机制。流式请求的 `logs.Other` 字段现在包含
  `streamStatus:{status, end_reason, end_error, errors}` 段，支持 done/timeout/client_gone/
  scanner_error/handler_stop/eof/panic/ping_fail 共 8 种结束原因。
- **关联计划**: `docs/superpowers/plans/2026-06-25-stream-status-port.md`

## 2026-06-11

### fix(anthropic): 更新 Vertex AI beta flags 白名单
- **分支**: `main`
- **类型**: fix
- **涉及文件**: `relay/channel/anthropic/beta.go`
- **说明**: 移除 Vertex 白名单中 5 个对应功能在 Vertex 上不支持的 flag（`mcp-client` x2、`files-api`、`code-execution`、`skills`），新增 3 个已验证支持的 flag（`compaction`、`context-editing`、`fallback-credit`）。经官方文档交叉验证。
- **关联计划**: 无

## 2026-06-09

### fix(streaming): SSE ping 格式改为 Claude 官方格式
- **分支**: `main`
- **类型**: fix
- **涉及文件**: `relay/channel/common.go`, `relay/helper/common.go`, `relay/helper/stream_scanner.go`
- **说明**: 将 ping 心跳从 SSE 注释格式 (`: PING`) 改为 Claude 官方格式 (`event: ping\ndata: {"type": "ping"}`)，与上游 Claude API 透传的 ping 保持一致。同时将 stream_scanner 中部分 println 调试日志改为 logger 正式日志。
- **关联计划**: 无

### feat(streaming): 等待上游响应期间发送 SSE ping 保活
- **分支**: `stream-ping`
- **类型**: 新功能
- **涉及文件**: `relay/channel/common.go`
- **说明**: 借鉴 new-api 实现，在 `DoRequest` 中增加 pre-request ping 机制。当流式请求等待上游（如 Claude thinking）响应时，定期发送 SSE 注释 (`: PING`) 防止中间代理层（ALB/nginx）误判连接空闲并断开。stop 函数同步等待 goroutine 退出，避免与后续 StreamScannerHandler 产生并发写入竞态。
- **关联计划**: 无（小功能，直接实现）

### refactor(logging): 改用原始 JSON 记录 message_delta 事件
- **分支**: `main`
- **类型**: 重构
- **涉及文件**: `relay/controller/claude.go`
- **说明**: 将 Claude 流式响应中 `message_delta` 的日志从仅记录 `stop_reason` 改为打印完整原始 JSON，便于排查 usage、output_tokens_details 等信息。

### feat(logging): Claude 流式响应增加 stop_reason 日志
- **分支**: `main`
- **类型**: 新功能
- **涉及文件**: `relay/controller/claude.go`
- **说明**: 在流式处理中记录 Claude 响应的 stop_reason 和 OutputTokens，用于排查客户反馈的 output_token 异常问题。
