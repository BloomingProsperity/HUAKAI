# CLIProxyAPI Account-to-API deep dive

Date: 2026-05-02
Lane: specifier (Claude — first session reading this repo).
Reference repo: `.omc/reference-src/cliproxy-api`
Snapshot: `main`, commit `56df36895a0e`, fetched 2026-05-02
License: MIT (LICENSE confirmed line 1)
Tracked file count: 524

## Why this dive matters for HUAKAI

CLIProxyAPI is the **structurally closest existing project** to HUAKAI's Account-to-API spine (per `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md`). Where the other 8 references treat upstream credentials as one input to a generic gateway, CLIProxyAPI is built around the chain `local API key → bound credential → per-provider executor → upstream`. Reading it surfaces concrete operator-facing knobs HUAKAI has not yet specified.

License is MIT — full read permitted, no clean-room defensive constraints beyond standard "don't verbatim-copy code / paths / comments". Per Owner's same-day relaxation (`feedback_clean_room_algorithm_relaxation.md`), algorithmic shape can be captured in paraphrased form.

## Source areas read in this pass

Top-level structure + config surface only. Not yet a code-level executor read.

- `AGENTS.md` (architecture pointer document)
- `config.example.yaml` (operator-facing surface — 120 lines confirmed)
- `internal/runtime/executor/` directory listing (24 files)
- `internal/store/` directory listing (4 files: gitstore, objectstore, postgresstore + tests)
- Top-level dir layout: `assets/auths/cmd/docs/examples/internal/sdk/test`

Open: full source-code dive for executor / store / wsrelay / cache logic — Phase 2 follow-up specifier session.

## Source-confirmed architectural facts

### A. Per-provider executor as first-class concept

`internal/runtime/executor/` contains one file per upstream provider/integration:
- `aistudio_executor.go` (Google AI Studio)
- `antigravity_executor.go` (Antigravity / Claude credits fallback)
- `claude_executor.go` + `claude_signing.go`
- `codex_executor.go` + `codex_websockets_executor.go` (HTTP + WebSocket variants)
- `gemini_cli_executor.go` + `gemini_executor.go` + `gemini_vertex_executor.go`
- `kimi_executor.go`
- `helps/` subdirectory for shared helpers (per AGENTS.md convention: executors stay one-file-per-provider; helpers live separate)

**HUAKAI mapping**: this directly maps onto HUAKAI's spine `F-ACCAPI-CRED-INJECT-001` + `F-ACCAPI-ERR-CLASSIFY-001` mount points. Each CLIProxyAPI executor is an instance of (CredentialInjector + ErrorClassifier) glued together for one provider. CLIProxyAPI bundles them; HUAKAI's audit §6 splits them — splitting yields independently testable concerns, but the CLIProxyAPI bundle is operationally simpler per provider. Tradeoff is real and Owner should pick.

**Two-variant pattern** (HTTP vs WebSocket) for Codex is direct evidence that HUAKAI must plan for at least one provider needing dual-protocol executors.

### B. Multiple credential storage backends as plugin

`internal/store/` has 4 files: `gitstore.go` + `objectstore.go` + `postgresstore.go` + their tests. Plus the default file-based store implied by `auth-dir: ~/.cli-proxy-api` in config.

So CLIProxyAPI supports four credential storage backends:
1. **File-based** (default; `~/.cli-proxy-api/`)
2. **Postgres** (`postgresstore.go`; env `PGSTORE_*`)
3. **Git** (`gitstore.go`; env `GITSTORE_*`)
4. **Object store** (`objectstore.go`; env `OBJECTSTORE_*`)

**HUAKAI mapping**: HUAKAI's current `provider_accounts.credentials jsonb` column is the equivalent of "Postgres only" mode. The plugin-backend pattern is a real product feature for personal edition users who want git-tracked auth or S3-backed centralized auth.

**Recommended**: add backlog row `F-ACCAPI-CRED-STORE-001` plugin layer (L3/L4) but DO NOT implement before L1 ships with Postgres-only.

### C. Routing strategy is operator-configurable enum

```yaml
routing:
  strategy: "round-robin" # round-robin (default), fill-first
  session-affinity: false # default
```

Two strategies, named explicitly. HUAKAI's F-POOL-001 spec describes "5-layer algorithm" but the user-facing strategy choice is buried.

**HUAKAI gap**: Owner-facing config should expose the strategy choice as named enum, same way. HUAKAI default would be `weighted-fill-first` (per F-POOL-001 priority + concurrency-aware), but `round-robin` should be one of the named alternatives.

### D. Session-affinity ID extraction is concrete

```yaml
# Session IDs are extracted from: metadata.user_id (Claude Code session format),
# X-Session-ID, Session_id (Codex), X-Amp-Thread-Id (Amp CLI),
# X-Client-Request-Id (PI), conversation_id, or first few messages hash.
```

Six explicit extraction sources, one per upstream client. HUAKAI's `F-SESSION-001` is currently abstract ("session_hash from cache_control / metadata.user_id / SessionContext"). CLIProxyAPI gives the production matrix.

**HUAKAI lesson**: hardcode the same 6 extraction sources (or document the rationale to omit any) in F-SESSION-001 acceptance test direction. This is operator-credibility surface — if a Claude Code user's session breaks because HUAKAI didn't recognize `metadata.user_id`, that's a launch blocker.

### E. Quota-exceeded auto-fallback policy is structured

```yaml
quota-exceeded:
  switch-project: true
  switch-preview-model: true
  antigravity-credits: true
```

Three independent auto-fallback toggles. Maps to HUAKAI:
- `switch-project` → cross-account fallback in F-POOL-001 (already covered)
- `switch-preview-model` → model-rewrite fallback (NOT in HUAKAI plan; useful operator policy: "if Claude Sonnet quota exhausted, automatically retry with Claude Sonnet preview")
- `antigravity-credits: true` → last-resort paid-credits fallback (HUAKAI doesn't have this; sub2api has antigravity-specific code; HUAKAI's Pool concept could absorb it as `last_resort_paid_pool` but this is product-positioning territory)

**HUAKAI gap**: F-RATE-001 + F-POOL-001 spec needs an explicit "model-substitution under quota exhaustion" subfeature. Recommend `F-ROUTE-MODEL-FALLBACK-001` at L2.

### F. Retry knobs are operator-visible

```yaml
request-retry: 3
max-retry-credentials: 0     # 0 = try all; non-zero caps it
max-retry-interval: 30       # max wait seconds for cooled credential
disable-cooling: false       # global kill-switch
```

Four operator-tunable retry-policy fields. HUAKAI's F-GW-004 retry/fallback spec mentions retry budget but doesn't pin these specific knobs.

**HUAKAI gap**: when promoting F-UPSTREAM-RETRY-002 (Codex backlog), include these four fields by name in acceptance tests. The `disable-cooling` global kill-switch is non-obvious — useful for "incident debug mode" where operator wants to bypass cooldown logic temporarily.

### G. Bounded auth-refresh worker pool

```yaml
# auth-auto-refresh-workers: 16
```

Default 16 workers for OAuth/file-based auth token refresh. Storm-prevention via bounded pool, not via lock-coalescing.

**HUAKAI mapping**: F-AUTH-005 spec describes "three-scope storm controls (account / provider-endpoint / global)". CLIProxyAPI uses just bounded worker count. Both work; HUAKAI's three-scope is a stronger guarantee. Note that CLIProxyAPI's choice is simpler to implement and reason about.

### H. Commercial-mode flag

```yaml
commercial-mode: false   # disables high-overhead middleware features under high concurrency
```

Single flag toggling memory/concurrency optimizations. Maps to HUAKAI's `F-MODE-001` Edition flag, but with a NARROWER scope (CLIProxyAPI's `commercial-mode` only flips middleware overhead; HUAKAI's `F-MODE-001` toggles SaaS-only features).

**Lesson**: HUAKAI's edition flag should not conflate "high-concurrency mode" with "SaaS feature visibility". Worth a sub-flag.

### I. Management API + Management Panel separation

```yaml
remote-management:
  allow-remote: false
  secret-key: ""
  disable-control-panel: false
  panel-github-repository: "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
```

Personal-default localhost-only. Separate management secret. Bundled control panel auto-downloaded from a separate GitHub repo (configurable URL).

**HUAKAI mapping**:
- `allow-remote: false` localhost default — HUAKAI's current admin endpoints (`/admin/v1/api-keys`) bind 0.0.0.0 by default; should add an `allow-remote` config to restrict to localhost in personal edition.
- `secret-key` separate from API keys — HUAKAI's N+4b2 admin tokens table is the equivalent. CLIProxyAPI uses a single secret-key (simpler personal-edition shape).
- **Management Panel as separate repo** is a strong product decision. HUAKAI's frontend is not yet started; treating it as a separate repo from Day 1 is operationally cleaner than a monorepo. Recommend Owner consider this for HUAKAI's frontend roadmap.

### J. TUI mode

```bash
go run ./cmd/server --tui --standalone
```

Bubbletea-based terminal UI for personal-mode users. No web UI required. Lowers self-host barrier.

**HUAKAI gap**: Personal edition has no UI plan today. TUI is a valid Phase 2/3 alternative to web UI for personal mode.

### K. Per-entry proxy override

```yaml
# Per-entry proxy-url also supports "direct" or "none" to bypass both the global proxy-url and environment proxies explicitly.
```

Each credential row can override the global proxy URL with a literal `direct`/`none` to bypass.

**HUAKAI mapping**: HUAKAI's `provider_accounts.proxy_id` references `proxies` table — this is fine for actual proxy assignment but doesn't have the "explicit bypass" sentinels. Add `direct`/`none` semantics to proxy_id resolution (or use NULL = "use global", `0` = "explicit direct").

### L. Force-model-prefix flag

```yaml
force-model-prefix: false   # unprefixed model requests only use credentials without a prefix (except when prefix == model name)
```

A single flag affecting how model names are matched against credential capability sets. Implementation detail, but the operator-facing toggle is the interesting part — gives admin a way to enforce strict prefix discipline without re-tagging every credential.

### M. Passthrough-headers default off

```yaml
passthrough-headers: false  # forward filtered upstream response headers; default OFF
```

Consistent with HUAKAI's F-SEC-005 "secure defaults" framing.

### N. Disable-image-generation tristate

```yaml
disable-image-generation: false  # supports false (default), true, or "chat"
```

Tristate enum: `false` (allow) / `true` (block all) / `"chat"` (block injection in non-image endpoints, allow image endpoints).

**Lesson**: tristate string-or-bool is a common product pattern for "feature off / on / scoped-on". HUAKAI should adopt this for F-CACHE-001, F-GUARD-001, etc. when feature has both global and scoped modes.

## Inferred items (need source-code verification)

- `inferred` Round-robin and fill-first strategies are likely implemented in `internal/sdk/cliproxy/` (not under runtime/executor) since they're routing-level. Need to read the SDK to confirm.
- `inferred` Codex WebSocket executor handles connection lifetime AND per-message billing; would explain why a separate file from `codex_executor.go`. Verify next pass.
- `inferred` `internal/cache/` (request signature cache) implements something like HUAKAI's `F-IDEM-001` HTTP-level idempotency, but CLIProxyAPI's framing is "request signature" not "idempotency key" — slightly different scope. Worth checking.
- `inferred` `internal/wsrelay/` is the per-session WebSocket relay (e.g. for OpenAI Realtime). Maps to HUAKAI's F-RT-001 deferred row but is already implemented here — useful concrete reference if HUAKAI wants to advance F-RT-001 from L3 to L2.
- `inferred` The `--local-model` flag disabling remote registry updates implies the registry has a remote-fetch background goroutine. HUAKAI's `model_registry` has versioning but no remote-fetch — operator sets registry by hand. CLIProxyAPI's "auto-fetch from upstream" is a usability win.

## Open questions (Phase 2 follow-up)

- `open-question` What is the exact extraction order for session-affinity? (`metadata.user_id` first then header? Header first then body? Hash of messages last?)
- `open-question` How does CLIProxyAPI handle credential refresh during in-flight requests? Does it block, or use stale-while-revalidate?
- `open-question` Does the postgres store implementation map cleanly onto HUAKAI's existing `provider_accounts.credentials jsonb` column? Source-code read needed.
- `open-question` What does "fill-first" routing actually do? Is it "use the first credential until quota exhausts, then move to next"? That would be operator-visible UX.
- `open-question` Management Panel auto-update from GitHub — what's the security boundary? Verified signature? Or unsigned trust on the URL?

## HUAKAI delta (specifier-lane recommendations, Owner approval needed)

| Topic | HUAKAI plan today | CLIProxyAPI evidence | Recommendation |
| --- | --- | --- | --- |
| Per-provider executor location | Implicit; `internal/proto` only does shape translation | One file per provider in `internal/runtime/executor/` | Add `internal/adapter/credential/` + `internal/adapter/errclass/` per audit §6, but keep the per-provider folder layout convention (one file per provider) |
| Credential storage backend | Postgres-only (`provider_accounts.credentials jsonb`) | 4 backends: file / Postgres / git / object | Add backlog `F-ACCAPI-CRED-STORE-001` (L3/L4 plugin) — not L1 |
| Session-affinity extraction | Abstract (cache_control / metadata.user_id) | 6 explicit sources matrix | Pin the 6 sources in F-SESSION-001 acceptance test |
| Quota-fallback policy | F-RATE-001 covers cooldown; cross-account fallback in F-POOL-001 | 3 independent toggles (switch-project / switch-preview-model / antigravity-credits) | Add `F-ROUTE-MODEL-FALLBACK-001` (L2): operator policy for "if model X quota exhausted on this account, retry with model Y" |
| Retry policy fields | F-GW-004 mentions retry budget | 4 operator-tunable knobs (`request-retry`, `max-retry-credentials`, `max-retry-interval`, `disable-cooling`) | Promote into F-UPSTREAM-RETRY-002 acceptance test direction |
| Auth refresh storm prevention | F-AUTH-005 spec: 3-scope storm controls | Bounded worker pool (16 default) | F-AUTH-005 already stronger; document why HUAKAI is not adopting CLIProxyAPI's simpler model |
| Edition flag scope | F-MODE-001 = SaaS feature toggle | `commercial-mode` = high-concurrency middleware | Split: F-MODE-001 (SaaS visibility) ≠ F-MODE-002 (high-concurrency mode); add separate flag |
| Management endpoint binding | Default 0.0.0.0 | `allow-remote: false` localhost default | Add `allow-remote` config; default false in Personal Edition |
| Management Panel deployment | No frontend plan yet | Separate GitHub repo, auto-download | Recommend Owner adopt "frontend in separate repo" pattern from Day 1 |
| TUI mode | Not planned | `--tui`, `--standalone` flags | Add as Phase 2/3 alternative to web UI for personal users |
| Per-entry proxy bypass | `proxy_id` references row | `proxy-url: direct/none/socks5://` | Add bypass sentinels to proxy_id semantics |
| Image-generation tristate | Implicit on/off | `false / true / "chat"` tristate | Adopt tristate pattern for feature flags with global vs scoped modes |
| Model registry remote-fetch | Manual operator | `--local-model` to disable, default = auto-fetch | Recommend L2: registry can fetch from upstream registry endpoint |
| WebSocket relay | F-RT-001 deferred to L3 | Already implemented in `internal/wsrelay/` | Use as concrete reference if Owner advances F-RT-001 to L2 |

## Anti-patterns to call out

CLIProxyAPI is generally well-designed. Two operator-facing patterns to be cautious of:

- **Auto-download management panel from external GitHub URL by default**: CLIProxyAPI defaults `disable-control-panel: false` (panel enabled) AND `panel-github-repository` is configurable. If an operator misconfigures the URL or the repo gets compromised, that's an RCE-grade surface. HUAKAI's frontend, if adopted, should require explicit operator action to enable upstream auto-update; default off.
- **`commercial-mode` as a single boolean** conflates orthogonal concerns. HUAKAI should split.

## Recommended follow-up

Phase 2 specifier session (separate agent ID, per CLAUDE.md #11) on these CLIProxyAPI source files:
- `internal/runtime/executor/codex_websockets_executor.go` — WS lifecycle + billing
- `internal/runtime/executor/claude_executor.go` + `claude_signing.go` — signing scheme is provider-specific
- `internal/store/postgresstore.go` — direct comparison to HUAKAI's `provider_accounts.credentials jsonb`
- `sdk/cliproxy/` — routing strategy implementations
- `internal/wsrelay/session.go` — session lifetime / liveness deadlines
- `internal/cache/` — request signature cache vs HUAKAI's `F-IDEM-001`
- `internal/registry/` — remote model fetch loop

Estimated 4-6 hours follow-up specifier work.

## Single-line summary

CLIProxyAPI is HUAKAI's Account-to-API closest existing reference; reading the AGENTS.md + config.example.yaml alone surfaces 14 concrete operator knobs and 4 architectural patterns HUAKAI's current plan abstracts at higher level. Highest-value adoptions: (a) name routing strategy as enum in operator config; (b) pin 6 session-affinity extraction sources in F-SESSION-001 AT; (c) split `F-MODE-001` into edition + concurrency flags; (d) add `F-ROUTE-MODEL-FALLBACK-001` and `F-ACCAPI-CRED-STORE-001` to backlog; (e) plan `allow-remote` admin binding for Personal Edition default localhost.
