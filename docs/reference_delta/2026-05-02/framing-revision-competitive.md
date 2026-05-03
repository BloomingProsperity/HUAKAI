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

## 一行总结

HUAKAI 是竞赛产品，竞品做了的核心 HUAKAI 必做。**开发者无限制开发权 + 使用者自决合规义务**（tool maker not tool user 立场）。Plugin opt-in 是 4 层机制（compile / runtime / splash+audit / README 声明），不是阉割功能 — 而是把启用决策 + 法律责任转给 operator。之前 7 项过度合规化，本次 v2 进一步开放：客户端桥接 P0、auto check-in L2、TLS plugin P2、WebDAV L2、响应头 forward 给 operator 选项。
