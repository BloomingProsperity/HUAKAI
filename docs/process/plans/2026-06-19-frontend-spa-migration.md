# Plan — Frontend migration: Next.js → React + Vite SPA (single-binary embed)

Date: 2026-06-19 · Author: Claude PM · **Owner-authorized decision** (see below). Execution to be loop-driven.

## Decision (LOCKED — Owner-authorized 2026-06-19)
Migrate the frontend from **Next.js → React + Vite SPA** (react-router), built to a static `dist/` and
`go:embed`-ed into the single Go gateway binary (sub2api build-tag pattern). Decided **purely on
functionality + maintainability** — Owner explicitly removed effort/code-volume from the criteria
("码量不再考虑范围内!要的是功能和维护"). Recorded in memory `frontend-stack-decision-react-vite-spa`.

> ## ⚠️ UPDATE 2026-06-19 — greenfield rebuild, not a port (supersedes "Migration scope" + "Sequencing" below)
> Owner clarified the existing Next.js frontend was **experimental/test-only, never finalized**
> ("删 都删掉,本来前端还没定好呢,只是测试。将计划里的前端技术栈也一起改掉"). Therefore:
> - The disposable Next.js test frontend under `frontend/` is **deleted wholesale** (backed up first to the
>   archive branch `archive/frontend-nextjs-pre-vite-2026-06-19` + a local tarball; restorable anytime).
>   **Backend is untouched** — only `frontend/` was removed.
> - The replacement is a **clean greenfield React + Vite + react-router SPA built from scratch**, NOT a
>   port that reuses the old components. So the "Migration scope (reuse components/lib/api)" and
>   "Sequencing (land the 12 frontend branches first)" sections below **no longer apply** — there is nothing
>   to preserve, no Sidebar.tsx constraint, no branch sequencing (those branches were test artifacts too;
>   verified stale via content-diff).
> - The gateway embed infrastructure from Phase 0 (`internal/webui`, build-tag) stays and receives the new
>   `vite build` output. Tech stack: **React + Vite + react-router + Tailwind**, embedded → single binary.

## Why (functionality + maintainability)
- **Functionality: parity, nothing lost.** Next.js's server-only strengths (SSR / server components /
  API routes / middleware / image optimization) are **unused** here (verified: 51 `use client` components,
  no `app/api`, no middleware, `images.unoptimized`) and are unreachable in static-export mode anyway. A
  React SPA serves the identical app. A future public SEO page is a separate static page, not a reason to
  keep Next.
- **Maintainability: SPA wins.** No framework impedance mismatch (Next is SSR-first; we use it as a static
  SPA); no fragile `output: 'export'` mode to babysit across Next majors; lighter deps + faster builds.

## #16 triple-mirror (real source)
- sub2api — Vue 3 + Vite (`frontend/package.json`), `vue-tsc && vite build` → `dist/`, embedded via
  `//go:embed all:dist` (`backend/internal/web/embed_on.go:27`, build-tag `embed` + `embed_off.go` stub).
- new-api — React 19 + rsbuild (`web/default/package.json`), `rsbuild build` → `dist/`, embedded via
  `//go:embed web/default/dist` (`main.go:38`); served with a SPA fallback (gin `NoRoute` → index.html).
- CLIProxyAPI — no in-repo frontend (admin is a separate Tauri app); **no equivalent**.
- **HUAKAI delta**: same single-binary embedded-SPA end-state as sub2api/new-api, reusing HUAKAI's existing
  React components + API layer; React+Vite chosen (components are already React → keep them, swap only the shell).

## Target architecture
`React + Vite + react-router` → `vite build` → static `dist/` → gateway `go:embed` (build-tag: `embed_on.go`
`//go:build embed` + `embed_off.go` stub so the default build needs no dist and stays testable) → gateway
serves the SPA (router NotFound → `index.html`, with an **API-path guard** so `/v1`,`/admin/v1`,`/debug`,
`/.well-known`,`/healthz` never fall through to the SPA) + the existing API. Caddy terminates TLS → gateway
**single upstream** (like sub2api). One container.

## Migration scope (real-code measured — for execution, not a decision input)
- **Reuse as-is (framework-agnostic):** `components/` (~1.1k LOC) + `lib/api/` (~11.2k LOC, the 646 wirings).
- **Port:** the **11 files** importing `next/*` (only `next/navigation` ×8 → react-router hooks
  `useNavigate`/`useLocation`/`useSearchParams`; `next/link` ×7 → react-router `Link`). The **45 `page.tsx`
  routes → a react-router route config**; **2 `layout.tsx` → layout routes**. **No dynamic `[param]` routes,
  no SSR** — this keeps the port mechanical.
- **Replace:** `next.config.mjs` `rewrites()` → Vite dev proxy (dev) + same-origin relative API base (prod);
  `lib/api/huakai.ts` API base → same-origin (served by the gateway, `/v1` hits the API directly).
- **New scaffolding:** `vite.config.ts`, `index.html`, `main.tsx` (router bootstrap); adapt the existing
  tailwind/postcss config.

## Phases (loop-executable)
**Phase 0 — gateway embed infrastructure (do NOW; safe + inert + testable; no frontend collision).**
New `backend/internal/webui/` package: `embed_off.go` (`//go:build !embed`, stub Handler that is disabled),
`embed_on.go` (`//go:build embed`, `//go:embed dist`), a shared SPA-serve handler (serve static asset if it
exists, else `index.html`; **never** serve for API path prefixes), a committed `dist/index.html` placeholder,
and unit tests (asset serve, SPA fallback, API-path guard — all testable under the default `!embed` build via
an injected `fs.FS`). Wire into the gateway router as the `NotFound` handler when enabled. **Inert by default**
(stub) → zero behavior change until built `-tags embed` with a real dist. Verify both `go build ./...` and
`go test ./internal/webui/` (and `-tags embed` compiles with the placeholder).

**Phase 1 — frontend port (AFTER the in-flight frontend branches land; see Sequencing).** Scaffold Vite +
react-router; port the 45 routes + 2 layouts; swap the 11 `next/*` files; same-origin API base; reuse
`components/` + `lib/api/` unchanged. Keep tailwind. Delete Next.js (`next.config.mjs`, next deps).

**Phase 2 — build pipeline + single-container.** Dockerfile multi-stage: node stage runs `vite build` → copy
`dist/` into `backend/internal/webui/dist/` → `go build -tags embed`. Rework `docker-compose.prod.yml` to a
**single app container** (drop the frontend service; Caddy → gateway only). Reuse the deploy plumbing already
drafted (migrate sidecar, audit key, secrets gen, runbook) from `wt-prod-deploy-bundle`.

**Phase 3 — validation (real machine; sandbox has no docker/node-run).** Go webui tests pass in CI. On a real
host: `vite build`, embedded boot, full smoke (register → login → API key → gateway request → UI loads), and a
**no-feature-loss parity check** (enumerate current routes/features, confirm each present in the SPA).

## Sequencing (preserve in-flight functionality — NOT an effort concern)
**12 active frontend branches** are adding features to the current Next app: `feat/frontend-admin-capability-binding,
-inherit-catalog, -notify-extra-emails, -proxies, -routes-enable`, `feat/frontend-apikey-expiry-update`,
`feat/frontend-model-capabilities`, `feat/frontend-routes-client`, `feat/routeadmin-priority`, `-update`,
`fix/model-admin-error-sanitize` (+ the base `feat/frontend-portal`). Porting `app/` while these churn it = lost
work + merge hell. **Rule: land/merge these into the Next app first, then freeze the frontend, then port the
consolidated app (Phase 1).** Phase 0 (gateway infra) does not touch the frontend and proceeds now.

## Success criteria
A single Go binary serves the API + the embedded React SPA; `docker compose up` (one app container + postgres +
caddy + migrate sidecar) brings up the full relay stack; **feature parity with the current frontend** (nothing
lost); zero Next.js dependency.

## Risk / blast radius
- Phase 0: additive, inert (build-tag stub) — low risk, fully testable.
- Phase 1: wholesale `app/` → `src/` restructure — high collision **if done before the 12 branches land**
  (hence the sequencing gate). Reuses components + API layer, so logic risk is bounded; the risk is routing/nav
  correctness + no-feature-loss (covered by the Phase 3 parity check).
- Phase 2: deploy/build change (Owner-gated class) — surface before landing.

## Owner decision points
- **Go-live timing:** clean-after-migration (default) vs a temporary Next static-export bridge if there is a
  hard "go live this week" deadline. (Owner indicated no rush → default to clean.)
- **When to freeze the 12 frontend branches** for the Phase-1 cutover (coordinate via `.coordination/`).
