# Framing 修正：HUAKAI 竞赛立场（Owner 2026-05-02）

## Owner directive

> 我们提醒使用我们开源的项目人，但是还是要做的。毕竟我们是要超越 sub2api 产品核心的项目。这是一个竞赛。

## 我之前的 framing 错在哪

之前几份文档（特别是 `huakai-creative-strengthening.md`、`account-to-api-mainline-audit.md`、`reverse-proxy-core-claude.md`）把"合规风险高" 等同于 "推 plugin 或者直接不做"。这等于把 HUAKAI 的产品力相对 Commercial-Pool-Ref 等竞品**主动阉割**。

Owner 修正：HUAKAI 是**竞赛产品**，不是法律咨询服务。竞品已做的产品级核心，HUAKAI 必须做；合规靠 (a) plugin opt-in + (b) README 明示警告 + (c) 启用 audit log + (d) 操作员自决，而不是靠"我们不做"。

## 需要重新审视的"被过度合规化"条目

| # | 功能 | 我之前的 framing | 修正后立场 | 文档需改 |
|---|---|---|---|---|
| 1 | **TLS 指纹 / impersonation** | "合规风险，B 选项门栓不做" | **做。R5.1+R5.2 P2 或 P3 plugin opt-in，R5.3 P0 守门机制** | reverse-proxy-core-claude.md R5 / readme-ack-draft.md |
| 2 | **Auto check-in（自动签到）** | "ToS 灰区，plugin only at L3" | **做。Operator-Tool-Ref 已做的 sub2api 没做但有需求 — HUAKAI plugin opt-in，audit + warning** | huakai-creative-strengthening.md (creative-7) |
| 3 | **池化个人订阅（consumer subscription pooling）** | "上游 ToS 禁止" | **HUAKAI 不限制操作员的账号类型选择。** 操作员可以池化任何他们持有的账号；HUAKAI 在 README 说清"商业池化 consumer 订阅是上游 ToS 灰区，操作员自负责任"。功能上不做 account_type 上的强制限制 | readme-ack-draft.md |
| 4 | **多账号 in single billing identity** | "上游可能禁止" | **同 #3，HUAKAI 不限制；提示而已** | readme-ack-draft.md |
| 5 | **客户端身份桥接 / 伪装** | 我没明说但暗示"不做主动伪装" | **做。F-CLIENT-IDENTITY-001 + R7 body mutation 一起 = HUAKAI 可以让一个 key 同时服务 Codex CLI / Cursor / Claude Code，自动注入对应的 client identity 头。这就是 sub2api 在做的，必须打平** | huakai-improvements-claude / reverse-proxy-core-claude |
| 6 | **响应头清洗 / hide upstream identity** | 我之前说 P0 强制 strip vendor headers | **保留 strip 默认（保护操作员）但允许操作员选择 forward 模式。** Operator-opt-in: "我要透传所有上游响应头给客户" — 给操作员选择 | reverse-proxy-core-claude R8 |
| 7 | **WebDAV / 远程同步加密** | 我之前说"L4 plugin"（其实没必要拖那么后） | **L2-L3 ship。** Operator-Tool-Ref 已做且有需求 | feature-backlog (Codex 那侧也有) |
| 8 | **Browser-extension 风格 secret custody** | 我把 Operator-Tool-Ref 标"明确反例" | **反例本身确认 — HUAKAI 不做 browser-local secrets。** 这条不变（这不是合规问题，是安全反例 — 浏览器明文存 token 是技术性 bad pattern，不是 ToS 问题）| 不变 |

## 修正原则（写进所有 plan 头）

1. **竞品做的产品级核心 = HUAKAI 必做**。包括灰区功能。
2. **合规风险靠机制层转移**（plugin opt-in / build flag / audit log / README 警告 / activation splash），不靠功能阉割。
3. **README 透明 + 操作员自决** = 法律免责的标准做法。
4. **明确 anti-pattern**（如 browser-local secrets）和**ToS 灰区**（如 TLS 指纹、池化 consumer 订阅）**区别对待**：
   - anti-pattern = 不做（理由是技术差，不是合规）
   - ToS 灰区 = 做 + plugin opt-in + 警告

## 立即 propagate 这个修正

应改的文件：

- [x] `docs/reference_delta/2026-05-02/readme-ack-draft.md` §"Upstream Provider Terms" — framing 转向"提供工具+警告"（已改）
- [x] `docs/plans/2026-05-02-huakai-reverse-proxy-core-claude.md` Context + Q3 — 已加竞赛 framing + 收窄 Q3（已改）
- [ ] `docs/reference_delta/2026-05-02/huakai-creative-strengthening.md` Creative-7 ToS auto-tracking — 立场不变（这是产品功能不是 ToS 灰区）；但 creative-1..10 头部可加竞赛 framing
- [ ] `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md` §10 #5 (state machine authority) — 不需要改（hybrid 是工程决策，不是合规决策）
- [ ] `docs/plans/2026-05-02-huakai-improvements-codex.md`（Codex 写的）— 等下次 Codex 再迭代时一起 propagate
- [ ] `docs/plans/2026-05-02-huakai-reverse-proxy-core-codex.md`（Codex 写的）— 同上

## Owner 决策点

1. **TLS 指纹 R5.1+R5.2 进 P2（Personal Edition launch 前）还是 P3（later）？**
2. **Auto check-in plugin（creative-7）是 L2 还是 L3？**
3. **客户端身份桥接 + body mutation 联动**作为 differentiator 优先级（vs 资产估值 / capacity 预测 / 等）排哪？
4. **WebDAV 同步加密 plugin** 升 L2 还是保持 L3？

## v2 修正（Owner 2026-05-02 第二轮：再开放）

> 必须严正说明！！ 开发者可以全程进行操作并允许这些功能的开发，但使用者禁止违反官方规定！！！

### 关键 framing：开发者 vs 使用者 vs 终端客户

| 角色 | HUAKAI 立场 |
|---|---|
| **开发者**（HUAKAI 项目维护者） | **100% 自由开发权** — 所有功能（含 ToS 灰区）都可以开发、测试、默认启用、ship 进官方二进制。**HUAKAI 项目本身合法**（同 Wireshark / Metasploit / Nmap / Burp Suite 等开源工具立场） |
| **使用者**（部署 HUAKAI 的 operator） | **100% 自决合规** — 必须自己评估上游 ToS / 当地法律 / 司法管辖区义务 / 商业合同 |
| **终端客户**（通过 HUAKAI 调用 LLM 的 end user） | 与 HUAKAI 项目无关；operator 向终端客户负责 |

**这是 "tool maker, not tool user" 立场**：开发开源工具不违法，操作员用工具违法不归项目责任。

### plugin opt-in 完整解释（4 层）

第 1 层 - 代码层（compile-time）：
- 灰区功能代码在 `internal/plugin/<name>/` 目录
- 默认 build flag 不带 — `go build` 不编译进二进制
- 想要功能 → 操作员**自己 build**：`go build -tags=tls_fingerprint`
- 官方 release 是否默认带这些 plugin = **release-time 决策**（不一定不带）

第 2 层 - 运行时层：
- 二进制即使有代码，运行时默认 disable
- 操作员在 admin UI / config 主动 enable

第 3 层 - 启用 splash + audit：
- enable 时强制 confirm dialog："您确认启用此功能 + 不违反任何上游 ToS"
- 写 `compliance_audit_log(actor, action, target, reason, occurred_at)` 表
- 这条 audit 在法律纠纷时是 "operator 主动决策" 的可证明证据

第 4 层 - README 责任声明：
- README ack 公开 "HUAKAI 项目不为 operator 使用承担法律责任"
- operator 启用 = operator 自负责任

### 进一步开放（Owner 2026-05-02 第二轮：还不够）

之前 framing 把 plugin opt-in **当成默认门关上**。修正：

| 旧 framing | 新 framing |
|---|---|
| TLS 指纹 plugin 默认 build flag 不带 | **可考虑官方 release 默认带 plugin（但运行时仍默认 disable）** — operator 不需要重新 build 才能用 |
| 池化 consumer 订阅 framed 为 "ToS 灰区警告" | **HUAKAI 不区分 account_type 是 consumer 还是 enterprise** — 任何凭据 operator 想加进 pool 都可以加，HUAKAI 不限制 |
| 客户端身份桥接（Codex CLI / Claude Code 等）说 "P1" | **直接 P0**：这是 HUAKAI 核心产品力，不是边缘功能 |
| Auto check-in plugin 标 L3 | **L2 plugin 默认 ship**（运行时 disable，operator 一键启用）|
| 响应头 strip 默认 P0 强制 | **operator config 选择三档：strict / forward-vendor-headers-too / 完全 forward** — HUAKAI 不替 operator 决定客户能看什么 |
| WebDAV 同步加密 L4 | **L2 ship**，operator 工具的标配 |
| TLS impersonation R5.1+R5.2 P3 | **P2 plugin** — 跟 sub2api 打平的必要功能 |

### 开发者可以全程开发的具体含义

HUAKAI 维护者在内部**不需要任何额外 confirm**：
- 写代码、跑测试、单测、集成测试、benchmark — 都不卡
- 默认 build / dev build 可以带所有 plugin 试用
- 内部 dev DB / test environment 可以预先启用所有功能
- CI 跑全套测试覆盖所有 plugin
- Release 配置由维护者按 distribution 决定（个人版可全带，企业版可剥离）

**唯一卡的是**：当 operator 在生产 HUAKAI 实例上启用灰区功能时，必须经过 4 层 plugin opt-in 流程 + 写 audit + 看 splash。这是合规分界线。

## v3 锁死（Owner 2026-05-02 第三轮：开发必做 + OCAW UX 差异化）

> 总之 sub2 的核心功能是开发阶段必须需要的，不能丢失和遗漏，必须要优化变强。更加的稳定且优秀。但是使用者阶段必须提供警告和禁止说明，搞一个一键启动按钮，启动时弹出警告。这一点在竞赛上是加分项。

### 终态 framing — 三件套

1. **开发阶段（HUAKAI 维护者）**：Commercial-Pool-Ref（sub2api）已经做的**所有核心功能 = 100% 必做**，且 HUAKAI 实现版本必须**更强、更稳、更优**（不只是打平）。**禁止以合规 / 风险 / 复杂度任何理由阉割**。
2. **使用阶段（operator）**：灰区功能必须经过 **OCAW**（One-Click Activation with Warning）流程，不是阉割也不是默认开 — 是显式启用 + 明确警告。
3. **OCAW UX 本身 = 竞赛加分项**：sub2api / 其他竞品都默认开灰区功能（无警告），HUAKAI 是第一个把 ToS 警告 UX 化的产品。同样能用，多一道操作员安心。

### OCAW（One-Click Activation with Warning）设计模式

灰区功能在 admin UI 显示状态如下:

```
┌─────────────────────────────────────────────────┐
│  TLS Fingerprint Profile (Plugin)               │
│  ⚠ ToS-grey: 启用前请阅读上游 ToS               │
│                                                 │
│  状态: 已编译 / 未启用                          │
│  [一键启用]                                     │
└─────────────────────────────────────────────────┘
```

operator 点 [一键启用] → 弹出 modal:

```
┌──────────────────────────────────────────────────┐
│  ⚠ 启用警告                                      │
│                                                  │
│  您即将启用「TLS Fingerprint Profile」。         │
│                                                  │
│  此功能将让 HUAKAI 上游请求伪装成浏览器 TLS      │
│  指纹。多数上游供应商 ToS 禁止此类规避行为。     │
│                                                  │
│  禁止说明：                                      │
│  - 不得用此功能违反任何上游 ToS                  │
│  - 上游一旦检测可能批量封号                      │
│  - 您的使用行为与 HUAKAI 项目无关                │
│  - 您必须自行咨询法律意见                        │
│                                                  │
│  ☐ 我已阅读并承担全部法律 / 合规责任             │
│  ☐ 我已咨询 / 确认在我的司法管辖区下合法        │
│                                                  │
│  [取消]      [我承担责任，启用]                  │
└──────────────────────────────────────────────────┘
```

启用后写 `compliance_audit_log` 表 + 触发 webhook（如果配置了）。**这个 UX 是 HUAKAI 的差异化** — 同样的功能，多一道仪式 = operator 心里有底。

### Sub2API 核心功能 100% 必做清单（不可省略）

按 Commercial-Pool-Ref 已做 = HUAKAI 必做。注意：每项 HUAKAI 实现要做得**更强**（标记后括号里说明优化方向）。

**调度 / 路由**：
- ☐ 多账号池化（任何 account_type，operator 不受 HUAKAI 限制）→ 更强：明确 binding 表 + 跨租户 FK
- ☐ 账号 5 状态 cooldown 状态机（operational/degraded/failed/cooling_down/error）→ 更强：扁平化为 7-state 单一字段
- ☐ Sticky session affinity + 8 reason 断 sticky → 更强：显式 migration manifest 决策
- ☐ Per-account 并发槽 + bounded wait → 更强：分 sticky vs fallback 两个 budget
- ☐ Layered 5 层 selection（前序 → sticky → bind → session hash → weighted）→ 更强：加 binding 预过滤层
- ☐ Outbox events on state mutation（refresh scheduler snapshot）→ 一致

**反代核心**：
- ☐ Bounded transport pool（idle / per-host / total + active-stream protect）→ 一致 / 加 dial-time DNS
- ☐ Per-account 隔离 transport（cookie jar）→ 一致
- ☐ TLS 指纹 / impersonation profile（10 字段 JA3）→ **OCAW plugin opt-in**
- ☐ 协议适配多 provider（Anthropic Messages / OpenAI Chat / OpenAI Responses / Gemini / Bedrock / Codex CLI）→ 更强：每对 protocol × 每 stream event 完整覆盖 + 100+ 单测
- ☐ Stream forwarder + scanner buffer + 客户断线 drain → 一致 / 加自适应 buffer
- ☐ 错误归一化 + retry 类别 → 更强：12 类标准错误 + 跨 vendor 统一
- ☐ Header firewall + 凭据重写 → 更强：双向 allowlist + operator 三档选 (strict / forward-vendor / full-forward)
- ☐ 上游 HTTP/SOCKS proxy 支持（Proxy 表 + per-account 绑定）→ 一致

**计费 / 配额**：
- ☐ Idempotent billing claim gate（请求 fingerprint）→ 一致 + N+5b 已 spec released
- ☐ Token-level cost engine（input/output/cache/image/tier multipliers）→ 更强：versioned snapshot
- ☐ API key 多窗口配额（5h/1d/7d 滑窗）→ 一致
- ☐ Per-user × per-account 并发限制 → 一致

**支付 / 充值**：
- ☐ Payment order 12 状态生命周期（pending → paid → recharging → completed / failed / cancelled / expired / refund_*）→ 一致
- ☐ Refund pinning（绑原 provider/order）+ rollback → 一致
- ☐ Webhook 幂等（unknown order → 2xx）→ 一致
- ☐ Resume token HMAC + canonical return URL → 一致
- ☐ 多支付通道（Alipay / WeChat / Stripe / Epay 各 plugin）→ OCAW 不需要（这是 operator 自己的支付集成）

**用户 / 认证**：
- ☐ 多 OAuth identity（GitHub / WeChat / LinuxDo / Discord / Google / OIDC 通用）→ 一致
- ☐ TOTP 2FA → 一致 + admin 强制
- ☐ Pending auth multi-step session（intent / channel / verification 状态机）→ 一致
- ☐ Identity adoption decision（OAuth bind 时选择 adopt 哪些字段）→ 一致

**用户体验**：
- ☐ 用户自定义属性（UserAttributeDefinition + Value）→ 一致
- ☐ 余额阈值通知 → 一致
- ☐ 公告 + 已读追踪 → 一致
- ☐ Promo code（带 max_uses，区分 redeem code）→ 一致
- ☐ User subscription（plan + 多窗口配额 daily/weekly/monthly）→ 一致
- ☐ Affiliate / rebate（migrations 130-133 套件）→ 一致

**运营**：
- ☐ Channel monitor 全套（runner + history + daily rollup + request template + SSRF guard + bounded worker pool）→ 一致 + 探针写入 unified state machine
- ☐ Auto check-in（自动签到上游 OAuth）→ **OCAW plugin opt-in**
- ☐ 周期清理任务（usage cleanup with filters / cancellation / progress）→ 一致
- ☐ Bounded usage write worker pool（防 unbounded goroutines）→ 一致
- ☐ 备份 / 恢复 → 一致 + WebDAV 加密同步
- ☐ Setup wizard（首次自动化）→ 一致
- ☐ Error passthrough rules（operator 可配错误透传）→ **OCAW plugin opt-in**（影响客户体验）

### 不走 OCAW 的（产品功能，非合规问题）

以下功能直接 ship 默认开（ no warning UX 需要）：
- 客户端身份桥接（Codex CLI / Claude Code / Cursor 兼容）— 是产品力，不是 ToS 灰区
- Session 迁移（cross-account context rebuild）— 同上
- 资产估值 / capacity 预测 / SLA oracle — 同上
- 凭据自动恢复 — 同上
- WebDAV 加密同步 — operator 自己的数据
- 多账号 in single billing identity — operator 决策，HUAKAI 不限制

### 走 OCAW 的灰区功能清单（共 4 项）

按"启用 = operator 主动走灰区" 标准筛：

1. **TLS 指纹 / impersonation plugin**（R5）— 多数 ToS 禁止规避 detection
2. **Auto check-in plugin** — 多数 ToS 禁止 automated signup
3. **Error passthrough rules** — 改写客户错误体验，可能误导客户
4. **Response header full-forward 模式** — 暴露 vendor identity 给客户

每项在 admin UI 是一个 [一键启用] 按钮 + warning splash + audit log。

### 维护者内部 0 卡

dev / test / CI 完全没有 OCAW UX：
- 默认 build 可以全带 plugin
- dev DB 默认全启用所有 plugin
- 单测 / 集成测试 / benchmark 都直接跑
- CI 跑全套覆盖

**OCAW 只在生产 HUAKAI 实例的 admin UI 上出现**。

## 一行总结

HUAKAI = sub2api 核心 100% 必做且做更强 + 灰区功能走 OCAW（一键启动 + 警告对话框 + audit log）= 工具完整 + UX 上的合规叙事 = 竞赛差异化。开发者无限制 / operator 自决合规 / OCAW 是 ux 礼仪不是阉割。
