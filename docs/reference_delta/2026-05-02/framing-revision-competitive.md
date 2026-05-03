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

## 一行总结

HUAKAI 是竞赛产品，竞品做了的核心 HUAKAI 必做；合规靠 plugin opt-in + 警告 + 操作员自决，不靠阉割功能。之前 7 项被我过度合规化，需要 Owner 评一下哪些升回 P0/P1/P2。
