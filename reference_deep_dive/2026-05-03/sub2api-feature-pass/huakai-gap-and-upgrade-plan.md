# HUAKAI gap and upgrade plan

Date: 2026-05-03

Reference: sub2api commit `48912014a16e2dd1cfca8b7cad785d0e8e7bfeec`

## Core judgment

HUAKAI is not fundamentally wrong. The problem is that the current plan under-specifies the account-to-API mainline. Sub2API already proves that the hard part is operating upstream accounts as capacity-bearing, credential-bearing, stateful assets.

The missing spine is:

`API key binding -> user/key/group contract -> pool routing -> capacity wait/fallback -> credential lease -> injector/adapter -> request attempts -> usage/billing -> account state -> ops trace`.

## L1 must add before real upstream hardcoding

| Feature ID | Name | Source docs | Why |
| --- | --- | --- | --- |
| `F-ACCAPI-ASSET-001` | Account asset model | `01-account-asset-model.md` | Account is the sellable/schedulable resource. |
| `F-ACCAPI-BIND-001` | API key binding contract | `02-api-key-user-group-contract.md` | Key must bind to pool/account policy, not just auth. |
| `F-ACCAPI-CAPACITY-001` | Account/user slot and wait plan | `04-concurrency-slot-wait-plan.md` | Forced binding still needs concurrency behavior. |
| `F-ACCAPI-CRED-LEASE-001` | Credential lease/token version trace | `08-credential-refresh-token-cache.md` | Must know which token version was injected. |
| `F-ACCAPI-CRED-INJECT-001` | Credential injector | `09-credential-injection-protocol.md` | Avoid handler-level hardcoding. |
| `F-PROTO-ADAPTER-001` | Protocol adapter | `09-credential-injection-protocol.md` | Separate request shape from credential injection. |
| `F-ACCAPI-ATTEMPT-001` | Request attempts | `06-retry-failover-account-switch.md` | Every account switch must be auditable. |
| `F-USAGE-A2A-TRACE-001` | Usage linked to binding/account/attempt | `11-usage-billing-settlement.md` | Billing must explain source of cost. |
| `F-REQ-BODY-001` / `F-LOG-SAFE-001` | Body/log guards | `16-security-body-log-guards.md` | Credential injection increases leakage risk. |

## L2 stability upgrades

| Feature ID | Name | Source docs | Why |
| --- | --- | --- | --- |
| `F-ACCAPI-STICKY-AUDIT-001` | Sticky break audit | `05-sticky-session-context.md` | Needed for context stability and incident review. |
| `F-ACCAPI-STATE-001` | Multi-axis account state view | `07-rate-limit-cooldown-state.md` | One coarse status is not operational enough. |
| `F-CH-MON-001` / `F-CH-MON-SSRF-001` | Channel monitor with SSRF guard | `13-channel-monitor-healthcheck.md` | Health checks must be safe and feed state. |
| `F-OPS-TRACE-001` | Request trace | `14-ops-admin-investigation.md` | Admin must answer why a request failed. |
| `F-ASYNC-BOUND-001` | Bounded workers | `15-async-workers-cleanup.md` | Prevent outage amplification. |
| `F-TRANSPORT-PROFILE-001` | Transport profile | `17-transport-proxy-tls-fingerprint.md` | Proxy/TLS affects conversion stability. |

## L3 operations / commercialization

| Feature ID | Name | Source docs | Why |
| --- | --- | --- | --- |
| `F-PAY-ORDER-001` | Payment order state machine | `12-payment-order-recovery-refund.md` | `support payment` is too broad. |
| `F-PAY-FULFILL-001` | Fulfillment retry/recovery | `12-payment-order-recovery-refund.md` | Paid-but-not-fulfilled is a real incident. |
| `F-PAY-REFUND-001` | Refund rollback/audit | `12-payment-order-recovery-refund.md` | Refund needs local and provider recovery. |
| `F-OPS-RETRY-001` | Admin retry | `14-ops-admin-investigation.md` | Lets ops reproduce/pin a failed account path. |

## L4 extensions

| Feature ID | Name | Source docs | Why |
| --- | --- | --- | --- |
| `F-TLSFP-001` | TLS fingerprint profiles | `17-transport-proxy-tls-fingerprint.md` | High leverage for specific providers, but can follow core spine. |
| `F-MODEL-ROUTE-001` | Advanced model routing | `10-model-routing-capability.md` | Useful after base capability snapshot is stable. |
| `F-SCHED-OUTBOX-001` | Scheduler snapshot/outbox | `15-async-workers-cleanup.md` | Scale optimization after MVP. |

## Fusion plan

1. Owner approves account-to-API spine as HUAKAI mainline.
2. Add matrix rows: `ASSET`, `BIND`, `CAPACITY`, `CRED-LEASE`, `INJECT`, `ATTEMPT`, `TRACE`.
3. Before Slice 5 upstream work, define `ProtocolAdapter`, `CredentialInjector`, `ErrorClassifier`, `AccountSelector`.
4. Add minimal migrations: `api_key_bindings`, `request_attempts`, usage binding/account/attempt columns, credential version trace.
5. Keep admin simple but traceable: key -> binding -> pool -> account -> credential -> attempt -> usage.
6. Push TLS fingerprint, advanced routing and rich payment UI after the L1 spine is testable.

## How HUAKAI can be stronger than sub2api

- More traceable: every selection and switch has a persisted reason.
- More testable: adapter/injector/classifier are separate contracts.
- More stable: account capacity, wait plan and transport profile are first-class.
- More operable: admin trace follows the exact request spine.
- Safer: body/log guards are mandatory before credential injection.

## Concrete message for Claude

Please do not implement real upstream credential injection directly inside gateway handlers. Before Slice 5, add the account-to-API spine:

- `api_key_bindings`
- `request_attempts`
- credential version/lease trace
- `AccountSelectionPlan`
- `ProtocolAdapter + CredentialInjector + ErrorClassifier`
- usage records linked to binding/account/attempt
- admin trace view path

Sub2API already has the operating mechanisms: account asset state, group/pool routing, account/user concurrency wait, sticky break, failover/account switch, refresh lock, usage settlement, monitor, ops retry and proxy/TLS identity. HUAKAI should not merely copy feature names; it should make them auditable and testable as one mainline.

---
Source files read: sub2api backend/ent/schema (account, api_key, user, group, usage_log, payment_order, channel_monitor, proxy, tls_fingerprint_profile), backend/internal/service (account, gateway_service, concurrency_service, antigravity_gateway_service, ops_service, ops_retry, channel_monitor_runner, payment_fulfillment, payment_refund, usage_record_worker_pool, scheduler_snapshot_service), backend/internal/handler (failover_loop, openai_gateway_handler, gemini_v1beta_handler)
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
