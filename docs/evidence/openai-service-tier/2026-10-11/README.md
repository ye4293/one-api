# OpenAI service_tier 本地实施证据

清理说明（2026-10-11）：按用户要求，测试原始日志、汇总 JSON、状态快照及人工配置演练输出已删除。本文保留此前执行的结果摘要；清理未重新运行测试，以下输出文件名仅为历史记录。核心计费 Go 回归测试和发布配置草稿保留；独立的 Python 辅助工具及其测试/专用说明已按用户要求移除。

日期：2026-10-11。代码基线 `fcca90e3b143363f95d8af6e4d7ca421516ef0f0`，分支 `codex/openai-service-tier`，Go `1.26.4 darwin/arm64`。

P00–P05 本地验证完成；P06 专项、构建、静态和 race 检查通过，全量测试仍有已复现的存量失败及环境限制；P07 配置草稿和回滚材料已准备，现场核对及发布未执行。没有修改真实价格、访问付费模型、启动生产 shipper、提交、推送或部署。

同日接续：计费专项、build、vet 再次通过，实际环境验收状态不变。下文保留核心计费实施与验证记录。

## 接续位置与改动

接手时实施文档仍为“未执行”，但工作区已有 P01–P04 主体实现和部分 P05 测试，当时的 `start-state.txt` 状态快照已清理。已保留这些改动，并核对以下链路：

- `openai_pricing.go` 只按响应档位精确查 map，逐项保留存在标记；`0` 有效，空/default 不查后缀，fast/priority 不互相回退。Responses 忽略固定价。
- Chat 在原结算位置应用覆盖，保留请求前输入倍率的回退、固定价优先、ceil、最低额度、零 token 与预扣差额。Responses 复用原公式及截断、全额扣费，命中时将实际价格交给异步日志。
- `GetLongContextMultipliers` 保留原 9 项和精确阈值优先级，仅增加 GPT-6+ 默认 `272000`，超过后整次输入/缓存 ×2、输出 ×1.5。
- 复用既有请求透传、响应采集及 `usageDetails.service_tier`；模型/路由/DTO/价格 options/数据库/管理 API/生产默认价格未扩张。

本轮补充 P05：Standard/auto 原样取价、SSE null/空值后的档位保留、最后 default、映射前后阈值差异、流结束缺少 DONE、零用量、坏 JSON、流内错误重试、已配置旧档位清理、三段折扣和原始响应保留。补充设计稿价格换算测试，并隔离恢复测试 GroupRatio，避免依赖其他用例的全局状态。

## 检查命令及结果

以下 Go 检查使用 `GOCACHE=/tmp/one-api-service-tier-go-cache`。第一次默认缓存访问 controller 时受写权限限制；当时日志记录了该环境错误，切换可写缓存后三个包通过；对应原始日志现已清理。

| 检查 | 退出码 | 历史输出（已清理） |
|---|---:|---|
| 原始 HEAD：common/controller 的 GPT5、档位透传、usage、日志及无覆盖兼容基线 | 0 | `baseline.log` |
| `go test ./common ./relay/controller ./common/shipper -count=1 -v` | 0 | `targeted.log` |
| `go build ./...` | 0 | `build.log` |
| `go vet ./...` | 0 | `vet.log` |
| `go test ./... -count=1` | 1 | `all-tests.log` |
| 新增 GPT6/档位查询、Chat/Responses 计费、兼容及集成测试的 `go test -race` | 0 | `race.log` |
| `git diff --check` | 0 | `diff-check.log` |

原 `checks.json` 参数/耗时汇总和原始日志已清理，本文保留命令及结果摘要。当时专项输出有实际执行记录，`TestOpenAITierPricingIntegration` 共 **130** 个子用例，通过三种入口 × 普通/SSE，再扩展价格、映射、折扣及异常场景；不是 `[no tests to run]`。

基线曾在 `/tmp/one-api-service-tier-baseline` 中从 `git archive HEAD` 解出（该临时副本及专用缓存现已清理，重跑需重新准备），未切换或重置工作区。额外复制 `openai_pricing_test.go` 的人工无覆盖夹具及其编译依赖 `openai_pricing.go`，未替换原始业务计费函数；后者未被旧业务路径调用。执行命令：

```sh
GOCACHE=/tmp/one-api-service-tier-go-cache go test -C /tmp/one-api-service-tier-baseline ./common ./relay/controller -run '^(TestGPT5Family|TestOpenAITierPricingCompatibility|TestOpenAIServiceTier|TestServiceTierOtherModelUsageLogs|TestAppendUsageDetails|TestExtractOpenaiResponse|TestBuildOpenaiResponse)' -count=1 -v
```

## 全量测试失败归因

以下均在原始 HEAD 复现，未修改无关业务来消除红灯。

| 范围 | 失败 | 归因/基线证据 |
|---|---|---|
| controller | `TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered` / `MixedChannels`，期望触发 1/2 次，实际 0 | 存量断言失败；`baseline-failures.log` |
| anthropic | `TestFilterBetaFlags/vertex_allows_files-api` 缺失 files-api 标记 | 存量断言失败；同上 |
| kling | `TestGetModelNameFromRequest/有model` 返回空模型 | 存量断言失败；同上 |
| controller/flux 视频测试 | `httptest` 监听本地 TCP 端口被拒绝并 panic | 沙箱限制；同上 |
| common/image | Wikimedia 域名解析失败，随后测试 nil 解引用 | 外网环境依赖；`baseline-image-failure.log` |

基线失败检查命令分别为 `go test -C /tmp/one-api-service-tier-baseline ./controller ./relay/channel/anthropic ./relay/channel/flux ./relay/channel/kling -count=1` 和 `go test -C /tmp/one-api-service-tier-baseline ./common/image -count=1`，同样使用可写缓存，退出码均为 1。全量结果不能记作通过；端口 panic 也意味着所在包之后的测试未必全部执行。

## 验收追踪

下表均为本地函数/内存数据库/HTTP transport mock 证据，具体 RUN/PASS 和异步路径原记录于 `targeted.log`、`race.log`，原始文件现已清理。集成测试通过真实 controller 与 adaptor，替换 HTTP transport，不开外网连接；`synctest` 等待派生 goroutine 并检测死锁后再恢复 map/数据库，不用固定 sleep 假定结算完成。

| 编号 | 实际测试/断言 | 结果与边界 |
|---|---|---|
| A01 | `TestOpenAIPriceOverrides`；Integration absent/null/empty/default/unconfigured | 通过，默认档位诱饵不生效 |
| A02 | `TestOpenAIChatTierPricing` / `TestOpenAIResponseTierPricing` 的 partial、各 zero、completion/read/write_only；Integration fast_priority/flex | 通过，显式零值与逐项回退按响应生效 |
| A03 | Overrides；Integration priority/fast/flex/ultrafast/default | 通过，fast 缺失不读 priority，反向响应差异也有独立金额 |
| A04 | Overrides；Integration Standard/auto/last_default | 通过，原值精确查询及最后 default |
| A05 | Integration 130 个用例；`TestOpenAIServiceTierRequestPassthrough` | 三入口两形态、映射重序列化、usage 分离通过；未接真实接口 |
| A06 | Chat fixed_override/fallback/zero/minus_one；Responses ignore_fixed | 通过，固定价不乘 long，Responses 继续 token 计费 |
| A07 | Chat/Responses 人工金额；Integration discount/combined_discount；Responses quota_per_unit | 通过，priority 2580、flex 523/522，三段折扣 0.8×0.5×0.5 后 516 |
| A08 | 两入口 long_priority/flex/default/absent/unknown/partial；Integration mapping_long_actual/original | 通过，选中价格各乘一次，阈值口径保持原映射规则 |
| A09 | `TestGPT5Family*` / `TestGPT6LongContextMultipliers` | 旧 9 项、三边界、版本格式/溢出、显式 1000/500000 阈值通过 |
| A10 | `TestOpenAIChatTierPricingGPT6AndMinimum` / `TestOpenAIResponseTierPricingGPT6LongContext` | 输入 272001 含缓存仍触发；272000 加输出不触发 |
| A11 | Chat/Responses 日志倍率断言；Integration 模型、usage、余额及日志条数 | 通过，价格后缀不发送上游、不写主体模型，缺失档位不伪造 |
| A12 | `TestOpenAIResponseTierPricingDesignPriceConversion` | 设计快照到短/长价格数学验证通过；官网现价、实际配置回退待核对 |
| A13 | `TestOpenAITierPricingCompatibility` 在 HEAD 与当前工作区；Chat priority/flex 预扣 | 通过，100 预扣最终 2580，以及 3000 预扣最终 523 退差额；统计与日志条件不变 |
| A14 | Compatibility nil/zero/invalid_cache/minimum/fixed；Integration retry_missing/stream_error_retry/stream_malformed/stream_zero_usage/stream_eof_after_usage；`TestServiceTierOtherModelUsageLogs` | 本地既有异常分支及兼容/独立响应处理器差异通过；不代表真实客户端断连演练 |
| A15 | `TestOpenAIChatTierPricingPreservesInputFallback`；Responses LegacyLogTiming 与各命中场景记录前修改 map | 通过，命中日志保留实际参数，无覆盖保留原查价时机；未建立全局热更新快照 |

A01–A15 覆盖实施计划关联的设计稿 §7 共 22 条，其中 A05 真实环境部分、A12 官方现价和环境配置部分归 P07 未完成。

## 保留的限制与下一步

- `common/shipper` 现有配置及未初始化 no-op 测试通过。静态核对 `model/log.go`：序列化本次已验证的数据库日志行，并用该行的 ModelName/Quota 进入原投递链路；没有执行真实 SQS 发送或接收端验收。
- 旧 Completions SSE 忽略上游 usage、采用本地估算。本夹具普通 priority 为 2580，SSE 因输入/输出各估算 1 token 为 10，不能把二者差异当成档位取价失败。
- 异常缓存分桶仍保留旧行为差异：Chat 钳为零、Responses 回退总输入；未宣称满足仓库理想硬校验契约。零 usage 的完整入口与 nil usage 函数测试分开记录。
- 全局价格 map 热更新竞争、预估偏低和异步扣费失败仍是既有边界。本次 race 通过不代表对这些问题的修复或生产保证。
- 官方文档首次读取 DNS 失败；提权读取被自动审批拒绝执行，原因为 `Quota exceeded`、审批未完成，非对访问安全性的否定。未绕过该检查，未完成官网复核。
- [配置草稿与回滚材料](release-preparation.md) 已交付；尚需目标环境的只读配置、实际同步周期、定价确认和相应执行授权。P07 未完成，不宣称上线或生产验收完成。

## 2026-10-12 提交前审查

本次审查覆盖 `0559f72a` 之后的档位透传提交及当前工作区计费实现，未修改业务代码。重新执行相关包测试（`common`、`relay/controller`、`relay/channel/openai`、`common/shipper`）、档位及长上下文专项 race、`go build ./...`、`go vet ./...`，退出码均为 0；其中 `relay/channel/openai` 无独立测试文件，其响应处理路径由 controller 测试覆盖。构建报告模块元信息缓存写入权限警告，但退出码为 0。Go 文件格式检查无差异。

`go test ./... -timeout 60s` 退出 1。通过临时 Go overlay 使用 `0559f72a` 的源文件复核后，渠道用量禁用、Anthropic beta 标记、Kling 模型解析三类业务断言在基线同样失败；其他失败涉及 DNS 和本地 TCP 监听权限。临时复现文件位于仓库外，没有加入业务实现或常规测试。

确认一项 P2：管理接口把 `模型_档位` 当成独立模型查询，缺少输出/缓存倍率时使用通用默认值，而结算回退基础模型；仅存在于输出/缓存倍率 map 的档位还会在价格列表遗漏。人工配置基础输出倍率 `5`、缓存读倍率 `0.1`，priority 仅配置输入倍率 `10` 时，管理接口返回 `input_price=20`、`output_price=20`、`cache_ratio=1`，实际结算使用输出单价 `100`、缓存读单价 `2`（美元/百万 token，短上下文、无折扣）。本地对照用例复现该差异，结算金额符合设计。

用户明确决定暂不处理该 P2 并授权本地提交。完整显式配置档位输入、输出和缓存读取倍率可规避上述展示差异，但不等于已修复部分配置支持。本次审查未发现其他新增阻断问题；既有全局价格 map 热更新竞争、预估偏低及异步结算限制仍保留。真实环境验收、价格配置写入、推送和部署不在本次提交范围内。
