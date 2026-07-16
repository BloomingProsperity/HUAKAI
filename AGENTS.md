This file is agent-facing and authoritative.

# Agent Operating Rules

This project builds an MIT clean-room AI Gateway + Account Hub + Admin Ops Platform with full feature parity or better against empirical reference projects.

## Non-Negotiables

- Reference projects are evidence, not source-code providers.
- Do not copy AGPL/GPL/LGPL source code, distinctive file structures, comments, schemas, UI source, or implementation details.
- License risk may change the implementation method, but must not remove a feature.
- Security risk may change rollout, gating, or defaults, but must not remove a feature.
- Every reference feature must be mapped to `Implemented`, `Implemented Better`, `Merged Equivalent`, `Safe Equivalent`, `Plugin`, `Feature Flag`, or `Mandatory Roadmap`.
- No feature may be silently dropped.
- Do not modify `LICENSE` without explicit owner instruction.
- **语言:全中文(Owner 硬规则)。** 代码注释(`.go` 生产代码与测试)、commit message 正文、计划与 `docs/process` 文档、面向 Owner 的汇报、以及派发给其它 agent 的指令与其返回报告,一律用中文;英文技术标识符(函数 / 类型 / 环境变量名、SQL 关键字)保留英文,只是注释与散文用中文。Dispatch subagent 时必须在 prompt 显式要求"代码注释用中文、返回报告用中文"。
- **代码注释禁止提及借鉴项目(Owner 硬规则 + clean-room)。** `.go` 代码注释里**绝不**出现 sub2api / new-api / CLIProxyAPI 等借鉴项目名,也不写"借鉴 / 参考某项目的做法"。注释只描述 HUAKAI 自身的意图与机制。对借鉴项目的 #16 调研与对照只保留在 `docs/process` 计划文档里,不进代码注释。

## Owner Start Gate

See [docs/RULES.md §2 Owner Start Gate](docs/RULES.md#2-owner-start-gate) for the canonical rule (S-001/S-002) and the full list of valid start signals. All agents follow that rule unchanged.

## Proactive Execution Rule

After Owner confirmation, agents should:

1. Read the relevant project rules.
2. Understand the assigned goal.
3. Execute the task to completion when safe.
4. Make reasonable engineering decisions.
5. Record assumptions.
6. Record risks.
7. Update required docs.
8. Run available checks when possible.
9. Produce a final Chinese summary for the Owner.

## Soft Scope Rule

Allowed and forbidden file scopes are guidance, not a reason to stop unnecessarily.

If a task requires touching a file outside the expected scope:

- Low-risk docs or tests: update directly and record it.
- Low-risk implementation support files: update if needed and explain why.
- High-risk files: stop and request Owner confirmation.

High-risk files include:

- `LICENSE`
- production secrets
- real credentials
- payment logic
- authentication core
- billing ledger
- quota enforcement
- database schema
- deployment scripts
- destructive migration files

## Risk-Based Confirmation Rule

Agents should use this risk model:

### Low Risk

Proceed without asking again.

Examples:

- docs updates
- tests
- prompts
- type fixes
- UI copy
- small refactors
- non-sensitive config examples

### Medium Risk

Proceed if needed, but record the reason and risk.

Examples:

- small implementation changes
- new helper utilities
- UI structure changes
- non-breaking API contract updates
- mock data
- experimental logic

### High Risk

Stop and ask Owner before acting.

Examples:

- deleting files
- changing `LICENSE`
- changing database schema
- changing auth core
- changing billing ledger
- changing quota enforcement
- adding new runtime dependency
- touching real secrets
- destructive shell commands
- production deployment

## Do Not Over-Block Rule

Agents must not refuse or stop just because a requirement is complex.

If a rule seems to block a real product requirement, the agent should:

1. Explain the conflict.
2. Propose a safe path.
3. Continue with a safe equivalent if possible.
4. Mark high-risk parts for Owner confirmation.
5. Never delete the feature silently.

## Owner Benefit-First Global Execution Rule (2026-07-16)

本节对所有 agent、所有目标、所有工作树全局生效。它扩展阅读和执行边界，但不覆盖
高风险确认、clean-room、真实性、测试质量和互审硬规则。

1. **有明确收益就深入。** 只要能增强基础、正确性、可维护性、可观测性、可测试性
   或真实功能闭环，agent 可以主动扩大源码阅读面和低/中风险修复面，不得用过窄的
   自设 scope 把跨模块链路问题切断。
2. **探索必须指向闭环。** 每项扩展阅读都要能回答一个具体问题，并落到证据、修复、
   测试、风险记录或明确的下一步；禁止无目标全仓扫描和重复消耗 token。
3. **低/中风险直接完成。** 对收益清楚、可测试、可回滚且不改变数据库结构、资金、
   鉴权核心、billing ledger、quota enforcement、真实密钥或生产部署的改动，直接
   实现、验证和记录，不为普通工程判断反复请求 Owner。
4. **仪式按风险和改动规模配置。** 已有计划、双计划、review、clean-room 等硬门仍然
   有效，但产物应简洁、复用现有证据并聚焦当前决策，不得把流程本身变成延迟交付或
   浪费算力的理由。
5. **高风险先交决策包再询问。** 真正要改变数据库 schema、资金路径、鉴权核心、
   billing ledger、强配额执行、运行时依赖或生产部署时，必须停下并向 Owner 提供：
   HUAKAI 当前真实源码链路；借鉴项目当前源码做法及逐项引用；双方优缺点和功能不缩水
   对比；至少两个可执行选项；迁移、测试、回滚和风险；agent 的明确推荐；以及需要
   Owner 决定的精确问题。涉及非 MIT 源码时，调研与实现必须保持 clean-room 分车道。
6. **跨模块影响必须检查。** 修复不能只盯报错点，要沿入口、装配、运行时状态、
   持久化、观测、恢复和相关端点检查辐射影响；无法在本轮安全闭环的部分必须记录，
   不能静默遗忘。

## Owner PR And Merge Gate (2026-07-16)

本节对后续所有 agent、目标和工作树全局生效。

1. **所有修复与改动必须通过 PR 提交。** 代码、测试、合同、规则和正式文档均在
   独立分支完成 review、测试、commit、push 后创建 PR；不得把改动直接提交到主线。
2. **默认创建 Draft PR。** PR 必须说明改了什么、根因、影响面、验证结果、风险、
   是否有功能缩水和需要 Owner 决策的事项。
3. **禁止 agent 自行合并。** 无论检查是否全部通过，进入 `main` 或其它主线分支前
   都必须获得 Owner 针对该 PR 的明确合并同意。创建 PR、更新 PR 和处理 review
   不等于获得合并授权。
4. **批次边界清楚。** 全局审计等长任务按可独立验证的闭环拆 PR；不得把无关目标、
   其它 agent 的改动或尚未核实的问题混入同一 PR。

## Feature Preservation Rule

License risk and security risk must not reduce functionality.

If a feature is risky, convert it to one of:

- `Safe Equivalent`
- `Plugin`
- `Feature Flag`
- `Manual First`
- `Experimental Module`
- `Mandatory Roadmap`

Do not remove the feature.

## Agent Roles

- Claude: PM-Orchestrator and lead architect.
- Gemini: frontend UI and operations dashboard engineer.
- Codex: production reviewer, scenario test writer, feature parity auditor, and small safe patch engineer.

Codex must not be the primary large-feature implementer unless explicitly assigned.

## Codex Practicality Rule

Codex should not be over-constrained into doing nothing.

After Owner confirmation, Codex should:

- review from real-world usage
- write scenario tests
- identify blockers
- make small safe fixes
- explain when a restriction blocks a real product need
- propose practical safe alternatives

Codex should not be forced to stop for every minor scope mismatch.

## Gemini Practicality Rule

Gemini may proactively build UI after Owner confirmation, but must not edit backend core logic.

Gemini may update:

- frontend pages
- components
- styles
- UI docs
- mock UI data
- API assumptions docs

Gemini must stop before changing:

- provider routing
- quota
- billing
- auth
- database schema
- `LICENSE`
- real secrets

## Where To Work

- Use `docs/` for authoritative planning, contracts, parity, risk, and release gates.
- Use `.agents/skills/` for complex workflows.
- Use `.claude/agents/` for Claude sub-agent definitions.
- Use `.gemini/hooks/` for Gemini guardrails.
- Do not write business implementation in this initialization pass.

## Owner Summary Rule

After each completed task, agents must output a Chinese summary:

1. 做了什么
2. 改了哪些文件
3. 为什么这样做
4. 有没有功能缩水
5. 有没有 clean-room 风险
6. 有没有安全风险
7. 哪些地方需要 Owner 确认
8. 下一步建议

## Cross-Review Protocol (added 2026-04-29)

When dispatched as **reviewer-lane** (e.g. `codex exec --sandbox read-only` with `docs/templates/codex-reviewer.md` piped to stdin), you MUST follow these rules — they survive `AGENTS.md` truncation only if you read the full template, so always read the template before producing output.

1. **Read-only physical guarantee**: you cannot edit any file. Output is a written report.
2. **Quoted evidence required**: every finding cites `file:line` for both the spec and the test code. Findings without citations are invalid.
3. **Coverage matrix is the spine**: every AT-* ID in the spec must appear in your matrix as one of COVERED / COVERED-WEAK / SKIPPED (with validity check) / MISSING.
4. **Severity is binding**:
   - HIGH = blocks Released-spec status. Owner cannot ship unfixed.
   - MED = must fix before opening the next vertical slice.
   - LOW = backlog item.
5. **Smell library** — flag these even if tests pass:
   - assertions like `res.X != bad` but never `res.X == good`
   - tests that `t.Skip` when a field is zero — coverage hole disguised as defense
   - "100 goroutines" in comment but `N=12` in code
   - test fixtures where winner and loser share the distinguishing feature
   - stubs not mirroring production SQL `WHERE` clauses
   - gate chains all `AllowAllGate` in tests, hiding gate-failure paths
6. **Output ends with Chinese 1-paragraph summary** for Owner: 总体覆盖度、最高优先级补测、是否阻塞下一 slice.

The cross-review template lives at `docs/templates/codex-reviewer.md`. Owner triggers it via `/cross-review` slash command in Claude Code.

## Truth-First Discipline (added 2026-04-29 third Owner directive)

> "保证真实 不造假"

The single most load-bearing rule in this project. It overrides every other rule when in conflict.

### What it means

- **A 4000-word honest file > a 9000-word file with 4000 words of speculation padding.**
- **Word count and section length are signals, not targets.** If a target says "minimum 6000 words" and you can only honestly cover 4000 from real source reading, write 4000 with a clear note in metadata that the source did not support more depth at this time.
- Every factual claim about upstream behavior must be traceable to a source region the agent actually read. Add inline citation markers. If a claim cannot be traced, it is speculation — either drop it, or move to "Open Questions".
- Three explicit categories for any behavior assertion:
  - **Observed** — agent read the source region and saw this behavior. Default category.
  - **Inferred** — agent did not directly observe but the claim follows from observed regions; mark explicitly `(inferred from §10 region X)`.
  - **Speculative** — NOT ALLOWED. Move to Open Questions or drop.
- HUAKAI-fit risk reasoning is its own category: comparing observed upstream behavior against HUAKAI's design constraints (DR-001 multi-tenant, DR-002 dual editions, DR-006 PostgreSQL). Reasoning is allowed; speculation about upstream is not.

### Why this matters more than depth targets

Pressure to hit a word count creates incentive to fabricate. The project's value depends on every decomposition being a faithful witness to upstream behavior. Synthesis stages combine multiple decompositions; if one is fabricated, downstream specs derived from it inherit the error and the cascade is invisible until production breaks.

### How agents enforce truth-first on themselves

- Before claiming a behavior in writing, ask: "Did I read the region that supports this?" If no, do not write it.
- After drafting a section, scan for unsupported claims; either cite or remove.
- In §10 Source Coverage Proof, list **every** region read and **what each contributed**. If §2 makes a claim no §10 region supports, the claim is speculation by definition.
- When critic-lane reviews, every uncited claim is a defect.

### Owner-facing signals

- Metadata block in every decomposition file MUST include: `Observed regions: N` / `Inferences: M` / `Open questions: K`. Owner reads these three numbers first to gauge depth-vs-honesty tradeoff.
- 1-paragraph Chinese summary at end of every decomposition must explicitly call out: 哪些是真观察 / 哪些是合理推断 / open question 数量.

## Plan-Before-Execute Discipline (added 2026-04-29 second Owner directive)

> "你自己执行的时候也要plan给我。"

The rule applies to **every agent in this project — not just Codex, not just Claude**. Before any non-trivial action, write a plan artifact to `docs/process/plans/YYYY-MM-DD-<descriptor>.md` and surface it for Owner review BEFORE execution.

### What counts as non-trivial (plan required)

- Codex batch dispatch (any number of parallel jobs)
- Writing more than ~200 lines of code in one work unit
- Schema migration / DB structural change
- Deleting files / branches / records
- Restructuring multi-file modules
- Cross-cutting refactors
- Long-running Codex reasoning tasks (decompositions, source-verified reads)
- Any task that could leave the repo / DB / running system in an inconsistent state mid-flight

### What counts as trivial (no plan needed)

- Typo / single-character fix
- Adding a single test case to an existing suite
- Reading files for understanding (no mutation)
- Running already-planned tests / linters
- Replying to a question with text only

### Plan content (minimum)

```
# YYYY-MM-DD <descriptor>
| Owner directive | <quote that triggered this work> |
| Scope | <what is in / out> |
| Success criteria | <how we know it worked> |
| Time estimate | <wall clock + agent time> |
| Blast radius | <what breaks if this fails> |
| Failure modes | <what could go wrong + mitigation> |
| Decision points | <what needs Owner sign-off mid-flight> |
| Pre-execution checklist | <ordered list of dependencies + sanity checks> |
```

### Discipline-of-discipline

If the temptation to skip planning arises ("this is small enough", "I'll just do it"), that itself is the signal that planning is needed. The agents that keep this codebase healthy plan small, plan often, and surface the plan even when it feels overhead. Skipping the plan is how earlier rounds in this project produced shallow specifier output that wasted Owner's time and required round-2 redo.

## Parallel Plans + Cross-Discuss (added 2026-04-30 Owner directive — corrected)

> "这个计划和 codex 讨论了吗？以后计划也要相互交叉讨论验证。做任何事情都需要"
> "不是让他对你的计划进行交叉审查，而是他也定计划 你也定，交叉讨论"

The Plan-Before-Execute discipline (above) produces the artifact. This rule says: **for non-trivial work, both Claude and Codex independently produce their own plan, then reconcile through cross-discussion**. It is parallel-draft, NOT sequential review of one plan.

The first interpretation Claude tried — "Codex reviews Claude's plan" — was explicitly rejected by Owner. Sequential review only catches mistakes IN one person's mental model. Independent drafts surface different starting assumptions, different blind spots, different priorities. Same round-table logic as the DR governance protocol.

### Workflow

1. Owner authorizes a non-trivial work unit. Plan-Before-Execute rule says "write a plan."
2. **Both agents draft independently:**
   - Claude writes `docs/process/plans/YYYY-MM-DD-<descriptor>-claude.md`
   - Codex writes `docs/process/plans/YYYY-MM-DD-<descriptor>-codex.md`
   - Each writes WITHOUT seeing the other's draft. Same brief / Owner directive / scope; fresh independent thinking.
3. Compare the two plans. Surface to Owner:
   - **Agreements** — points both plans landed on (likely correct)
   - **Conflicts** — points where they disagree (Owner picks)
   - **Gaps** — things one plan caught that the other missed
4. Owner approves a synthesized plan. Either:
   - Write `docs/process/plans/YYYY-MM-DD-<descriptor>.md` (no suffix) as the merged authoritative version, OR
   - Amend one of the two with a "synthesized after diff" header — record which.
5. Only after the synthesized plan exists does execution begin.

### Codex dispatch prompt template

```
codex exec --full-auto "Owner has authorized <work unit description>.

Independently write your own plan to docs/process/plans/YYYY-MM-DD-<descriptor>-codex.md.
Do NOT read any docs/process/plans/YYYY-MM-DD-<descriptor>-claude.md if it exists — the
point is independent thinking.

Plan content (per AGENTS.md Plan-Before-Execute):
  - Owner directive (quote that triggered this work)
  - Scope (in / out)
  - Success criteria
  - Time estimate
  - Blast radius
  - Failure modes + mitigations
  - Decision points needing Owner sign-off
  - Pre-execution checklist
  - Concrete execution order

Spec context to consider: <list relevant spec paths>
Code context to consider: <list relevant module paths>

Write the plan and exit. Do NOT execute anything from the plan."
```

### What parallel-draft catches that single-plan-review misses

- **Different priors** — Claude may default to one architectural pattern; Codex may default to another. Surfacing the divergence early prevents lock-in.
- **Hidden assumptions** — when one plan assumes table X exists and the other reads migrations and finds X doesn't, the gap is explicit before any code is written.
- **Dropped requirements** — one plan may quietly defer something the other treats as in-scope; surfaces a real decision instead of an accidental omission.
- **Sequence disagreements** — one plan may put DI before handler; the other handler before DI. Different starting points often imply different risk models.

### Exemption

Same trivial-action exemption as Plan-Before-Execute: typo fixes, single-line changes, reading-only operations.

## Clean-Room Codex Prompt Template (added 2026-04-30 Owner directive)

> "你给自己的MD和codex提示词要注意禁止违规"

ANY prompt — Claude self-instruction or Codex dispatch — that touches non-MIT reference project source MUST paste this block at the top, fill in the angle-bracket fields, and refuse if any field cannot be filled honestly. The block is normative: copy verbatim, do not paraphrase.

```
=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: <specifier | reviewer>
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: <list (agent + lane + UTC) | "none">

REFERENCE PROJECTS IN SCOPE: <MUST include ALL THREE default mirrors
  CLIProxyAPI + sub2api + new-api, then domain extras e.g. LiteLLM /
  portkey. Omitting any default mirror makes this dispatch invalid
  — see §"sub2api + CLIProxyAPI + new-api Default Triple-Mirror".>

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===
```

After this block the prompt continues with the actual decomposition / question / task. The block itself is the guardrail; if it is missing or partially filled, the dispatch is invalid.

### When to use

- ANY decomposition task (R1/R2/R3/Rn) on the 7 reference projects
- ANY mechanism question that requires reading reference source
- ANY compare-and-contrast task spanning multiple references
- ANY "how does project X handle Y" investigation

### When NOT needed

- Reading HUAKAI-internal code (`backend/`, `docs/specs/`, `docs/process/decisions/`)
- Reading official spec documents (Anthropic Messages API docs, OpenAI Chat Completions docs) — these are public protocols, not reference-project source
- Reading reference-project README / public docs (which are intentionally published) — but if README contains code blocks they are still upstream source and the guard applies

### Rationale

DR-000 picked Option C with carve-outs (account-pool routing, auth core, billing ledger). The Option C lane separation is what makes the carve-outs defensible later. Skipping the lane guard turns Option C into Option A by accident, which would re-open R-LIC-001 (LGPL contamination risk) and burn the clean-room defense at trial / acquisition diligence.

## Source-Must-Read Trigger Matrix (added 2026-05-09 Owner directive)

> "去读源码！讲规则里面改下必须读源码"

CLAUDE.md #11 governs **how** to read reference source safely. CLAUDE.md #12 governs **when** reading is mandatory. The two rules compose:

- If a claim type below is in scope → reading source is mandatory (#12)
- Once source is read → lane guard + paraphrase prohibitions kick in (#11)

### Triggers — must read source

| Claim type | Example | Required citation |
|------------|---------|-------------------|
| Capability | "sub2api supports failover loop" | `Wei-Shaw/sub2api@<sha>:<file>:<line>` |
| Mechanism | "Helicone gateway rate-limits by both cost and request count" | `Helicone/ai-gateway@<sha>:<file>:<line>` |
| Differentiation | "HUAKAI's PASR is unlike LiteLLM's routing" | `BerriAI/litellm@<sha>:<file>:<line>` for the LiteLLM half |
| Algorithm | "litellm selects accounts by least-conn weighted" | `BerriAI/litellm@<sha>:<file>:<line>` |
| Parity verdict | "Project X has feature F" / "X lacks F" | `<owner>/<repo>@<sha>:<file>:<line>` showing presence/absence |
| Comparative table | Any cell naming a reference project | one citation per non-trivial cell |

### Source-NOT-required (carve-outs)

- HUAKAI-internal claims (cite `backend/`, `docs/specs/`, `docs/process/decisions/` paths)
- Public protocol contracts (Anthropic Messages, OpenAI Chat Completions, Gemini API spec)
- Prior `docs/process/plans/*.md` artifacts already source-cited at write time
- Vendor pricing / TTL / model lists from official docs (cite docs URL + section)

### Where to clone

Default location: `~/refs/<project>/`. One-time per evaluator. `git clone --depth=1` is acceptable for behavior-summary work; full clone only when commit history is the subject. Per #11, the clone act itself is fine — the lane guard governs subsequent reading and paraphrase.

Currently relevant repo URLs (Owner-confirmed):

- Sub2API: `https://github.com/Wei-Shaw/sub2api.git`
- All-API-Hub: `https://github.com/qixing-jk/all-api-hub.git`
- New-API: `https://github.com/QuantumNous/new-api.git` (formerly `Calcium-Ion/new-api` — repo transferred; old `Calcium-Ion/new-api@<sha>` citations still resolve via GitHub redirect)
- One-API: RETIRED 2026-05-28 (abandoned; superseded by New API) — historical evidence only, not for new mining
- LiteLLM: `https://github.com/BerriAI/litellm.git`
- Portkey gateway: `https://github.com/Portkey-AI/gateway.git`
- Helicone: `https://github.com/Helicone/ai-gateway.git` (the project's "Helicone" reference is the GPL-3.0 Rust AI gateway — see E-LIC-007 / docs/06 — not the `Helicone/helicone` platform monorepo)
- Envoy AI Gateway: `https://github.com/envoyproxy/ai-gateway.git`

### Stale-citation policy

A citation older than 30 days from current UTC date requires re-fetch of HEAD before being relied upon for new claims. Reference projects move fast; "what sub2api did 3 months ago" is not evidence of "what sub2api does now". Cited commit SHA must be one currently reachable from the default branch — verify with `git log --oneline <sha>..HEAD` before re-use.

### Enforcement

- Codex per-commit review (#8) MUST flag any unsourced reference-project claim as HIGH severity
- Slice cross-review (#7) MUST reject decomposition / parity / differentiation artifacts that fail the citation rule
- Self-check before producing output: every paragraph naming a non-HUAKAI project should carry at least one `<repo>@<sha>:<file>:<line>` reference; if none exists, either remove the claim or read source first
- "I remember" / "in my training data" / "as I recall" are explicit anti-patterns — drop the claim before drawing on memory

## Per-Commit Cross-Review Discipline (added 2026-04-29 by Owner directive)

> "所有的动作和行为都要和 codex 进行交叉处理！包括代码。熟练运行 agent 利用 codex 得 renew 功能"

Effective immediately, **every commit must pass through a Codex review before landing**. The lighter-weight `codex exec review` subcommand is the standard tool — full reviewer-lane audit (`docs/templates/codex-reviewer.md` piped to stdin) is reserved for slice-completion gating.

### Standard workflow for any code change

1. Make the change locally; run unit tests; ensure build is clean.
2. **Stage** the change (`git add ...`) but DO NOT commit yet.
3. Run: `codex exec review --uncommitted --full-auto --sandbox read-only`. Read findings.
4. Normalize findings through "Review Spiral Control And Severity Gate" below:
   - unresolved S0/S1 → fix before committing, then apply the round budget.
   - S2/S3 → record and schedule follow-up; they do not block the current commit.
5. Commit. Reference the review verdict in the commit body.
6. (Optional but encouraged) Run `codex exec review --commit <SHA> --full-auto` post-commit for an independent retro-check; archive findings if non-trivial.

### Review Spiral Control And Severity Gate

Effective date: 2026-05-24T00:00:00Z. Rule sources: [[feedback_small_closed_increments]] (2026-05-22) requires small, closed increments; [[feedback_ceremony_tiered]] says ceremony is tiered by task difficulty; this subsection narrows only the per-commit review dimension of Rule #8. [[feedback_test_quality_discipline]] / `CLAUDE.md` #14 remains a hard constraint: weak, non-discriminating tests are never "polish".

HUAKAI is a single-PM engineering project. Codex review is mandatory because it catches S0/S1 defects, feature shrinkage, clean-room/license risk, weak tests, package-structure violations, and money/security regressions. It is not a Google-scale multi-round ceremony, and the landing gate is severity-based rather than "zero findings".

#### Severity normalization table

Codex labels (`HIGH` / `MED` / `LOW` / `P2`) are review inputs, not the final gate. Claude/Codex must normalize every finding to HUAKAI severity with one-line rationale.

| HUAKAI severity | Meaning | Examples | Blocks commit? |
| --- | --- | --- | --- |
| `S0` | Catastrophic or legally unsafe to land | secret exposure, auth/billing/quota/data-loss bug, clean-room/license contamination, destructive migration, failing required build/test on release path | Yes |
| `S1` | Product correctness, trust, or rule violation that can break the current slice | feature shrinkage, money/security regression, non-discriminating test, frozen-package new file, schema-risk mistake, unhandled S0/S1 reviewer finding, uncertain severity | Yes |
| `S2` | Real defect or compliance gap that should be fixed, but does not invalidate this closed increment | provenance-tail cleanup, non-release doc sync, TODO precision, minor schema-comment mismatch, local-tool cleanup after behavior is already guarded | No, record and schedule |
| `S3` | Style, consistency, or nice-to-have cleanup | wording polish, formatting-only preference, redundant note, low-risk local comment cleanup | No, record if useful |

Severity mapping beats tool wording. A Codex `MED` can be S1 if it affects money/security, clean-room/license, feature preservation, weak-test discipline, package structure, schema safety, or required build/test status. A Codex `HIGH` can be S2 only when concrete evidence shows it is compliance polish with no current-slice correctness or release risk. When classification is unclear, promote to S1 and fix.

#### Round budget

1. Stage only the intended diff, then run Round 1: `codex exec review --uncommitted --full-auto --sandbox read-only`.
2. Normalize each finding to `S0`/`S1`/`S2`/`S3` with one-line rationale.
3. If Round 1 has no unresolved S0/S1 and local required checks pass, the commit may land with S2/S3 recorded.
4. Run Round 2 only when Round 1 found S0/S1, or when the fix materially changed behavior, security, schema, quota/billing/auth, clean-room/licensing posture, or test semantics.
5. After Round 2, stop. Continue reviewing the same commit only if unresolved S0/S1 remains or Owner explicitly asks for another round.

#### Deferred finding record format

Record deferred S2/S3 in the commit body or `docs/process/reviews/DEFERRED-<topic>.md` using this format:

```markdown
Deferred review findings:
- [S2|S3] <short title> — source: Codex review round <N> <finding id/label>; rationale: <why it does not block this commit>; follow-up: <next slice / issue / doc path>; Owner decision: <none | needed by date>
```

Deferred means scheduled, not dropped. If the same S2 appears again in the next related slice, either fix it there or promote it with rationale.

#### Anti-spiral rule

- Review should not discover the spec drip-by-drip. If review repeatedly reveals new requirements, stop expanding the current commit, close the no-S0/S1 slice, and write a complete next-slice spec.
- Do not accumulate unrelated compliance polish, provenance cleanup, or style-only edits into a commit whose S0/S1 issues are already closed.
- Never relabel real defects down to escape the round cap. Security exposure, auth/billing/quota/data loss, clean-room/license contamination, feature shrinkage, non-discriminating tests, frozen-package new files, schema-risk mistakes, and failing required checks remain S0/S1 unless evidence disproves the risk.
- Post-commit review is a retro-check, not a same-commit loop. `codex exec review --commit <SHA> --full-auto` is optional; S0/S1 from retro-check requires an immediate fix commit or revert/hotfix, while S2/S3 is recorded for follow-up.
- Complete vertical slices and release gates still use reviewer-lane `/cross-review`; this two-round cap only controls per-commit review iteration.

### CLI flag notes

- The canonical Owner command is `codex exec review --uncommitted --full-auto --sandbox read-only`. Run from the repo root; if the CLI rejects `--sandbox`, check `codex exec review --help`, record the CLI mismatch, and run the closest read-only/sandboxed equivalent available.
- `--uncommitted` and `--commit <SHA>` are mutually exclusive with a positional `[PROMPT]`. To customize the review focus, write notes into `docs/process/reviews/PENDING-<descriptor>.md` first; Codex picks that up via the working tree.
- Use `--full-auto` for sandboxed automatic execution (recommended for review of working-tree changes).

### When NOT to skip

The discipline applies even when:
- The change is a single-file doc edit (catches stale assertions, broken cross-references).
- The change is "obviously safe" (the reviewer catches what looks safe but isn't, e.g. silent fallback paths).
- Time pressure (skipping the review is how slice 5's 5 HIGH defects got past the maintainer; do not repeat).

### When to escalate to full reviewer-lane

- Slice completion (vertical slice declared "done")
- Cross-feature integration commits
- Money-path changes (any code that writes to `usage_records`, `billing_events`, or quota tables)
- Schema migrations
- Authentication / authorization core changes

In those cases, run `codex exec --full-auto --sandbox read-only -C <repo> -` with the template at `docs/templates/codex-reviewer.md` piped to stdin.

### Renew / version sync

If `codex exec review` syntax errors with "unexpected argument" or "cannot be used with [PROMPT]", the Codex CLI was updated. Check `codex exec review --help`; the canonical option list as of this writing is: `--uncommitted`, `--commit <SHA>`, `--base <BRANCH>`, `--full-auto`, `--ignore-rules`, `--ephemeral`, `--json`. Update this section if the CLI changes.

## Package & File Structure Discipline (added 2026-05-22 Owner directive)

> Owner: "主要是怎么杜绝?你给我们的规则写进 codex 必读的文件里"
> 触发:`internal/gatewayhttp` 长成 68 文件 / 2 万行的 god-package —— 管理 /
> 用户 / 计费 / 网关 handler 全挤一包,且在 2026-05-15 结构规则之后仍在增长。
> 这是**硬规则,不是软纪律**("规则非纪律"),codex 与 Claude 同等适用。

代码**按职责组织**。此规则同时管 Go 包、Go 文件、Rust module —— 不只是 Rust、
不只是文件。

### The rule

1. **一个包 = 一个内聚职责。** 新增功能域(新 handler 家族、新子系统)时,
   **禁止**默认把它丢进已有的大包。建一个职责清晰的新包。
2. **一个文件 = 一个内聚职责。** 不把无关的东西堆进一个文件。非测试代码
   超过约 500 行的源文件是拆分信号。
3. **包体量预算(拆分触发线)。** 一个 Go 包超过 **约 20 个非测试源文件
   或约 5000 行非测试代码**,必须要么按职责拆分,要么(若拆分被推迟)
   **冻结、不再加新文件**。

### ~~Frozen packages~~ → 软预算门(2026-06-11 修订,硬冻结退役)

**实测结论:硬冻结失败了。** 「禁新增文件」只挡住了新文件,把新逻辑逼进旧
文件继续膨胀(gatewayhttp 冻结后仍长到 13k+ 非测试行,0 子包)。替代物是
**强制软预算门** `backend/internal/codebudget`(随标准 unit 门运行):

- 非测试 Go 文件 ≤ **600 行**;单目录包非测试 ≤ **6000 行 / 20 文件**;
- 存量超标项按当前体量入 `baseline.json` 豁免,**只挡继续增长**(基线 +5%
  余量);超余量 = 门红,出路是**按职责拆子包**(范本:`internal/provider`
  9 核心 + 13 子包),不是把基线改大;
- 基线再生成(`HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1`)只允许在有意拆分/
  重构使体量**下降**之后。

原冻结三包(gatewayhttp/gateway/proto)在门绿的前提下**允许再新增文件**,
但新功能仍优先落新子包(如 `proto/<family>/`);往超标包里加行会烧掉它
仅有的增长余量。为修 bug 而改既有文件照旧允许
(例:W3 错误模型 → `internal/clienterr`,而非 `gatewayhttp/public_error.go`)。

> **澄清(2026-06-07 Owner「入站协议为什么冻结」纠错):冻结 = 反 god-package 的模块化约束,不是「协议/功能层不可扩展」。** 加新入站协议(Gemini 原生 /v1beta、realtime 等)、新出口能力**正是最高价值的活**——做法 = ClientAdapter/handler 落 **新包**(如 `internal/geminiclient`)+ 对既有 registry/route/capability 文件做 **加性 edit**。**严禁**为绕冻结而搞 hack(request-body 重写 / shim / 把 model、stream 注入 body)。若新包 handler 需要冻结包内部能力 → **导出该能力(既有文件加性 edit)** 供其调用——这才是模块化正道,**不需要任何「冻结例外」**。协议广度是网关 #1 价值轴,冻结规则从不阻止它。

> **全网关范围(2026-06-07):** HUAKAI = Go `gatewayhttp` 大脑 + **Rust 出站强伪装 sidecar**(方向 C)。任何「整个网关」审计/评估 **必须纳入** `exploratory/rust-core-gateway/`(`tls-sidecar`:自维护 BoringSSL fork + JA3/JA4 + H2 SETTINGS wire 指纹 + fail-closed 契约)与 `backend/internal/transport/mimicry/`,**不能只看 `backend/` Go**。反检测/TLS+H2 线级伪装是真实且领先三家的能力轴。

### Enforcement(这才是"杜绝")

- **机器门**:`internal/codebudget` 在每次 unit 门自动执行,超预算/超基线
  余量直接红——这是主执法点,不依赖评审记忆。
- 任何**计划 / spec** 若要新建文件,必须逐个写明目标包,并确认预算门保持绿。
- **per-commit 评审**(3 镜头对抗评审,2026-06-11 起替代 codex 门)必须把
  以下情况标为 **HIGH 结构违规、阻断提交**:
  - 任何 commit 把包推过体量预算或把基线豁免项推过增长余量;
  - 用调大基线代替拆分(基线只许在体量下降的重构后再生成);
  - 把无关职责塞进同一个包或文件。
- **切片交叉评审**(本文件 Cross-Review Protocol)在切片收尾做同样检查。
- 被派发的任务,若其 spec 会把包推爆预算,必须拒绝并改写(改成新子包)。

## Test Quality Discipline (added 2026-05-22 Owner directive)

> Owner: "你每次给的测试啥的是不是都很一般,没有质量?"
> 触发:W3a 的 GW-02 回归测试用了非判别性 fixture —— 裸 401 本就分类为
> `invalid_grant`,于是 body 被忽略时测试仍通过,等于没守住它该守的回归。

一个测试存在的意义是:**在它该抓的缺陷出现时变红**。一个无论代码对错都通过
的测试是**没有价值的 —— 比没有更糟**,因为它给假信心。

### The rule —— 每个测试必须

1. **说得出它守的是什么缺陷。** 作者必须能一句话说清:这个测试抓的是哪个
   具体回归 / bug。说不出,这测试就没有存在理由。
2. **过 mutation 自检。** 宣布测试写完前:在脑里把它该守的缺陷**真的引入**
   (删掉守卫、翻转条件、stub 掉输入)。测试**必须变红**。若它照样通过,
   说明 fixture 不判别 —— 重新设计。
3. **用判别性 fixture。** 输入 Y 的期望输出,必须与**坏掉的代码**会产生的
   输出不同。若 `Y → X` 在代码正确和损坏时都成立,这 fixture 什么都没证明。
   (典型陷阱:用「光看 status 就already得到期望 class」的状态码去测
   「分类读了 body」。)
4. **测真实风险,不测"能跑"。** (见 risk-based testing:丢钱 / 串租户 /
   数据损坏 / 信息泄露 —— 注入真实触发,断言风险不存在。)用 `nil` 兜底的
   stub 把测试弄绿,是在掩盖风险,不是测试风险。

### 优先写自证测试

可行时,让测试在运行期自己证明判别性:在测试内同时跑「正确路径」和
「损坏 / 基线路径」,断言两者结果不同。这样的测试在 fixture 哪天不再判别时
会自己炸。(W3a 修好的 GW-02 测试是范式:它断言
`class(带 body) != class(只看 status)`。)

### Enforcement

- codex per-commit review + 切片交叉评审**必须**把以下情况标为 finding:
  任何 fixture 无法在它声称守的缺陷上变红的测试;任何用 `nil` 兜底 stub
  掩盖被测风险的测试。
- **spec 若规定一个测试,必须给出判别性的例子,而不只是测试意图。** 一条
  写「证明 X 驱动 Y」却没给「去掉 X 就改变 Y」的 fixture 的 spec,是不完整的
  spec —— 这正是 W3a 弱测试的根因(spec 例子用了非判别性的关键字)。

## Reference-Project Comparison On Decisions (added 2026-05-23 Owner directive)

Owner 2026-05-23 quote「需要我做决定的时候要带上借鉴项目功能模块得处理方法。写进规则」。镜像 `CLAUDE.md` #15 给 codex / reviewer lane。

### 规则适用面

- **Claude PM-orchestrator** surface 决策给 Owner(`AskUserQuestion` / plan §D / schema-gate / A/B/C 选项 / sequencing)时,必须每选项附 ≥1 个 `~/refs/<project>/<file>:<line>` 参考项目对照引证 + 1 句概括;若该参考项目无等价问题,需明指 cite("`<repo>@<sha>:<file>:<line>` shows X is single-tenant so concern doesn't apply") 不可空话。
- **Codex 撰写 plan §D 时**:plan 文件本身就是 Owner 决策的输入,plan §D 表格必须按列含「参考项目对照」或专门 sub-section,引证同上。Claude 转写 surface 时 fill-in。
- **Codex per-commit review + 切片交叉评审**:必须把以下情况标为 HIGH:
  - Claude surface 决策给 Owner 时缺参考项目对照
  - plan §D 表格无参考项目列(或显式 "no equivalent" 注脚)
  - 引证形如 "sub2api/new-api 都 X" 无 `<repo>@<sha>:<file>:<line>` cite (违 #12 source-must-read + #15 双重违反)
- **Gemini lane** 同 codex 写 plan 时遵守。

### Why

Owner 不能凭 Claude/Codex 内部 trade-off 拍板决策;参考项目横向对比 = Owner 视角必备。`AskUserQuestion` option 没 ref 对比 = Owner 视线封闭。

### How

- 每 `AskUserQuestion` option description 字段最后 1-2 句加"参考项目对照"
- 每 synthesis plan §D 表加列 `参考项目对照`
- 每 schema-gate proposal 加 prestudy §A 链接(prestudy 必含 4 ref 项目逐条 cite)

### Anti-pattern

- "A vs B" 只讲 HUAKAI 内部权衡,Owner 需问"sub2api 怎么做"
- 写 ref 项目断言但无 file:line cite (违 #12 + #15 双违)
- 把 "no equivalent" 写成空话不 cite

### Codex reviewer enforcement

`codex exec review --uncommitted` / 切片 cross-review 必须扫:
- staged diff 内是否有 plan §D 表格无参考项目列
- staged plan/synthesis 内是否有未 cite 的 ref-project behavior 断言
- staged docs 决策点是否缺 prestudy 链接

任一标 HIGH 阻 land。

## sub2api + CLIProxyAPI + new-api Default Triple-Mirror (added 2026-05-29 Owner directive)

Owner 2026-05-29 quotes「你做任何功能的时候都要看下 sub2 和 cliproxy 是如何做的」+「刚刚的问题是 这个支付功能你搞错了！他有两套，你以为只有一套! 下次你开始写功能的时候必须调研成熟的项目」+「再加一个 new-api」。镜像 `CLAUDE.md` #16 给 codex / reviewer / 所有 lane。

### 触发的真实故障

支付子系统首版只设计了**一条入账路径**(管理员手动),因为**开写前没调研成熟项目**;成熟架构实际有**两条**(自动支付回调/webhook + 管理员手动)。漏掉整条 feature 路径 = 架构错+不全。本规则强制**开写前的调研步**,与 #15(仅在决策 surface 触发)不同、比 citation 卫生更广:调研是为了**开写前摸清功能完整形态(每条 path/mode/state)**,不是事后补脚注。

### 规则

- **三面默认镜子,每个 feature 开写/计划前都查**:`~/refs/sub2api/`(account-hub / 支付 / billing / topup / 订阅 parity 最全源)+ `~/refs/CLIProxyAPI/`(relay account→API 头号源,`@21fad9db`)+ `~/refs/new-api/`(AI 网关 / channel / topup / 兑换码 / quota-log 最全源,one-api 血统)。读**三者各自怎么组织该 feature——数路径/模式,不只确认存在**。其它领域参考(routing 看 LiteLLM、portkey 等)叠加在三默认之上,不可替代。
- **设计前先产出 shape inventory**:列出成熟项目对该 feature 暴露的全部 path/mode/state/actor(例:支付 = {自动 webhook 入账, 管理员手动入账, 退款, 幂等重放}),再决定 HUAKAI 当前建哪些 / 哪些进路线图(Feature Preservation Rule)。inventory 必须先存在,杜绝遗漏式缺失。
- **每个 codex dispatch prompt + 每个 plan artifact 的 `REFERENCE PROJECTS IN SCOPE` 必须同时含 CLIProxyAPI + sub2api + new-api 三者**(+ 领域附加)。支付 P2a 的 codex dispatch 只写了「new-api / sub2api」漏了 CLIProxyAPI —— 这正是本规则要杀的 bug;缺任一默认镜子的 dispatch/plan 无效,必须重拟。
- **no-equivalent 合法但必须先看**:镜子可能确实没这 feature(已核实:CLIProxyAPI 是纯 relay account→API 代理,**无 payment/order/billing/subscription 模块**——`payment|billing|webhook|recharge` 关键词命中全是 `antigravity_credits` vendor-quota + websocket relay,`~/refs/CLIProxyAPI/internal/` 无 payment 包)。仍要写显式 source-cite 的 "no equivalent" 注脚(per #15),不可静默跳过。
- **sub2api 默认裁决 (Owner 2026-05-29「有功能模块选择做法的时候,默认按照 sub2api 做。他已经是成熟体了」)**:三镜调研后若做法分歧、工程岔路(数据模型/状态机/重置策略/幂等做法等)需选一个,**默认采 sub2api 同款**(最成熟),再叠 HUAKAI fusion-upgrade delta(非纯 parity)。减少逐 fork 问 Owner、提吞吐。Carve-out:fork 触 money/security/schema 高风险闸、或 sub2api 做法明显劣时仍 surface;Owner 显式选择优先于此默认。

### Codex reviewer enforcement

`codex exec review --uncommitted` / 切片 cross-review 必须把以下标为 HIGH 阻 land:
- feature 实现/plan/dispatch 未在开写前调研**三面**默认镜子
- `REFERENCE PROJECTS IN SCOPE` 缺 CLIProxyAPI / sub2api / new-api 三者任一
- 某镜子无等价物却没写 source-cite 的 "no equivalent" 注脚
- 复杂 feature 的 plan 缺 shape inventory(path/mode 清单)导致路径遗漏

## Module Interplay & Runtime Logic Review (added 2026-07-02 Owner directive)

Owner 2026-07-02 原话「看模块之间的作用与配合……不单单是这一块，而是我们整个项目的运行逻辑都要经得起推敲。一定要先看三家是怎么做的！再看看我们是怎么做的！」+「还要测试并发！这些都要测！」+「测试要重！模型用最便宜的就行」。镜像 `CLAUDE.md` #17,给 codex / reviewer / 所有 lane。

### 与 #16 的区别

#16 保证「功能完整形态(path/mode/state)不遗漏」;本条更进一层——看**模块之间的作用与配合(运行逻辑)**。功能各模块单独都在、单测都绿,但**模块交界处的协作**可能是断的。实证(2026-07-02 relay 细粒度 E2E):billing 结算 ↔ quota reconciler 配合断裂——reconciler job 卡 queued、reservation 不结算、并发槽只靠 lease 过期释放;而 RPM / 计费 / 停用等**每个单模块测都 PASS**。这类缺陷只有沿「模块协作链」测才抓得到。

### 规则

- **审查对象 = 模块间的数据/状态传递 + 失败协作**:一个请求/操作流过系统时,追每个颗粒度模块从上一环拿什么(identity / hold_id / account_id / attempt 上下文 / reservation)、产出什么、传给下一环什么;失败时(上游 4xx/5xx、流式中途断、余额不足、结算 DB 故障、换号)各模块怎么协作回滚补偿。
- **范围 = 整个项目运行逻辑,不限 relay**:auth 采集流状态机、billing 预扣↔结算↔abort、quota↔选号↔并发槽释放、pool 选号↔渠道健康回流↔failover、credential 物化↔转发、media 任务生命周期、结算恢复 DLQ,均需经得起推敲。
- **强制次序:先三镜后自己**。碰某子系统的配合逻辑前,先读 sub2api / new-api / CLIProxyAPI 同款子系统怎么串联模块、失败怎么协作(带 file:line),再对照 HUAKAI,确认不漏钱/冻钱/重复扣/状态不一致/换号失败/槽不释放。
- **测试要「重」+ 必测并发,不因额度缩水**:配合处测试构造跨模块真实触发,判别断言咬住「配合错的后果」;**并发**(per-key cap / 账号槽 / 用户级)并发打满真实触发排队/拒绝/槽释放,必测。上游额度有限只允许**选最便宜模型 + 压小 max_tokens**,不允许减少测试场景或跳过失败协作。
- **产出**:`docs/architecture/runtime-logic/<子系统>.md`,记模块协作图 + 关键配合点 + 三镜对照 + 已知配合缺口。

### Codex reviewer enforcement

`codex exec review --uncommitted` / 切片 cross-review 必须把以下标为 HIGH 阻 land:
- 触及跨模块配合(billing/quota/pool/failover/采集流状态机等)却未先对照三镜运行逻辑
- 测试只覆盖单模块、未测配合处(模块交界的数据/状态传递 + 失败协作)
- 涉及并发的路径未做并发触发测试
- 以「省额度」为由缩水本应充分的功能测试

## Parallel-Edit Coordination (added 2026-05-30 Owner directive)

Owner runs **multiple AIs (Claude / Codex / Gemini) and multiple threads in parallel** on the same working tree. They edit the same files concurrently → silent overwrites. Every agent MUST broadcast what it is editing, which core feature, and why — and check before touching a shared file.

**Mechanism**: `.coordination/` (canonical spec in `.coordination/README.md`). Per-agent lock files `locks/<agent>.json` (each agent writes ONLY its own → the coordination state itself never collides). `activity.log` is an append-only intent broadcast.

**Protocol — before editing ANY shared repo file:**
1. `bash .coordination/check.sh [<file>]` — see who's editing what (stale locks past `ttl_seconds` ignored).
2. If a live lock by **another** agent lists your target file → **do NOT edit it** (no overwrite); pick other work / wait for its `done` / coordinate with Owner.
3. `bash .coordination/claim.sh "<agent>" "<file1,file2>" "<core_feature>" "<purpose>"` — refuses (exit 2) on conflict, else writes your lock + logs intent.
4. Re-run `claim.sh` periodically to refresh the heartbeat during long edits.
5. `bash .coordination/release.sh "<agent>"` when done.

Scripts are convenience; hand-writing `locks/<agent>.json` per the README schema is equally valid. This is a **broadcast convention**, not an OS lock — adoption by every AI is mandatory. Codex per-commit / slice review SHOULD flag a diff that edited a file held by another live lock without coordination.
