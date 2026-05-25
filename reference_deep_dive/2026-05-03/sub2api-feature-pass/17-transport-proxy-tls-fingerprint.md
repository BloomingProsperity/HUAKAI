# 17 Transport / proxy / TLS fingerprint

## Sub2API behavior summary

Sub2API has a proxy entity with fields, edges, and indexes. A proxy service model builds an active proxy URL and account summaries. The proxy service supports full CRUD, active listing, per-proxy account counts, connection testing, and URL lookup. A proxy latency and quality cache tracks connection performance. An upstream HTTP transport port supports both plain and TLS-specific transport with proxy and account concurrency awareness. A TLS fingerprint profile table exists with configuration fields and can bind to accounts through supplementary metadata. A TLS profile service has a local cache, hot-path lookup, and account-based profile resolution. Admin can test and check proxy quality and persist latency and quality cache results.

## Entity / fields

Transport identity includes account-bound proxy, proxy latency/quality cache, HTTP client transport, TLS fingerprint profile, and account concurrency-aware connection behavior.

## Request chain

Account selected -> resolve proxy/TLS profile -> choose transport/client -> upstream uses account-specific network identity -> proxy quality informs admin and routing.

## State machine

`proxy_configured -> active/tested -> quality_checked -> account_bound -> transport_resolved -> upstream_sent`.

## Failure modes

- Same credential through bad proxy lowers conversion stability.
- Shared connection pool across accounts can leak network identity.
- TLS fingerprint mismatch can make account conversion unstable.

## Sub2API capability

Sub2API has proxy entities, account proxy references, proxy quality/latency cache, HTTP transport port and TLS fingerprint profile resolution.

## HUAKAI current capability

HUAKAI has not treated transport identity as core in the reviewed account-to-API docs.

## HUAKAI gap

`MISSED_BY_HUAKAI`: proxy/TLS is not a nice-to-have plugin. For account conversion stability it is part of account identity and SLA.

## HUAKAI stronger design

Define `TransportProfile`: `proxy_id`, `tls_fingerprint_profile_id`, connection pool isolation key, DNS/proxy health, latency/quality snapshot and transport error classifier.

## Suggested Feature ID / level

- `F-TRANSPORT-PROFILE-001`: L2
- `F-PROXY-QUALITY-001`: L2
- `F-TLSFP-001`: L4 initially, L2 for high-risk account providers.
- `F-CONN-ISOLATION-001`: L2

## Acceptance tests

- Two accounts with different proxies do not share transport identity.
- Failed proxy health excludes account or marks transport axis blocked.
- Attempt stores proxy/TLS snapshot.

## Open questions

- open-question: whether TLS fingerprint is MVP or provider-specific later work.

---
Source files read: sub2api backend/ent/schema/proxy, backend/internal/service/proxy, backend/internal/service/proxy_service, backend/internal/service/proxy_latency_cache, backend/internal/service/http_upstream_port, backend/ent/schema/tls_fingerprint_profile, backend/internal/service/tls_fingerprint_profile_service, backend/internal/service/admin_service
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
