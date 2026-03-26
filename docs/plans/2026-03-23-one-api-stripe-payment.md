# one-api Stripe Payment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 `one-api` 中接入与 `new-api` 对齐的 Stripe Checkout Session + Webhook 充值链路，并在 `ezlinkai-web` 的 `dashboard/setting/payment` 页面中增加 Stripe 系统设置项，同时保留旧 `/api/charge/*` 流程不受影响。

**Architecture:** 复用 `one-api` 现有 `options` 配置体系和 `TopUp` 订单模型，新增一条 `new-api` 风格的 Stripe 充值链路：`/api/user/stripe/amount`、`/api/user/stripe/pay`、`/api/stripe/webhook`。前端系统设置页继续沿用 `/api/option` 的读写模式，在原易支付页面中增加 Stripe 配置卡片。

**Tech Stack:** Go + Gin + GORM + Stripe Go SDK (`stripe-go/v78`)；Next.js 14 + React + TypeScript + Shadcn UI。

---

### Task 1: 对齐 `one-api` 的 Stripe 配置项

**Files:**
- Modify: `D:\my\one-api\common\config\config.go`
- Modify: `D:\my\one-api\model\option.go`
- Modify: `D:\my\one-api\controller\option.go`
- Create: `D:\my\one-api\controller\option_stripe_test.go`
- Test: `D:\my\one-api\controller\option_stripe_test.go`

**Step 1: Write the failing tests**

创建 `controller/option_stripe_test.go`，至少覆盖以下行为：

```go
package controller

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/gin-gonic/gin"
    "github.com/songquanpeng/one-api/common/config"
)

func TestUpdateOptionRejectsStripeEnabledWithoutRequiredConfig(t *testing.T) {
    gin.SetMode(gin.TestMode)

    config.StripeApiSecret = ""
    config.StripeWebhookSecret = ""
    config.StripePriceId = ""

    body, _ := json.Marshal(map[string]string{
        "key":   "StripePaymentEnabled",
        "value": "true",
    })

    req := httptest.NewRequest(http.MethodPut, "/api/option", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    c.Request = req

    UpdateOption(c)

    if w.Code != http.StatusOK {
        t.Fatalf("unexpected status: %d", w.Code)
    }
    if !bytes.Contains(w.Body.Bytes(), []byte("Stripe")) {
        t.Fatalf("expected stripe validation error, got %s", w.Body.String())
    }
}
```

再补一个用例，验证 `GetOptions` 不返回 `StripeApiSecret` 和 `StripeWebhookSecret`。

**Step 2: Run tests to verify they fail**

Run: `go test ./controller -run "TestUpdateOptionRejectsStripeEnabledWithoutRequiredConfig|TestGetOptionsHidesStripeSecrets" -v`

Expected: FAIL，因为新的 Stripe 配置键和校验逻辑尚未补齐。

**Step 3: Implement the minimal config changes**

在 `common/config/config.go` 中新增与 `new-api` 对齐的运行时变量：

```go
var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""
var StripeUnitPrice = 8.0
var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false
```

在 `model/option.go` 中：

- 在 `InitOptionMap` 中加入以上 key
- 在 `updateOptionMap` 中补充对应的 `switch` 赋值逻辑
- 保留已有 `StripePaymentEnabled`

在 `controller/option.go` 中将 Stripe 启用校验更新为：

- `StripePaymentEnabled == true` 时，要求 `StripeApiSecret`
- `StripeWebhookSecret`
- `StripePriceId`

至少都已配置

**Step 4: Run tests to verify they pass**

Run: `go test ./controller -run "TestUpdateOptionRejectsStripeEnabledWithoutRequiredConfig|TestGetOptionsHidesStripeSecrets" -v`

Expected: PASS

**Step 5: Commit**

```bash
git add common/config/config.go model/option.go controller/option.go controller/option_stripe_test.go
git commit -m "feat: add stripe payment options"
```

---

### Task 2: 新增 Stripe 充值控制器与用户路由

**Files:**
- Create: `D:\my\one-api\controller\topup_stripe.go`
- Modify: `D:\my\one-api\router\api-router.go`
- Create: `D:\my\one-api\controller\topup_stripe_test.go`
- Test: `D:\my\one-api\controller\topup_stripe_test.go`

**Step 1: Write the failing tests**

创建 `controller/topup_stripe_test.go`，先覆盖两个纯请求层行为：

```go
func TestRequestStripeAmountRejectsBelowMinTopUp(t *testing.T) {}

func TestRequestStripePayRejectsWhenStripeDisabled(t *testing.T) {}
```

测试重点：

- `StripePaymentEnabled=false` 时，请求直接失败
- `amount < StripeMinTopUp` 时，请求直接失败

**Step 2: Run tests to verify they fail**

Run: `go test ./controller -run "TestRequestStripeAmountRejectsBelowMinTopUp|TestRequestStripePayRejectsWhenStripeDisabled" -v`

Expected: FAIL，因为新控制器和路由尚不存在。

**Step 3: Implement the controller**

新增 `controller/topup_stripe.go`，实现以下内容：

```go
type StripePayRequest struct {
    Amount        int64  `json:"amount"`
    PaymentMethod string `json:"payment_method"`
    SuccessURL    string `json:"success_url,omitempty"`
    CancelURL     string `json:"cancel_url,omitempty"`
}

func RequestStripeAmount(c *gin.Context) { /* 读取 amount，校验最小值，返回应付金额 */ }

func RequestStripePay(c *gin.Context) { /* 校验配置与入参，创建本地 TopUp 订单，返回 pay_link */ }

func StripeWebhook(c *gin.Context) { /* 读取 raw body，验签，分发 completed / expired */ }
```

实现要求：

- 复用 `topup.go` 中的 `LockOrder` / `UnlockOrder`
- 新增 `getStripeAvailability()` 与 `getStripePayMoney()` 辅助函数
- 下单逻辑只依赖新配置键，不读旧 `StripePrivateKey` / `StripeEndpointSecret`

在 `router/api-router.go` 中新增：

```go
apiRouter.POST("/stripe/webhook", controller.StripeWebhook)
selfRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.RequestStripePay)
selfRoute.POST("/stripe/amount", controller.RequestStripeAmount)
```

**Step 4: Run tests to verify they pass**

Run: `go test ./controller -run "TestRequestStripeAmountRejectsBelowMinTopUp|TestRequestStripePayRejectsWhenStripeDisabled" -v`

Expected: PASS

**Step 5: Commit**

```bash
git add controller/topup_stripe.go controller/topup_stripe_test.go router/api-router.go
git commit -m "feat: add stripe topup endpoints"
```

---

### Task 3: 复用 `TopUp` 完成 Stripe 订单落库与回调幂等

**Files:**
- Modify: `D:\my\one-api\model\topup.go`
- Create: `D:\my\one-api\model\topup_stripe.go`
- Create: `D:\my\one-api\model\topup_stripe_test.go`
- Test: `D:\my\one-api\model\topup_stripe_test.go`

**Step 1: Write the failing tests**

创建 `model/topup_stripe_test.go`，至少覆盖：

```go
func TestCompleteStripeTopUpMarksOrderSuccessOnce(t *testing.T) {}

func TestExpireStripeTopUpMarksPendingOrderExpired(t *testing.T) {}
```

测试重点：

- 同一 `trade_no` 的成功回调重复执行时，只允许第一次真正加额度
- `pending` 订单收到过期事件时，状态改为 `expired`

**Step 2: Run tests to verify they fail**

Run: `go test ./model -run "TestCompleteStripeTopUpMarksOrderSuccessOnce|TestExpireStripeTopUpMarksPendingOrderExpired" -v`

Expected: FAIL，因为 Stripe TopUp 辅助函数尚未实现。

**Step 3: Implement the model helpers**

新增 `model/topup_stripe.go`，提供独立辅助函数，例如：

```go
func CreateStripeTopUp(userID int, amount int64, money float64, tradeNo string) error
func CompleteStripeTopUp(tradeNo string) error
func ExpireStripeTopUp(tradeNo string) error
```

实现要求：

- `CreateStripeTopUp` 统一写入 `TopUp{PaymentMethod: "stripe", Status: "pending"}`
- `CompleteStripeTopUp` 内部可复用 `CompleteTopUpOrder`，但应确保日志文案与支付方式可区分
- `ExpireStripeTopUp` 只把 `pending` 订单改为 `expired`
- 保持与易支付一致的额度换算逻辑：`Amount * QuotaPerUnit`

如果需要，可在 `model/topup.go` 中补一个通用的“按 tradeNo 将 pending 订单置为 expired”的辅助函数。

**Step 4: Run tests to verify they pass**

Run: `go test ./model -run "TestCompleteStripeTopUpMarksOrderSuccessOnce|TestExpireStripeTopUpMarksPendingOrderExpired" -v`

Expected: PASS

**Step 5: Commit**

```bash
git add model/topup.go model/topup_stripe.go model/topup_stripe_test.go
git commit -m "feat: reuse topup model for stripe payments"
```

---

### Task 4: 接入 Stripe Checkout Session 与 Webhook 分发

**Files:**
- Modify: `D:\my\one-api\controller\topup_stripe.go`
- Modify: `D:\my\one-api\go.mod` (only if imports need tidy)
- Modify: `D:\my\one-api\go.sum` (only if imports need tidy)
- Test: `D:\my\one-api\controller\topup_stripe_test.go`

**Step 1: Write the failing tests**

在已有 `controller/topup_stripe_test.go` 中补充针对控制器辅助函数的测试：

```go
func TestGetStripeAvailabilityRequiresAllConfiguredFields(t *testing.T) {}

func TestStripeWebhookRejectsInvalidSignature(t *testing.T) {}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./controller -run "TestGetStripeAvailabilityRequiresAllConfiguredFields|TestStripeWebhookRejectsInvalidSignature" -v`

Expected: FAIL，因为 Stripe Session 创建和 Webhook 验签逻辑尚未接入。

**Step 3: Implement the Stripe integration**

在 `controller/topup_stripe.go` 中：

- 使用 `github.com/stripe/stripe-go/v78`
- 设置 `stripe.Key = config.StripeApiSecret`
- 通过 `checkout/session.New` 创建 Session
- `ClientReferenceID` 使用本地 `trade_no`
- `AllowPromotionCodes` 读取 `StripePromotionCodesEnabled`
- 默认跳转地址优先使用前端传入，否则退回 `config.ServerAddress`

Webhook 部分：

- 使用 `io.ReadAll(c.Request.Body)` 读取原始请求体
- 使用 `webhook.ConstructEventWithOptions` 验签
- 处理：
  - `checkout.session.completed`
  - `checkout.session.expired`
- `completed` 时调用 `model.CompleteStripeTopUp`
- `expired` 时调用 `model.ExpireStripeTopUp`

**Step 4: Run tests to verify they pass**

Run: `go test ./controller -run "TestGetStripeAvailabilityRequiresAllConfiguredFields|TestStripeWebhookRejectsInvalidSignature" -v`

Expected: PASS

**Step 5: Commit**

```bash
git add controller/topup_stripe.go go.mod go.sum
git commit -m "feat: integrate stripe checkout and webhook"
```

---

### Task 5: 在 `ezlinkai-web` 增加 Stripe 支付设置卡片

**Files:**
- Modify: `D:\my\ezlinkai-web\sections\setting\view\paymentSettingPage.tsx`
- Modify: `D:\my\ezlinkai-web\app\dashboard\setting\payment\page.tsx` (only if page metadata/text needs update)

**Step 1: Capture the current behavior**

先记录页面当前能力：

- 只显示易支付
- 通过 `/api/option` 加载和保存
- `EpayKey` 不回显，仅输入时更新

**Step 2: Implement the Stripe form state**

在 `paymentSettingPage.tsx` 中新增以下 state：

```tsx
const [stripePaymentEnabled, setStripePaymentEnabled] = useState(false);
const [stripeApiSecret, setStripeApiSecret] = useState('');
const [stripeWebhookSecret, setStripeWebhookSecret] = useState('');
const [stripePriceId, setStripePriceId] = useState('');
const [stripeUnitPrice, setStripeUnitPrice] = useState('8');
const [stripeMinTopUp, setStripeMinTopUp] = useState('1');
const [stripePromotionCodesEnabled, setStripePromotionCodesEnabled] = useState(false);
```

在 `fetchOptions()` 中读取：

- `StripePaymentEnabled`
- `StripePriceId`
- `StripeUnitPrice`
- `StripeMinTopUp`
- `StripePromotionCodesEnabled`

并将两个 secret 字段在加载后重置为空字符串。

**Step 3: Implement the save flow**

在 `handleSave()` 中新增：

```tsx
const stripeOptionsToSave = [
  { key: 'StripePaymentEnabled', value: stripePaymentEnabled.toString() },
  { key: 'StripePriceId', value: stripePriceId.trim() },
  { key: 'StripeUnitPrice', value: stripeUnitPrice.trim() || '8' },
  { key: 'StripeMinTopUp', value: stripeMinTopUp.trim() || '1' },
  {
    key: 'StripePromotionCodesEnabled',
    value: stripePromotionCodesEnabled.toString()
  }
];
```

并在用户有输入时才保存：

- `StripeApiSecret`
- `StripeWebhookSecret`

**Step 4: Implement the UI card**

在现有易支付卡片后新增一个 Stripe 卡片，字段布局参考当前页的“开关卡片 + 参数卡片 + 说明卡片”风格，确保：

- 手机端单列
- 桌面端双列/三列
- 密钥字段使用 `type="password"`
- 提示文案明确“留空不更新”

**Step 5: Run frontend verification**

Run: `npm run lint`

Working directory: `D:\my\ezlinkai-web`

Expected: PASS

Run: `npm run build`

Working directory: `D:\my\ezlinkai-web`

Expected: PASS

**Step 6: Commit**

```bash
git add sections/setting/view/paymentSettingPage.tsx app/dashboard/setting/payment/page.tsx
git commit -m "feat: add stripe payment settings page"
```

---

### Task 6: 做联调验证并确认旧流程未受影响

**Files:**
- Verify only: `D:\my\one-api`
- Verify only: `D:\my\ezlinkai-web`

**Step 1: Run backend test suite for touched packages**

Run: `go test ./controller ./model -v`

Working directory: `D:\my\one-api`

Expected: PASS

**Step 2: Run targeted frontend checks**

Run: `npm run lint && npm run build`

Working directory: `D:\my\ezlinkai-web`

Expected: PASS

**Step 3: Manual verification**

手工检查以下路径：

1. 以 root 身份进入 `dashboard/setting/payment`
2. 确认 Stripe 卡片显示且可保存非敏感字段
3. 刷新页面后确认 `StripeApiSecret` 与 `StripeWebhookSecret` 不回显
4. 将 `StripePaymentEnabled` 打开但故意缺少 Price ID，确认后端阻止启用
5. 访问旧充值页 `dashboard/topup`，确认旧 Stripe / 易支付按钮仍可正常渲染
6. 如有测试环境 Stripe 配置，调用新 `/api/user/stripe/pay` 并检查是否生成 `pay_link`

**Step 4: Commit verification-safe follow-up changes if needed**

```bash
git add .
git commit -m "fix: polish stripe payment rollout"
```

---

## Notes

- 本计划明确要求**保留旧 `/api/charge/*` 逻辑**，直到 `dashboard/topup` 在后续阶段切换完成。
- 若在实现中发现 `TopUp` 现有字段不足以承载 Stripe 元数据，可追加极小字段扩展，但不要把旧 `charge_order` 逻辑与新链路混在同一个完成流程中。
- 前端仓库目前没有现成测试框架，本次以 `lint + build + 手工验证` 作为 UI 验证手段。

## Execution Handoff

Plan complete and saved to `docs/plans/2026-03-23-one-api-stripe-payment.md`. Two execution options:

**1. Subagent-Driven (this session)** - 我在当前会话里按任务逐步实现，每完成一块就回顾与验证。

**2. Parallel Session (separate)** - 你开一个新的执行会话，按这份计划批量推进。

Which approach?
