# HUAKAI Frontend — rebuild in progress (React + Vite SPA)

The previous Next.js frontend was experimental/test-only and has been removed.
The replacement is a clean **React + Vite + react-router single-page app**, built
to a static `dist/` and embedded into the gateway binary via `internal/webui`
(see the gateway's build-tag embed, already in place).

Plan: `docs/process/plans/2026-06-19-frontend-spa-migration.md`.
Decision rationale: this is the reference-aligned stack (sub2api = Vue+Vite,
new-api = React+rsbuild — both embed a static SPA into one Go binary), chosen on
functionality + maintainability.
