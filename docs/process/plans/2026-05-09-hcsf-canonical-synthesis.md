# HCSF Canonical 选型综合 — Claude × Codex × Issue Mining 三 lane

**日期**: 2026-05-09
**前置 lanes**:
- `docs/research/2026-05-09-market-research-claude.md`（Sonnet 市场调研，独立）
- `docs/research/2026-05-09-market-research-codex.md`（Codex 市场调研，独立）
- `docs/research/2026-05-09-issue-mining-cross-repo.md`（Issue mining，4 repo GitHub issues）
- `docs/research/2026-05-09-axis3-protocol-translation-{litellm,portkey,envoy}.md`（3 specifier lane 已读上游）
- `docs/research/2026-05-09-axis3-huakai-current-state.md`（HUAKAI 现状盘点）

**触发**: Owner 2026-05-09 quote "自研的话你去和codex去调研下市场。看看现在啥情况，再做决定" + "你还要看借鉴项目的issue"

## TL;DR — 三 lane 共识 + 一个分歧 + 合成方案

**共识（3 lane 一致）**：拒绝 LiteLLM 式单 OpenAI canonical。理由：
- OpenAI Responses tool search / Computer Use / Gemini Live WSS / xAI server-side tools / MCP 等 vendor-only 能力压平后丢失
- Anthropic 在中文中转主战场（claude-code / Codex / Antigravity）是事实标准
- 跨 vendor capability 异质（不是 OpenAI 子集 vs 超集，是相互不包含）

**分歧（HCSF 应该是几个 canonical）**：
- Sonnet: 2 (Portkey-style bi-canonical)
- Issue mining: 1 主 + 1 view (Anthropic-primary)
- Codex: 0 canonical + capability graph (capability-driven IR)

**合成方案**：**OpenAI-compatible storefront + Anthropic-native side-entry + capability graph IR + per-capability native passthrough**——融合 3 lane 答案。详见 §3。

## 1. 市场现状（3 lane 一致认定）

### 1.1 行业整合（窗口在收紧）
- Portkey 被 Palo Alto Networks 宣布收购（2026-04-30，per Sonnet R-PANW-PORTKEY；Codex R-PANW-PORTKEY）
- Helicone 被 Mintlify 收购（2026-03-03，per Sonnet R-HELICONE-MINTLIFY；Codex R-HELICONE-MINTLIFY）
- 独立活的：OpenRouter / LiteLLM / Vercel AI Gateway / Cloudflare AI Gateway / Kong / TrueFoundry

→ **HUAKAI "独立 OSS gateway" 市场窗口正在关闭**。需要快速进入特定垂直（不是 generic gateway）。

### 1.2 Unit economics（OpenRouter 锚点）
- $50M ARR / 5.5% markup / 150K MAU / 2.5M 用户 / 8.4T tokens 月 / $100M+ 年化 inference spend (per Sonnet R-OPENROUTER-SACRA；Codex R-OPENROUTER-SACRA)
- → **markup 天花板 5%**
- → **12 个月最重要 metric = monthly annualized inference spend**

### 1.3 Portkey 现实数据（codex 补充）
- Portkey OSS Gateway 1T+ tokens/day, 120M+ AI requests/day, 24,000+ orgs (per R-PORTKEY-OSS)
- 即使是被收购的 Portkey 现在仍处理巨量流量，说明 gateway 功能本身有强需求（不是商业失败）

### 1.4 中国市场特殊性
- TokenNav 收录 106 中转站，92 可用 (per Codex R-TOKENNAV)
- Token1000 收录 59 站，10 已验证推荐，9 高风险勿入 (per Codex R-TOKEN1000)
- 价格区间 "az 1元/刀，官转 2.5元/刀" (per Codex R-OPENAIROUTER-CN)
- Anthropic 2026-04-17 上实名验证 (per Sonnet)
- 2026-04-06 OpenAI/Anthropic/Google 联合反中国复制 (per Sonnet)

→ **HUAKAI Personal Edition 主战场是合规离岸中转**，不是买账号 / 不规避实名。

## 2. HUAKAI 真 PMF 区（3 lane 共识）

| 客户层 | 市场容量信号 | 付费意愿 | HUAKAI fit |
|---|---|---|---|
| L1 个人开发者 | 高频 + 单笔小 | 低 | 不是 PMF |
| L2 中小团队多 vendor | 中 | 中 | 部分 fit（顺便覆盖） |
| **L3 AI agent 框架后端** | 高（OpenAI changelog 连续新增 agent 能力 per Codex R-OPENAI-CHANGELOG） | **中-高** | **PMF zone**——HCSF 保真度直接决定能不能服务 |
| **L4 中国订阅→API 中转** | 中（Token1000/TokenNav 数据 per Codex） | **中-高** | **PMF zone**——sub2api 灵魂的真客户 |
| L5 Enterprise generic gateway | 高 | 高 | **不是 PMF**——Portkey/Helicone 收购 + Kong/TrueFoundry 占位 |

→ HUAKAI 应专注 **L3 + L4**，不打 L5。

## 3. HCSF Canonical 合成方案（3 lane 答案融合）

### 3.1 设计原则（3 lane 共识 + 自然推论）

1. **拒绝单 canonical 压平**（3 lane 一致）
2. **OpenAI-compatible 必须是默认入口**（Sonnet / Codex 一致——市场获客现实）
3. **Anthropic 必须有 native 显式入口**（Issue mining 强证据 + Sonnet bi-canonical 主张）
4. **per-capability 异质能力可见**（Codex capability graph 主张）
5. **per-vendor 保真兜底**（Codex native passthrough 主张）

### 3.2 提议架构（HCSF v0.4 — 合成 3 lane）

```
                    ┌──────────────────────────────┐
                    │       Inbound Entry Layer     │
                    │  ┌─────────┐  ┌─────────────┐│
                    │  │/v1/chat/│  │/v1/messages ││
                    │  │ comple- │  │  (Anthropic ││
                    │  │ tions   │  │  native)    ││
                    │  └────┬────┘  └──────┬──────┘│
                    │       │              │        │
                    └───────┼──────────────┼───────┘
                            ▼              ▼
                    ┌────────────────────────────┐
                    │   Capability Graph IR        │
                    │  (per-capability schema)     │
                    │                              │
                    │ - text                       │
                    │ - tool_use / tool_result     │
                    │ - thinking / reasoning       │
                    │ - cache_control              │
                    │ - structured_output          │
                    │ - computer_use               │
                    │ - file / image / audio / video│
                    │ - live (WSS)                 │
                    │ - batch                      │
                    │ - mcp_server                 │
                    │ - data_retention            │
                    └──────────────┬──────────────┘
                                   │
                    ┌──────────────┴───────────────┐
                    ▼              ▼               ▼
             ┌──────────┐  ┌────────────┐  ┌──────────┐
             │ Anthropic│  │ OpenAI     │  │ Gemini   │
             │  adapter │  │  adapter   │  │ adapter  │
             │  (rich)  │  │  (lossy?)  │  │ (rich?)  │
             └──────────┘  └────────────┘  └──────────┘
                                   │
                    ┌──────────────┴───────────────┐
                    │   Per-Vendor Native          │
                    │  Passthrough Endpoints       │
                    │  (capability 不能 normalize 时) │
                    │                              │
                    │ /v1/native/anthropic/...     │
                    │ /v1/native/openai/responses  │
                    │ /v1/native/gemini/live (WSS) │
                    └──────────────────────────────┘
```

### 3.3 三 lane 答案在合成中的位置

| 元素 | 来自 | 在合成中的角色 |
|---|---|---|
| `/v1/chat/completions` 入口 | Codex (OpenAI storefront) + Sonnet | 市场获客主入口 |
| `/v1/messages` 入口 | Sonnet (bi-canonical) + Issue mining | Anthropic-native 显式入口；中文 CLI 必备 |
| Capability graph IR | Codex（核心创新） | 内部抽象层 |
| 每 capability 优先 Anthropic-rich schema | Issue mining（cache_control / 中文 CLI 证据） | IR schema 设计倾向 |
| Per-vendor native passthrough | Codex（保真兜底） | capability 之外的 vendor-only 能力 |

### 3.4 与上游 3 家比较（fusion-upgrade 三维 delta）

| Feature | LiteLLM | Portkey | envoy-ai-gateway | HUAKAI HCSF v0.4 delta | 维度 |
|---|---|---|---|---|---|
| Canonical 数量 | 1 | 2 | 3+ | OpenAI 入口 + capability graph IR (实际 11+ capabilities) | 架构 |
| Anthropic 保真 | Lossy（OpenAI 子集） | 双 canonical 但分裂 | per-endpoint，要求显式 | 默认 IR Anthropic-rich，OpenAI client view | 算法 |
| Native passthrough | 否 | 否 | 否 | 显式 `/v1/native/<vendor>/<capability>` | 架构 + 生态 |
| Capability matrix UI | 否 | 部分 | 否 | 暴露 capability matrix metric per vendor (per-account 切片) | 生态 |

### 3.5 实施估算（基于 Lane 4 HUAKAI 现状盘点）

axis-3 真实进度 25-30%。HCSF v0.4 实施需：
- HCSF struct 真实化（当前 `proto.HCSF struct{}` 空壳）：1-2 周
- Capability graph IR schema 设计：2 周（涉 Anthropic / OpenAI / Gemini API spec 全面读）
- ClientAdapter 落地（当前全空 0 实现）：2-3 周
- `CanonicalToProviderRequest` Phase B（当前全 ErrNotImplemented）：1-2 周
- per-vendor native passthrough endpoints：2 周
- 3 vendor 真实测试 + property test：2-4 周

**总：10-15 周（2.5-4 个月）**——比 Lane 4 给的 11-21 周略低，因为 HCSF v0.4 设计减少了"15 vendor 真验证"的范围（capability graph 抽象后 vendor adapter 实现路径标准化）。

项目不急，2.5-4 个月可以接受。

## 4. 商业含义（基于 HCSF v0.4）

### 4.1 12 个月 metric
**Monthly annualized inference spend**（OpenRouter benchmark $100M+，HUAKAI 起步先做到 $1M+/月年化）

### 4.2 客户层节奏
- 月 1-3：HCSF v0.4 实施 + 真账号 smoke test（Owner 本机）
- 月 4-6：L4 中国中转 PMF 验证（合规离岸架构 + 客户端 Claude Code/Codex 兼容验证）
- 月 7-9：L3 AI agent 框架后端 PMF 验证（capability graph 真完整支持 agent workload）
- 月 10-12：估值叙事建立（依据 Sacra OpenRouter 估值范本：8.4T tokens / $100M GMV ≈ $1.3B 估值）

### 4.3 不打的市场
- L5 enterprise generic gateway（被 Portkey 收购证明窗口已关，且 Kong/TrueFoundry 占位）
- 不做 Portkey 那种"governance / observability / compliance" full-stack——HUAKAI 单点突破 capability fidelity

## 5. 决策点（待 Owner）

| ID | 决策 | 选项 | 推荐 |
|---|---|---|---|
| **D-HCSF** | HCSF canonical 选什么 | (1) 单 OpenAI / (2) Bi-canonical / (3) Anthropic-primary / (4) HCSF v0.4 合成 | **(4) HCSF v0.4 合成**——3 lane 答案的并集 |
| **D-PMF** | HUAKAI 主攻客户层 | L1 / L2 / L3 / L4 / L5 / L3+L4 | **L3 + L4** |
| **D-METRIC** | 12 个月最重要 metric | ARR / MAU / monthly annualized inference spend / token volume | **monthly annualized inference spend** |
| **D-PACE** | HCSF v0.4 实施节奏 | 5 周 / 10 周 / 15 周 / 项目不急按 axis 实施完整度 | **按 axis 实施完整度**（per `feedback_pace_not_urgent.md`） |
| **D-AXIS** | axis 实施次序 | 仅 axis 3 / axis 3 + 5 / axis 3 → 1 → 5 / 等 | 待 sprint plan 双 lane 决定 |

### Owner 待回复

1. D-HCSF 是否同意 v0.4 合成方案？
2. D-PMF 是否专注 L3 + L4？
3. D-METRIC 是否采纳 inference spend 作为 north star？
4. 如同意 D-HCSF / D-PMF / D-METRIC，是否派双 lane plan 起 HCSF v0.4 实施 phased delivery plan？

## 6. 风险与盲点（自评）

- 3 lane 都没访谈真实客户。HUAKAI 真 PMF 区是基于公开数据 + 项目特性推断
- HCSF v0.4 是合成而非任一 lane 独立提议——可能引入 lane 之间的隐性矛盾，需要 Owner / Codex 二次审视
- 估算 10-15 周基于"capability graph 设计能在 2 周完成"——这个假设可能偏乐观，实际 schema 设计常拉锯
- "L5 不打"的判断假设 HUAKAI 不需要 enterprise 收入。如果 Owner 商业目标变化，决策要重看
- "5% markup 天花板"是 OpenRouter benchmark，不是物理定律——HUAKAI 在合规离岸场景可能有溢价空间

## 7. Source 引用记录

详见 3 个 lane 文件 + Lane 4 现状盘点。每条断言可回溯到具体 file:line / URL：

- 市场数据：`docs/research/2026-05-09-market-research-{claude,codex}.md`（含 30+ URL refs，全部 2026-05-09 access date）
- Issue 数据：`docs/research/2026-05-09-issue-mining-cross-repo.md`（含 110+ issue references）
- 上游 mechanism：`docs/research/2026-05-09-axis3-protocol-translation-{litellm,portkey,envoy}.md`
- HUAKAI 现状：`docs/research/2026-05-09-axis3-huakai-current-state.md`

## Tail block

Source files read: 7 docs/research/2026-05-09-*.md (HUAKAI internal—exempt per CLAUDE.md #12); 0 upstream source files
Lane: synthesizer (合成 3 market+issue lanes 与 4 specifier lanes 的产出)
Agent: Claude opus-4-7 [1m]
UTC timestamp: 2026-05-09T15:55Z
