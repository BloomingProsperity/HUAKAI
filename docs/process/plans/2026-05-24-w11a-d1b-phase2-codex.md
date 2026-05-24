# 2026-05-24 W11-A D-1b Phase 2 Codex Plan

> **For agentic workers:** REQUIRED SUB-SKILL: implementation must use `superpowers:subagent-driven-development` or `superpowers:executing-plans` after Claude/Codex parallel drafts are reconciled. This file is an independent Codex draft only. Do not execute from it until the synthesized Phase 2 plan exists.

**Goal:** Make the Go control plane consume `RouteQueryRequest.client_credential`, derive tenant/user/API-key identity authoritatively, reject mismatches with Rust's legacy `tenant_id`, and define the safe boundary for retiring Rust Manual First.

**Architecture:** Keep identity authority in Go. Rust remains a data plane: it extracts and forwards the opaque client credential, but Go parses the canonical value, authenticates through the existing table-backed API key resolver, and uses the derived identity for registry/router decisions. Phase 2 should first land a Go control-plane identity gate and RouteQuery contract tests; production billable routing requires a separate Owner decision because the current `RouteQueryRequest` does not yet carry enough billing/idempotency inputs for a safe `ClaimGate.Reserve`.

**Tech Stack:** Go 1.25, existing `internal/auth`, `internal/registry`, `internal/router`, `internal/pool`, `internal/provider`, PostgreSQL/sqlc auth queries, Rust `route.v1` protobuf contract, and likely new Go gRPC/protobuf runtime dependencies if an actual RouteService server is enabled.

---

| Owner directive | "请独立起草 W11-A D-1b Phase 2 计划 draft, 写到 docs/process/plans/2026-05-24-w11a-d1b-phase2-codex.md (新文件). 你必须独立思考, 不要去读 claude.md 同名 draft" |
|---|---|
| Independence statement | I did not read `docs/process/plans/2026-05-24-w11a-d1b-phase2-claude.md`. Searches explicitly excluded that path. |
| Scope | Phase 2 Go-side plan only; no code execution in this dispatch. |
| Success criteria | Go derives identity from `client_credential`; `x-tenant-id`/legacy tenant mismatch cannot steer routing; raw credentials never enter logs/status; Manual First can be disabled only after Go identity gate passes. |
| Time estimate | Plan review: 0.25 day. Safe implementation slice: 1.5-2.5 days. Production billable RouteQuery slice: additional 1-2 days after Owner decides claim/billing contract. |
| Blast radius | Auth boundary, route-control service wiring, generated proto package, route planning DI, Rust/Go integration tests, and possibly runtime dependencies. |
| Clean-room posture | HUAKAI internal code/docs only. No reference-project source was read for this draft. |

## Context Read

I based this draft on HUAKAI-internal files only:

- `docs/process/plans/2026-05-24-w11a-d1b-phase1-synthesis.md`
- `docs/process/plans/2026-05-24-w11a-d1b-phase1-codex.md`
- `docs/process/plans/2026-05-22-rust-hardening-plan.md`
- `exploratory/rust-core-gateway/merged/proto/route.proto`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_auth/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_auth/credential.rs`
- `backend/internal/auth/api_key_resolver.go`
- `backend/sql/queries/auth_inbound.sql`
- `backend/internal/router/router.go`
- `backend/internal/router/route_plan.go`
- `backend/internal/router/default_router.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/pool/router/types.go`
- `backend/internal/provider/vault.go`
- `backend/internal/provider/postgres_vault.go`
- `backend/internal/billing/billing.go`
- `backend/internal/config/config.go`
- `backend/cmd/gateway/main.go`
- `backend/cmd/gateway/wiring.go`
- `backend/cmd/gateway/routes.go`

Current observed facts:

- Rust Phase 1 already sends `RouteQueryRequest.client_credential = 10` as `"bearer:<token>"` or `"x-api-key:<key>"`.
- Rust Phase 1 leaves `tenant_id` empty when Manual First is OFF, or writes a static Manual First tenant when enabled.
- Go already has `auth.APIKeyResolver.Resolve(ctx, *http.Request)` backed by `api_keys`, `users`, and `tenants`.
- Go `internal/router.DefaultRouter` requires `RequestContext.TenantID != 0`.
- Go has no observed generated `route.v1` protobuf package or RouteService gRPC server.
- `backend/internal/gatewayhttp`, `backend/internal/gateway`, and `backend/internal/proto` are frozen/oversized packages; Phase 2 must not add files there.
- `backend/go.mod` currently does not include `google.golang.org/grpc` or `google.golang.org/protobuf`.

## Scope In

1. Parse Go-side canonical `client_credential` values from RouteQuery:
   - `bearer:<secret>`
   - `x-api-key:<secret>`

2. Authenticate the secret using the existing `auth.APIKeyResolver` semantics:
   - Reuse the table-backed resolver.
   - Normalize both canonical kinds into `Authorization: Bearer <secret>` for resolver reuse.
   - Do not add an auth-core schema or resolver rewrite in the first safe slice.

3. Make Go the authoritative tenant source:
   - If Rust sends empty legacy `tenant_id`, proceed with the Go-derived tenant.
   - If Rust sends a matching legacy `tenant_id`, proceed and emit a non-secret reconciliation signal.
   - If Rust sends a non-empty mismatching legacy `tenant_id`, reject before registry/router.

4. Add a new cohesive Go package for RouteQuery control-plane behavior:
   - Recommended package: `backend/internal/routecontrol`.
   - Do not add files to frozen `gatewayhttp`, `gateway`, or `proto`.

5. Add a new generated protobuf package outside frozen `internal/proto` if gRPC is enabled:
   - Recommended package: `backend/internal/routepb`.

6. Add config and wiring behind an explicit default-off server flag:
   - `HUAKAI_ROUTE_CONTROL_ENABLED=false` by default.
   - UDS socket path required when enabled in production.
   - HTTP loopback may be allowed only for dev/test.

7. Add mutation-resistant tests for identity consumption and mismatch gates.

## Scope Out

- Do not delete Rust Manual First in this Phase 2 plan. Phase 3 removes it after Go identity consumption is proven.
- Do not add files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Do not change `LICENSE`.
- Do not change DB schema in the safe identity slice.
- Do not make production billable RouteQuery claims unless Owner explicitly approves the missing claim/billing contract decision below.
- Do not read or copy any non-MIT reference source.

## Critical Finding: Phase 2 Has A Claim Contract Gap

`RouteQueryRequest` currently has request id, tenant id, model, protocol, stream, deadline, previous attempts, capability hints, and client credential. It does not carry the normalized payload hash, idempotency key, billing policy version, request class, or predicted cost inputs that Go `billing.ClaimGate.Reserve` expects.

That means Phase 2 has two safe layers:

1. **Identity-authoritative RouteQuery gate**: safe to implement now. It authenticates `client_credential`, rejects mismatch, and can prove registry/router receive the derived tenant.

2. **Production billable RoutePlan**: not safe to declare complete until Owner chooses how RouteQuery obtains claim/billing inputs. Returning a plan that selected an account without a real claim risks bypassing billing, quota, and acquisition accounting.

Recommended decision: land Phase 2A identity gate first, then write a short synthesized Phase 2B plan for claim-safe production RouteQuery. This preserves the feature without pretending the money path is solved.

## File Structure

Create:

- `backend/internal/routecontrol/credential.go`
  - Parse canonical `client_credential`.
  - Normalize `bearer:` and `x-api-key:` to a resolver request.
  - Expose redacted fingerprint helpers for logs/errors.

- `backend/internal/routecontrol/service.go`
  - RouteQuery orchestration: parse credential, authenticate, reconcile tenant, resolve model, call router.
  - Return typed gRPC errors without raw credentials.

- `backend/internal/routecontrol/plan_mapper.go`
  - Map Go router/selector/provider outputs into `route.v1.RoutePlan`.
  - In Phase 2A this file should only map a fake or test plan if billable production mapping is not approved.

- `backend/internal/routecontrol/errors.go`
  - Stable internal error codes: `missing_client_credential`, `invalid_client_credential`, `tenant_id_mismatch`, `auth_backend_error`, `route_contract_incomplete`.

- `backend/internal/routecontrol/credential_test.go`
  - Unit tests for canonical parsing and redaction.

- `backend/internal/routecontrol/service_test.go`
  - Unit tests with stub auth/registry/router proving derived tenant is authoritative.

- `backend/internal/routecontrol/integration_test.go`
  - Optional Postgres-backed integration test using existing `api_keys` fixtures after dependency/wiring is approved.

Create if gRPC server is approved:

- `backend/internal/routepb/route.pb.go`
- `backend/internal/routepb/route_grpc.pb.go`

Modify:

- `backend/go.mod`
  - Add `google.golang.org/grpc` and `google.golang.org/protobuf` only with Owner approval.

- `backend/internal/config/config.go`
  - Add route-control config fields only if server wiring is included.

- `backend/internal/config/route_control.go`
  - Keep route-control env parsing in a focused config file.

- `backend/internal/config/route_control_test.go`
  - Default-off, production UDS required, dev loopback allowed.

- `backend/cmd/gateway/wiring.go`
  - Pass auth/registry/router/selector/vault dependencies to `routecontrol`.

- `backend/cmd/gateway/control_plane_server.go`
  - Start and stop the route-control gRPC server.

- `backend/cmd/gateway/lifecycle.go`
  - Add server shutdown hook if needed.

Do not create:

- `backend/internal/gatewayhttp/*`
- `backend/internal/gateway/*`
- `backend/internal/proto/*`

Package budget check:

- `backend/internal/routecontrol` is a new cohesive package for one responsibility: RouteService control-plane behavior.
- `backend/internal/routepb` is generated contract code, separate from frozen `internal/proto`.
- `backend/internal/config` is not frozen and remains below the package budget after one focused config file.
- `backend/cmd/gateway` is not frozen; one server wiring file is acceptable but should stay small.

## RouteCredential Contract

Recommended Go internal shape:

```go
type ClientCredentialKind string

const (
	ClientCredentialBearer ClientCredentialKind = "bearer"
	ClientCredentialXAPIKey ClientCredentialKind = "x-api-key"
)

type ClientCredential struct {
	Kind        ClientCredentialKind
	secret      string
	Fingerprint string
}

func ParseClientCredential(raw string) (ClientCredential, error)
func (c ClientCredential) ResolverRequest(ctx context.Context) (*http.Request, error)
```

Parsing rules:

- Empty value: unauthenticated.
- Missing `:` separator: invalid.
- Unknown prefix: invalid.
- Empty secret after prefix: invalid.
- Prefix must be exactly `bearer` or `x-api-key`.
- Raw secret has no exported getter.
- `ResolverRequest` sets only `Authorization: Bearer <secret>`.
- `x-api-key:<secret>` is normalized to Bearer for HUAKAI API keys because `auth.APIKeyResolver` authenticates the HUAKAI key namespace, not vendor upstream keys.

Mutation check:

- If parser ignores the prefix and trusts legacy `tenant_id`, service tests must fail.
- If parser logs the raw secret in `err.Error()` or `fmt.Sprintf("%+v")`, redaction tests must fail.

## RouteQuery Identity Flow

Recommended `RouteQuery` order:

1. Validate request is non-nil.
2. Parse `client_credential`.
3. Authenticate through existing inbound auth resolver.
4. Compare legacy `tenant_id`:
   - empty: accept, use derived tenant.
   - numeric string equal to derived tenant: accept.
   - non-empty mismatch or non-numeric: reject.
5. Validate `requested_model` and `request_id` are non-empty.
6. Resolve model using `Registry.ResolveModel(ctx, requested_model, derivedTenantID)`.
7. Call `Router.Plan` with `router.RequestContext{TenantID, UserID, APIKeyID, RequestID}`.
8. If Phase 2B claim contract is not approved, stop at a typed `route_contract_incomplete` path in production mode and keep route-plan mapping test-only.
9. If Phase 2B is approved, reserve/claim/select/resolve credential in a transaction-safe sequence and map to `route.v1.RoutePlan`.

Recommended gRPC code mapping:

- Missing/invalid credential: `codes.Unauthenticated`.
- Auth backend/misconfigured: `codes.Unavailable`.
- Tenant mismatch: `codes.PermissionDenied`.
- Unknown model / no tenant model access: `codes.NotFound`.
- No capacity / queue wait: `codes.ResourceExhausted` with retry-safe redacted message.
- Claim contract incomplete in production: `codes.FailedPrecondition`.
- Internal mapping bug: `codes.Internal`.

## Acceptance Gates

### P2-A1 Go Derives Tenant From Client Credential

Fixture:

- Auth stub returns `auth.Identity{TenantID: 7, UserID: 70, APIKeyID: 700}` for `bearer:hk_test_route_phase2_good`.
- RouteQuery has `tenant_id = ""`.
- Registry/router stubs record received tenant.

Expected:

- Registry receives tenant `7`.
- Router receives tenant `7`.
- No code path uses empty tenant or Rust legacy tenant.

Mutation self-check:

- If service passes `req.TenantId` to registry/router instead of derived identity, test fails because stubs see `0` or empty-derived value.

### P2-A2 Legacy Tenant Mismatch Fails Closed

Fixture:

- Auth stub derives tenant `7`.
- RouteQuery carries `tenant_id = "8"`.

Expected:

- RouteQuery returns `PermissionDenied` / `tenant_id_mismatch`.
- Registry/router/selector are not called.
- Error message contains no raw credential.

Mutation self-check:

- If mismatch is downgraded to a warning or if Go trusts Rust tenant, test fails because downstream call counters are non-zero.

### P2-A3 Matching Manual First Dual-Write Is Allowed Temporarily

Fixture:

- Auth stub derives tenant `7`.
- RouteQuery carries `tenant_id = "7"`.

Expected:

- Request proceeds.
- A non-secret reconciliation counter/log can record `tenant_match=true`.
- This path is explicitly temporary until Phase 3.

Mutation self-check:

- If service rejects all non-empty `tenant_id`, Phase 1 Manual First compatibility test fails.

### P2-A4 Canonical `x-api-key:` Authenticates As HUAKAI Key

Fixture:

- Same fake HUAKAI plaintext key appears once as `bearer:<key>` and once as `x-api-key:<key>`.
- Auth resolver stub expects an `Authorization: Bearer <key>` request in both cases.

Expected:

- Both canonical kinds reach the resolver through the same bearer-normalized request.
- Unknown prefix like `cookie:<key>` fails before auth.

Mutation self-check:

- If `x-api-key` is ignored or treated as a tenant hint, test fails.

### P2-A5 Raw Credential Never Appears In Logs, Status, Or Debug

Fixture:

- Raw credential string: `hk_test_PHASE2_RAW_SECRET_NEVER_LOG_1234567890`.
- Force parse error, tenant mismatch, and auth backend error cases.

Expected:

- `err.Error()`, gRPC status message, zap observed logs, and `%+v` formatting do not contain raw secret.
- They may contain `kind=bearer` and a short SHA-256 fingerprint.

Mutation self-check:

- If error formatting includes `req.ClientCredential` or `secret`, test fails.

### P2-A6 Production Billable RouteQuery Cannot Bypass Claim

Fixture:

- Production config enabled.
- Service has auth/registry/router but no approved claim contract fields.

Expected:

- Service returns `FailedPrecondition` before returning a RoutePlan that contains account/upstream auth.
- Test name states the risk: no claim inputs means no billable production plan.

Mutation self-check:

- If code returns a usable RoutePlan without claim reservation, test fails.

## Decision Points Needing Owner Sign-Off

### OD-1 New Go Runtime Dependencies

Recommendation: approve `google.golang.org/grpc` and `google.golang.org/protobuf` for Go RouteService. Without them, Go cannot serve tonic-compatible gRPC cleanly.

Risk: high under AGENTS because this adds runtime dependencies.

Fallback: keep Phase 2A as package-level service tests without starting a real gRPC server. This proves identity semantics but does not complete Rust-Go integration.

### OD-2 Production RouteQuery Claim Contract

Recommendation: do not allow production billable RoutePlan until RouteQuery carries or otherwise derives the inputs required for `ClaimGate.Reserve`.

Concrete choices:

- Add controlled proto fields in a Phase 2B contract: `endpoint_family`, `idempotency_key`, `normalized_payload_hash`, `billing_policy_version`, `request_class`, and enough cost/pricing inputs to reserve safely.
- Or split RouteQuery from billing, but then write a separate settlement design proving acquisition tokens cannot bypass quota/ledger. I do not recommend this without a full money-path review.

Risk: high because billing ledger/quota enforcement are high-risk domains.

### OD-3 Auth-Core Change Or Adapter

Recommendation: do not change `internal/auth` in Phase 2A. Reuse `APIKeyResolver.Resolve` via a routecontrol adapter that constructs a synthetic bearer request.

Risk: touching auth core is high-risk. The adapter keeps the blast radius lower and preserves existing resolver tests.

Owner sign-off needed if adding `ResolveToken` or changing bearer format validation inside `internal/auth`.

### OD-4 Legacy `tenant_id` Reconciliation Policy

Recommendation:

- Empty legacy tenant: accept.
- Matching legacy tenant: accept temporarily and count.
- Mismatch/non-numeric: reject.

Owner sign-off needed only if wanting "warn but continue" for mismatch. I recommend rejecting because "warn but continue" silently preserves Rust data-plane identity authority.

### OD-5 Manual First Phase 3 Cutover Gate

Recommendation: Phase 3 may remove Manual First only after:

- Go RouteQuery identity tests pass.
- Rust integration proves Manual First OFF still gets a successful Go-derived plan in non-production smoke.
- No mismatch events observed in a defined smoke window.

Owner must set the smoke window duration before deleting Manual First.

## Failure Modes And Mitigations

Raw credential leak:

- Mitigation: `ClientCredential.secret` unexported; status/log errors use fingerprint helper only; test forced errors with distinctive raw secret.

Go trusts Rust legacy `tenant_id`:

- Mitigation: service compares legacy tenant only after auth; downstream receives derived tenant; mismatch gate test asserts no downstream calls.

`x-api-key` clients stop working:

- Mitigation: parse `x-api-key:<secret>` and normalize to `Authorization: Bearer <secret>` for existing resolver.

Auth backend outage becomes 401:

- Mitigation: preserve `auth.ErrAuthBackend` -> `codes.Unavailable`, not `Unauthenticated`.

Generated proto lands in frozen package:

- Mitigation: generate into `backend/internal/routepb`, never `backend/internal/proto`.

RouteService returns account/upstream auth without claim:

- Mitigation: production guard returns `FailedPrecondition` until OD-2 is approved and implemented.

Adding gRPC dependencies breaks build or license review:

- Mitigation: OD-1 explicit approval; run `go test ./...`; run dependency license audit before commit.

Rust Manual First removed too early:

- Mitigation: Phase 2 only adds Go consumption and reconciliation; Phase 3 deletion has its own cutover gate.

## Concrete Execution Order

### Commit 1: Route Credential Parser And Identity Gate Tests

Files:

- Create `backend/internal/routecontrol/credential.go`
- Create `backend/internal/routecontrol/credential_test.go`
- Create `backend/internal/routecontrol/errors.go`

Steps:

1. Write parser tests first:
   - empty -> missing
   - `bearer:hk_test_x` -> bearer secret
   - `x-api-key:hk_test_x` -> x-api-key secret
   - `Bearer:` or `bearer:` -> invalid
   - `cookie:hk_test_x` -> invalid
   - raw secret absent from errors/debug

2. Implement parser with unexported secret.

3. Implement resolver request adapter that normalizes both accepted kinds to `Authorization: Bearer <secret>`.

4. Run:

```bash
cd backend
go test ./internal/routecontrol
```

Expected: parser and redaction tests pass.

### Commit 2: RouteQuery Service Without Network Server

Files:

- Create `backend/internal/routecontrol/service.go`
- Create `backend/internal/routecontrol/service_test.go`

Steps:

1. Define small interfaces in `routecontrol`:

```go
type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}
```

2. Add service method that accepts a local request struct mirroring `RouteQueryRequest` if generated Go proto is not approved yet.

3. Write tests for A1-A5:
   - derived tenant flows to registry/router stubs
   - mismatch rejects before downstream
   - matching legacy tenant passes
   - x-api-key normalizes
   - raw credential redacted

4. Run:

```bash
cd backend
go test ./internal/routecontrol
```

Expected: all identity gates pass without adding gRPC dependencies.

### Commit 3: Generated Proto And gRPC Server (Only If OD-1 Approved)

Files:

- Create `backend/internal/routepb/route.pb.go`
- Create `backend/internal/routepb/route_grpc.pb.go`
- Modify `backend/go.mod`
- Modify `backend/go.sum`
- Modify `backend/internal/routecontrol/service.go`
- Add/modify `backend/internal/routecontrol/grpc_test.go`

Steps:

1. Generate Go protobuf files into `backend/internal/routepb`.

2. Make `routecontrol.Service` implement `routepb.RouteServiceServer`.

3. Implement:
   - `RouteQuery` with identity gate.
   - `HealthCheck` returning schema/status.
   - `Heartbeat` returning ack/drain defaults.
   - `AttemptReport` as explicit `Unimplemented` or `FailedPrecondition` until its settlement contract is approved.

4. Run:

```bash
cd backend
go test ./internal/routecontrol
go test ./...
```

Expected: generated contract compiles and identity tests still pass.

### Commit 4: Config And Gateway Wiring (Only If OD-1 Approved)

Files:

- Modify `backend/internal/config/config.go`
- Create `backend/internal/config/route_control.go`
- Create `backend/internal/config/route_control_test.go`
- Modify `backend/cmd/gateway/wiring.go`
- Create `backend/cmd/gateway/control_plane_server.go`
- Modify `backend/cmd/gateway/lifecycle.go`

Steps:

1. Add config:
   - `HUAKAI_ROUTE_CONTROL_ENABLED`
   - `HUAKAI_ROUTE_CONTROL_TRANSPORT=uds|loopback_http`
   - `HUAKAI_ROUTE_CONTROL_UDS_PATH`
   - `HUAKAI_ROUTE_CONTROL_HTTP_ADDR` for dev/test only

2. Validate production:
   - enabled production requires UDS or mTLS if mTLS is later added.
   - loopback HTTP rejected in production.
   - UDS path must be non-empty.

3. Wire service from existing deps:
   - `d.inboundAuth`
   - `d.modelRegistry`
   - `d.routePlanner`
   - selector/vault only after OD-2.

4. Add lifecycle shutdown.

5. Run:

```bash
cd backend
go test ./internal/config ./cmd/gateway ./internal/routecontrol
```

Expected: default config does not start the server; enabled dev config starts test listener; production unsafe config fails fast.

### Commit 5: Production RoutePlan Mapping (Only After OD-2)

Files:

- Modify `backend/internal/routecontrol/service.go`
- Modify `backend/internal/routecontrol/plan_mapper.go`
- Add `backend/internal/routecontrol/plan_mapper_test.go`
- Possibly modify `exploratory/rust-core-gateway/merged/proto/route.proto` if OD-2 adds claim inputs.

Steps:

1. Add or derive claim-safe inputs per OD-2.

2. Use derived identity for:
   - `Registry.ResolveModel`
   - `Router.Plan`
   - `ClaimGate.Reserve`
   - `Selector.Select`
   - `CredentialVault.Resolve`

3. Map selected account to Rust `RoutePlan`:
   - `account_id`
   - `acquisition_token`
   - `vendor`
   - `upstream_model`
   - `vendor_endpoint`
   - `credentials_handle`
   - `auth_mode`
   - `route_ttl_ms`
   - `attempt_deadline_ms`
   - `max_body_bytes`
   - `max_stream_frame_bytes`
   - `upstream_auth`

4. Add a mutation-resistant test proving a mismatched tenant cannot resolve another tenant's credential even if account IDs collide.

5. Run:

```bash
cd backend
go test ./internal/routecontrol ./internal/auth ./internal/router ./internal/pool/... ./internal/provider/...
```

Expected: route plan mapping is claim-safe and tenant-derived.

## Pre-Execution Checklist

- Confirm synthesized Phase 2 plan exists and Owner approved it.
- Confirm no one read the same-name Claude Phase 2 draft while writing this Codex draft.
- Confirm OD-1 before adding gRPC/protobuf dependencies.
- Confirm OD-2 before returning production billable RoutePlan.
- Confirm no files are added under frozen `gatewayhttp`, `gateway`, or `proto`.
- Confirm `git status --short` and do not include unrelated existing changes.
- Run baseline:

```bash
cd backend
go test ./internal/auth ./internal/router ./internal/config
```

- If generated proto is in scope, run:

```bash
cd backend
go test ./...
```

- Stage intended files only and run:

```bash
codex exec review --uncommitted --full-auto
```

## Test Matrix

| Gate | Test file | Discriminating fixture | Expected mutation failure |
|---|---|---|---|
| P2-A1 derived tenant | `routecontrol/service_test.go` | empty legacy tenant, auth derives tenant 7 | using request tenant sends 0/empty to registry |
| P2-A2 mismatch reject | `routecontrol/service_test.go` | auth tenant 7, request tenant 8 | warn-and-continue calls registry/router |
| P2-A3 match allowed | `routecontrol/service_test.go` | auth tenant 7, request tenant 7 | rejecting all non-empty legacy tenant breaks compatibility |
| P2-A4 x-api-key normalize | `routecontrol/credential_test.go` | `x-api-key:hk_test_same` | resolver sees no Authorization header |
| P2-A5 no raw leak | `routecontrol/credential_test.go` | distinctive raw key in forced errors | status/log/debug contains raw key |
| P2-A6 no claim bypass | `routecontrol/service_test.go` | production service without claim contract | service returns usable account/upstream auth |

## Risks

- **Security risk:** raw client credentials enter Go process memory. Mitigation: no exported secret getter, redacted errors/logs, UDS/mTLS-only production transport.
- **Auth risk:** changing `internal/auth` directly can break existing HTTP auth. Mitigation: adapter first; auth-core change only with Owner approval.
- **Billing/quota risk:** returning a RoutePlan without a claim can bypass money-path guarantees. Mitigation: P2-A6 and OD-2.
- **Dependency risk:** gRPC/protobuf additions need license and build review.
- **Package-structure risk:** generated code in `internal/proto` would violate frozen package rule. Mitigation: `internal/routepb`.
- **Rollout risk:** deleting Manual First before Go identity is proven can break smoke/staging. Mitigation: Phase 3 cutover gate.

## Assumptions

- HUAKAI client API keys can be presented via either OpenAI-style Bearer or Anthropic-style x-api-key, but both represent the same HUAKAI `api_keys` table namespace.
- Rust `client_credential` canonical values are opaque transport values, not audit/log fields.
- Phase 2A can be useful even before production RoutePlan mapping because it closes the identity authority gap and creates the mismatch gate.
- Production billable traffic remains blocked until claim/billing inputs are resolved.

## Feature Preservation / Clean-Room / Safety Notes

- No functionality is dropped. Production billable RouteQuery is marked as a mandatory Phase 2B decision instead of being silently weakened.
- No clean-room risk observed in this draft: only HUAKAI internal code/docs were read.
- No security risk is accepted silently: raw credential transport, auth-core touch, gRPC dependency, and billing claim gaps are explicit decision points.
- Owner confirmation is required before dependency additions, auth-core changes, or money-path/claim contract changes.

## Owner Summary

1. 做了什么：独立起草 W11-A D-1b Phase 2 Codex draft，重点放在 Go control plane 消费 `client_credential`、派生权威 tenant、拒绝 legacy tenant mismatch，以及暴露当前 RouteQuery 缺少 billing/claim 输入的生产阻断点。
2. 改了哪些文件：只写本计划文件 `docs/process/plans/2026-05-24-w11a-d1b-phase2-codex.md`。
3. 为什么这样做：Phase 1 已解决 Rust 透传；Phase 2 的关键不是继续在 Rust 派生身份，而是让 Go 成为身份权威，同时避免绕过 billing/quota。
4. 有没有功能缩水：没有；生产 RoutePlan 被标为 Phase 2B Owner 决策，不被删除。
5. 有没有 clean-room 风险：没有；本 draft 未读 reference 项目源码，也未读同名 Claude Phase 2 draft。
6. 有没有安全风险：有 raw credential 入 Go 内存、gRPC transport、auth-core、billing claim 风险，均已列为 gate/Owner decision。
7. 哪些地方需要 Owner 确认：gRPC/protobuf 依赖、生产 RouteQuery claim contract、是否允许改 auth core、tenant mismatch 策略、Manual First Phase 3 cutover gate。
8. 下一步建议：与 Claude 独立稿交叉讨论，先合成 Phase 2A 身份消费计划；生产 billable RoutePlan 单独开 Phase 2B money-path 计划。
