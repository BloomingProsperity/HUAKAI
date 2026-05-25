# 2026-05-13 Portkey Dir Skeleton Codex Lane

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none; previous codex retry produced no output doc.

REFERENCE PROJECTS IN SCOPE: portkey / Portkey-AI gateway

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors -
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

## 0. Metadata

- Agent: codex lane.
- Ref: portkey.
- Local path: `~/refs/portkey/`.
- Git remote observed locally: `https://github.com/Portkey-AI/gateway.git`.
- SHA: `351692fd9236`.
- Local last commit date: 2026-03-25.
- Local last commit message observed: merge of a forward-header fix branch.
- GitHub API recency check: archived=false, disabled=false, pushed_at=2026-03-25T09:33:55Z, default_branch=main.
- Current mining mode: T1 directory skeleton, not T2 feature slice and not T3 file-level precision.
- Observed regions: 47 representative files or file regions.
- Inferences: 31 HUAKAI upgrade inferences, each marked as upgrade reasoning rather than upstream fact.
- Open questions: 10.
- Constraint: no clone, no fetch, no other reference project reads, no HUAKAI implementation-code reads.
- Evidence posture: source and public docs under this ref were used as behavior evidence; no upstream code blocks are reproduced.

## 1. Step 1 Result: SHA + Directory Tree

- HEAD observed as `351692fd9236`; commit date observed as 2026-03-25.
- Top-level runtime files include package manifest, container files, Cloudflare worker config, gateway config examples, TypeScript config, and build config.
- Top-level directories observed: `.github/`, `.husky/`, `.vscode/`, `cookbook/`, `docs/`, `patches/`, `plugins/`, `src/`, `tests/`.
- `.git/` is present but intentionally excluded from product decomposition.
- The project exposes both a worker-style entry and a node server entry through package scripts and runtime files.
- Package metadata declares MIT license and exposes build output plus public assets for distribution (`Portkey-AI/gateway@351692fd9236:package.json:17`, `Portkey-AI/gateway@351692fd9236:package.json:18`).
- Package scripts show local dev, node dev, worker dev, deploy, build, plugin build, test, pre-push, and postinstall patch flow (`Portkey-AI/gateway@351692fd9236:package.json:25`).
- Runtime dependency choices show a lightweight HTTP stack, node serving, worker tooling, retry support, Redis support, JWT support, and schema validation support (`Portkey-AI/gateway@351692fd9236:package.json:43`).
- Docker packaging uses a build stage, installs dependencies, compiles, prunes development packages, and runs the node server output (`Portkey-AI/gateway@351692fd9236:Dockerfile:2`, `Portkey-AI/gateway@351692fd9236:Dockerfile:24`, `Portkey-AI/gateway@351692fd9236:Dockerfile:49`).
- Worker config points at the main TypeScript entry, enables node compatibility, and separates staging/production vars (`Portkey-AI/gateway@351692fd9236:wrangler.toml:1`, `Portkey-AI/gateway@351692fd9236:wrangler.toml:24`).
- Example config enables a curated plugin subset, optional cache, and integration metadata containing provider, credential, rate-limit, model, and pricing placeholders (`Portkey-AI/gateway@351692fd9236:conf.example.json:1`, `Portkey-AI/gateway@351692fd9236:conf.example.json:20`).

## 2. Root Files / Runtime Shape

### 2.1 用途

- 根目录定义这个 ref 的运行形态：一个 TypeScript gateway 可以在 node server、Cloudflare worker、Docker image、npm binary 中运行。
- 它把运行时入口、构建、插件生成、测试、发布和 deployment 示例都放在顶层，适合开源用户直接安装或自托管。
- 观察证据来自 package manifest、container file、worker config、example config 和 startup script。

### 2.2 关键文件

- `package.json`: 82 LoC；定义分发名、MIT license、scripts、runtime dependencies、binary entry (`Portkey-AI/gateway@351692fd9236:package.json:1`).
- `Dockerfile`: 50 LoC；定义 two-stage build and runtime launch (`Portkey-AI/gateway@351692fd9236:Dockerfile:2`).
- `wrangler.toml`: 30 LoC；定义 worker main file and environment vars (`Portkey-AI/gateway@351692fd9236:wrangler.toml:1`).
- `conf.example.json`: 48 LoC；展示 enabled plugins、credentials、cache flag、integration model metadata (`Portkey-AI/gateway@351692fd9236:conf.example.json:1`).
- `start-test.js`: 22 LoC；做 headless startup smoke (`Portkey-AI/gateway@351692fd9236:start-test.js:1`).
- `jest.config.js`: 8 LoC；设置 node test runtime and timeout (`Portkey-AI/gateway@351692fd9236:jest.config.js:1`).

### 2.3 入口 / 调用关系

- package scripts route developer flows into worker dev, node dev, deploy, build, plugin build, tests, and patch-package (`Portkey-AI/gateway@351692fd9236:package.json:25`).
- The worker deployment path points directly at the HTTP app entry (`Portkey-AI/gateway@351692fd9236:wrangler.toml:3`).
- The Docker runtime launches the compiled node server path through npm (`Portkey-AI/gateway@351692fd9236:Dockerfile:49`).
- The smoke script launches compiled headless server and kills it after startup window (`Portkey-AI/gateway@351692fd9236:start-test.js:5`, `Portkey-AI/gateway@351692fd9236:start-test.js:16`).

### 2.4 Core Logic / Algorithm

- Root-level logic is packaging orchestration, not request routing.
- The main algorithmic signal is "one gateway, multiple runtime envelopes": the same app can be wrapped for worker, node, Docker, and npm usage.
- Runtime config is pushed through headers and JSON config rather than a persistent server-side database in this ref's OSS path.
- Postinstall applies local dependency patches, which means retry behavior depends partly on patched package internals (`Portkey-AI/gateway@351692fd9236:package.json:41`).

### 2.5 暴露功能

- User can install and run the gateway with package tooling.
- Operator can deploy as Docker, Cloudflare Worker, or node server.
- Contributor can run formatting, build, plugin build, and gateway/plugin tests.
- Config author can enable plugin subsets and optional cache in a JSON file.

### 2.6 HUAKAI 升级点

- 架构升级：HUAKAI should separate runtime envelope config from tenant/product config, so node/worker/container launch does not own business rules.
- 架构升级：replace root JSON credential examples with a typed secret-provider contract and explicit local-dev-only examples.
- 生态升级：make packaging smoke tests report readiness, version, provider-registry checksum, and plugin-registry checksum.
- 安全升级：dependency patching should become a tracked compatibility adapter or vendored shim, with CI proving the exact patched behavior.
- 运维升级：runtime mode should be surfaced in admin ops and audit logs, not only inferred from process launch.

## 3. `.github/`

### 3.1 用途

- This directory defines contribution policy, issue intake, security contact, and release/test automation.
- It is not gateway request logic, but it materially controls release quality and external operator expectations.
- Workflows show Docker publication, npm publication, link checking, formatting, tests, and triage automation.

### 3.2 关键文件

- `.github/workflows/run_tests.yml`: 75 LoC；comment-triggered gateway test workflow (`Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:1`).
- `.github/workflows/docker_publish.yml`: 44 LoC；release-triggered multi-arch image publication (`Portkey-AI/gateway@351692fd9236:.github/workflows/docker_publish.yml:1`).
- `.github/workflows/npm_publish.yml`: 20 LoC；release-triggered npm publication (`Portkey-AI/gateway@351692fd9236:.github/workflows/npm_publish.yml:1`).
- `.github/workflows/link-checker.yml`: 51 LoC；docs link hygiene workflow, observed in directory listing.
- `.github/ISSUE_TEMPLATE/bug_report.yml`: 29 LoC；minimal bug intake form (`Portkey-AI/gateway@351692fd9236:.github/ISSUE_TEMPLATE/bug_report.yml:1`).
- `.github/SECURITY.md`: 11 LoC；single supported major series and email reporting path (`Portkey-AI/gateway@351692fd9236:.github/SECURITY.md:1`).

### 3.3 入口 / 调用关系

- Test workflow starts only when a PR issue comment contains a trigger phrase, not on every push (`Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:3`, `Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:9`).
- It installs dependencies, builds, launches local gateway, waits on root endpoint, runs gateway tests, then writes a GitHub check (`Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:18`, `Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:24`, `Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:36`).
- Docker and npm publishing both trigger on released tags (`Portkey-AI/gateway@351692fd9236:.github/workflows/docker_publish.yml:3`, `Portkey-AI/gateway@351692fd9236:.github/workflows/npm_publish.yml:2`).
- Docker publication targets amd64 and arm64 (`Portkey-AI/gateway@351692fd9236:.github/workflows/docker_publish.yml:36`).

### 3.4 Core Logic / Algorithm

- CI design is operator-triggered rather than always-on for heavyweight gateway tests.
- Release algorithm is "build from current release source and push package artifacts"; no observed staged canary or provenance attestation in the read region.
- Security intake is human contact based, not a structured disclosure workflow with SLA tiers.
- Test result publishing creates a check tied to PR head SHA, which makes comment-triggered tests visible in PR status.

### 3.5 暴露功能

- Maintainers can trigger gateway tests from PR comments.
- Release maintainers can publish Docker image and npm package from GitHub release events.
- Contributors get issue templates and PR templates.
- Security reporters get an email contact and supported major version statement.

### 3.6 HUAKAI 升级点

- 生态升级：HUAKAI should require default CI on PR for gateway critical paths, with optional expensive provider tests separately gated.
- 安全升级：add security intake states, severity labels, embargo policy, and audit trail.
- 运维升级：release workflows should produce provenance artifacts, image digest, npm tarball checksum, and SBOM.
- 架构升级：test workflow should run against typed compatibility fixtures and local mocked provider surfaces before any live-key tests.
- 生态升级：link checker and docs checks should be tied to docs changed paths and publish a machine-readable broken-link report.

## 4. `.husky/`

### 4.1 用途

- This directory provides local git hook guardrails for contributors.
- It is a very thin wrapper over package scripts and does not hold production gateway logic.

### 4.2 关键文件

- `.husky/pre-commit`: 1 LoC；runs format check and formats on failure (`Portkey-AI/gateway@351692fd9236:.husky/pre-commit:1`).
- `.husky/pre-push`: 0 or 1 visible command line depending line count tool; delegates to package pre-push (`Portkey-AI/gateway@351692fd9236:.husky/pre-push:1`).
- `package.json`: 82 LoC；owns the actual pre-push build and startup smoke script (`Portkey-AI/gateway@351692fd9236:package.json:39`).

### 4.3 入口 / 调用关系

- Git invokes the local hook; hook calls the package script; package script builds and starts a smoke process.
- The hook layer depends on developer environment setup and is bypassable by contributors, so it is not a release gate.
- The pre-commit path emphasizes formatting, not behavioral tests.

### 4.4 Core Logic / Algorithm

- Local hook algorithm is "fail early on formatting, then rebuild before push".
- It does not inspect provider coverage, cache behavior, auth behavior, or plugin registry consistency.
- Because the pre-commit hook can auto-format after a failed format check, the user gets a visible failure and changed files to review.

### 4.5 暴露功能

- Contributors get fast local feedback on formatting.
- Contributors get a build/startup check before push when hooks are installed.
- Maintainers get fewer style-only PR diffs.

### 4.6 HUAKAI 升级点

- 生态升级：HUAKAI should keep local hooks small but mirror all mandatory checks in CI.
- 安全升级：pre-push should never be the only place that proves startup health.
- 架构升级：add a local fixture smoke that verifies gateway route registry, plugin registry, and config parser at startup.

## 5. `.vscode/`

### 5.1 用途

- This directory contains editor debug and task recipes for contributors.
- It supports worker debugging and node debugging, plus build and cleanup tasks.

### 5.2 关键文件

- `.vscode/launch.json`: 42 LoC；debug profiles for worker attach and node launch (`Portkey-AI/gateway@351692fd9236:.vscode/launch.json:1`).
- `.vscode/tasks.json`: 36 LoC；TypeScript build output and cleanup tasks (`Portkey-AI/gateway@351692fd9236:.vscode/tasks.json:1`).
- `src/start-server.ts`: 198 LoC；node server path used by debug flows (`Portkey-AI/gateway@351692fd9236:src/start-server.ts:1`).

### 5.3 入口 / 调用关系

- Worker debug attaches to a known inspection port after a dev command has started (`Portkey-AI/gateway@351692fd9236:.vscode/launch.json:15`).
- Node debug launches compiled server output after running a TypeScript build task (`Portkey-AI/gateway@351692fd9236:.vscode/launch.json:25`, `Portkey-AI/gateway@351692fd9236:.vscode/tasks.json:7`).
- Cleanup deletes build output after debugging (`Portkey-AI/gateway@351692fd9236:.vscode/tasks.json:23`).

### 5.4 Core Logic / Algorithm

- The debug workflow assumes build output exists for node debugging.
- The worker workflow assumes a separate dev process and debugger attach.
- There is no observed container debug profile or multi-tenant fixture profile in this directory.

### 5.5 暴露功能

- Contributor can debug worker runtime and node runtime in an editor.
- Contributor can run TypeScript compilation into a local build directory.
- Contributor can clean generated build output from the editor.

### 5.6 HUAKAI 升级点

- 生态升级：add debug profiles for local fake-provider mesh, tenant-scoped request replay, and plugin failure replay.
- 运维升级：debug tasks should load `.env.example` only, never real credentials.
- 架构升级：generate route maps during debug startup to catch missing handler registration early.

## 6. `cookbook/`

### 6.1 用途

- This directory is user-facing learning material for gateway usage.
- It documents product-level workflows: config, retry, cache, fallback, load distribution, integrations, monitoring agents, providers, and use cases.
- It contains notebooks and markdown examples rather than production runtime code.

### 6.2 关键文件

- `cookbook/README.md`: 64 LoC；navigation across getting-started, providers, integrations, monitoring agents, use cases (`Portkey-AI/gateway@351692fd9236:cookbook/README.md:22`).
- `cookbook/getting-started/writing-your-first-gateway-config.md`: 336 LoC；explains config, dashboard-managed config, request headers, retry config (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/writing-your-first-gateway-config.md:38`).
- `cookbook/getting-started/automatic-retries-on-failures.md`: 132 LoC；explains retries on status/timeouts and default status set at user level (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/automatic-retries-on-failures.md:37`).
- `cookbook/getting-started/enable-cache.md`: 219 LoC；explains simple and semantic cache concepts (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/enable-cache.md:7`).
- `cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md`: 265 LoC；teaches weighted distribution and nested backup behavior (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md:31`).
- Provider and integration notebooks are broad but not deeply read in this T1 pass.

### 6.3 入口 / 调用关系

- User starts from README table of contents and picks a workflow category (`Portkey-AI/gateway@351692fd9236:cookbook/README.md:10`).
- Config tutorial maps dashboard config or request header config into gateway behavior (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/writing-your-first-gateway-config.md:55`, `Portkey-AI/gateway@351692fd9236:cookbook/getting-started/writing-your-first-gateway-config.md:110`).
- Retry tutorial maps user-level gateway config into request retries (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/automatic-retries-on-failures.md:41`).
- Cache tutorial maps config to cache modes and user-visible latency/cost savings (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/enable-cache.md:47`).
- Resilience tutorial maps a nested routing diagram into multiple provider targets and trace-based log reading (`Portkey-AI/gateway@351692fd9236:cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md:42`, `Portkey-AI/gateway@351692fd9236:cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md:118`).

### 6.4 Core Logic / Algorithm

- The cookbook expresses gateway behavior as declarative request-time instructions rather than application-side loops.
- User-facing algorithm for resilience is: define target group, attach weights or ordered backup, then let the gateway handle provider selection.
- User-facing cache model distinguishes exact request reuse from semantic similarity reuse, but production source read shows OSS in-memory middleware only clearly supports exact body+URL hash in the observed region (`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:14`, `Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:70`).
- User-facing retry model aligns with source retry handling that reacts to selected statuses and provider wait hints (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:103`, `Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:108`).

### 6.5 暴露功能

- Developer can learn how to apply retries, cache, weighted provider distribution, fallback, and config references.
- Operator can trace a routed request through logs if a trace value is provided at call time.
- Teams can use notebooks to integrate with provider SDKs and agent frameworks.
- Product narrative highlights dashboard-managed secrets and config versions, though those managed SaaS surfaces are not fully present in the OSS runtime read here.

### 6.6 HUAKAI 升级点

- 生态升级：HUAKAI cookbook should pair every user recipe with an acceptance test ID and an ops dashboard screenshot contract.
- 架构升级：separate OSS-runtime evidence from managed-product claims; mark which features require HUAKAI control plane.
- 算法升级：cache docs should distinguish exact, semantic, tenant-scoped, and policy-scoped cache with hit/miss observability.
- 运维升级：trace examples should map to request ID, attempt ID, provider-account ID, tenant ID, and policy verdict.
- 安全升级：recipes that mention vault-managed credentials should show redaction, rotation, audit, and emergency revoke behavior.

## 7. `docs/`

### 7.1 用途

- This directory contains deployment documentation and image assets.
- It is operational documentation for running the gateway on managed service, local node, Docker, Cloudflare Workers, App Stack, Replit, and other platforms.

### 7.2 关键文件

- `docs/installation-deployments.md`: 486 LoC；main deployment matrix and platform-specific instructions (`Portkey-AI/gateway@351692fd9236:docs/installation-deployments.md:1`).
- `docs/deploy-on-replit.md`: 44 LoC；Replit quick deployment page and example request (`Portkey-AI/gateway@351692fd9236:docs/deploy-on-replit.md:1`).
- `docs/images/*`: visual assets; not behavior logic, used by README/cookbook/deployment docs.
- `Dockerfile`: 50 LoC；deployment doc points to runtime container behavior indirectly.
- `deployment.yaml`: observed in root listing; not deeply read in this pass to stay T1.

### 7.3 入口 / 调用关系

- The deployment guide organizes deployment options into managed, local, enterprise, and platform-specific paths (`Portkey-AI/gateway@351692fd9236:docs/installation-deployments.md:3`).
- Local deployment path points to package install, node server, worker, Docker, Compose, Replit, Supabase Functions, and Fastly options (`Portkey-AI/gateway@351692fd9236:docs/installation-deployments.md:15`).
- Replit doc frames the gateway as a ready-to-run hosted endpoint and shows a direct chat request through the gateway (`Portkey-AI/gateway@351692fd9236:docs/deploy-on-replit.md:20`, `Portkey-AI/gateway@351692fd9236:docs/deploy-on-replit.md:28`).
- The App Stack region includes infrastructure credential variables and load-balancer/header injection setup, which is operationally sensitive (`Portkey-AI/gateway@351692fd9236:docs/installation-deployments.md:75`, `Portkey-AI/gateway@351692fd9236:docs/installation-deployments.md:132`).

### 7.4 Core Logic / Algorithm

- Deployment docs expose a "gateway as HTTP shim" pattern: deploy endpoint, inject provider header/credential, send OpenAI-shaped request.
- Multiple deployment modes imply the runtime is stateless enough to run in edge/serverless/container contexts.
- Some docs include long shell and infrastructure examples; they are useful for ops but should not become HUAKAI implementation source.
- Public docs emphasize ease and provider unification; source entry confirms route breadth across chat, completions, embeddings, images, audio, files, batches, responses, messages, and realtime (`Portkey-AI/gateway@351692fd9236:src/index.ts:132`, `Portkey-AI/gateway@351692fd9236:src/index.ts:147`, `Portkey-AI/gateway@351692fd9236:src/index.ts:210`).

### 7.5 暴露功能

- Operator can choose deployment target and follow direct launch steps.
- Operator can set provider and credential headers at an upstream load balancer in at least one deployment recipe.
- Developer can use a hosted or self-hosted gateway URL as an OpenAI-compatible endpoint.
- Docs show the product positioning as 100+ LLM routing through one API.

### 7.6 HUAKAI 升级点

- 运维升级：HUAKAI deployment docs should never include copy-paste production secret patterns without explicit vault/secret-manager alternatives.
- 安全升级：header-injection deployment should be marked high-risk and paired with least-privilege, rotation, and audit controls.
- 架构升级：deployment recipes should include state backend requirements for cache, rate limits, quota, billing, and account pools.
- 生态升级：each deployment guide should include health check, readiness check, rollback, observability, and incident drill.
- Clean-room note: docs are evidence for user-visible workflows, not a template for HUAKAI file structure or infrastructure naming.

## 8. `patches/`

### 8.1 用途

- This directory holds patch-package deltas applied to third-party retry dependency and its type declaration.
- It is a small directory, but it has outsized behavior impact because retry timing uses patched internals.

### 8.2 关键文件

- `patches/async-retry+1.3.3.patch`: 13 visible lines; adds access to retry operation object in callback context (`Portkey-AI/gateway@351692fd9236:patches/async-retry+1.3.3.patch:1`).
- `patches/@types+async-retry+1.4.5.patch`: 13 visible lines; aligns TypeScript type surface with the runtime patch (`Portkey-AI/gateway@351692fd9236:patches/@types+async-retry+1.4.5.patch:1`).
- `package.json`: postinstall applies patches after install (`Portkey-AI/gateway@351692fd9236:package.json:41`).
- `src/handlers/retryHandler.ts`: runtime retry path uses the extra callback object to manipulate queued waits in provider wait-hint handling (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:87`, `Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:128`).

### 8.3 入口 / 调用关系

- Package install triggers patch-package.
- The runtime retry handler depends on patched callback arity to access queue control.
- When a provider wait hint is accepted, queued retry waits are adjusted and remaining retry budget is reduced (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:128`, `Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:138`).

### 8.4 Core Logic / Algorithm

- The behavior is not simply exponential retry; it incorporates provider-supplied cool-down hints where available.
- A maximum cumulative wait budget prevents unbounded waiting (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:84`, `Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:130`).
- If the wait hint exceeds budget, the retry path marks skip and surfaces the last error response (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:134`, `Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:214`).
- Patch dependency creates supply-chain and maintainability risk if upstream package changes.

### 8.5 暴露功能

- User sees better behavior under rate limits when provider returns wait hints.
- Operator benefits from bounded retry time instead of indefinite backoff.
- Contributor must understand that dependency behavior differs from vanilla package behavior.

### 8.6 HUAKAI 升级点

- 算法升级：implement retry budget and provider wait-hint handling in HUAKAI-owned code, not patched dependency internals.
- 安全升级：add tests proving max wait, skip semantics, and no retry beyond tenant quota reservation.
- 生态升级：emit retry decision events with status, wait hint, budget left, attempt ordinal, provider-account ID, and tenant ID.
- 架构升级：isolate retry policy from HTTP transport so the same contract applies to streaming, non-streaming, batch, and realtime surfaces.

## 9. `plugins/`

### 9.1 用途

- This directory implements an extension surface for guardrails and mutators around request/response lifecycle.
- It contains built-in checks, vendor guardrail integrations, registry wiring, plugin utility helpers, manifests, tests, and a build script.
- It is a major product capability: users can enforce input/output policy without changing core routing code.

### 9.2 关键文件

- `plugins/README.md`: 213 LoC；explains plugin concepts, hooks, guardrails, checks, manifest, testing (`Portkey-AI/gateway@351692fd9236:plugins/README.md:21`).
- `plugins/index.ts`: 179 LoC；static registry of plugin groups and callable checks (`Portkey-AI/gateway@351692fd9236:plugins/index.ts:70`).
- `plugins/types.ts`: 33 LoC；plugin handler contracts, not deeply read.
- `plugins/utils.ts`: 375 LoC；extracts and mutates request/response text payloads for plugin checks (`Portkey-AI/gateway@351692fd9236:plugins/utils.ts:45`, `Portkey-AI/gateway@351692fd9236:plugins/utils.ts:127`).
- `plugins/build.ts`: 43 LoC；generates registry from enabled plugin manifests (`Portkey-AI/gateway@351692fd9236:plugins/build.ts:1`).
- `plugins/default/jsonSchema.ts`: 132 LoC；built-in JSON-shape validation check (`Portkey-AI/gateway@351692fd9236:plugins/default/jsonSchema.ts:10`).
- `plugins/bedrock/index.ts`: 213 LoC；vendor guardrail integration with credential handling and content transform (`Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:80`).

### 9.3 入口 / 调用关系

- Core hook middleware imports the plugin registry and runs configured hooks around the request lifecycle (`Portkey-AI/gateway@351692fd9236:src/middlewares/hooks/index.ts:14`).
- The hook context extracts request text from prompts, chat/messages content, or embeddings input (`Portkey-AI/gateway@351692fd9236:src/middlewares/hooks/index.ts:93`).
- The plugin README says hooks can run at lifecycle stages and guardrails produce verdicts that shape request/response handling (`Portkey-AI/gateway@351692fd9236:plugins/README.md:40`, `Portkey-AI/gateway@351692fd9236:plugins/README.md:51`).
- Registry groups include built-in validators, first-party checks, multiple vendor policy systems, search/online checks, cloud safety checks, and prompt/response protection groups (`Portkey-AI/gateway@351692fd9236:plugins/index.ts:70`, `Portkey-AI/gateway@351692fd9236:plugins/index.ts:140`).
- Build script reads enabled plugin names from root config and emits a registry file (`Portkey-AI/gateway@351692fd9236:plugins/build.ts:4`, `Portkey-AI/gateway@351692fd9236:plugins/build.ts:32`).

### 9.4 Core Logic / Algorithm

- Plugin lifecycle is policy-as-hook: build a context, extract relevant text, run one or more checks, collect verdict/error/data/transformation, then continue/fail/alter output depending on hook semantics.
- Utilities normalize content extraction across request types and can write transformed content back into request/response bodies (`Portkey-AI/gateway@351692fd9236:plugins/utils.ts:60`, `Portkey-AI/gateway@351692fd9236:plugins/utils.ts:141`).
- JSON validator check tries to find JSON in text, validates against supplied schema, supports inverse expectation, and returns explanation plus validation errors (`Portkey-AI/gateway@351692fd9236:plugins/default/jsonSchema.ts:19`, `Portkey-AI/gateway@351692fd9236:plugins/default/jsonSchema.ts:52`, `Portkey-AI/gateway@351692fd9236:plugins/default/jsonSchema.ts:88`).
- A cloud guardrail plugin can validate credentials, optionally assume roles, send content for policy evaluation, and optionally redact content (`Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:16`, `Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:24`, `Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:96`).

### 9.5 暴露功能

- User can attach input guardrails and output guardrails to gateway config.
- Operator can enable or disable plugin groups at build/config time.
- Security operator can route content through built-in regex/schema/JWT/metadata checks or vendor policy systems.
- Developer can contribute new plugins with manifest plus handler and tests.
- Managed-product posture is suggested by docs, but OSS runtime observed here focuses on local registry and configured hooks.

### 9.6 HUAKAI 升级点

- 架构升级：HUAKAI should version plugin ABI and separate guardrail verdict schema from plugin implementation detail.
- 安全升级：plugin credentials need tenant-scoped secret references, not raw credentials in request config or root JSON.
- 算法升级：policy verdict should be typed as allow/block/transform/warn/manual-review with deterministic downstream behavior.
- 运维升级：each plugin invocation should produce audit-safe traces with latency, external call outcome, redaction action, and failure mode.
- 生态升级：plugin marketplace should support lifecycle states, license metadata, sandbox mode, egress policy, and per-tenant enablement.
- Clean-room guard: HUAKAI can implement equivalent plugin capability but must not copy this registry shape or handler code.

## 10. `src/`

### 10.1 用途

- This is the production runtime of the OSS gateway.
- It owns HTTP routes, request validation, config parsing, routing strategies, provider adapters, retries, streaming, caching, hooks, logs, types, static public UI, and test helpers.
- It is the only top-level directory in this ref that directly handles gateway requests.

### 10.2 关键文件

- `src/index.ts`: 298 LoC；HTTP app assembly, middleware registration, and endpoint routing (`Portkey-AI/gateway@351692fd9236:src/index.ts:45`).
- `src/start-server.ts`: 198 LoC；node runtime, static public UI routes, log stream, realtime websocket support (`Portkey-AI/gateway@351692fd9236:src/start-server.ts:20`).
- `src/handlers/handlerUtils.ts`: 1353 LoC；request-hop orchestration, config inheritance, strategy handling, headers, provider attempts.
- `src/handlers/retryHandler.ts`: 220 LoC；retry, timeout, provider wait-hint budget, terminal error response (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:65`).
- `src/handlers/streamHandler.ts`: 494 LoC；provider stream parsing and response stream adaptation (`Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:300`).
- `src/services/conditionalRouter.ts`: 156 LoC；conditional target resolution over metadata/params/url context (`Portkey-AI/gateway@351692fd9236:src/services/conditionalRouter.ts:44`).
- `src/services/transformToProviderRequest.ts`: 292 LoC；provider-specific body mapping with defaults and min/max constraints (`Portkey-AI/gateway@351692fd9236:src/services/transformToProviderRequest.ts:75`).
- `src/providers/index.ts`: 153 LoC；provider registry spanning many vendors and surfaces (`Portkey-AI/gateway@351692fd9236:src/providers/index.ts:78`).
- `src/providers/types.ts`: 456 LoC；provider adapter contract and endpoint surface list (`Portkey-AI/gateway@351692fd9236:src/providers/types.ts:85`).
- `src/middlewares/requestValidator/index.ts`: 478 LoC；request header/content-type/provider/custom-host validation (`Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:85`).
- `src/shared/services/cache/index.ts`: 180+ observed LoC；pluggable cache backend service (`Portkey-AI/gateway@351692fd9236:src/shared/services/cache/index.ts:42`).

### 10.3 入口 / 调用关系

- HTTP app imports middleware, handlers, logger, config, and optional Redis-backed cache setup (`Portkey-AI/gateway@351692fd9236:src/index.ts:14`, `Portkey-AI/gateway@351692fd9236:src/index.ts:39`, `Portkey-AI/gateway@351692fd9236:src/index.ts:42`).
- It adds compression conditionally based on runtime, then exposes root health text and pretty JSON formatting (`Portkey-AI/gateway@351692fd9236:src/index.ts:57`, `Portkey-AI/gateway@351692fd9236:src/index.ts:92`).
- It registers models, hook middleware, optional cache middleware, error handler, and the major provider-compatible endpoints (`Portkey-AI/gateway@351692fd9236:src/index.ts:102`, `Portkey-AI/gateway@351692fd9236:src/index.ts:105`, `Portkey-AI/gateway@351692fd9236:src/index.ts:108`).
- Route surface includes messages, count tokens, chat, completions, embeddings, images, audio, files, batches, responses, realtime, and proxy classes (`Portkey-AI/gateway@351692fd9236:src/index.ts:132`, `Portkey-AI/gateway@351692fd9236:src/index.ts:147`, `Portkey-AI/gateway@351692fd9236:src/index.ts:195`, `Portkey-AI/gateway@351692fd9236:src/index.ts:210`, `Portkey-AI/gateway@351692fd9236:src/index.ts:233`).
- A specific chat route reads JSON, extracts headers into config, then delegates to the shared request-hop engine (`Portkey-AI/gateway@351692fd9236:src/handlers/chatCompletionsHandler.ts:16`, `Portkey-AI/gateway@351692fd9236:src/handlers/chatCompletionsHandler.ts:20`).
- The proxy route additionally handles JSON, multipart, and audio-like bodies before delegating to the shared request-hop engine (`Portkey-AI/gateway@351692fd9236:src/handlers/proxyHandler.ts:9`, `Portkey-AI/gateway@351692fd9236:src/handlers/proxyHandler.ts:26`).

### 10.4 Core Logic / Algorithm

- Request validation rejects unsupported content type, missing provider/config header, invalid provider, and unsafe custom host (`Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:88`, `Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:111`, `Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:130`, `Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:148`).
- Custom-host validation includes explicit local/metadata/private/reserved-host protections and trusted local override through environment (`Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:25`, `Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:28`, `Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:53`, `Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:69`).
- Shared request-hop logic merges inherited target config for retry/cache/guardrails/headers/custom host/timeout before choosing strategy (`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:520`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:630`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:646`).
- Strategy modes observed: ordered backup attempts, weighted random target selection, conditional query resolution, single target, and direct provider post (`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:662`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:693`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:725`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:767`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:781`).
- Weighted selection normalizes missing weights to 1 and samples by cumulative weight (`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:693`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:699`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:704`).
- Conditional selection evaluates query predicates against metadata, params, and URL context, then picks named target or default (`Portkey-AI/gateway@351692fd9236:src/services/conditionalRouter.ts:44`, `Portkey-AI/gateway@351692fd9236:src/services/conditionalRouter.ts:64`, `Portkey-AI/gateway@351692fd9236:src/services/conditionalRouter.ts:137`).
- Provider adapter surface covers text, embeddings, rerank, image, audio, realtime, files, batch, fine-tune, responses, messages, and token count surfaces (`Portkey-AI/gateway@351692fd9236:src/providers/types.ts:85`).
- Provider registry spans a large set of hosted and cloud providers, with each adapter supplying configs, transforms, request handlers, and response transforms where needed (`Portkey-AI/gateway@351692fd9236:src/providers/index.ts:1`, `Portkey-AI/gateway@351692fd9236:src/providers/index.ts:78`).
- Request body transformation applies provider config defaults, min/max constraints, required defaults, and nested property mapping (`Portkey-AI/gateway@351692fd9236:src/services/transformToProviderRequest.ts:28`, `Portkey-AI/gateway@351692fd9236:src/services/transformToProviderRequest.ts:75`, `Portkey-AI/gateway@351692fd9236:src/services/transformToProviderRequest.ts:143`).
- Streaming handler has a special binary-event path for one cloud provider family and a text split path for the rest, then may convert JSON-stream providers into event-stream response (`Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:61`, `Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:300`, `Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:392`).
- In-memory cache middleware hashes request body plus URL, supports force refresh, misses on expiry, and skips stream caching (`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:14`, `Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:38`, `Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:70`).
- Shared cache service has memory, file, Redis, and edge KV backends; get/set/delete/keys/clear/stats/cleanup are abstracted behind a backend interface (`Portkey-AI/gateway@351692fd9236:src/shared/services/cache/index.ts:51`, `Portkey-AI/gateway@351692fd9236:src/shared/services/cache/index.ts:93`, `Portkey-AI/gateway@351692fd9236:src/shared/services/cache/index.ts:170`).
- Memory cache backend maintains hit/miss/set/delete/expiry stats and evicts oldest entries when size limit is reached (`Portkey-AI/gateway@351692fd9236:src/shared/services/cache/backends/memory.ts:19`, `Portkey-AI/gateway@351692fd9236:src/shared/services/cache/backends/memory.ts:51`).
- Redis backend scopes keys by a database name and optional namespace, stores serialized entries, and uses Redis TTL when present (`Portkey-AI/gateway@351692fd9236:src/shared/services/cache/backends/redis.ts:53`, `Portkey-AI/gateway@351692fd9236:src/shared/services/cache/backends/redis.ts:91`).
- Logs service builds structured request/response objects and OpenTelemetry-like tool spans for tool-call events (`Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts:9`, `Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts:94`).

### 10.5 暴露功能

- User can call many OpenAI-compatible endpoint families through one gateway endpoint.
- User can configure provider selection, retry, cache, guardrail hooks, custom headers, custom host, and timeout via request config/header path.
- Operator can run node or worker runtime and optionally enable cache.
- Developer can add providers through adapter registry and endpoint-specific transformations.
- Operator can inspect a simple public log UI in node non-production mode; static UI route is served from public asset path (`Portkey-AI/gateway@351692fd9236:src/start-server.ts:20`, `Portkey-AI/gateway@351692fd9236:src/start-server.ts:43`).
- Realtime and websocket path exists for node runtime with special upgrade handling (`Portkey-AI/gateway@351692fd9236:src/index.ts:65`, `Portkey-AI/gateway@351692fd9236:src/start-server.ts:8`).

### 10.6 HUAKAI 升级点

- 架构升级：split route registry, policy engine, account pool, provider adapter, transport, cache, and observability into typed modules with explicit contracts.
- 算法升级：weighted random routing should be upgraded with tenant/account quota, health, latency, cost, recent errors, and cooldown scoring.
- 算法升级：fallback should become attempt-graph execution with no transparent fallback after partial streaming emission unless explicitly allowed.
- 安全升级：custom host should require tenant policy, SSRF proof, DNS rebinding defense, and audit log.
- 运维升级：cache should be tenant-scoped and policy-scoped, with HIT/MISS/REFRESH metrics and invalidation audit.
- 运维升级：streaming should emit terminal state, partial usage estimate, disconnect reason, and provider chunk parser errors.
- 架构升级：provider adapter registry should carry capability matrix, protocol contract version, and test fixture coverage.
- 安全升级：request config from headers should be bounded, validated with typed schema, and optionally disallowed for low-trust clients.
- 生态升级：static public logs should become authenticated ops UI with tenant isolation and redaction.

## 11. `tests/`

### 11.1 用途

- This directory contains integration-level tests for live gateway behavior and request builder utilities.
- A second test root under `src/tests/` contains provider-driven common tests and route-specific test helpers.
- Tests rely on credentials for some provider paths and skip or disable broader provider-specific groups in observed areas.

### 11.2 关键文件

- `tests/integration/src/handlers/tryPost.test.ts`: 574 LoC；integration checks for chat, streaming, files, audio, images, proxy, and provider-specific paths (`Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:7`).
- `tests/integration/src/handlers/requestBuilder.ts`: 204 LoC；helper for constructing gateway URLs and request options.
- `src/tests/common.test.ts`: 18 LoC；loops provider fixtures and executes chat endpoint tests when key exists (`Portkey-AI/gateway@351692fd9236:src/tests/common.test.ts:5`).
- `src/tests/resources/requestTemplates.ts`: 54 LoC；shared request bodies.
- `src/tests/resources/testVariables.ts`: observed file path for provider credentials/test values, not deeply read here.
- `start-test.js`: 22 LoC；startup smoke used in package pre-push (`Portkey-AI/gateway@351692fd9236:start-test.js:5`).
- `jest.config.js`: 8 LoC；node test environment with 30s timeout (`Portkey-AI/gateway@351692fd9236:jest.config.js:1`).

### 11.3 入口 / 调用关系

- Package scripts expose gateway tests and plugin tests separately (`Portkey-AI/gateway@351692fd9236:package.json:37`).
- GitHub comment-triggered workflow builds gateway, starts it, waits for root endpoint, then runs gateway tests (`Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:21`, `Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:24`).
- Common tests iterate provider fixtures and execute endpoint-specific tests only if a key exists and adapter declares chat support (`Portkey-AI/gateway@351692fd9236:src/tests/common.test.ts:5`, `Portkey-AI/gateway@351692fd9236:src/tests/common.test.ts:14`).
- Integration tests call a running gateway URL and assert status/body shape for core workflows (`Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:14`, `Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:25`, `Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:39`).
- Provider-specific block is skipped in observed file region, which is a real coverage limitation (`Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:116`).

### 11.4 Core Logic / Algorithm

- Tests focus on end-to-end request success and response shape for representative surfaces.
- Streaming test verifies a stream body exists, not full chunk semantics or terminal event correctness (`Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:25`).
- File/audio/image/proxy tests validate the gateway can move non-trivial payload types through provider paths (`Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:39`, `Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:60`, `Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:83`, `Portkey-AI/gateway@351692fd9236:tests/integration/src/handlers/tryPost.test.ts:100`).
- Live-key gating reduces deterministic CI coverage because absent keys skip provider cases (`Portkey-AI/gateway@351692fd9236:src/tests/common.test.ts:9`).

### 11.5 暴露功能

- Maintainer can run gateway tests and plugin tests through npm scripts.
- Maintainer can smoke startup through pre-push script.
- Integration tests document user-visible workflows: chat, stream, binary upload, audio, image, proxy.
- Tests give some confidence in route wiring but not full policy/routing/cache/failure-state coverage.

### 11.6 HUAKAI 升级点

- 生态升级：HUAKAI should maintain acceptance tests for every public endpoint and every provider capability class using deterministic fake providers.
- 算法升级：add tests for weighted selection distribution, fallback stop conditions, conditional selection, retry budget, wait-hint budget, and cache isolation.
- 安全升级：SSRF/custom-host tests should include DNS, internal IP, encoded IP, scheme, and trusted-host override cases.
- 运维升级：streaming tests should assert terminal event, usage finalization, chunk parser warnings, abort handling, and no fallback after partial emission.
- 架构升级：live-provider tests should be optional conformance tests, not the only proof for production behavior.

## 12. Cross-Directory Workflow Trace

### 12.1 HTTP Request Hop

- Step 1: runtime envelope starts either worker app or node server; package scripts and worker config point to the same app entry (`Portkey-AI/gateway@351692fd9236:package.json:27`, `Portkey-AI/gateway@351692fd9236:wrangler.toml:3`).
- Step 2: app entry registers middleware and route handlers, including validation, hooks, optional cache, and endpoint handlers (`Portkey-AI/gateway@351692fd9236:src/index.ts:94`, `Portkey-AI/gateway@351692fd9236:src/index.ts:105`, `Portkey-AI/gateway@351692fd9236:src/index.ts:108`).
- Step 3: route handler reads request body/headers and converts header config into request-hop config (`Portkey-AI/gateway@351692fd9236:src/handlers/chatCompletionsHandler.ts:18`, `Portkey-AI/gateway@351692fd9236:src/handlers/proxyHandler.ts:31`).
- Step 4: shared request-hop engine merges inherited config and chooses the target execution strategy (`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:520`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:662`).
- Step 5: strategy branches to ordered backup, weighted sampling, conditional rule, single target, or direct provider post (`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:663`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:693`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:725`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:767`).
- Step 6: provider adapter transforms body/header/url according to adapter config and endpoint surface (`Portkey-AI/gateway@351692fd9236:src/services/transformToProviderRequest.ts:75`, `Portkey-AI/gateway@351692fd9236:src/providers/types.ts:47`).
- Step 7: retry handler wraps outbound request with timeout and retry decision (`Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:65`).
- Step 8: response handler may map provider stream/JSON into gateway response shapes; streaming path has provider-specific stream conversion (`Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:300`).
- Step 9: hooks/log/cache services record or mutate context around request/response (`Portkey-AI/gateway@351692fd9236:src/middlewares/hooks/index.ts:18`, `Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts:165`, `Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:83`).

### 12.2 Guardrail / Plugin Hop

- Step 1: config can declare default or target-specific input/output guardrails that are normalized into hook lists (`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:520`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:560`, `Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:571`).
- Step 2: hook middleware creates a lifecycle context with request body, response body, provider, request type, metadata, and headers (`Portkey-AI/gateway@351692fd9236:src/middlewares/hooks/index.ts:64`).
- Step 3: plugin utility extracts current textual content according to request or response direction (`Portkey-AI/gateway@351692fd9236:plugins/utils.ts:45`, `Portkey-AI/gateway@351692fd9236:plugins/utils.ts:78`, `Portkey-AI/gateway@351692fd9236:plugins/utils.ts:98`).
- Step 4: selected plugin handler returns verdict/data/error and may transform content (`Portkey-AI/gateway@351692fd9236:plugins/default/jsonSchema.ts:10`, `Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:80`).
- Step 5: streaming response can emit hook-result chunks when strict compatibility is not required (`Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:29`, `Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:327`).

### 12.3 Cache Hop

- Step 1: config enables cache middleware at app level (`Portkey-AI/gateway@351692fd9236:src/index.ts:108`).
- Step 2: cache middleware attaches cache lookup helpers to context (`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:83`).
- Step 3: exact cache key uses serialized request body plus URL (`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:14`).
- Step 4: non-stream response may be stored with expiration after downstream handler completes (`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:89`, `Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:99`).
- Step 5: shared cache service can back cache with memory/file/Redis/edge KV, but this observed middleware path is simpler than the full service abstraction (`Portkey-AI/gateway@351692fd9236:src/shared/services/cache/index.ts:51`).

### 12.4 Deployment / Test Hop

- Step 1: local user follows package or Docker/worker entry.
- Step 2: CI build compiles the gateway, launches a local server, waits for root endpoint, then runs tests (`Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:21`, `Portkey-AI/gateway@351692fd9236:.github/workflows/run_tests.yml:24`).
- Step 3: pre-push script builds and starts headless smoke via package scripts (`Portkey-AI/gateway@351692fd9236:package.json:39`, `Portkey-AI/gateway@351692fd9236:start-test.js:5`).
- Step 4: release event publishes npm and Docker artifacts (`Portkey-AI/gateway@351692fd9236:.github/workflows/npm_publish.yml:3`, `Portkey-AI/gateway@351692fd9236:.github/workflows/docker_publish.yml:3`).

## 13. Open Questions

- OQ-1: The managed dashboard and vault behavior appears in docs, but this T1 pass did not verify its implementation in this repo.
- OQ-2: The OSS cache docs mention semantic cache, but the observed simple middleware region only proves exact body+URL cache; deeper source read would be needed for semantic cache in this repo.
- OQ-3: Provider registry is large; this T1 pass sampled adapter contracts and examples, not every provider adapter.
- OQ-4: Streaming terminal semantics need T3 read because current T1 saw parser shape but not all response finalization branches.
- OQ-5: Realtime websocket behavior needs T2/T3 read across runtime-specific handlers.
- OQ-6: Batch API parity needs deeper read across OpenAI, Azure, Bedrock, Vertex, and file surfaces.
- OQ-7: Logs service emits structured span-like objects, but persistence/export path was not fully traced in T1.
- OQ-8: Plugin vendor integrations need separate clean-room license/security review before any HUAKAI plugin marketplace implementation.
- OQ-9: Test coverage for strategy failure modes is not clear from T1; provider-specific integration suite has skipped sections.
- OQ-10: No HUAKAI code was read by directive, so punch list uses "HUAKAI current state not verified in this lane" instead of asserting local gaps.

## 14. HUAKAI Overall Upgrade Punch List

| ref 项 | HUAKAI 现状 | HUAKAI 升级建议 | 升级维度 | 优先级 |
|---|---|---|---|---|
| Root runtime packaging | 本轮未读 HUAKAI code；待 PM 对照 | Split runtime envelope from tenant/business config; emit startup registry checksums | 架构/生态 | P0 |
| Package postinstall patch path | 本轮未读 HUAKAI code；待 PM 对照 | Use HUAKAI-owned retry scheduler instead of dependency monkey patch | 算法/安全 | P0 |
| `.github` comment-triggered gateway tests | 本轮未读 HUAKAI code；待 PM 对照 | Mandatory PR CI for fake-provider gateway core plus optional live-provider conformance | 生态 | P0 |
| `.github` release publish | 本轮未读 HUAKAI code；待 PM 对照 | Add SBOM, checksum, image digest, provenance and release gate report | 安全/生态 | P1 |
| `.husky` local hook | 本轮未读 HUAKAI code；待 PM 对照 | Keep local hooks as convenience; mirror gates in CI | 生态 | P2 |
| `.vscode` debug profiles | 本轮未读 HUAKAI code；待 PM 对照 | Add tenant replay, fake provider mesh, plugin failure replay profiles | 生态 | P2 |
| `cookbook` config examples | 本轮未读 HUAKAI code；待 PM 对照 | Pair each recipe with acceptance test and ops visibility screenshot contract | 生态 | P1 |
| `cookbook` retry/cache/fallback docs | 本轮未读 HUAKAI code；待 PM 对照 | Add trace ID, attempt ID, tenant ID, account ID, policy verdict into recipes | 运维 | P0 |
| `docs` deployment guide | 本轮未读 HUAKAI code；待 PM 对照 | Add secret-manager-first deployment variants and rollback/health drill | 安全/运维 | P0 |
| `docs` header injection examples | 本轮未读 HUAKAI code；待 PM 对照 | Mark as high-risk; require least-privilege, rotation, audit and redaction | 安全 | P0 |
| `plugins` lifecycle | 本轮未读 HUAKAI code；待 PM 对照 | Design typed plugin ABI with allow/block/transform/warn/manual-review verdicts | 架构/安全 | P0 |
| `plugins` registry | 本轮未读 HUAKAI code；待 PM 对照 | Add plugin license metadata, sandbox, egress policy, per-tenant enablement | 生态/安全 | P1 |
| `plugins` vendor guardrails | 本轮未读 HUAKAI code；待 PM 对照 | Route credentials through tenant-scoped secret references and audit every egress | 安全/运维 | P0 |
| `src` route surface | 本轮未读 HUAKAI code；待 PM 对照 | Maintain route capability matrix covering chat/messages/responses/files/batch/audio/image/realtime | 架构 | P0 |
| `src` strategy routing | 本轮未读 HUAKAI code；待 PM 对照 | Upgrade weighted routing into quota/health/cost/latency/cooldown scorer | 算法 | P0 |
| `src` fallback behavior | 本轮未读 HUAKAI code；待 PM 对照 | Model attempts as graph with terminal partial-stream rules and audit events | 算法/运维 | P0 |
| `src` conditional router | 本轮未读 HUAKAI code；待 PM 对照 | Replace ad hoc condition DSL with typed, validated, explainable policy rules | 架构/安全 | P1 |
| `src` request validator | 本轮未读 HUAKAI code；待 PM 对照 | Add SSRF DNS rebinding tests, tenant allowlist, and signed custom-host policy | 安全 | P0 |
| `src` cache middleware | 本轮未读 HUAKAI code；待 PM 对照 | Tenant/policy scoped cache with exact/semantic modes, invalidation audit, metrics | 架构/运维 | P0 |
| `src` stream handler | 本轮未读 HUAKAI code；待 PM 对照 | Terminal state, usage finalization, abort reason, no fallback after partial emission | 算法/运维 | P0 |
| `src` provider registry | 本轮未读 HUAKAI code；待 PM 对照 | Provider capability registry with adapter contract version and fixture coverage | 架构/生态 | P0 |
| `src` public logs UI | 本轮未读 HUAKAI code；待 PM 对照 | Replace static public logs with authenticated tenant-safe ops console | 安全/生态 | P0 |
| `tests` live provider gating | 本轮未读 HUAKAI code；待 PM 对照 | Fake-provider deterministic acceptance tests first; live tests as conformance layer | 生态 | P0 |
| `tests` streaming assertions | 本轮未读 HUAKAI code；待 PM 对照 | Assert terminal events, chunk ordering, usage reconciliation, abort semantics | 算法/运维 | P0 |
| `tests` provider-specific skips | 本轮未读 HUAKAI code；待 PM 对照 | No silent skip for released capability; skipped tests need validity reason and issue ID | 生态 | P1 |

## 15. Truth-First Notes

- 真实观察：route registration, request validation, routing strategy branches, provider registry shape, request transformation, retry budget, stream conversion, cache backends, plugin registry, CI, deployment docs, and tests were directly read from this local ref.
- 合理推断：HUAKAI upgrade items are design deltas based on observed Portkey behavior plus HUAKAI's Owner-stated need for stronger gateway/account/admin operations.
- 未观察：managed SaaS dashboard internals, server-side vault internals, every provider adapter, every plugin vendor, every streaming terminal branch, and HUAKAI current implementation.
- 功能缩水：没有故意丢弃 reference capabilities；all observed gateway, route, strategy, cache, plugin, docs, deployment, test capabilities are mapped to either upgrade item or open question.
- Clean-room risk: medium if future implementers copy file layout, adapter names, registry shape, or patched retry mechanism; this document avoids code and provides behavior-only evidence.
- Security risk: high for any future implementation touching credentials, custom host, plugin egress, cache isolation, routing quota, batch file handling, realtime, or public logs.
- Owner confirmation needed before HUAKAI implementation changes in auth core, billing/quota ledger, DB schema, secret storage, payment logic, dependency additions, or deployment scripts.

## 16. Owner 中文总结

做了什么：按 T1 brief 对 `~/refs/portkey/` 做目录骨架拆解，覆盖 root runtime、`.github/`、`.husky/`、`.vscode/`、`cookbook/`、`docs/`、`patches/`、`plugins/`、`src/`、`tests/`，并补了跨目录 request-hop / plugin-hop / cache-hop / deployment-test-hop trace。改了哪些文件：新增本报告 `docs/research/2026-05-13-portkey-dir-skeleton-codex.md`，另按防死要求写了 `/tmp/codex-deep-mining-portkey-codex-retry.txt`。为什么这样做：Owner 要按 ref 项目目录逐层拆清用途、关键文件、入口关系、logic、暴露功能和 HUAKAI 升级点；本轮只做 Portkey codex lane，不读其他 ref 和 HUAKAI code。有没有功能缩水：没有，观察到的路由、provider adapter、策略路由、retry、cache、stream、plugin guardrail、deployment、CI/test 都映射进 punch list 或 open questions。有没有 clean-room 风险：本报告只列 file:line 证据和行为级总结，不复制代码、注释或实现结构；后续实现若照搬 registry/adapter/patch 形状会有风险。有没有安全风险：本报告不改生产代码；未来落地高风险点是 credentials/custom-host/plugin egress/cache isolation/routing quota/batch/realtime/public logs。哪些地方需要 Owner 确认：涉及 auth、billing/quota、DB schema、secret storage、dependency、deployment scripts 的实现都需 Owner 确认。下一步建议：Claude PM synthesis 时把本 codex lane 与 sonnet lane 对照，先抽 P0 acceptance tests，再决定 T2 深挖 streaming、routing strategy、plugin verdict、cache isolation 和 batch API。

---
Agent: codex
Ref: portkey
SHA: 351692fd9236
Pushed: 2026-03-25T09:33:55Z via GitHub API; local commit date 2026-03-25
Mining started: 2026-05-13T09:01:14Z
Mining done: 2026-05-13T09:18:57Z
Output LoC: 645
Source files read (per CLAUDE.md #11 closing): package.json; Dockerfile; wrangler.toml; conf.example.json; start-test.js; jest.config.js; .github/workflows/run_tests.yml; .github/workflows/docker_publish.yml; .github/workflows/npm_publish.yml; .github/ISSUE_TEMPLATE/bug_report.yml; .github/SECURITY.md; .husky/pre-commit; .husky/pre-push; .vscode/launch.json; .vscode/tasks.json; cookbook/README.md; cookbook/getting-started/writing-your-first-gateway-config.md; cookbook/getting-started/automatic-retries-on-failures.md; cookbook/getting-started/enable-cache.md; cookbook/getting-started/resilient-loadbalancing-with-failure-mitigating-fallbacks.md; docs/installation-deployments.md; docs/deploy-on-replit.md; patches/async-retry+1.3.3.patch; patches/@types+async-retry+1.4.5.patch; plugins/README.md; plugins/index.ts; plugins/types.ts; plugins/utils.ts; plugins/build.ts; plugins/default/jsonSchema.ts; plugins/bedrock/index.ts; src/index.ts; src/start-server.ts; src/handlers/proxyHandler.ts; src/handlers/chatCompletionsHandler.ts; src/handlers/retryHandler.ts; src/handlers/handlerUtils.ts; src/handlers/streamHandler.ts; src/services/conditionalRouter.ts; src/services/transformToProviderRequest.ts; src/providers/index.ts; src/providers/types.ts; src/providers/openai/index.ts; src/providers/anthropic/messages.ts; src/providers/bedrock/createBatch.ts; src/middlewares/requestValidator/index.ts; src/middlewares/hooks/index.ts; src/middlewares/cache/index.ts; src/shared/services/cache/index.ts; src/shared/services/cache/backends/memory.ts; src/shared/services/cache/backends/redis.ts; src/handlers/services/logsService.ts; src/data/providers.json; src/errors/GatewayError.ts; src/errors/RouterError.ts; src/apm/index.ts; src/public/index.html; src/tests/common.test.ts; tests/integration/src/handlers/tryPost.test.ts
Lane: specifier
Agent: GPT-5 Codex / codex lane
UTC timestamp: 2026-05-13T09:18:57Z
