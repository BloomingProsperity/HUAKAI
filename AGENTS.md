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

REFERENCE PROJECTS IN SCOPE: <list e.g. sub2api / one-api / portkey>

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
| Mechanism | "Helicone batches usage writes via Postgres outbox" | `Helicone/helicone@<sha>:<file>:<line>` |
| Differentiation | "HUAKAI's PASR is unlike LiteLLM's routing" | `BerriAI/litellm@<sha>:<file>:<line>` for the LiteLLM half |
| Algorithm | "one-api selects accounts by least-conn weighted" | `songquanpeng/one-api@<sha>:<file>:<line>` |
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
- New-API: `https://github.com/Calcium-Ion/new-api.git`
- One-API: `https://github.com/songquanpeng/one-api.git`
- LiteLLM: `https://github.com/BerriAI/litellm.git`
- Portkey gateway: `https://github.com/Portkey-AI/gateway.git`
- Helicone: `https://github.com/Helicone/helicone.git`
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
3. Run: `codex exec review --uncommitted --full-auto`. Read findings.
4. If findings exist:
   - HIGH severity → fix before committing. Repeat step 3.
   - MED severity → fix or document explicitly in commit message why deferred.
   - LOW severity → may proceed; mention in commit message.
5. Commit. Reference the review verdict in the commit body.
6. (Optional but encouraged) Run `codex exec review --commit <SHA> --full-auto` post-commit for an independent retro-check; archive findings if non-trivial.

### CLI flag notes

- `codex exec review` does NOT accept `--sandbox` / `-C` flags directly. Run from the repo root.
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
