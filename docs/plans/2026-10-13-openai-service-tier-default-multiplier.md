# OpenAI 响应档位默认倍率

在现有显式档位配置之下增加平台默认倍率，不生成或写回派生价格，不改数据库结构和预扣策略。

## 取价顺序

1. 响应档位缺失、空值或 `default`：原基础价格。
2. 当前入口精确命中 `模型_响应档位` 的任意可用价格配置：沿用现有逐项覆盖与回退规则，显式 `0` 有效，不叠加默认倍率。
3. 完全未命中档位配置：符合适用范围且具有有效基础价格时，`flex` 使用 0.5，`fast` / `priority` 使用 2，`ultrafast` 使用 6。
4. 其他情况：原计费行为。未知档位仍可通过显式配置定价。

`fast` / `priority` 只共享默认倍率，显式配置不互相查找。仅配置一项缓存倍率也会阻止该入口启用自动倍率；需要完全继承时，应在核对后移除该档位的所有可用配置。

## 适用范围

- 仅 `ChannelTypeOpenAI` 渠道，且模型映射后的名称属于 `gpt-`、`chatgpt-` 或 `o数字`（包括 `o3-mini`）系列。
- Azure、其他渠道、其他模型系列保持原行为，已有显式档位配置不受此范围限制。
- 基础价格必须来自现有模型配置或模型默认价格，且为有限非负数；未知模型 getter 的通用兜底值不启用自动倍率。
- Chat 继续按实际模型计费；Responses 保留现有按原模型取价的规则。实际模型仅参与自动倍率范围判断，不改变已有模型映射和计费口径。

这组倍率是本系统业务规则，不代表所有模型的官方价格永久同比。

## 计算和日志

- Token：只对 `ModelRatio` 乘档位倍率，输出、缓存读写的相对倍率不重复乘算。
- Chat 按次：基础固定价乘档位倍率；`-1` 仍表示 token 计费。Responses 保持 token 计费。
- 后续长上下文系数、折扣、取整、零用量规则和扣费流程不变；Chat 沿用请求阶段输入倍率。
- `usageDetails.service_tier` 保留实际响应值。
- `billingDetails.service_tier_price_source` 为 `base`、`explicit` 或 `default_multiplier`；仅自动倍率时写入 `service_tier_multiplier`。现有 `tier_ratio` 仍是用户等级折扣。
- 自动倍率和显式档位配置都将实际计费参数传入异步日志，避免记录时重新查价。

预扣与余额检查仍基于原策略，高档位最终费用可能超过预估。本次不增加前端派生价格展示。

## 验证

复用现有内存数据库、HTTP transport mock、SSE 和异步等待设施。新增默认倍率的金额、部分配置、显式零值、按次特殊值、长上下文、取整、范围限制及价格快照测试；集成测试增加三入口 × 普通/流式共 60 个用例，覆盖响应降档、缺失、重试、折扣和模型映射。

```sh
GOCACHE=/tmp/one-api-service-tier-go-cache go test ./common ./relay/controller -count=1
GOCACHE=/tmp/one-api-service-tier-go-cache go test -race ./relay/controller -run '^(TestOpenAIDefaultTier|TestOpenAIPriceOverrides|TestOpenAIChatTierPricing|TestOpenAIResponseTierPricing|TestOpenAITierPricing)' -count=1
GOCACHE=/tmp/one-api-service-tier-go-cache go build ./...
GOCACHE=/tmp/one-api-service-tier-go-cache go vet ./...
```

上述测试使用本地人工价格，不调用真实付费接口，也不验证生产部署。

2026-10-13 本地执行结果：上述包测试、专项 race、全仓构建和 vet 均通过，`git diff --check` 通过。构建退出码为 0，另有 Go 模块版本缓存写权限提示；未执行全仓测试或生产验收。
