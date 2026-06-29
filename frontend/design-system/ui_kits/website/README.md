# HUAKAI 官网首页 — Website UI kit

A self-hosted-product **marketing / landing homepage** for HUAKAI. This surface does
**not** exist in the source repo — the repo ships only the operations console — so it
is a *new* extension of the system.

**Current theme:** a **light sky-blue** palette (vertical gradient sampled from a
reference site: `#F4FBFF` → `#C3E9FF` → `#ABDFFF`) with white cards, dark slate text,
and a **lime accent** (vivid lime fills + dark text; deep lime for thin text/links).
The hero terminal stays a dark code block. This is a deliberate divergence from the
console's dark teal — re-themed entirely via a `:root` override in `index.html`
(scoped to this page; the console is untouched).

Open `index.html` — it is an interactive, scrollable page and a **Starting Point**.

## Structure
Each section is a small JSX file attached to `window.*` and composed by the inline
`SiteApp` in `index.html` (same pattern as `ui_kits/console/`). DS primitives are
pulled from `window.HUAKAIDesignSystem_36f9be`.

- `icons.jsx` — `Icon` (Lucide via CDN, matches the console).
- `Nav.jsx` — `HKLogo` lockup + sticky, blur-on-scroll top nav with `Button` CTA.
- `Hero.jsx` — headline, lead, CTAs, trust stats, and the hero visual: a
  syntax-highlighted `POST /v1/chat/completions` terminal (SSE) + a floating
  **dispatch** card (`StatusDot` + health-aware routing).
- `Providers.jsx` — the 5 supported upstream providers (Anthropic, OpenAI, Google
  Vertex, AWS Bedrock, OpenRouter) with channel + model chips.
- `Features.jsx` — 6 capability cards (unified protocol · health-aware dispatch ·
  rate-limit & retry · usage/billing · audit `MOCK` · self-host), tone-chipped icons.
- `CTA.jsx` — `SiteDeploy`: self-host band with a `docker compose` command line.
- `Footer.jsx` — link columns + heartbeat status + MIT/version line.

## On-brand notes
- **Copy** follows CONTENT FUNDAMENTALS: Simplified Chinese with English technical
  terms inline (`POST /v1/chat/completions`, `operational` / `cooling_down` /
  `degraded`, `usage.actual_cost`), sentence case, system voice, candid about state
  (the audit card carries the violet `MOCK` badge). No emoji.
- **Numbers are monospace + tabular** (`$38.42`, `1,284,500 tokens`, `3/10 · 42ms`).
- **Teal glow** is used sparingly (logo, live status dot, hero ambient, CTA wash) —
  never as blanket decoration.
- **New for this surface:** a marketing-scale type ramp (`clamp()` H1 up to ~62px)
  and a faint masked grid + radial glow in the hero. These extend the console scale
  upward for landing use; everything else maps to existing tokens.

## Iteration ideas
Add a compact request-path / architecture band, a metrics strip pulling the real
console KPIs, or a docs/quickstart section — kept to the same restrained, data-first
tone.
