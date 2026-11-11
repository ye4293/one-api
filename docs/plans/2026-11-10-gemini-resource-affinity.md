# Gemini 签名来源与重试资源类型一致性

## 目标与范围

仅对 gemini / gemini-* 模型携带的协议签名约束渠道类型，覆盖 Gemini 原生、Chat Completions 兼容及 Responses 入口。无签名请求沿用原有重试和兜底规则，显式指定渠道优先。不改变计费、响应格式或签名内容，不新增数据库结构或依赖。

2026-11-11 按用户要求调整：响应写出后异步缓存；类型筛选忽略无关配置解析错误；无签名不固定类型；指定渠道与签名冲突时仍使用指定渠道。

## 请求与重试规则

1. 指定渠道号时，保留管理员权限、渠道存在性和启用状态等原有校验。跳过签名来源查询和类型约束，使用指定渠道，不产生签名冲突 409。原有指定渠道禁止重试的规则保持不变。
2. 未指定渠道时，先按原规则进行 Response-ID、规则亲和或优先级选渠。
3. 仅检查协议签名字段：contents、messages、input 中的 thoughtSignature / thought_signature，以及兼容协议的 extra_content.google。文本、工具参数和 schema 不作为签名。请求内解析结果复用。
4. 无签名时不安装 Gemini 类型约束，包括有效 X-Response-ID 和 previous_response_id 命中的请求。失败后仍可跨 Gemini 官 key、Vertex 和兼容渠道重试，保留原来的最后渠道兜底。
5. 携带签名且命中 Response-ID 映射中的原渠道时，保留其渠道及 Key 索引，按该渠道类型约束本次重试，跳过签名来源查询。同区域备用渠道不算原渠道命中。
6. 携带签名且没有命中 Response-ID 原渠道时（包括选到同区域备用渠道），查询 Redis 来源。所有来源已知且一致才恢复类型；未知、过期、混合、冲突或 Redis 不可用时，保留原首次选渠，以选中的类型约束本次重试。
7. 已恢复签名来源与普通选渠不一致时，在同类型内重新选渠。没有可用同类型渠道仍返回 503。规则亲和被替换时清除旧 Key 索引及禁止重试标记，成功后更新原亲和缓存。
8. 签名约束存在时，重试和失败列表清空后的循环都保留类型过滤，同类型渠道不可用时停止，不回退失效旧快照。
9. 类型过滤只读取 channel.Type，不依赖 Config 解析成功；沿用旧流程在设置上下文时使用部分解析结果或默认配置。Azure 区域、count_tokens 等真正需要配置的能力筛选仍严格检查配置。静态/动态优先级、状态、分组、模型权限和权重规则继续复用。

## 响应签名缓存

- 在响应或流式块写出后提取签名摘要，再进行异步 Redis 写入。响应处理不等待缓存完成，也不等待后台执行名额。
- 每个进程最多 16 个后台写入，无等待队列；单批最多 256 个去重签名。名额耗尽或批次过大时记录告警并跳过，防止慢 Redis 堆积 goroutine、任务和响应体。
- 后台任务只保留 Redis 客户端、用户/渠道/类型 ID、请求 ID 和摘要键，不捕获 Gin Context、请求体或响应体。
- 后台使用独立的 2 秒超时，不随请求完成或客户端断开而取消；普通错误、超时和 panic 均隔离并释放执行名额，不改变响应或计费结果。
- Redis 键保持 `gemini_signature_resource:v2:{用户ID}:SHA256(签名字节)`，值为 channel.Type。Base64 合法时先解码再哈希，否则按不透明字符串哈希。相同用户的键共享 Cluster hash tag，保留多签名 Lua 原子操作。
- TTL 保持 24 小时滑动过期。写入和成功恢复来源会续期；同一签名来自不同类型时写为冲突标记，不覆盖绑定。有效 Response-ID 亲和不续期签名缓存。

## 边界和降级

- 异步写入存在短暂的跨请求窗口：客户端立即发起下一轮时，来源可能尚未落入 Redis。此时按未知签名处理，首次选渠沿用原规则，然后固定本次类型；不等待后台写入，也不引入节点本地来源缓存。
- 后台过载、写失败、进程退出可能丢失该次缓存写入；缓存属于尽力记录，不承担响应成功的前置条件。
- 请求阶段查询签名来源仍是同步读取，单次预算 2 秒，失败时降级放行。此次消除的是已生成响应被签名缓存写入阻塞的问题。
- 指定渠道号时不保证签名与上游资源兼容，上游可能自行拒绝；网关按用户指定渠道转发。
- 约束的是配置的 channel.Type，无法约束第三方代理内部路由。非 Gemini 名称的任意模型别名不在名称识别范围内。
- 未记录、外部导入、过期或冲突的签名不能保证跨请求类型一致。原有请求参数、计费及签名透传不变。

## Gemini responseId 渠道映射补充（2026-11-11）

- Gemini 原生及转 Chat 的流式、非流式响应，将上游返回的非空 `responseId` 写入现有 `cache_response_id_<id>` 映射，值继续使用 `channelId` / `channelId:keyIndex`，并保留原缓存时长辅助键和 24 小时 TTL。Vertex 的 Gemini 响应复用同一处理器。
- 首轮不需要请求头 `X-Response-ID`，也不要求携带签名。下一轮在该请求头中传上次的 ID，或在 `/responses` 请求体中传 `previous_response_id`，即可复用原有查询流程。
- 不改写客户端传入的 `X-Response-ID`；响应中的 `responseId`（转 Chat 后为 `id`）和上下文 `x_response_id` 保持原有含义。上游没有返回 `responseId` 时不缓存转换器临时生成的 UUID。
- 在响应写出后异步写入，流式连续相同 ID 只记录一次。Response-ID 写入独立限制为每进程 16 个后台任务、无队列、2 秒超时；任务保存 Redis 客户端和标量 ID，不持有 Gin Context。满载丢弃和错误不会改变响应。
- 现有同步 Response-ID 写入与新增异步入口共享键值编码，OpenAI/Claude 原有调用方式和各自 TTL 保持不变。
- 映射完成写入后才能被后续请求读到，仍存在短暂的异步窗口；本补充没有把缓存成功变成响应返回的前置条件。
- 验证通过：`go test -race ./model ./middleware ./service ./relay/controller ./relay/channel/gemini ./relay/channel/openai ./relay/channel/vertexai ./relay/channel/aws/claude ./router -count=1`，151 个顶层测试通过；OpenAI 适配包无独立测试文件。
- 重试回归通过：`go test -race ./controller -run 'Test(Gemini|NonGemini|VertexClaude|AzureRetry|NonAzureRetry)' -count=1`，8 个顶层测试通过。未发现竞态；已覆盖首轮建映射、下一轮复用渠道/Key、四条响应路径、流式去重、慢 Redis、满载丢弃及异常隔离。

## 验证计划

- 原生、Gemini 转 Chat、Chat 透传、Responses 透传的流式/非流式路径：Redis 被阻塞时仍能写出完整响应；放行后正确缓存；请求取消和上下文复用不改变后台来源。
- 后台并发上限、满载丢弃、命令错误、超时、panic 隔离及后续恢复。
- 无签名的原生/Chat/Responses 请求，以及 Response-ID 命中，不安装类型约束；无签名重试可跨类型。
- 已知/未知签名继续限制同类型；指定渠道覆盖签名冲突且跳过签名查询；普通选渠无同类型仍返回 503。
- 配置字段类型错误时，同类型首次替换及重试仍可选中该渠道；Azure 等能力筛选继续严格。
- 非 Gemini、Azure、静态/动态优先级、Response-ID、规则亲和及多 Key 的回归。

## 验证结果（2026-11-11）

- `go test -race ./service ./middleware ./controller ./relay/controller ./relay/channel/gemini ./relay/channel/openai ./relay/channel/vertexai -run 'Test(Gemini|NonGemini|VertexClaude|AzureRetry|NonAzureRetry)' -count=1` 通过。
- `go test -race ./service ./middleware ./model ./relay/controller ./relay/channel/gemini ./relay/channel/openai ./relay/channel/vertexai -count=1` 完整回归通过；openai 适配包无独立测试，但由响应入口测试覆盖并编译通过。
- 八种响应路径在 Redis 被主动阻塞时均先写出内容并返回，随后缓存成功；请求取消和 Gin 字段变更不影响原始签名来源。
- 已覆盖后台满载丢弃、命令错误、2 秒超时、panic 隔离及恢复，以及静态/动态优先级下的配置解析兼容、无签名跨类型重试、显式渠道覆盖冲突。
- `git diff --check` 通过。修改保留未提交，不推送、不部署，未调用真实上游。

## 合并 main 的兼容性（2026-11-11）

- 合并 `main` 的 `1b64d4bb`，保留 Azure 区域亲和、加密历史关联 ID 提取和同渠道降级重试。
- Response-ID 查询统一使用三段解析，兼容 `channelId`、`channelId:keyIndex` 和 `channelId:keyIndex:region`。Gemini 异步写入的旧格式仍可正常读取。
- 查询返回的布尔值仅表示是否命中映射中的原渠道。同区域备用选择保留，但不得据此绕过 Gemini 签名来源检查，也不得继承原渠道的 Key 索引。
- 合并回归使用临时 Go overlay：20 个顶层测试通过，包含 83 个子测试，开启 race 检查；覆盖缓存格式、区域兜底、Redis 失败、多 Key、四种 Azure 关联入口、静态/动态优先级、指定渠道、签名约束及 Azure 降级重试。
- `go test -race ./... -run '^$' -count=1 -timeout=60s` 通过，确认全项目及测试包编译成功；该命令不执行测试用例。
- 上述回归不覆盖此前发现的流式回调晚于请求结束、读取复用 Gin Context 的缺陷；该缺陷尚未修复。此前常规缓存测试通过不能据此证明这一取消场景安全。OpenAI 兼容响应中的同步 Response-ID 写入仍保留。
