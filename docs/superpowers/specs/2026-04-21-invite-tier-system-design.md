# 邀请返利 + 等级自动升级系统 设计 spec

**日期**：2026-04-21
**相关 commit**：`9d5bf758`（计费三段折扣系统落地）之后

## Context

现有状态是"有地基没厨房"：
- `User.AffCode`（4 字符随机）、`User.InviterId`、`User.Group` 已存在
- 注册接口会读 body 里的 `affCode` 建立邀请关系
- config 里有 `QuotaForInviter` / `QuotaForInvitee` 在注册时一次性发额度
- `controller/stripeCharge.go:UserLevelUpgrade` 里埋了一段 **Stripe 专属**、**硬编码阈值** 的升级逻辑（Lv2: 2.5M / Lv3: 25M / Lv4: 50M / Lv5: 125M quota）
- 刚落地了 `GroupConfig` 表（Lv1-Lv6 + 可配置 Discount 乘数）

断掉的链条：
1. 前端注册页**完全不读** `?aff=xxx` URL 参数——所有通过邀请链接点进来的用户都丢失了邀请关系
2. 升级逻辑只在 Stripe 回调里跑，其他渠道充值（支付宝 / 兑换码 / 手工）**永远不会触发升级**
3. 升级阈值硬编码在 Go 里，不能调
4. 邀请人没有"持续返利"激励——只有被动一次性奖励
5. 前端"我的邀请"卡片展示的是 `$0 / $0 / 0 users` 硬编码假数据

## 目标

让整条邀请链路跑通并变成可配置的系统：
- 注册时自动捕获 aff 码（修前端 URL 参数 bug）
- 用户升级基于 "累计充值金额" 或 "有效邀请人数"（任一满足即升）
- 阈值在已有的 `GroupConfig` 管理后台可配置
- 邀请人拿"一次性奖励 + 永久 commission"双轨返利
- 前端"我的邀请"显示真实数据 + "距离下一级还差"提示

## 设计选择（已与用户对齐）

| 决策点 | 选择 | 备注 |
|---|---|---|
| "1 个有效邀请" | 被邀请者**注册 + 首次充值**才算 | 防刷僵尸号 |
| "升级金额"的含义 | **累计充值** (`TotalTopup`) | 单调递增，不会回撤 |
| 升/降级 | **只升不降**（终身锁定） | 事件驱动，无需 cron |
| 返利模型 | **一次性 + 永久 commission**（newapi 模型） | 同时保留短期回报和长期激励 |
| 邀请码格式 | **6 位随机字符** + 注册时碰撞重试 | 从现有 4 位升级，支撑到 ~3 万用户无压力 |

## § 1 — 数据模型变更

### `User` 新增 6 个字段

| 字段 | 类型 | GORM tag 要点 | 含义 |
|---|---|---|---|
| `TotalTopup` | `int64` | `default:0;index` | 累计充值金额（quota 单位，`500000 = $1`），**只增** |
| `HasPaid` | `bool` | `default:false;index` | 是否完成过至少 1 次真实充值 |
| `FirstPayAt` | `int64` | `default:0` | 首充 Unix 时间戳，用于"1 个有效邀请"判定 |
| `AffCount` | `int` | `default:0` | **有效**邀请数缓存（派生字段，用于快速查询 + 升级判定） |
| `AffQuota` | `int64` | `default:0` | 邀请 commission 可提现余额 |
| `AffHistoryQuota` | `int64` | `default:0` | 累计获得 commission 的历史总额（统计展示用，不递减） |

**为什么 `AffCount` 作为冗余字段而不是聚合查询？**
- 升级判定发生在每次充值 hook，想在 `evaluate_upgrade` 里做
  `count(*) where inviter_id=me AND has_paid=true` 会随用户量变慢
- 有效邀请 +1 的触发点有且仅有"被邀请者首充"——可精确原子递增，没有一致性漂移风险

**邀请码字段变更**：`User.AffCode` 保留原字段，但生成逻辑改为 6 位随机 + 注册时最多 3 次碰撞重试；历史 4 位 AffCode 不动（仍然可用），只是新生成的都是 6 位。

### `GroupConfig` 扩列（不新建表）

| 新字段 | 类型 | 含义 |
|---|---|---|
| `UpgradeThresholdTopup` | `int64`, `default:0` | 升到本等级需累计充值 ≥ 此值（quota 单位） |
| `UpgradeThresholdInvitees` | `int`, `default:0` | 或 / 满足有效邀请 ≥ 此值 |

**语义**：`threshold = 0` 表示"不通过此指标升级"。同时为 0 的等级（比如 Lv1 基础级）永远不会被 evaluate 升进去——它只通过降级或默认赋值。

### 新 option 配置项（option 表）

| Key | 类型 | 默认 | 含义 |
|---|---|---|---|
| `AffCommissionRate` | float 0-1 | `0` | 被邀请者每笔充值，这个比例的金额进邀请人 `AffQuota` |
| `AffMinWithdraw` | int64 | `500000` (=$1) | 邀请人从 `AffQuota` 提现到 `Quota` 的最小额度 |
| `QuotaForInviter` / `QuotaForInvitee` | int64 | 沿用现有值 | 一次性奖励——**发放时机改为被邀请者首充时**（见 §3） |

### 不新增的东西
- ❌ 单独的 `aff_history` / `invite_records` 表——所有 commission 发放 / 提现 / 升级事件都写进现有 `logs` 表（`LogType` 枚举新增 `LogTypeAffCommission`、`LogTypeAffWithdraw`、`LogTypeTierUpgrade`）

## § 2 — 事件驱动的升级引擎

**设计原则**：升级评估发生在有限的事件点上，不起 cron，不做全表扫描。

### 唯一的业务入口：`TopupSuccessHook(userId, amount)`

所有充值渠道（Stripe / 支付宝 / 兑换码 / 手工调额）成功后必须调用此 hook。伪代码：

```go
func TopupSuccessHook(ctx context.Context, userId int, amount int64) error {
    user := GetUserById(userId)

    // 1. 累计充值 + 首充标记
    user.TotalTopup += amount
    firstTopup := !user.HasPaid
    if firstTopup {
        user.HasPaid = true
        user.FirstPayAt = time.Now().Unix()
    }

    // 2. 如有邀请人：发一次性奖励 + commission + 邀请人 effective invite +1
    if user.InviterId > 0 {
        inviter := GetUserById(user.InviterId)

        // 2a. 被邀请者首充 → 发一次性奖励（挪自原"注册时"）
        if firstTopup {
            if config.QuotaForInviter > 0 {
                inviter.Quota += config.QuotaForInviter
                RecordLog(inviter.Id, LogTypeSystem, "邀请好友首充奖励")
            }
            if config.QuotaForInvitee > 0 {
                user.Quota += config.QuotaForInvitee
                RecordLog(user.Id, LogTypeSystem, "使用邀请码首充奖励")
            }
            inviter.AffCount += 1
            EvaluateUpgrade(inviter)  // 邀请人可能因 AffCount 升级
        }

        // 2b. 无论是否首充都发 commission（按本次充值金额）
        if rate := config.AffCommissionRate; rate > 0 {
            commission := int64(float64(amount) * rate)
            inviter.AffQuota += commission
            inviter.AffHistoryQuota += commission
            RecordLog(inviter.Id, LogTypeAffCommission,
                fmt.Sprintf("好友充值返利 +%s (from user %d)",
                    common.LogQuota(commission), user.Id))
        }
        SaveUser(inviter)
    }

    // 3. 自己可能因 TotalTopup 升级
    EvaluateUpgrade(user)
    SaveUser(user)
    return nil
}
```

### 升级评估函数：`EvaluateUpgrade(user)`

```go
func EvaluateUpgrade(user *User) {
    configs := GetAllGroupConfigsOrderedBySortDesc()  // Lv6 → Lv1
    for _, cfg := range configs {
        hitTopup := cfg.UpgradeThresholdTopup > 0 &&
                    user.TotalTopup >= cfg.UpgradeThresholdTopup
        hitInvite := cfg.UpgradeThresholdInvitees > 0 &&
                     user.AffCount >= cfg.UpgradeThresholdInvitees
        if !(hitTopup || hitInvite) {
            continue
        }
        // 只升不降：比较 SortOrder，新等级更高才接受
        if cfg.SortOrder > currentSortOrder(user.Group) {
            old := user.Group
            user.Group = cfg.GroupKey
            RecordLog(user.Id, LogTypeTierUpgrade,
                fmt.Sprintf("等级升级 %s → %s", old, user.Group))
            InvalidateUserCache(user.Id)  // 让计费立刻看到新等级
        }
        break  // 只匹配最高可达等级，break
    }
}
```

### 废弃 `controller/stripeCharge.go:UserLevelUpgrade`

- 把 Stripe 回调里原本调 `UserLevelUpgrade(userId)` 的地方改为调 `TopupSuccessHook(ctx, userId, amount)`
- 其他充值入口（支付宝 / 兑换码 / 手工）同步接入这个 hook
- `UserLevelUpgrade` 函数及其硬编码阈值常量删除（保留一个迁移 commit 证据）

### 管理员手动改 User.Group 的行为

- 管理员可以直接通过 `UpdateUser` 接口把 user.Group 改到任意等级（升或降）
- 下次该用户充值触发 `EvaluateUpgrade` 时，引擎只检查"当前等级 SortOrder 是否低于应得等级"；如果管理员把用户调到了**比应得更高**的等级，引擎不会降它；如果管理员把用户**调低**到应得以下，下次充值会把用户升回应得等级
- 这个行为是故意的——管理员要做"永久降级"请改阈值或改 `AffCommissionRate`，不要手动改用户等级

## § 3 — 返利流程

### 两层并存

**一次性奖励**（现有 `QuotaForInviter` / `QuotaForInvitee`）：
- **发放时机变更**：从"邀请者注册成功时"挪到"**被邀请者首充成功时**"
- 原因：防止注册即刷小号套奖励（现在用户大量反映的 pain point）
- 失效条件：被邀请者注册后**永不充值** → 一次性奖励永远不发，反正对平台无损

**永久 commission**（新增）：
- 被邀请者每次充值（**包括首次充值**）都按 `AffCommissionRate` 的比例进入邀请人 `AffQuota`
- 不限次数、不随时间衰减
- 不受被邀请者后续等级影响
- `AffCommissionRate = 0` 时（默认），系统行为退化为"只发一次性奖励"

### 提现流程

- 邀请人在"我的邀请"页看到 `AffQuota` 余额
- 点"提现到账户余额" → 若 `AffQuota >= AffMinWithdraw`，`AffQuota -= X; Quota += X`；写 `LogTypeAffWithdraw`
- **不支持** 提现到微信/支付宝 / 银行卡（不做法币提现，范围外）

## § 4 — 管理后台 + 前端

### 管理后台修改

**`/dashboard/setting/discount` (折扣设置页)** — 已存在，编辑弹窗加 2 个 input：
- "升级充值门槛"：`UpgradeThresholdTopup`（美元单位展示、保存时 ×500000 转 quota 单位）
- "升级邀请门槛"：`UpgradeThresholdInvitees`（整数）
- 帮助文案："达到任一条件即升级"

**`/dashboard/setting` 系统设置页** — 在邀请返利面板新增：
- "邀请返利比例" `AffCommissionRate`（百分比 0-100，保存时 ÷100）
- "提现最小额度" `AffMinWithdraw`（美元输入）
- 把 `QuotaForInviter` / `QuotaForInvitee` 挪到同一面板，加说明"**被邀请者首充时发放**"

### 前端 4 处修改（`ezlinkai-web-next`）

1. **`sections/auth/user-auth-form.tsx` 注册表单**：
   - `useSearchParams()` 读 `aff` 参数
   - 写入 form 隐藏字段 `affCode`
   - 表单 submit 时放进 POST body（和现有后端契合）

2. **`sections/topup/invite-card.tsx`**：
   - 接 `/api/user/self` 返回的 `aff_count` / `aff_quota` / `aff_history_quota`
   - "Available Earnings" → `aff_quota`（可提现）
   - "Total Earnings" → `aff_history_quota`（历史累计）
   - "Invited Users" → `aff_count`
   - 加"提现"按钮 → POST `/api/user/aff/withdraw`

3. **新增"我的邀请人"页面** `app/dashboard/invites/page.tsx`：
   - 展示 `inviter_id = me` 的用户列表（脱敏 email + 首充状态 + 注册时间）
   - 分页，后端新 API `GET /api/user/invitees?page=&size=`

4. **等级进度卡片**（放在充值页或 profile 页）：
   - 当前等级 `user.group`（查 `GroupConfig` 拿 `DisplayName`）
   - 下一级的 `UpgradeThresholdTopup` / `UpgradeThresholdInvitees`
   - 进度条 + "距离下一级：再充 $X 或再邀请 Y 人"

## § 5 — 老用户迁移

一次性迁移脚本，`model/migrations/2026_04_21_invite_tier_backfill.go`，在 DB migrate 之后调用。**幂等**：通过 `migration_history` 表（不存在则建）记录 `2026_04_21_invite_tier_backfill` 是否已执行，重复调用跳过。

```
1. 遍历 logs 表里 type=LogTypeTopup 的记录
   按 user_id 聚合 sum(quota) → user.TotalTopup
   取 min(created_at) → user.FirstPayAt
   user.HasPaid = (TotalTopup > 0)

2. 遍历 users 表
   user.AffCount = count(SELECT FROM users u2
                         WHERE u2.inviter_id = user.id
                         AND u2.has_paid = true)

3. AffQuota / AffHistoryQuota 不回填
   理由：commission 规则是新开的，历史不追溯，口径清晰

4. 对每个 user 调一次 EvaluateUpgrade(user)
   把能升的都批量升上去（此时不需要 InvalidateUserCache，下次登录自然读新值）

5. 日志：打印迁移前后的等级分布统计
```

**回滚策略**：迁移脚本的执行记录写到一个 `migration_history` 表，若出问题可以按 user_id 清掉新字段（TotalTopup=0, HasPaid=false, AffCount=0）然后手动处理。`Group` 字段的回滚需要业务确认——一般不回滚。

## § 6 — YAGNI（故意不做）

| 特性 | 不做理由 |
|---|---|
| 降级 / 滑动窗口重评 | 用户明确只升不降；架构可省掉 cron 和窗口字段 |
| 多级返利（邀请人的邀请人也分成） | 金字塔模式有合规/道德风险，本版不做 |
| 自定义邀请码 | 6 位随机 + uniqueIndex 足够，自定义带来碰撞/敏感词/抢注等次生问题 |
| 邀请活动期临时加倍 | 后期可加 `AffCommissionRateOverride` 时间窗，本版不做 |
| 邀请链接二维码 / 社媒卡片分享 | 单纯 URL 链接已够，本版不做 |
| AffQuota 提现到微信/支付宝 | 超出范围，本版纯 Quota 闭环 |

## 验证（手测 checklist）

实施完成后：

1. **注册链路**：
   - 浏览器访问 `/sign-in?aff=ABC123` → 注册新账号 → 新账号 `InviterId` 正确指向 ABC123 的拥有者 ✓
   - 邀请码错误时静默忽略（`InviterId = 0`），不阻塞注册 ✓

2. **首充奖励**：
   - 新账号首次充值 → 邀请人 `AffCount +1`、`Quota += QuotaForInviter`、新账号 `Quota += QuotaForInvitee` ✓
   - 新账号**第二次**充值 → 不再给一次性奖励，只给 commission ✓

3. **Commission**：
   - `AffCommissionRate = 0.1`（10%）时，被邀请者充 $10 → 邀请人 `AffQuota += $1`、`AffHistoryQuota += $1` ✓
   - logs 里能看到 `LogTypeAffCommission` 记录 ✓

4. **等级升级**：
   - 管理后台把 Lv2 的 `UpgradeThresholdTopup` 设为 $5、`UpgradeThresholdInvitees` 设为 3
   - 用户累计充值到 $5 → 自动从 Lv1 升到 Lv2 ✓
   - 另一个用户邀请 3 人首充 → 自动升到 Lv2 ✓
   - 升级后"距离下一级"提示刷新 ✓

5. **提现**：
   - 邀请人 `AffQuota` 点提现 → `Quota += AffQuota`、`AffQuota = 0`，logs 里有 `LogTypeAffWithdraw` ✓
   - `AffQuota < AffMinWithdraw` 时按钮禁用 + 提示 ✓

6. **老用户迁移**：
   - 迁移前后对若干历史用户抽查：`TotalTopup` 等于 logs 聚合值、`AffCount` 等于实际已付费邀请数、合格用户的 `Group` 被提升 ✓

## 关键文件清单

### 后端（`ezlinkai`）
- `model/user.go` — User 结构体加 6 字段；`Insert` 改 6 位随机 + 碰撞重试
- `model/group_config.go` — GroupConfig 加 2 字段
- `model/topup_hook.go`（新） — `TopupSuccessHook` + `EvaluateUpgrade`
- `model/migrations/2026_04_21_invite_tier_backfill.go`（新）
- `controller/user.go` — 注册时不再发一次性奖励（挪到 hook）
- `controller/stripeCharge.go` — 删 `UserLevelUpgrade`，改调 `TopupSuccessHook`
- `controller/topup.go`、兑换码回调等 — 接入 `TopupSuccessHook`
- `controller/aff.go`（新） — 提现接口、邀请人列表接口
- `model/log.go` — 新增 LogType 常量
- `common/config/config.go` — 新增 `AffCommissionRate` / `AffMinWithdraw`
- `router/api-router.go` — 挂新路由 `/api/user/aff/withdraw`、`/api/user/invitees`

### 前端（`ezlinkai-web-next`）
- `sections/auth/user-auth-form.tsx` — URL `?aff=` 捕获
- `sections/topup/invite-card.tsx` — 接真实数据 + 提现按钮
- `sections/user/invitees-list.tsx`（新） — 邀请人列表
- `components/tier-progress-card.tsx`（新） — 等级进度卡
- `locales/{zh,en}.ts` — 新增 key（invite/tier progress/withdraw 相关）
- `sections/setting/view/discountPage.tsx` — GroupConfig 编辑弹窗加 2 input
- `sections/setting/view/systemPage.tsx`（或类似） — 返利面板新增 3 个 option

## 回滚

- 所有 DB schema 变更都是 `ALTER TABLE ADD COLUMN`（非破坏性），回滚只需 revert 代码 + 留下未使用的列
- 现有 `UserLevelUpgrade` 若替换后发现问题，可短期内 Git revert 恢复 Stripe 路径
- 迁移脚本通过 `migration_history` 表判断是否已运行，幂等
