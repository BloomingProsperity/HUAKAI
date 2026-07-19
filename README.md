# HUAKAI (华凯)

> MIT clean-room AI Gateway + Account Hub + Admin Ops Platform.

**Languages:** [English](README.md) · [简体中文](README_CN.md) · [Tiếng Việt (TBD)](README_VI.md) · [日本語 (TBD)](README_JA.md) · [한국어 (TBD)](README_KO.md) · [Español (TBD)](README_ES.md)

---

## ⚠️ Disclaimer

> **Read this before using HUAKAI.**
>
> 🚨 **Terms-of-service risk.** Using HUAKAI to reach an upstream LLM provider may
> violate that provider's Terms of Service (Anthropic, OpenAI, Google, AWS, and others).
> Whether any given deployment is permitted is the operator's own determination, and
> **all risk of using this project is borne solely by the operator.**
>
> 📖 **Intended use.** HUAKAI is provided for technical learning, security research,
> and operator self-hosting only. The authors and contributors accept **no liability** for
> account suspension, service interruption, financial loss, or any other consequence
> arising from use of this project.
>
> Full terms: the "Compliance & responsibility" section below and [LEGAL.md](LEGAL.md).

## What HUAKAI is

HUAKAI is an **operator-side, self-hosted reverse proxy and account router** for LLM API
provider accounts (Anthropic, OpenAI, Google Vertex, AWS Bedrock, OpenRouter). It runs in
front of one or more upstream accounts the operator already lawfully holds, and provides:

- A unified protocol surface for downstream clients
- Health-aware account dispatch
- Rate-limit / cooldown / retry handling
- Usage / billing accounting
- Optional impersonation of an upstream's first-party CLI client at the application and
  transport layers (where the operator's lawful use case requires it — see
  "Transport-level impersonation" below)

HUAKAI is intended for **personal use, internal team self-hosting, and security
research environments**. The repository is open source so operators can audit the
software running on their own infrastructure.

## What HUAKAI is NOT

- HUAKAI is **not affiliated with, endorsed by, or partnered with** any upstream LLM
  provider. Names like "Claude Code", "Claude", "Anthropic", "OpenAI", "ChatGPT",
  "Cursor", "Vertex AI", "Gemini", "Bedrock", and similar are the property of their
  respective owners. HUAKAI references these names only to describe interoperability.
- HUAKAI does **not** ship with any pre-loaded credentials, captured fingerprints, or
  other operational artifacts. All configuration is supplied by the operator.
- The maintainers of this project **do not operate** HUAKAI as a commercial SaaS.
  The project is published as software an operator may self-host. **If an operator
  chooses to deploy HUAKAI as a SaaS or any third-party-facing service, that
  operator is solely responsible for verifying compliance with each upstream
  provider's Terms of Service and with all applicable law in their jurisdiction.**
  The HUAKAI maintainers do not warrant that any specific deployment shape is
  permitted by any specific upstream provider.

## Intended use cases

- An operator runs HUAKAI on machines they own or fully control, with their own
  legitimately obtained upstream accounts.
- A small team self-hosts HUAKAI internally to share usage of accounts the team
  members lawfully hold.
- Security researchers, students, or developers study reverse-proxy / multi-account
  routing patterns in a controlled environment.
- An operator may also deploy HUAKAI in a SaaS or service form, **at the operator's
  sole responsibility for ToS and legal compliance**.

## Prohibited use cases

- Using HUAKAI to bypass any specific upstream provider's Terms of Service for
  paid services rendered to the public, when that bypass is not authorized by the
  upstream provider.
- Phishing, man-in-the-middle attacks, or any unauthorized observation of network
  traffic of parties other than the operator.
- Use of HUAKAI against accounts the operator does not lawfully hold.
- Misrepresenting HUAKAI as a product of, or endorsed by, any upstream provider.

## Compliance and responsibility

**The operator is solely responsible for ensuring their use of HUAKAI complies
with the Terms of Service of every upstream provider, with the laws of their
jurisdiction, and with the rights of any third parties involved.** The HUAKAI
project authors and contributors:

- Provide HUAKAI on an "AS IS" basis with no warranty.
- Make no claim that any HUAKAI deployment shape is permitted by any specific
  upstream provider.
- Accept no liability for the operator's use, misuse, account suspension,
  financial loss, legal exposure, or any other consequence.

If you are unsure whether your intended use is compliant with a given upstream's
ToS, **consult the upstream provider's documentation directly and / or seek
independent legal advice before deploying HUAKAI**.

## Transport-level impersonation (advanced, gated)

HUAKAI ships an optional transport-mimicry module (internally referenced as
`R3`) that can adjust outbound TLS / HTTP-2 fingerprints to match a first-party
CLI client. This module:

- Is **off by default**. Operators must explicitly enable it per upstream provider.
- **Ships with no fingerprint templates.** Operators must capture their own
  first-party client fingerprint using the bundled `tools/fingerprint-collector`
  tool, on their own machine, against their own client, in their own legitimate
  network environment.
- Is intended only for cases where the operator's lawful use case requires the
  outbound to be indistinguishable from the first-party client at the transport
  layer.

By using R3, the operator confirms that:

1. They have the right to capture and use the source client fingerprint they collected.
2. Their use of impersonation does not violate the upstream provider's ToS or any
   applicable law in their jurisdiction.

The fingerprint-collector tool's separate
[README](tools/fingerprint-collector/README.md) defines stricter rules about
what the tool may and may not be used for and what files may or may not leave
the operator's machine.

---

## Project status

**Status:** Phase C / N+5b in progress. The backend has a working clean-room gateway core
slice, not just governance documents. The current implemented request path is:

```text
Inbound Auth -> Model Registry -> Router Plan -> ClaimGate Reserve
-> Resource Pool Select -> Stream Forwarder -> Billing/Observability Settler
```

The project is still early. Multi-attempt fallback routing, first-class `attempt_id` /
`lease_id`, real provider adapters, production pricing, admin APIs, and the frontend
console are still active roadmap work. Strong-impersonation modules (R7 application-layer
6-step body transform and R3 transport-layer mimicry) are in active development behind
feature flags.

## Mission

Reach full feature parity or better with high-signal maintained AI gateway and account
hub projects, using a clean-room reimplementation that stays MIT-compatible. Reference
projects are evidence sources only; no reference feature may be silently dropped, and
risk changes implementation method rather than scope.

## Repository Layout

| Path | Purpose |
| --- | --- |
| [backend/](backend/) | Go backend core: gateway HTTP entrypoint, inbound auth, model registry, router engine, resource pool, protocol translation, streaming forwarder, billing/observability ledger, SQL migrations, and tests. |
| `frontend/` | No current frontend workspace is authoritative; the operations console will be rebuilt from page specifications and live API contracts. Self-hosting today is API-only. |
| [docs/deploy/](docs/deploy/) | Production deploy + first-boot bootstrap guide (`docker-compose.prod.yml`, env example, startup gates). |
| [tools/](tools/) | Operator tools (e.g. `fingerprint-collector` for transport-mimicry preparation). Each tool ships its own README with use-boundary rules. |
| [CLAUDE.md](CLAUDE.md) / [GEMINI.md](GEMINI.md) / [AGENTS.md](AGENTS.md) | Per-agent operating charters. |
| [docs/](docs/) | Authoritative governance, contracts, parity matrix, risk register, release gates, specs, and plans. |
| [docs_zh/](docs_zh/) | Owner-facing Chinese summaries. English docs remain canonical unless a decision says otherwise. |
| [docs/process/plans/](docs/process/plans/) | Execution plans and Claude/Codex cross-discussion records for implementation slices. |
| [backend/sql/migrations/](backend/sql/migrations/) | PostgreSQL migrations for pool routing, billing/observability, inbound auth, model registry, and related core tables. |
| [.agents/skills/](.agents/skills/) | Tool-agnostic skill definitions. |
| [.claude/skills/](.claude/skills/) | Mirror of `.agents/skills/` for Claude Code discovery. |
| [.claude/agents/](.claude/agents/) | Claude sub-agent role definitions. |
| [.gemini/hooks/](.gemini/hooks/) | Gemini guardrail shell hooks. |
| [LEGAL.md](LEGAL.md) | Trademark notices, compliance and liability terms, DMCA contact, data handling rules. |

## Current backend slice

The live inbound surface spans 44 distinct `/v1/*` and `/admin/v1/*` route prefixes
(not just `POST /v1/chat/completions`) — including `/v1/messages`, `/v1/embeddings`,
`/v1/images`, `/v1/audio`, `/v1/responses`, and `/v1/rerank`
(`backend/cmd/gateway/routes.go:106` registers `/v1/messages`).

Implemented:

- Table-backed inbound API key auth in `backend/internal/auth`.
- PostgreSQL-backed model registry in `backend/internal/registry`.
- L0 router engine in `backend/internal/router`.
- Resource pool selection and claim writeback in `backend/internal/pool`.
- Streaming forwarder and usage draft extraction in `backend/internal/gateway`.
- Tx1/Tx2 billing and observability settlement in `backend/internal/billing`.
- PostgreSQL migrations through `0207_account_bundle_audit_actions`.
- R7 application-layer mimicry primitives (system rewrite, cache_control breakpoints,
  tool-name obfuscation, metadata user_id rewrite, 6-step composer) in
  `backend/internal/gateway/`.

Known limitations:

- Router is still L0: one primary attempt from `PoolCandidates[0]`.
- Gateway executor logic is still embedded in the chat handler.
- `attempt_id` and `lease_id` are documented but not yet first-class schema fields.
- Provider adapters are production-wired: a default registry registers real passthrough
  adapters (Grok / Kimi / DeepSeek / Mistral and more), while OAuth/session mimicry egress
  is handled only by the Rust/BoringSSL sidecar. Missing socket, capability, or profile
  fails closed and never falls back to Go native TLS.
- Successful requests settle with real micro-USD pricing
  (`backend/internal/billing/public_price_table.go:166`), not a fixed placeholder cost.
- Admin APIs are implemented and mounted (20+ `/admin/v1/*` route groups in
  `backend/cmd/gateway/routes.go:815` plus `internal/{adminhttp,adminuserhttp,adminquotahttp,
  proxyadminhttp,modelbindingadminhttp}` packages); only the frontend operations console
  is not yet built.
- Transport-layer mimicry is production code: the Go gateway sends a versioned profile,
  proxy, cancellation, and timeout contract to the Rust sidecar over a Unix socket. The
  image starts both processes and readiness requires the sidecar contract to be healthy.

## Where to start

1. Read [docs/01_PROJECT_BRIEF.md](docs/01_PROJECT_BRIEF.md) for product scope.
2. Read [AGENTS.md](AGENTS.md) and [docs/RULES.md](docs/RULES.md) for the current operating rules.
3. Read the one current execution plan for the assigned goal.
4. Read [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) before touching anything driven by external references.
5. Read [docs/16_PHASED_DELIVERY_PLAN.md](docs/16_PHASED_DELIVERY_PLAN.md) to understand phasing.
6. For backend core work, read [docs/specs/_invariants/cross-module-boundaries.md](docs/specs/_invariants/cross-module-boundaries.md) before editing `backend/internal/{auth,registry,router,pool,gateway,gatewayhttp,billing,obs,proto}`.
7. For the current request path, start with [backend/cmd/gateway/main.go](backend/cmd/gateway/main.go) and [backend/internal/gatewayhttp/chat_completions_handler.go](backend/internal/gatewayhttp/chat_completions_handler.go).
8. For UI work, read [docs/14_UI_CONTRACTS.md](docs/14_UI_CONTRACTS.md) and [docs/08_REAL_WORLD_SCENARIOS.md](docs/08_REAL_WORLD_SCENARIOS.md).

## Verification

From `backend/`:

```bash
go test ./...
go test -tags integration_pg ./...
go test -tags smoke ./cmd/gateway
```

`integration_pg` and `smoke` require `HUAKAI_DATABASE_URL` to point at a migrated
PostgreSQL database.

## Reference projects

Reference projects are evidence sources only, never source-code providers. Their
license types determine clean-room handling. Verified license status is tracked in
[docs/24_REFERENCE_TRACKING_POLICY.md](docs/24_REFERENCE_TRACKING_POLICY.md).

## How decisions are made

The currently assigned executor owns the work unit end to end. Decisions are based on
HUAKAI source, official contracts, domain-appropriate source evidence, whole-chain impact,
and operational recovery. An independent reviewer is a quality gate, not a second planner.
Only the decision conditions in [docs/RULES.md](docs/RULES.md) require the Owner to stop and pick.
Historic Decision Records remain under [docs/process/decisions/](docs/process/decisions/).

## License

[MIT](LICENSE). Contributions to this repository must remain MIT-compatible. See
[docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) for what is allowed and
forbidden when learning from external projects.

Third-party libraries used by HUAKAI are subject to their own licenses and must pass the
dependency license gate before release.

## Legal

See [LEGAL.md](LEGAL.md) for trademark notices, compliance and liability terms, DMCA
contact, and data handling rules.

## Contributing

Implementation is active. All changes remain owner-directed and must follow the
clean-room policy, plan-before-execute discipline, cross-review protocol, and
cross-module boundary invariants. Contributor terms have not yet been published.

## No warranty

```
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```
