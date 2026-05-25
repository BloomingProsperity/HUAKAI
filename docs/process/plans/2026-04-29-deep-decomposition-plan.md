# 2026-04-29 Deep Decomposition Plan — 7 reference projects

| Field | Value |
| --- | --- |
| Owner directive | "我要的是每个都深，如果不深就没有意义" + "每次执行任务之前必须做plan" |
| Author | Claude PM-Orchestrator |
| Trigger | Round-1 specifier (90-110 lines/700 words) too shallow; Round-2 in flight at 2500-word target may STILL be too shallow per Owner's pushback |
| Status | DRAFT — to be reviewed by Owner BEFORE next round dispatch |

This plan exists because I was dispatching codex tasks without a per-project depth baseline. Owner's process critique: "你给他的提示词是什么样子的的。每次执行任务之前必须做plan". Captured.

---

## Diagnosis of why prompts so far were under-spec'd

1. **Round 1 prompt** asked "600–1500 words" (template default). For repos of one-api / portkey / litellm scale, that's 1-2% of what real source-verified deep decomposition needs. Outputs hugged the lower bound.
2. **Round 2 prompt** raised to "2500-4500 words" + force critic-finding addressing. Better but still under-spec'd: a 50K-LOC repo's single feature like "channel auto-disable" (touching: relay loop / retry / cache / monitor goroutines / status DB write / notification dispatch / scheduled probe) is **roughly 8-15 source files** of behavior. 2500 words ≈ 8-12 pages ≈ NOT enough room to enumerate 8-15 files of behavior at the per-sub-behavior detail Owner wants.
3. **Parallelism**: 7-way concurrent codex dispatch ≈ each task gets 1/7 of the model's effective focus. For deep work this is the wrong tradeoff. Sequential or 2-way parallel preserves per-task budget.
4. **No reading-path guidance**: I told codex "read source from URL" — that's not enough. Real depth requires "read these N entry points, then trace down M call paths, then write." Without the path, codex picks shortcuts.

---

## Per-project realistic depth baseline

Based on each project's repo size + the feature's actual surface area:

| Project | Repo LOC est. | Feature surface | Realistic word target | Sub-behavior count | Source files codex MUST read before writing |
|---|---|---|---|---|---|
| **one-api** | ~50K Go | Channel auto-disable: 2 disable gates × 4 error classes × 3 entry paths × monitor goroutine × notification dispatch | **8,000–12,000** | 18-25 | Relay error classifier; channel cache; monitor goroutine; status DB writer; scheduled-test runner; notification dispatcher; 3-4 retry-related files |
| **portkey** | ~80K TS | Streaming: handler entry + SSE parser + per-provider stream format × 6 providers + terminal frame detector + partial usage extractor + error classifier + response transformer | **10,000–15,000** | 20-30 | Stream handler entry; SSE parsing module; per-provider transformer (sample 3-4 providers: Anthropic, OpenAI, Gemini, Bedrock); usage extractor; error normalizer |
| **helicone** | ~60K TS+Rust | Cost routing: rule engine + cost forecast + weight vector resolver + tier mapping + custom-rule chains + audit | **8,000–12,000** | 18-25 | Router core; rule chain evaluator; cost lookup table; tier resolver; audit log dispatcher; provider catalog |
| **litellm** | ~250K Python | Cooldown + retry hierarchy: 4 retry levels × failure-rate algo × traffic-volume floor × connection-error class taxonomy × per-deployment override × exception-type override | **10,000–15,000** | 25-35 | Cooldown handler; retry resolver; exception taxonomy module; per-deployment policy reader; traffic counter; connection-error classifier |
| **new-api** | ~60K Go | Cache billing: 4-bucket pricing + reasoning effort + token accounting + audit + multi-provider mapping | **6,000–10,000** | 15-22 | Pricing snapshot reader; bucket categorizer; reasoning effort translator; usage event extractor; settler tx |
| **all-api-hub** | ~30K TS | Vault: site recognizer + storage layer + cross-source aggregator + price compare panel + secure storage primitives | **5,000–8,000** | 12-18 | Site recognition heuristic; secure storage adapter; multi-source aggregator; price compare component |
| **envoy-ai-gateway** | ~40K Go | Outer/inner topology + AI Route CRD + Backend resource + Quota Policy + reconcile loop + status conditions | **10,000–15,000** | 20-30 | Outer-tier auth + global limits; inner-tier dispatcher; AI Route reconciler; Backend resource controller; quota controller; status condition lifecycle |

**Total realistic depth: ~57,000–87,000 words across 7 projects** (vs round-1's ~5,000 words total — a 12-17× gap).

---

## Sequential execution plan (replaces "7-parallel")

Reasoning: 7-parallel dilutes Codex's per-task budget. Run **2 at a time** (one heavy + one light) to give each meaningful budget without stalling progress.

Dispatch order (by repo LOC, heaviest first so any blocker surfaces early):

| Wave | Task A (heavy) | Task B (light) |
|---|---|---|
| Wave 1 | litellm cooldown+retry (~250K LOC) | all-api-hub vault (~30K LOC) |
| Wave 2 | portkey streaming (~80K LOC) | new-api cache billing (~60K LOC) |
| Wave 3 | envoy outer/inner+CRD (~40K LOC) | helicone cost routing (~60K LOC) |
| Wave 4 | one-api channel auto-disable (~50K LOC) | (single — last waveform) |

Each wave: dispatch 2 codex tasks in parallel, **wait for both to complete** before next wave. Estimated 30-60 min per wave. Total: 2-4 hours.

---

## Strict prompt requirements (Round 3 if needed)

Round 2 may produce 2500-word outputs that still miss depth. If R2 outputs land below the per-project word target above, Round 3 dispatches with stricter prompts containing:

1. **Word target inline**: e.g. "MUST produce 8,000-12,000 words. Below 7,000 = REJECTED."
2. **Sub-behavior enumeration target**: e.g. "MUST enumerate 18-25 distinct sub-behaviors as S-1 through S-25 each with state transitions + concurrency notes."
3. **Reading path**: e.g. "BEFORE writing, you MUST inspect the following 8 source regions: <list>. State which you read and one sentence about each."
4. **Critic findings as scope minimum** (already in R2 prompt; keep).
5. **Lifecycle tracing** (already in R2; expand from 3 to 5 lifecycles for heavy projects).
6. **Failure mode enumeration**: 12+ for heavy projects (vs current 8).

Format compliance enforcement: "missing any of §1-§11 = REJECTED + the file is moved to `_superseded-roundN/`."

---

## Synthesis stage (after deep round)

Each project ends with **3 inputs** (specifier-deep + critic + claude-draft) → **1 synthesis output**:

```
docs/decompositions/<project>/<feature>-synthesis.md  (~3,000-5,000 words)
```

The synthesis combines:
- Specifier's deep behavior trace (canonical truth where they disagree on facts)
- Critic's gap-finding (must-address points)
- Claude-draft's HUAKAI-fit risk framing
- Owner's explicit divergence calls (Personal vs SaaS Edition, multi-tenant, PostgreSQL native)

I write each synthesis manually (cross-cutting/ pattern). 7 syntheses ≈ 1.5-2 hours.

---

## Final fusion-architecture document

After all 7 syntheses land, I write:

```
docs/02_HUAKAI_FUSION_ARCHITECTURE.md  (~6,000-8,000 words)
```

Structure:
- §1 product elevator pitch (1 paragraph) + architecture diagram (mermaid)
- §2 typical request lifecycle (chat completion full-stack timing diagram)
- §3 module boundary table (per `internal/<pkg>/`)
- §4 reference fusion table (which pattern from which project, why, what diverged)
- §5 differentiation thesis (HUAKAI vs each reference, 3 paragraphs each)
- §6 user journeys (end-user / personal-edition operator / saas tenant)
- §7 open architectural questions for Owner

This is the document Owner originally asked for. It needs all 7 deep syntheses as input — that's why it's the LAST step, not the first.

---

## Time/cost honest estimate

| Phase | Codex time (parallel waves) | Claude time | Wall clock |
|---|---|---|---|
| Round 2 (in flight) | 30-60 min | — | 1 hour |
| Round 3 (if R2 shallow) | 90-120 min | — | 2 hours |
| Synthesis | — | 90-120 min | 2 hours |
| Fusion architecture doc | — | 60-90 min | 1.5 hours |

**Total: 6-7 hours** if Round 2 succeeds; **8-10 hours** if Round 3 needed.

This is multi-session work, not a single sit. Owner should expect this to span 2-3 working sessions.

---

## What I should NOT do anymore

- **Dispatch codex without a per-task depth target**. Word count target inline.
- **Run 7 parallel for deep work**. Max 2 parallel. Heavy work serial.
- **Treat critic as primary depth source**. Critic is verification; specifier must be the deep canonical decomposition.
- **Skip the planning step**. Every codex batch from now on writes a plan first to docs/process/plans/, gets Owner sign-off, then dispatches.

---

## Decision pending Owner

**Pending now**: 7 R2 specifier in flight. Two scenarios:

- **A. R2 outputs land at 2500-3500 words** → likely still under per-project target. Dispatch Round 3 (sequential 2-parallel) with stricter prompts.
- **B. R2 outputs land at 5000+ words and address all critic findings** → accept; move to synthesis.

Owner: please tell me which threshold you want me to apply for "deep enough" — strict (per per-project target above), medium (5000+ flat), or lenient (R2 as-is). Default if no answer: strict.

---

## Self-criticism re: this very plan

This plan was written AFTER Owner explicitly said "每次执行任务之前必须做plan". The 7 R2 are already running because I dispatched them BEFORE writing this plan. That's exactly the failure mode Owner called out. Logging it so I don't do it again.

Going forward: every codex batch dispatch is preceded by a `docs/process/plans/<date>-<taskname>.md` artifact.
