# 账号转 API — D 决策固化(Owner 2026-05-24)

UTC: 2026-05-24T07:55Z(本次 cross-discuss + AskUserQuestion 双批连选完成)
Lane: Claude PM-orchestrator(本文是决策锚定,不是 plan)
适用范围:本日全部 2026-05-24-* plan 在切片落地前以本文锁定的 D 选项为准。

## §1 第一批 4 决策(切片启动卡点)

| ID | 决策 | Owner 选 | 推荐方 | 影响切片 |
|---|---|---|---|---|
| AS-D1 | Anthropic OAuth transport mimicry 时机 | **现在就接(transport.Factory 伪装)** | codex | C1 启动前提:[[project_l1_tls_boringssl]] backend 先就位 |
| PH-D1 | placeholder registry default 启用策略 | **完工逐个翻** | Claude | P-Z 切片按 P-A → P-Z 序;UI 半亮半暗可接受 |
| TR-D1 | refresh worker health 持久化 | **加 schema 列**(provider_account 加 health_state + cooldown_until) | Claude | L-D 必须包 migration 0008 走 Owner 高风险 schema 流程 |
| TR-D3 | refresh worker vendor enablement 顺序 | **第一波全 fake/test mode,逐 vendor 解锁** | 共识 | L-A 切片先 fake endpoint 跑通 OAuth code/state/PKCE/CSRF 风险测试 |

## §2 第二批 4 决策

| ID | 决策 | Owner 选 | 推荐方 | 切片影响 |
|---|---|---|---|---|
| AS-D2 | Anthropic protocol family 是否拆 | **拆新** `anthropic_claude_session` 与 `anthropic_messages` 并列 | codex | model binding + registry 改;billing/dispatch 分流 |
| AS-D3 | Anthropic token exchange 时机 | **callback 时立刻 exchange** | codex | callback handler 设计:state+code 验证 → exchange → finalize-pending |
| AS-D6 | Anthropic client_id 来源 | **复用 Anthropic 公开 CLI client_id** | 共识 | 启动前 Owner 法务确认 ToS;不需 HUAKAI 自注册 OAuth app |
| TR-D2 | mimicry legal review 闸门 | **不加额外 review 闸门**(代码默认 disabled 已足) | Claude | L-C 切片只走 audit-only resolver + env var 默认 off,无额外 ops 流程 |

## §3 Claude 默认补的剩余 D(不再 surface,按推荐默认锁定)

| ID | 决策 | 默认锁定 | 理由 |
|---|---|---|---|
| AS-D4 | auth mode runtime mapping | **A:一个 adapter + Extra metadata** | codex 推 A;`claude_ai_oauth` 与 `claude_code` 用同一 runtime adapter,mode 走 metadata |
| AS-D5 | long-lived setup token | **A:默认禁用**(Owner-flag 后排,本波不做) | 一致 codex D8-A;[[feedback_huakai_better_than_sub2api]] 不引入未必要 vendor 风险 |
| AS-D7 | Anthropic schema 显式列 vs JSON | **跟随 TR-D1:加显式 schema 列**(`auth_mode` / `client_id_source`) | 既然 TR-D1 选 schema,Anthropic 一致用 schema 而非 JSON 减少异构 |
| PH-D2 | placeholder schema migration | **跟随 TR-D1:加 schema** (endpoint catalog 表 / oauth devicecode 列) | 同上,一致性 |
| PH-D3 | 错误响应 cooldown | **D:配置可调,默认 3 连封 + 30min** | Claude 推 D;不同 vendor 风控阈值不同 |
| TR-D4 | cooldown 时长策略 | **D:配置可调,默认 3 连封 + 30min** | 跟 PH-D3 共用配置项 |

## §4 启动顺序与依赖

```
[STAGE 0 前提] (Owner 必决,本文未列)
  └── transport backend 选型 — [[project_l1_tls_boringssl]] 是否启动 BoringSSL/uTLS/wreq slice
        ↑ 因 AS-D1 选了"现在就接 mimicry",此前提不就位 → 整个 Anthropic OAuth 切片 (C1+) blocked

[STAGE 1 立即启动]
  ├── L-A bootstrap (fake/test mode) — TR-D3 锁定,无外部依赖
  └── P-A copilot device-code (litellm paraphrase) — PH-D5 共识 A,无外部依赖

[STAGE 2 等 STAGE 0/1 完成]
  ├── C1+ Anthropic OAuth slice — 依 AS-D1 transport + AS-D2 family + AS-D3 exchange + AS-D6 client_id
  ├── P-B gemini_advanced — 依 G-2 endpoint catalog (TR-D1 schema 路径)
  ├── P-C antigravity — 依 P-A 经验固化后开
  ├── L-B endpoint catalog — TR-D1 schema 加列 migration 0009/0010
  └── L-D health maintenance — TR-D1 schema 0008 + 加 health probe tick

[STAGE 3 后排]
  ├── P-D cursor (等 Owner 抓包)
  ├── P-E/F windsurf/kiro (研究切片,无 ref)
  ├── L-C mimicry profile resolver (audit-only,等 STAGE 0 backend)
  └── L-Z dispatcher 接入 health_state (压轴)
```

## §5 卡死 STAGE 0 的关键 surface 项

Owner 需在下一轮拍:**transport backend 选型** — 因 AS-D1 锁定"现在接 mimicry",C1 Anthropic OAuth 切片不能开工直至:
- BoringSSL fork (跟 [[project_l1_tls_boringssl]] 同路线)
- 或 uTLS Go (refraction-networking/utls)
- 或 wreq Rust 子层 (跟 [[project_relay_core_path]] Rust 路线)
- 或 curl_cffi (Python 子进程,跟 Go 主体不顺)

本决策不在本文 surface,转 [[project_l1_tls_boringssl]] 下一波专门 plan。

## §6 Lane + commit attribution

- Decision-log written: Claude (claude-opus-4-7)
- UTC: 2026-05-24T07:55Z
- Input: AskUserQuestion 第一+二批 8 个 Owner pick + Claude 默认推荐 6 个
- Pair files:
  - [2026-05-24-anthropic-oauth-inversion-{claude,codex,synthesis}.md](2026-05-24-anthropic-oauth-inversion-synthesis.md)
  - [2026-05-24-placeholder-session-adapters-{claude,codex,synthesis}.md](2026-05-24-placeholder-session-adapters-synthesis.md)
  - [2026-05-24-token-refresh-worker-closure-{claude,codex,synthesis}.md](2026-05-24-token-refresh-worker-closure-synthesis.md)
  - [../2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md)
