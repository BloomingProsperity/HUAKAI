# HUAKAI Frontend Build Spec (for Claude Design)

> **Purpose.** This is the complete, authoritative brief for building the HUAKAI web frontend.
> It is written so the UI **perfectly interfaces with the existing backend**. Visual/UX design is
> yours (Claude Design); this document fixes the **contract, architecture, auth, page→endpoint map,
> and cross-cutting rules** so nothing drifts from the backend.
>
> **Golden rule:** the backend is DONE and is the source of truth. The UI adapts to the API — never
> the reverse. If a shape is unclear, read `docs/openapi/openapi.yaml`, never guess.

---

## 0. What HUAKAI is (so the UI tells the right story)

HUAKAI is a **multi-tenant commercial AI relay gateway + account center + operations console** — same
class as new-api / sub2api. Three audiences, three surfaces:

1. **Public site** — anonymous visitors: landing, model pricing, public rankings, sign-up/login.
2. **User portal** — a paying tenant user: API keys, balance/recharge, usage & billing, subscriptions,
   vouchers, referrals, check-in, a chat playground, and account security (passkey/2FA).
3. **Admin console** — platform operators: providers/channels, upstream accounts & pools, credentials,
   billing/pricing, users, audit, observability, alerting, DLQ, moderation, model sync.

The UI must feel like a polished commercial product (think a fusion of OpenAI's platform dashboard +
a billing console + an ops cockpit), bilingual **zh-CN / en**.

---

## 1. The contract is the source of truth — codegen, do not hand-type

- **Single source of truth:** `docs/openapi/openapi.yaml` (~16k lines, ~40 tags). Every request/response
  shape, enum, and error is defined there.
- **MANDATORY:** generate the TypeScript client/types from it. Add a dev dependency and script:
  ```jsonc
  // package.json
  "devDependencies": { "openapi-typescript": "^7" },
  "scripts": { "gen:api": "openapi-typescript ../docs/openapi/openapi.yaml -o lib/api/schema.d.ts" }
  ```
  Then type all fetch calls against `paths`/`components['schemas']` from `schema.d.ts`. **Never** write a
  request/response interface by hand — this is what guarantees "完美对接后端". Regenerate on every backend
  contract change (CI check: `gen:api` produces no diff).
- The existing `frontend/lib/api/*.ts` are thin hand-written wrappers; **keep the wrapper pattern**
  (`client.ts` already injects `Authorization: Bearer <token>`), but back every call with generated types.

---

## 2. Tech stack (locked — extend the existing app, don't rewrite)

- **Next.js 15 App Router** + **React 18** + **TypeScript 5.5** + **Tailwind 4**. Already in `frontend/`.
- Data layer: keep `lib/api/client.ts` (fetch + Bearer injection + JSON error throw). Add **TanStack Query**
  (`@tanstack/react-query`) for caching/mutations/loading-error states. Forms: **react-hook-form + zod**
  (zod schemas can be derived from the generated types).
- API base: `process.env.HUAKAI_GATEWAY_URL` (default `http://localhost:8080`); same-origin `/v1`, `/admin/v1`,
  `/v1/admin` in prod (Next rewrites/proxy). Streaming (chat) uses the Fetch streaming body / SSE.
- **Do not** introduce a different framework, CSS-in-JS, or a second HTTP client. Tailwind + shadcn-style
  components only.

---

## 3. Auth model (exactly how each surface authenticates)

- **Inference + user-portal calls** authenticate with an **`hk_` API key** as `Authorization: Bearer hk_...`
  (bcrypt-verified server side → resolves tenant Identity{TenantID, APIKeyID, UserID}). A logged-in user
  session yields a session/JWT used for portal management calls; the **playground** lets the user pick one of
  their own `hk_` keys.
- **Login/session:** `/v1/auth/*` (password), **passkey** (`/v1/auth/passkey/*`, `/v1/me/passkeys`),
  **2FA** (`/v1/auth/2fa/*`), **sessions** (`/v1/sessions`). Persist the session token; refresh per the auth tag.
- **Admin console** calls (`/admin/v1/*`, `/v1/admin/*`) require a **platform-admin** identity. The UI must
  gate the entire admin surface behind the admin role and never render admin nav to a normal user.
- **RBAC in UI:** three guards — `public` (no auth), `user` (session/key), `admin` (admin role). Route groups:
  `app/(public)`, `app/(portal)`, `app/(admin)`.

---

## 4. Information architecture → page ↔ openapi-tag map

Build these routes. Each page lists the **openapi tag(s)/endpoints** that power it — wire only those.

### 4A. Public `app/(public)`
| Route | Purpose | Endpoints / tags |
|---|---|---|
| `/` | Landing / marketing | static + `pricing` |
| `/pricing` | Model price table + plans | `GET /v1/pricing/page`, `GET /v1/pricing/rate-table`, `pricing` tag |
| `/rankings` | Public model/usage rankings | `GET /v1/public/rankings` |
| `/login`, `/register` | Auth | `auth`, `user-passkeys`, 2FA tags |
| `/trust` | Verifiable audit / transparency | `trust` tag, `/.well-known/huakai-pubkey.json`, `POST /v1/trust/verify` |

### 4B. User portal `app/(portal)`
| Route | Purpose | Endpoints / tags |
|---|---|---|
| `/dashboard` | Balance, quota, recent usage at a glance | `user-quota` (`/v1/me/quota`), `GET /v1/me/usage`, analytics |
| `/playground` | Chat/messages/responses test bench (streaming), model picker | `gateway`: `/v1/chat/completions`, `/v1/messages`, `/v1/responses`, `GET /v1/models` |
| `/keys` | Create/list/rotate/revoke API keys, per-key limits (IP, expiry, USD quota, model allow/deny, IP allow/deny) | `/v1/api-keys`, `user-api-key-controls`, `GET /v1/me/keys/{id}/usage-summary` |
| `/usage` | Usage history + analytics charts | `GET /v1/me/usage`, `GET /v1/me/analytics/time-series`, `GET /v1/generation` |
| `/billing` | Balance, recharge, payment history, invoices/receipts | `user-recharges`, `/v1/users/me/payments`, `user-vouchers` (`/v1/users/me/vouchers`), `pricing/snapshots` |
| `/subscriptions` | Plan/subscription management | `/v1/users/me/subscriptions` |
| `/referrals` | Invite friends, referral rewards | `GET /v1/me/invitations`, `GET /v1/me/referrals`, `GET /v1/me/referrals/rewards` |
| `/checkin` | Daily check-in bonus | `user-checkin` tag |
| `/notifications` | In-app notices + announcements | `user-notifications`, `announcements` tags |
| `/account` | Profile, password, passkeys, 2FA, sessions | `auth`, `user-passkeys`, 2FA, `sessions` |
| `/audit` | The user's own verifiable receipts | `user-audit`, `/v1/audit/*`, `/v1/receipts/*` |

### 4C. Admin console `app/(admin)`  (every `admin-*` tag)
| Route | Purpose | Endpoints / tags |
|---|---|---|
| `/admin` | Ops cockpit: system health, DLQ depth, alert状态, revenue snapshot | `admin-usage`, `admin-billing`, `admin-alerting`, system-health aggregate |
| `/admin/providers` | Provider catalog CRUD | `admin-models`/providers: `GET/POST/PUT/DELETE /admin/v1/providers` |
| `/admin/channels` | Channel catalog + test templates | `GET /admin/v1/channels`, `/admin/v1/channel-test-templates` (CRUD) |
| `/admin/accounts` | Upstream provider-accounts (the heart): list/create/edit, tags, enable/disable, modes, health, per-account limits (sessions, window-cost, cooldown, proxy, TLS profile) | `admin-accounts` (`/admin/v1/provider-accounts`, `/v1/admin/provider-accounts`, `/v1/admin/pool-accounts`), `admin-channel-health` |
| `/admin/pools` | Pooling groups, routing | `admin-pools` (`/admin/v1/pools`), `/v1/admin/routes` |
| `/admin/credentials` | Credential vault + acquisition | `admin-credential-acquisition`, `/admin/v1/credentials` |
| `/admin/users` | User management + verbs | `admin-users` (`/admin/v1/users`) |
| `/admin/keys` | Platform-wide key admin | `admin-api-keys` (`/admin/v1/api-keys`) |
| `/admin/billing` | Claims, balances, usage records | `admin-billing` (`/admin/v1/billing`, `/admin/v1/billing/claims`), `/admin/v1/balances`, `GET /admin/v1/usage` |
| `/admin/pricing` | Rate tables, versions, cache-price overrides | `admin-pricing`, `/v1/admin/cache-price-overrides`, `pricing/snapshots` |
| `/admin/payments` | Payments, vouchers, disputes, subscriptions | `admin-vouchers`, `/v1/admin/payments`, `/v1/admin/disputes`, `/v1/admin/subscriptions` |
| `/admin/referrals` | Referral program overview | `GET /v1/admin/referrals*` |
| `/admin/observability` | Metrics (Prometheus), live vars | `/debug/vars`, `admin-usage` |
| `/admin/alerting` | Alert rules, silences, events | `admin-alerting` |
| `/admin/audit` | Audit events, merkle tree, trust | `admin-audit`, `/admin/v1/audit-events`, `/v1/audit/merkle-tree.json` |
| `/admin/dlq` | Dead-letter queues, replay | `admin-dlq` (`/admin/v1/dlq/{handler}`, replay) |
| `/admin/cache` | L2 cache controls | `admin-cache` (`/admin/v1/cache/l2`) |
| `/admin/model-sync` | Upstream model sync | `admin-model-sync` (`/admin/v1/model-sync`) |
| `/admin/notifications` | Broadcast notices/announcements/email | `admin-notifications`, `admin-announcements`, `admin-email` |
| `/admin/moderation` | Content moderation queue | `admin-moderation` |
| `/admin/settings` | Platform settings, TLS fingerprint profiles | `admin-platform-settings`, `/v1/admin/tls-fingerprint-profiles` |

---

## 5. Cross-cutting rules (these prevent backend drift)

1. **Errors:** the backend returns structured error codes (e.g. `pricing_unavailable`, `insufficient_balance`,
   `insufficient_quota`, `idempotency_conflict`, `invalid_size`, `invalid_n`, `prompt_too_long`, `quota_denied`,
   `reserve_error`, `settle_error`, `upstream_dispatch_error`, `audit_ledger_error`). Map each to a clear,
   localized toast/inline message. Never show a raw 500 body. Read the error schema in openapi.
2. **Money:** balances/costs are high-precision (`numeric(20,8)`; pricing in **micro-USD = USD per 1M tokens**;
   image `image_base_micro_usd` = USD/image × 1e6). **Never** do lossy float math in the UI — format from the
   server-provided decimal strings; show 2–6 significant digits with the currency. Image cost = `n × base ×
   size_mult × quality_mult / 1e6`; token cost = `tokens × rate / 1e6`.
3. **Streaming:** the playground must stream `text/event-stream` for chat/messages/responses (render deltas;
   handle `content_block_start/delta/stop`, tool calls, and **image** output blocks for image-capable models).
4. **Pagination & filtering:** list endpoints use cursor/limit + filters (tags, status, date) — read each list
   endpoint's params; build reusable table components with server-side pagination.
5. **Idempotency:** recharge/payment/image actions accept idempotency keys — generate and send them so retries
   don't double-charge.
6. **i18n:** zh-CN + en, every string in a message catalog; default zh-CN.
7. **Optimistic vs. confirmed:** money/admin mutations must wait for server confirmation (no optimistic UI on
   billing/keys/accounts).

---

## 6. Out of scope / frozen (do NOT build real versions)

- **Real PSP checkout** (Stripe/Alipay/WeChat/EasyPay): the recharge/payment UI should call the existing
  recharge/webhook endpoints and render a **mock/redirect placeholder** for the actual payment step — the real
  PSP integration is Owner-gated. Build the full flow UI; stub the payment-provider handoff.
- **Mimicry / anti-ban internals**: the existing `app/mimicry` ops view stays admin-only and read-mostly.
- Do not invent endpoints. If a page needs data with no endpoint, flag it — do not fabricate a contract.

---

## 7. How to use this with Claude Design (the actual prompts)

Build **one page/section at a time**, each as its own prompt to Claude Design. Use this template per page:

> **Prompt template:**
> "Design and implement the **`<route>`** page for the HUAKAI <portal|admin|public> surface, Next.js 15 App
> Router + Tailwind 4 + TanStack Query. It is powered by these backend endpoints: **<list from §4>**. The
> request/response types come from `lib/api/schema.d.ts` (generated from `docs/openapi/openapi.yaml`) — import
> and use them, never hand-type shapes. Auth guard: **<public|user|admin>**. Must handle loading / empty /
> error (map error codes per §5.1) / paginated states. Money formatting per §5.2. <Page-specific UX goals>.
> Match the HUAKAI design system (see §8). Deliver the page component, its `lib/api/<area>.ts` typed calls,
> and the route under the correct `app/(group)/` segment."

Suggested build order (vertical slices, each shippable):
1. Auth + session shell (login/register/passkey/2FA, the app layout + nav + RBAC guards).
2. User dashboard + keys + playground (the core daily-use loop).
3. Billing + usage + subscriptions (the money loop).
4. Admin cockpit + accounts + channels + pools (the ops core).
5. Admin billing/pricing/payments + audit + alerting + DLQ (ops depth).
6. Public site (landing/pricing/rankings/trust).

---

## 8. Design direction (yours to own — visual)

- **Brand feel:** trustworthy, technical-but-approachable commercial SaaS. Dark + light themes. Dense data
  tables for admin; calm, generous spacing for user portal; bold/clear for public pricing.
- **Component inventory** (build once, reuse): top nav + side nav (role-aware), data table (sortable, paginated,
  server-filtered), stat cards, money figure, token/usage charts (recharts or visx), code/JSON viewer, streaming
  chat transcript, key/secret reveal-copy, status badges (account health, channel, alert severity), confirm
  dialogs for destructive/money actions, toast system mapped to error codes, empty/skeleton states.
- **Accessibility:** keyboard nav, focus states, ARIA on tables/dialogs, color-contrast AA.
- Keep the existing `app/chat`, `app/observability`, `app/accounts` work where good; refactor into the new
  route groups.

---

## 9. Acceptance checklist (definition of "完美对接")

- [ ] `lib/api/schema.d.ts` generated from `docs/openapi/openapi.yaml`; **zero hand-typed** API shapes.
- [ ] Every page wires only the endpoints listed in §4 for that route.
- [ ] All three auth guards enforced; admin surface invisible to non-admins.
- [ ] Every backend error code has a localized UI message; no raw error bodies.
- [ ] Money rendered from server decimals, never float-recomputed.
- [ ] Chat playground streams and renders text + tool + image output blocks.
- [ ] Lists are server-paginated/filtered against the real params.
- [ ] zh-CN/en complete.
- [ ] PSP payment step is a clearly-labeled stub (frozen), the rest of the flow is real.

---

*Source of truth: `docs/openapi/openapi.yaml`. Backend landing: `fix/h-fixes`. When the contract and this doc
disagree, the contract wins — regenerate types and update this map.*
