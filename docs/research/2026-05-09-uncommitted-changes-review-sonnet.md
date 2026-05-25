# Uncommitted Changes Review — Sonnet Lane (Codex sandbox 挂掉的 backup)

**日期**: 2026-05-09
**Lane**: reviewer (sonnet via oh-my-claudecode:code-reviewer)
**触发**: Codex `codex exec review --uncommitted` 在本环境因 vendored bubblewrap 无法 init network namespace 失败（`RTM_NEWADDR: Operation not permitted`）；`--sandbox danger-full-access` 被 harness 阻止。Sonnet 作 backup reviewer。

## TL;DR

- 3 HIGH / 6 MED / 5 LOW
- Verdict: **REQUEST CHANGES**——3 HIGH 全是 CLAUDE.md #12 / AGENTS.md 新规则的**自我违反**。规则首次 commit 即 out-of-compliance 会让规则在 day 1 失信
- 修 HIGH ~30 min，HIGH+MED ~50 min，可 commit

## HIGH severity（必修）

### H1 — Missing first-cite recency check for ALL 8 ref repos
**文件**: `docs/process/plans/2026-05-09-three-directions-synthesis.md:29-38`（SHA 表）+ `~/.claude/projects/-home-codex-HUAKAI/memory/reference_local_refs_clones.md:11-21`
**问题**: CLAUDE.md #12 first-cite recency check 要求记录 `archived: false` / `disabled: false` / `pushed_at within 90 days` / `HEAD SHA timestamp + commit message snippet`。当前 SHA 表只有 `Repo@SHA + License + Lane`——4 项必填字段全缺。
**修法**: 加 4 列 `pushed_at | archived | HEAD msg | UTC timestamp`

### H2 — Differentiation table missing mandatory delta + dimension columns
**文件**: `docs/process/plans/2026-05-09-three-directions-synthesis.md:90-99`
**问题**: CLAUDE.md #12 明确"Differentiation table column convention: `feature | upstream A cite | upstream B cite | HUAKAI delta | dimension(s)`. Without the dimension column, the table can't survive Owner / Codex review."当前表是 `feature | sub2api | new-api | one-api | LiteLLM | Portkey | Helicone | envoy | HUAKAI PASR` 用 ✅/❌——正是规则 anti-pattern flag 命名的形式。
**修法**: 重写表，按规则列约定。数据在 `project_pasr_real_diff_matrix.md` 已按架构/算法/生态分类，hoist 进 synthesis 即可。

### H3 — Lane B "true in all three" 被升级为 "8 项目集体空白"
**文件**: `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:3,22` + `synthesis.md:142,174`
**问题**: Lane B 只读 one-api/Portkey/LiteLLM 三家，verdict "TRUE in all three"。Synthesis 把这条传到"8 项目集体空白"未注明 Lane A/B/C 的具体 line 引用。CLAUDE.md #12 anti-pattern flag: 'no project does Y' MUST 'no project at our precision does Y' with precision dimension named OR explicit per-lane crossrefs。
**修法**: 把"8 项目集体空白"改写为"集体空白 (sub2api Lane A:209-211, new-api Lane A:209-211, one-api/Portkey/LiteLLM Lane B:22, helicone/envoy/all-api-hub Lane C:64)"或软化为"no project at our precision does"。

## MED severity

### M1 — `project_pasr_real_diff_matrix.md` 路径错
- 文件: memory `project_pasr_real_diff_matrix.md`
- 错: `litellm/streaming_handler.py:2268-2328`
- 实际: `litellm/litellm_core_utils/streaming_handler.py:2268`
- 修: 替换路径

### M2 — Lane A "lines around 939" 漂移
- 文件: `docs/research/2026-05-09-source-read-sub2api-newapi.md:266`
- 错: "lines around 939"
- 实际: line 943
- 修: 改为精确 `Wei-Shaw/sub2api@dbc8ae65:backend/internal/service/account.go:943`

### M3 — R5/R7/R8 立项 claim 缺 fusion-upgrade delta
- 文件: `synthesis.md:154`
- 错: 单源借鉴写作 fusion-upgrade，没指 delta
- 修: 改为 "R5/R7/R8 = LiteLLM MidStreamFallbackError pattern + delta: <e.g. 多 vendor continuation prompt synthesis> + dimension: 算法升级"

### M4 — Lane B 行号范围 truncated
- 文件: `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:210, 217`
- 错: 2052-2194
- 实际: 函数延续到 ~2200，sync wrapper 从 2200+
- 修: 验证后 pad 范围或拆两条 cite

### M5 — `helicone-chain-reverify.md` UTC 时间为 `00:00Z`
- 文件: `docs/research/2026-05-09-helicone-chain-reverify.md:5,184`
- 错: `00:00Z`（看起来像 placeholder 没替换）
- 修: 替换实际完成 UTC 或注明确实在 midnight 完成

### M6 — `MEMORY.md` 索引 entry 缺 inline citation
- 文件: `~/.claude/projects/-home-codex-HUAKAI/memory/MEMORY.md:22-23`
- 问题: index 命名 LiteLLM 但无 `<repo>@<sha>:<file>:<line>` 行内引用
- 修: 加 inline cite 或 `(see <linked-file> for citations)`

## LOW severity

### L1 — AGENTS.md "Where to clone" 列 8 但实际 11
- 文件: `AGENTS.md:454-463`
- 修: 加注 3 个 official-vendor SDK 在 `~/refs/` 但属 public-protocol exemption

### L2 — Lane B SHA 形式不统一（full vs 8-char）
- 文件: `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:5-9`
- 修: 标准化为 8-char 或 full

### L3 — synthesis 决策点 §8 缺 AT-/DR- ID
- 文件: `synthesis.md:184-191`
- 修: 编 DR-2026-05-09-001..006 或绑既有 DR-*

### L4 — Codex 草案 verdict 用 "暂停验证" 不在 parity-taxonomy
- 文件: `codex.md:13,15` + synthesis 沿用
- 修: 映射到 `Mandatory Roadmap` / `Plugin` / `Manual First` / 等正式 taxonomy

### L5 — synthesis line 30 缺 lane-guard close-out
- 文件: `synthesis.md:30`
- 修: 加 tail block `Source files read / Lane / Agent / UTC`

## Cross-reference 自洽性

- CLAUDE.md #12 + AGENTS.md "Source-Must-Read Trigger Matrix"：互相加强不冲突 ✅
- Stale-citation 30 day policy：8 SHA 全今天 clone，远未过期 ✅
- Circular reference：CLAUDE.md=when，AGENTS.md=how/where，互引但非循环 ✅

## 整体评估

| 维度 | 状态 |
|------|------|
| clean-room compliance | PASS（无 verbatim 抄；AGPL repo paraphrase 已守） |
| citation discipline | **FAIL HIGH**（H1/H2/H3） |
| fusion-upgrade 三维 coverage | **FAIL HIGH**（synthesis 表只 ✅/❌） |
| 自洽性 | PASS |

## 推荐路径

1. 修 H1（recency 4 列加进 SHA 表 + memory）— 10 min
2. 修 H2（重写 differentiation 表带 delta + dimension）— 20 min
3. 修 H3（"8 项目集体空白" 加 per-lane crossrefs 或软化）— 10 min
4. 同 pass 修 M1/M2/M5/M6 — 10 min
5. Commit
6. M3/M4/L1-L5 follow-up commit

总 ~50 min HIGH+MED 修完。

## 备注

Sonnet sub-agent 受系统提示限制不能写 .md 报告，本文件由 orchestrator (Claude Opus 4.7) 落盘做 audit trail。原始 inline 报告完整保留（本文件即等价转录）。
