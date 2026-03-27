# one-api Stripe 支付接入设计

**创建时间**: 2026-03-23
**状态**: 已确认设计，待实施
**涉及仓库**: `one-api`, `ezlinkai-web`

---

## 1. 目标

本次改造的目标是让 `one-api` 接入一条与 `new-api` 对齐的 Stripe 支付链路，并在 `ezlinkai-web` 的 `dashboard/setting/payment` 页面中增加对应的 Stripe 系统设置项。

本次只完成以下范围：

- 在 `one-api` 中新增或对齐 `new-api` 风格的 Stripe 配置项、下单接口和 Webhook 处理。
- 在 `ezlinkai-web` 的 `dashboard/setting/payment` 页面中新增 Stripe 配置卡片。
- 保留 `dashboard/topup` 当前的旧 Stripe 下单方式，不在本次切换。

本次**不做**以下内容：

- 不替换 `ezlinkai-web` 的 `dashboard/topup` 现有 Stripe 按钮逻辑。
- 不删除 `one-api` 现有 `/api/charge/*` 旧 Stripe 流程。
- 不引入订阅型 Stripe 支付，仅处理余额充值。

---

## 2. 现状总结

### 2.1 `new-api`

`new-api` 已有完整的 Stripe Checkout Session + Webhook 充值流程：

- 下单接口：`POST /api/user/stripe/pay`
- 金额预览接口：`POST /api/user/stripe/amount`
- 回调接口：`POST /api/stripe/webhook`
- 配置项：`StripeApiSecret`、`StripeWebhookSecret`、`StripePriceId`、`StripeUnitPrice`、`StripeMinTopUp`、`StripePromotionCodesEnabled`
- 支付成功后写入本地 `TopUp` 订单，并通过订单号完成用户额度充值

### 2.2 `one-api`

`one-api` 已有一套较早的 Stripe 逻辑，主要围绕 `charge_config`、`charge_order` 和 Payment Link：

- 旧接口：`/api/charge/get_config`、`/api/charge/create_order`、`/api/charge/stripe_callback`
- 旧配置：`StripePaymentEnabled`、`StripeCallbackUrl`、`StripePrivateKey`、`StripePublicKey`、`StripeEndpointSecret`
- 额度换算与易支付链路分离，存在独立的旧业务规则

### 2.3 `ezlinkai-web`

前端支付设置页 `dashboard/setting/payment` 已支持易支付，使用统一的 `/api/option` 接口加载和保存配置；Stripe 设置尚未出现在该页面中。

充值页 `dashboard/topup` 中虽然已有 Stripe 按钮，但仍调用旧的 `/api/charge/create_order` 固定面额逻辑。

---

## 3. 设计决策

### 3.1 总体策略

采用“增量替换”的方式：

- 在 `one-api` 中新增一条与 `new-api` 对齐的 Stripe 充值链路。
- 在 `ezlinkai-web` 中补齐 Stripe 系统设置页。
- 旧 `/api/charge/*` 流程继续保留，避免影响当前充值页。
- 等后台配置与新后端链路稳定后，再单独将 `dashboard/topup` 切换到新接口。

### 3.2 配置键策略

本次在 `one-api` 中新增并对齐以下配置键：

- `StripeApiSecret`
- `StripeWebhookSecret`
- `StripePriceId`
- `StripeUnitPrice`
- `StripeMinTopUp`
- `StripePromotionCodesEnabled`

同时保留现有：

- `StripePaymentEnabled`

`StripePaymentEnabled` 用作显式开关，便于系统设置页面控制是否启用 Stripe；其余字段与 `new-api` 完全对齐，降低后续迁移和维护成本。

旧字段：

- `StripeCallbackUrl`
- `StripePrivateKey`
- `StripePublicKey`
- `StripeEndpointSecret`

本次不删除，但新链路不再依赖这些旧字段。

### 3.3 安全策略

- `StripeApiSecret` 与 `StripeWebhookSecret` 均以 `Secret` 结尾，可继续复用 `one-api` 当前 `GET /api/option` 不回显敏感项的行为。
- 设置页保存敏感项时仅在用户重新填写时提交。
- Webhook 必须使用 Stripe 原始请求体进行验签，避免被中间层二次解析后失效。
- 支付完成逻辑必须保证幂等，防止重复回调导致重复加额度。

---

## 4. 后端设计

### 4.1 新增接口

在 `one-api` 中新增与 `new-api` 对齐的接口：

- `POST /api/user/stripe/amount`
- `POST /api/user/stripe/pay`
- `POST /api/stripe/webhook`

其中：

- `/api/user/stripe/amount` 用于按用户输入的充值数量计算应付金额。
- `/api/user/stripe/pay` 用于创建 Stripe Checkout Session 并返回 `pay_link`。
- `/api/stripe/webhook` 用于接收 Stripe 的异步支付通知。

旧接口保持不变：

- `GET /api/charge/get_config`
- `POST /api/charge/create_order`
- `POST /api/charge/stripe_callback`

### 4.2 订单与数据复用

优先复用现有 `TopUp` 表，而不是继续扩展旧 `charge_order` 体系。

原因：

- `TopUp` 已承载易支付充值流程，具备通用的充值订单语义。
- `CompleteTopUpOrder` 已包含订单状态检查、事务处理和余额增加逻辑。
- 新 Stripe 充值与易支付本质上都属于“在线充值”，统一到 `TopUp` 更容易保证额度规则一致。

新链路中的本地订单字段约定如下：

- `TradeNo`: 使用 Stripe Checkout Session 的 `client_reference_id`
- `PaymentMethod`: 写为 `stripe`
- `Status`: `pending` / `success` / `expired`
- `Amount`: 用户充值数量
- `Money`: 实际支付金额

### 4.3 金额计算

新链路不沿用旧 `/api/charge/*` 的固定面额和旧倍率逻辑，而是对齐 `new-api` 风格：

- 使用 `StripeMinTopUp` 限制最低充值数量
- 使用 `StripeUnitPrice` 计算基础支付金额
- 沿用 `one-api` 当前最终额度结算规则，通过 `QuotaPerUnit` 统一给用户加额度

这样可以避免：

- Stripe 与易支付的同金额充值对应不同额度
- 充值页切换新接口时需要再改一次额度计算逻辑

### 4.4 Stripe Checkout Session

下单时后端执行以下步骤：

1. 校验 `StripePaymentEnabled` 是否开启，以及必要配置是否齐全
2. 校验用户输入的充值数量不低于 `StripeMinTopUp`
3. 计算应付金额并生成本地订单号
4. 创建本地 `TopUp` 订单，状态为 `pending`
5. 调用 Stripe Checkout Session API，设置：
   - `ClientReferenceID = TradeNo`
   - `Price = StripePriceId`
   - `Quantity = Amount`
   - `AllowPromotionCodes = StripePromotionCodesEnabled`
6. 返回 `pay_link`

成功和取消跳转 URL 优先允许前端传入，若前端不传则退回系统默认地址。

### 4.5 Webhook 处理

Webhook 接收 `checkout.session.completed` 和 `checkout.session.expired` 两类事件：

- `checkout.session.completed`
  - 读取 `client_reference_id`
  - 加锁并查找本地 `TopUp`
  - 若订单仍为 `pending`，则调用统一充值完成逻辑
  - 记录日志

- `checkout.session.expired`
  - 读取 `client_reference_id`
  - 若订单仍为 `pending`，则更新为 `expired`

支付成功后不再走旧 `charge_order` 的 Stripe 逻辑，也不调用旧的 `UserLevelUpgrade` 链路。

### 4.6 与旧 Stripe 流程的兼容

本次新增链路与旧 Stripe 流程完全并行：

- 旧流程继续服务当前 `dashboard/topup`
- 新流程先用于后台配置打通与后端联调
- 后续前端充值页切换时，仅需把请求从 `/api/charge/create_order` 改到 `/api/user/stripe/pay`

该策略的核心目标是让本次改动“新增而不破坏”。

---

## 5. 前端设计

### 5.1 页面位置

在 `ezlinkai-web` 的 `dashboard/setting/payment` 页面中新增 Stripe 设置区块，保留现有易支付设置不变。

### 5.2 页面结构

建议新增一个 `Stripe` 卡片，字段顺序如下：

- 启用 Stripe 支付（`StripePaymentEnabled`）
- `StripeApiSecret`
- `StripeWebhookSecret`
- `StripePriceId`
- `StripeUnitPrice`
- `StripeMinTopUp`
- `StripePromotionCodesEnabled`

页面继续沿用当前支付设置页的实现方式：

- 加载：`GET /api/option`
- 保存：逐项 `PUT /api/option`
- 敏感项不回显，仅重新填写时更新

### 5.3 文案与交互

- Stripe 配置说明明确指出：需要先配置 API Secret、Webhook Secret 与 Price ID，才能正常创建支付会话。
- `StripeUnitPrice` 和 `StripeMinTopUp` 需说明会影响后端金额校验与未来充值页金额展示。
- 启用开关打开前，若基础参数未配置完整，后端会拒绝启用。

---

## 6. 错误处理与回滚策略

### 6.1 错误处理

- 若 Stripe 配置缺失，后端下单接口直接返回清晰错误信息。
- 若 Stripe 验签失败，Webhook 返回 400，且不得更新订单状态。
- 若 Stripe 重复推送相同成功事件，订单完成逻辑必须幂等，后续重复回调不重复加额度。

### 6.2 回滚策略

本次改动可以按最小粒度回滚：

- 前端只新增一个设置卡片，删除该卡片即可回退 UI 改动。
- 后端新增接口独立于旧接口，若出现问题可只关闭 `StripePaymentEnabled`，不影响旧 `/api/charge/*` 充值路径。

---

## 7. 验证策略

后端至少验证以下内容：

- 系统设置可写入并正确加载新 Stripe 配置项
- 未配置完整参数时不能启用 Stripe
- 充值数量小于最小值时下单失败
- 创建 Checkout Session 成功时能写入本地 `TopUp` 订单
- `checkout.session.completed` 回调只能成功加额度一次
- `checkout.session.expired` 能把 `pending` 订单改为 `expired`

前端至少验证以下内容：

- `dashboard/setting/payment` 能正常显示 Stripe 配置项
- 重新进入页面时敏感项不回显
- 保存后普通配置项可正确回显
- 开关与字段保存失败时有错误提示

---

## 8. 后续阶段

本次完成后，下一阶段再切换 `ezlinkai-web` 的 `dashboard/topup`：

- 移除旧 `charge_id` 固定面额依赖
- 改为调用 `/api/user/stripe/amount` 和 `/api/user/stripe/pay`
- 用统一的充值数量和支付金额展示方式替换旧 Stripe 独立逻辑

在那之前，`dashboard/topup` 与新 Stripe 设置页可以并存，但仅后台配置和新后端链路会完成对齐。
