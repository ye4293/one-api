# 价格配置热更新并发修复计划

## 背景与目标

2026-10-22 的三个进程重启都紧随系统配置同步。已使用项目现有 `UpdateModelRatioByJSONString` 和 `GetModelRatio`，在本地合成数据下复现 `fatal error: concurrent map read and map write`。线上本次原生 stderr 已轮转，因此事故归因仍为高置信候选；代码并发缺陷本身已证实。

目标是在保留现有计费规则、配置格式及 HTTP API 的前提下，消除价格和分组配置 map 的并发读写与遍历风险。用户已确认按方案本地修复、验证后提交，不部署生产。

## 方案设计

1. 在 common 内建立价格配置统一读写锁。覆盖模型输入、输出、音频、图片、缓存、Claude 缓存创建、按次价格和分组折扣；缓存写价格的现有读取也统一纳入保护。
2. JSON 更新先解析到局部 map，成功后持写锁替换，避免半成品配置提前对请求可见；解析失败保留旧配置。JSON 序列化与标量读取持读锁。
3. 增加受保护的单项更新、删除、精确查找和副本读取入口。请求路径不复制整张价格表；模型列表与配置初始化等遍历路径使用锁内复制的副本。
4. 替换所有生产调用处的直接 map 访问。管理端设置单项/批量价格、增删分组、模型列表和上游模型识别均使用相同的保护机制。任何价格锁都不跨数据库、HTTP 或日志 I/O 持有；回退 getter 不重复持锁，避免递归读锁与等待中的写锁构成死锁。
5. 现有测试对全局变量的串行设置暂予保留；新增并发测试使用正式读写入口。检查剩余直接访问仅为初始化、锁内实现或串行测试准备。

预计涉及的生产文件（11 个）：

- `common/model-ratio.go`、`common/group-ratio.go`：更新、读取、序列化及受保护访问入口。
- `controller/pricing.go`：单项与批量修改、模型列表和价格展示。
- `controller/group_config.go`、`controller/group.go`：分组修改和遍历。
- `controller/model_plaza.go`、`controller/channel_upstream_probe.go`：模型遍历与存在性判断。
- `model/group_config.go`：初始化遍历分组副本。
- `relay/channel/openai/token.go`、`relay/channel/kling/billing.go`：模型遍历与精确查价。
- `relay/controller/openai_pricing.go`：档位价格精确查找，保留显式零值与回退语义。

另新增并发回归测试，并按仓库要求更新 `CLAUDE.md` 的教训记录及 `docs/CHANGELOG.md`。

## 影响范围

- 仅修复已经复现的价格配置并发缺陷，不调整价格、计费公式、默认值、配置键或对外 API。
- 不修改数据库 schema，不操作生产数据，不修改线上服务或告警阈值。
- 多个不同价格配置项仍沿用现有逐项更新语义，本次不扩展为跨配置事务。
- 启动阶段约数分钟的一致性检查与原生 stderr 的持久化采集是独立后续事项，本次不混入。

## 验证方式

1. 保留原始崩溃复现：`/tmp/oneapidown-ratio-repro-20261022.go`；修复前已触发 runtime fatal，修复后同一脚本须通过。
2. 新增并发测试，交叉执行 JSON 全量刷新、标量读取、管理端单项修改、删除、序列化及副本遍历，使用 `go test -race` 验证。
3. 验证非法 JSON 保留旧配置；副本修改不影响全局配置；零值、缺失项及原有 fallback 行为保持。
4. 运行相关 common、controller、model、relay 包的测试，重点保留 OpenAI service tier 和 Claude 缓存定价回归。
5. 执行 `go build ./...`、`go vet ./...` 与 `go test ./...`。如发现既有失败，记录其原始输出和与本次改动的关系。
6. 检查 diff，按仓库规范本地提交；不推送、不部署。

## 实施记录

- 11 个生产文件已改为统一的读写锁、成功解析后替换配置以及受保护的查找、修改和副本读取入口。
- 新增 common、controller、relay/controller 三处回归测试，覆盖全量刷新与单项修改并发、序列化、视图遍历、分组删除、显式零值和非法 JSON 保留原配置。
- 修复前，非法 JSON 保留配置的 11 组用例全部失败；原始并发脚本触发 `concurrent map read and map write`。
- 修复后，同一并发脚本退出码为 0；价格相关专项 `go test -race` 通过。
- `go build ./...`、`go vet ./...` 通过。
- 专项命令 `go test -race ./common ./controller ./relay/controller -run 'Test(Pricing|OpenAI.*(Pricing|Tier)|GPT.*(Ratio|Billing|LongContext))' -count=1` 通过，包括新并发测试及现有档位计费集成测试。
- `go test ./...` 仍有 JPEG 解码、按用量禁用渠道、Flux upscale、Anthropic beta 和 Kling 模型解析等存量失败。已从修改前的 HEAD 导出隔离基线，对全部失败测试复测，失败条目完全一致，本次无新增失败。
- 本次未改动生产配置、未重启或部署。线上本次退出的原生堆栈缺失，修复已复现的缺陷不等于已完成生产稳定性验收。
