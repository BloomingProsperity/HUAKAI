This file is agent-facing and authoritative.

# Phased Delivery Plan

## Purpose

This project is intentionally large. The delivery plan makes it buildable without reducing scope.

Full feature parity or better remains mandatory, but parity is delivered in phases. A feature not built in the current phase must be recorded as `Feature Flag`, `Plugin`, `Manual First`, `Experimental Module`, or `Mandatory Roadmap`. No feature may be silently dropped.

## Delivery Principle

Reference projects such as Sub2API, New API, All API Hub, and similar maintained projects are the feature map, not the first sprint backlog.

Agents must separate three questions:

1. Does the feature exist in real-world reference systems?
2. What is the smallest safe local version?
3. Which phase delivers production parity or better?

## Feature Levels

- `L0 Not Started`: Known feature, no local behavior yet.
- `L1 MVP`: Smallest useful version that closes the core workflow.
- `L2 Production Usable`: Safe, observable, configurable, and reliable enough for real use.
- `L3 Reference Parity`: Covers reference project behavior and edge cases.
- `L4 Better Than Reference`: Improves safety, operations, UX, reliability, or extensibility beyond references.

## Phase 0: Governance Baseline

### Goal

Establish rules, clean-room policy, agent roles, and release gates.

### Deliverables

- Root agent files.
- `docs/` authority files.
- `docs_zh/` owner summaries.
- Agent skills.
- Claude agents.
- Gemini hooks.

### Exit Criteria

- Agent rules exist.
- Clean-room policy exists.
- Feature preservation rule exists.
- Owner Start Gate exists.

### Status

Current baseline is present.

## Phase 1: Reference Evidence And Feature Map

### Goal

Convert real-world reference experience into clean-room evidence and a feature matrix.

### Deliverables

- Filled `docs/07_REFERENCE_EVIDENCE_LEDGER.md`.
- Filled `docs/03_FEATURE_PARITY_MATRIX.md`.
- Updated `docs/10_RISK_REGISTER.md`.
- Updated `docs/11_ACCEPTANCE_TEST_MATRIX.md`.
- Initial feature level assignments in `docs/17_FEATURE_LEVEL_MATRIX.md`.

### Non-Goals

- No business implementation.
- No copied source code.
- No database schema design copied from references.
- No UI source copied from references.

### Exit Criteria

- Every known reference feature has an evidence row or a placeholder requiring research.
- Every known reference feature has a disposition.
- Every high-risk feature has a safe implementation path or roadmap entry.
- MVP candidate features are identified.

## Phase 2: MVP Scope Lock And Architecture Contracts

### Goal

Define the first buildable product slice.

### MVP Core Loop

1. A user sends an OpenAI-compatible request with a local API key.
2. The gateway authenticates the key.
3. The gateway selects a configured provider account.
4. The gateway forwards the request.
5. The provider returns a response.
6. The gateway returns the response to the user.
7. The platform records request status, model, provider account, and usage estimate.

### L1 MVP Modules

- API key intake (platform-issued keys for end-users).
- **Provider Account pooling**: one or more manually configured upstream Provider Accounts grouped into one logical pool that the platform routes against. This is the relay-station identity feature ([F-POOL-001](03_FEATURE_PARITY_MATRIX.md), [01_PROJECT_BRIEF.md §Product Identity](01_PROJECT_BRIEF.md)) and must be in L1, not deferred.
- OpenAI-compatible request path for a small model set.
- Basic provider forwarding.
- Basic routing selection (with pool-aware selection inside one Channel).
- Basic usage log with token-level granularity (sets up F-POOL-001 fairness).
- Basic request log.
- Basic admin inspection surface or documented admin API.
- Edition-flag plumbing ([F-MODE-001](03_FEATURE_PARITY_MATRIX.md)) so SaaS-only features can be turned off cleanly in Personal Edition deployments.

### Deferred But Preserved

These are not deleted. They are recorded for later phases:

- Full billing ledger.
- Recharge and balance operations.
- Advanced quota policy.
- Weighted routing.
- Cost optimization.
- Provider health automation.
- Full admin dashboard.
- Plugin marketplace.
- Multi-tenant organization model.
- Advanced audit and compliance workflows.

### Exit Criteria

- MVP acceptance tests are listed.
- API contracts are clear enough for implementation.
- UI assumptions are clear enough for Gemini planning.
- High-risk files and operations are identified.

## Phase 3: Project Skeleton And Test Harness

### Goal

Create the implementation skeleton only after Phase 2 locks the MVP.

### Deliverables

- Go module skeleton (per [DR-003](decisions/DR-003-technology-stack.md)) with one HTTP framework picked and locked.
- TypeScript frontend skeleton with types **generated** from the backend's OpenAPI artifact (codegen tool selected here or in a follow-up DR).
- Test framework: `go test` with `-race` enabled by default; vitest or equivalent for the frontend.
- Provider-neutral streaming abstraction stub (must exist before any provider integration begins, per DR-003 Constraint 3).
- Basic lint or type checks: `go vet`, `staticcheck`, `golangci-lint` for Go; `tsc --noEmit` for TS.
- Configuration examples without real secrets.
- Minimal CI or local check plan that runs lint + tests on every commit.
- **Naming-discipline guardrails** (per [DR-003](decisions/DR-003-technology-stack.md) Constraint 8): `golangci-lint.yml` and ESLint configs include lint rules enforcing glossary-aligned naming (e.g. forbidden synonyms list), no-duplicate-logic linters where available, and import-cycle / dead-code detection on by default. CI rejects PRs that introduce naming drift or redundant logic.
- DR-005 (Go HTTP framework) and DR-006 (database) decided BEFORE skeleton is committed.

### Risk Rule

Adding runtime dependencies is high risk under the project rules and requires Owner confirmation.

### Exit Criteria

- The repository can run a minimal check.
- No real credentials exist.
- No business feature claims are made without tests.

## Phase 4: Gateway Core MVP

### Goal

Build the smallest working OpenAI-compatible gateway path.

### Deliverables

- Request authentication.
- Request validation.
- Provider forwarding.
- Response return path.
- Error normalization at a basic level.
- Request ID propagation.

### Exit Criteria

- A local request can be accepted and forwarded through a configured provider abstraction.
- Success and failure are logged.
- No secrets are exposed.

## Phase 5: Provider Account Hub And Routing Lite

### Goal

Make provider accounts manageable and routable.

### Deliverables

- Provider account record.
- Enable and disable state.
- Manual credential rotation path.
- Basic route selection.
- Disabled accounts are excluded from routing.
- [F-COMPAT-001](03_FEATURE_PARITY_MATRIX.md) — Warm-up interception 凭据 flag（Personal Edition opt-in plugin；DECISION-POINT-Q1 待 Owner 复核是否 reject 不做）。

### Exit Criteria

- Operator can add, disable, and inspect provider accounts.
- Disabled account routing is tested.
- Credential values are redacted after creation.

## Phase 6: Usage, Quota Lite, And Billing Preparation

### Goal

Record usage reliably and add simple quota controls without pretending to have full billing parity.

### Deliverables

- Usage records.
- Request status records.
- Basic token or request count estimate.
- Simple quota check.
- Billing ledger design notes for later phases.
- [F-AUTH-006](03_FEATURE_PARITY_MATRIX.md) — OAuth 引导子系统（commercial blocker，配合 F-AUTH-005 续期形成完整 OAuth 套利路径；L0-1 in 02_HUAKAI_FUSION_ARCHITECTURE.md）。
- [F-COMM-001](03_FEATURE_PARITY_MATRIX.md) — 邀请 / 推荐子系统 plugin 壳（与 F-PAY-001 并列；DECISION-POINT-Q2 待 Owner 复核是否升 Mandatory Roadmap）。

### Exit Criteria

- Usage can be inspected by user, key, model, and provider account.
- Quota failure path is tested.
- Billing parity gaps are recorded as roadmap items.

## Phase 4.5: Async Task Backbone (axis 5 扩展, 2026-05-09 Codex audit 修补)

### Goal

补齐 axis 5 异步任务实现（[02_HUAKAI_FUSION_ARCHITECTURE.md](02_HUAKAI_FUSION_ARCHITECTURE.md) §5 复杂度轴当前 0%）；同时补齐 F-OBS-001 失败流计费 4-state 语义。挂在 Phase 4 与 Phase 5 之间，作为先于 Account Hub 完整化、先于真实 upstream 接入的运行时基础。

### Deliverables

- [F-OBS-003](03_FEATURE_PARITY_MATRIX.md) — 4-state 失败流计费扩展（client_gone / upstream_timeout / output_token_zero / upstream_5xx），落在 F-OBS-001 Tx2 结算钩子内，不另开 spec。
- [F-OBS-004](03_FEATURE_PARITY_MATRIX.md) — 14 段异步处理器链 + 每批 drain 边界（按角色命名，避免上游 identifier 抄袭）。
- [F-OBS-005](03_FEATURE_PARITY_MATRIX.md) — DLQ + 15 min 超时降级 + 显式低优先级 lane + 主备队列非对称双写。

### Exit Criteria

- 四种失败终态在 Usage Record 上有显式 `terminal_class` 字段且各自路径有正常 / 退款 / 部分计费断言。
- 14 段链每段有幂等键 + 每批 drain 边界单测。
- DLQ 重投幂等闸 + 优先级 lane 不会让账单类事件被 starve；双写分歧有对账接口。

## Phase 7: Admin Lite

### Goal

Give operators enough UI or admin API surface to run the MVP.

### Deliverables

- Users or API keys inspection.
- Provider accounts inspection.
- Route inspection.
- Usage log inspection.
- Request log inspection.
- Basic status indicators.

### Exit Criteria

- An operator can investigate a failed request from key to provider account to log entry.
- Dangerous actions are confirmed and audited where available.
- Secrets are redacted.

## Phase 8: Production Hardening

### Goal

Move from MVP to production usable.

### Deliverables

- Better retry and timeout policy.
- Health checks.
- Failover.
- Stronger quota controls.
- Stronger audit log.
- Security review.
- Reliability review.
- Operational alerts or alert design.

### Exit Criteria

- Normal path, failure path, and operator recovery path are tested for each core module.
- High-risk release blockers are closed or explicitly blocked.

## Phase 9: Advanced Parity, Provider Catalog Breadth, and Better Than Reference

### Goal

Close remaining reference parity gaps; deliver the Provider Catalog Breadth that is HUAKAI's commercial differentiator per [DR-007](decisions/DR-007-product-positioning-and-breadth.md); improve beyond references.

### Provider Catalog Breadth Exit Criterion (per DR-007)

By the end of Phase 9, HUAKAI's supported Provider catalog must **materially exceed Sub2API's catalog** (target: 15+ unique upstream Providers with verified per-provider acceptance tests). This is a binding exit criterion for Phase 9, not a stretch goal.

### Deliverables

- Advanced account pool behavior.
- Weighted and policy routing.
- Full billing and recharge workflows.
- Cost analysis.
- Advanced admin dashboard.
- Plugin boundaries.
- Feature flags.
- Better observability and investigation workflows.
- [F-CRED-001](03_FEATURE_PARITY_MATRIX.md) — 凭据提供者 + 预轮换 + OIDC→cloud STS 子系统（Phase 9+ SaaS enterprise tier；与 F-AUTH-005 续期 / F-AUTH-006 引导职能边界明确分割）。
- [F-PROTO-003](03_FEATURE_PARITY_MATRIX.md) — 服务侧压缩 native passthrough 路径 `/v1/native/openai/responses/compact`（已被 P-4 passthrough 覆盖；DECISION-POINT-Q3 待 Owner 复核是否升级到 first-class HCSF capability，14→15）。

### Exit Criteria

- Parity matrix has no unmapped reference feature.
- Mandatory roadmap items are resolved or explicitly accepted as release blockers.
- Release gates pass.

## Agent Responsibilities By Phase

| Phase | Claude | Codex | Gemini |
| --- | --- | --- | --- |
| 0 | Maintain governance | Audit rules | Read UI constraints |
| 1 | Mine evidence and map features | Audit parity and clean-room risk | Identify UI workflow evidence |
| 2 | Lock MVP scope and contracts | Review testability and risk | Plan Admin Lite |
| 3 | Approve architecture | Review skeleton and checks | Prepare frontend assumptions |
| 4 | Resolve architecture conflicts | Write scenario tests and review small fixes | No primary role |
| 5 | Define account/routing rules | Review routing/account risks | Plan account UI |
| 6 | Define quota/billing boundaries | Review usage/quota/billing tests | Plan usage UI |
| 7 | Confirm operations workflows | Review operator recovery scenarios | Build or review Admin Lite |
| 8 | Manage release risks | Run release readiness review | Review operations dashboard |
| 9 | Close parity gaps | Audit final parity | Improve UI parity |

## Phase 10: SaaS Distribution Edition (Post-MVP, Owner-Triggered)

### Goal

Activate the SaaS Edition after Personal Edition (Phase 1-9) has validated user feedback. See [DR-002](decisions/DR-002-product-editions.md).

### Trigger

Owner explicit signal that Personal Edition feedback supports SaaS. Phase 10 is **not** a default exit gate from Phase 9; it is opt-in.

### Deliverables

- Tenant onboarding workflow (UI and API).
- Tenant suspension and termination workflows.
- Per-tenant billing dashboard and ledger isolation.
- Cross-tenant abuse investigation tools (operator-only).
- Compliance export per-tenant.
- Tenant-scoped feature flags and rate limits.
- Multi-tenant admin UI surfaces (tenant switcher, per-tenant settings panel).
- Marketing or onboarding surfaces if applicable.

### Edition Gating Rule

All Phase 10 features must be gated by configuration / feature flag and must default **off** in Personal Edition deployments. No Phase 10 code path may execute by default in a Personal Edition install.

### Exit Criteria

- A new SaaS deployment can onboard a tenant end-to-end.
- Two tenants on the same deployment cannot observe each other's data, even via crafted queries.
- Per-tenant billing reconciles correctly.
- Personal Edition deployments are unaffected (regression-tested).

## Anti-Big-Bang Rule

Agents must not treat full parity as a single implementation task. Full parity is the destination. Each phase must have a small, testable outcome.
