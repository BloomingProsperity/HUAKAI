# Plan: feature-tree gap-closure — multi-agent implementation waves

**Date:** 2026-06-03 · **PM:** Claude (Opus, lead architect) · **Charter:** CLAUDE.md #8/#9/#13/#14/#15
**Owner directive:** "扫描功能树看缺失 → 全面多Agent推进 → 最大算力 → 禁止偷懒" (multi-agent push, max compute, no laziness, quality-first). Owner Start Gate satisfied (repeated 继续/持续推进/开始).

## Scope

Close the 9 actionable feature-tree gaps via **verify-then-residual** (build only the true residual, never the design's from-scratch over-statement). `totp-2fa` is **Owner-gated** (excluded until Owner authorizes — design's own Owner-Decision-Context blocks it).

Gaps: usage-dashboard (slice 1 landed; remaining slices), platform-settings, multi-oauth, pricing-catalog, per-key-controls, app-rate-limit, tiered-billing, notifications, content-moderation.

## Already landed this session (verify-then-residual + mutation-tested + pushed)
- tls-fp-crud admin CRUD (4b9d7a4)
- relay-log → token residual on GET /v1/me/usage (3142c07)
- usage-dashboard → GET /v1/me/analytics/time-series self-serve slice (c7ee46c)

## Approach (per pm-orchestrator role-split)

1. **Verify (running — workflow wsnmtj4ax):** 9 parallel sonnet agents produce real-code-verified residual specs (false premises flagged, riskClass, reserved migration #, file manifest, discriminating-test plan, durable spec in docs/process/gap-specs/).
2. **Implement, risk-tiered:**
   - **safe-read / schema (low-risk):** sonnet agent drafts first slice in an **isolated git worktree** (no cross-edit collision) + discriminating tests + builds/tests in-worktree. Parallel, batched (≤~5 to bound machine + my review load).
   - **money / auth / hot-path (high-risk):** **Claude hand-codes** (charter: high-risk files = Owner confirmation). Park completed work in `needs_owner` for Owner approval before landing.
3. **Adversarial review (independent lane — satisfies charter #8 "no self-approve without review"):** a separate agent reviews each drafted slice: build green? tests green? **discriminating-test mutation-verified (#14)**? CMB-5/CMB-7 honored? frozen-package compliance (#13)? modularity <500/<80 (#13)? parity/clean-room (#11/#12)? Verdict + severity (S0–S3). Codex per-commit review (#8) layered on risky commits where codex isn't saturated by the burn.
4. **PM integration gate (serialized — Claude):** I review verdicts, re-run build/vet/test + mutation on landing, resolve integration collisions, land green low-risk slices, **park high-risk for Owner**.

## Integration serialization (collision control)
- **Migration numbers:** 7 designs all claimed 0077. Assigned per-gap at verify time (0077+i); finalized sequentially at integration. Never parallel-claim a number.
- **routes.go:** each slice adds a distinct route block; merged serially by PM (frozen-package-safe: routes.go is cmd/, not a frozen internal pkg).
- **sqlc:** worktree agents add only `.sql` + `sqlc.yaml` entries; PM runs `sqlc generate` **once** on landing (avoids generated-file merge conflicts; commit only real diffs, restore CRLF noise).

## Quality gates (all mandatory before land)
- Build `go build ./...` + `go vet` green; package tests green.
- **Discriminating test mutation-verified** (#14): reintroduce the defect → test goes red → restore. Evidence in commit body.
- CMB-5 (no creds logged/selected), CMB-7 (router reads no creds/writes nothing).
- Frozen pkgs internal/{gatewayhttp,gateway,proto}: no new files (#13).
- Modularity: files <500 lines, funcs <80 (#13).
- Feature preservation (#charter): never drop a capability — convert risky→Safe-Equivalent/Flag/Roadmap.

## Owner-park items (high-risk → Owner approval before land)
- **tiered-billing** (money: billing DSL + funding-source on settle path).
- **multi-oauth** (auth: OAuth providers, pending-oauth, sessions).
- **app-rate-limit** (hot-path: per-user gate in request path) — gate logic reviewed extra.
- **content-moderation** (hot-path: inbound screening in dispatch).
- **Any new migration** (schema = high-risk per charter) — surfaced before apply.

## Success criteria
Each gap's highest-value first slice: landed (or Owner-parked if high-risk), build+vet+test green, discriminating tests mutation-verified, pushed to origin. Coverage-verification + roadmap + per-gap specs durable in docs/. No feature silently dropped.

## Blast radius / what could go wrong
- Worktree Go builds on Windows are heavy — bound parallelism; if thrash, serialize.
- Agent-drafted code quality risk (sonnet botched hard design before) → mitigated by adversarial review + PM mutation gate + risk-tiering (agents only draft low-risk; Claude hand-codes money/auth).
- sqlc regen churns line-endings on ~50 files → stage only real diffs, restore noise (proven on c7ee46c).
- Coordination tooling (.coordination/*.py) broken locally (python alias) — no active contention (locks empty), proceed; fix python as follow-up.

## Decision points for Owner
- Approve each Owner-parked high-risk slice (tiered-billing, multi-oauth, app-rate-limit, content-moderation, any migration) before landing.
- Confirm totp-2fa authorization if/when 2FA is wanted (currently excluded).
