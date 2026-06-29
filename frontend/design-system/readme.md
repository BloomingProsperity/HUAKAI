# HUAKAI Design System

A brand + UI design system for **HUAKAI (华凯)** — a self-hosted, operator-side
**AI Gateway · Account Hub · Admin Ops platform**. HUAKAI sits in front of an
operator's own upstream LLM provider accounts (Anthropic, OpenAI, Google Vertex,
AWS Bedrock, OpenRouter) and provides a unified protocol surface, health-aware
account dispatch, rate-limit / retry handling, and usage / billing accounting.

This system captures the visual language of HUAKAI's **operations console** — a
dense, dark, data-first admin UI — so agents can generate on-brand screens,
components, slides, and mocks.

## Sources

Everything here was derived from the company's own codebase. Explore these to go
deeper / refresh the system:

- **GitHub:** https://github.com/BloomingProsperity/HUAKAI — the monorepo
  (Go backend + Next.js `frontend/`). The design language was lifted from:
  - `frontend/app/globals.css` + `frontend/tailwind.config.ts` — tokens (teal primary, slate neutrals, shadows).
  - `frontend/components/ui/*` — shadcn-style Button / Badge / Card / Table.
  - `frontend/components/dashboard/*` — StatCard, TrendChart, load states.
  - `frontend/components/layout/*` — Sidebar, Header, AppLayout.
  - `frontend/app/dashboard/page.tsx`, `app/accounts/*`, `app/chat/*` — real screen composition.

No standalone brand book, logo files, or Figma were provided; the brand mark is a
text **"HK" monogram** (see *Visual Foundations → Logo*).

---

## CONTENT FUNDAMENTALS

How HUAKAI writes copy. The product is an internal operator tool, so the voice is
**terse, technical, and bilingual**, never marketing-y.

- **Language:** UI is primarily **Simplified Chinese (zh-CN)** with **English
  technical terms left inline and untranslated**. This code-switching is the
  defining trait. Examples: "测试 Chat 调试器", "POST /v1/chat/completions + Anthropic messages，双 tab，支持 SSE 流式", "P95 结算耗时", "缓存读取占比 read / (creation + read)". Keep API paths, struct names (`MimicryPlan`), status enums (`operational`, `cooling_down`), and metric names in English/code.
- **Casing:** Sentence case throughout; no Title Case. Headings are short noun
  phrases ("运营总览", "账号池", "异常告警条件").
- **Person:** Impersonal / system voice. Neither "I" nor "you" — it describes
  state ("当前没有触发告警条件的账号。", "真实后端账号池健康、成本、用量与缓存效率集中视图"). Imperative for actions ("刷新", "新增账号").
- **Honesty about state:** Copy is unusually candid about what is real vs mocked.
  Panels carry a purple **MOCK** badge when the backend isn't implemented; empty
  states say so plainly ("真实 usage 记录不足，暂不绘制趋势；不会用本地假曲线补齐。", "后端未返回 provider account。"). Never fabricate data to fill a chart — say it's missing.
- **Numbers:** Always formatted and tabular — thousands separators (`1,284,500`),
  fixed-precision cost (`38.42`), durations as `1.24s` / `42ms`, ratios as `87.4%`,
  concurrency as `3/10`. Render in monospace.
- **Tone / vibe:** Operator-grade dashboard. Calm, factual, information-dense.
  Think "internal SRE console", not "SaaS landing page".
- **Emoji:** Not used in product UI. (The repo README uses ⚠️/🚨 in disclaimers, but
  the console itself has none.) Don't add emoji to console screens.

---

## VISUAL FOUNDATIONS

### Theme
The console ships **dark by default** (`<html class="dark">`, app background
`#020617`). A light theme exists (tokens are defined for both) but dark is the
canonical look. Every card/specimen here uses `class="dark"`.

### Color
- **One brand accent: teal.** `--primary-500 = #14b8a6` with a full 50–950 scale.
  It marks every "live/active/selected" affordance: the HK logo, active sidebar
  item, focus rings, primary buttons, trend line, progress fill, link text. Used
  sparingly against a neutral field — it should always read as *the* signal color.
- **Neutrals are slate** (internally named "accent", 50–950, `#f8fafc → #020617`).
  Dark surfaces: app `950`, cards `900`, insets `800`, borders `800`, body text
  `100`, muted text `400`, subtle `500`.
- **Semantic:** success `#10b981` emerald, warning `#f59e0b` amber, danger
  `#ef4444` red, info `#3b82f6` blue, plus a rarely-used violet `#8b5cf6` for MOCK
  badges. Each has soft bg/border/fg variants for pills.
- **Imagery:** none — there is no photography or illustration in the product. The
  visual field is pure UI: surfaces, type, data, and the teal accent.

### Type
- **System font stack only** — no webfonts (`system-ui … "PingFang SC", "Microsoft
  YaHei"`), chosen for fast first paint and native CJK rendering.
- **Monospace** (`ui-monospace …`) for all data: IDs, API keys, timestamps,
  latencies, token counts, concurrency — with `tabular-nums`.
- Scale: page H1 / stat value **24px/700**, panel title **18px/600**, body
  **14px/400**, table-head & eyebrow **12px/600 uppercase +0.04em**, meta **12px/400**.

### Spacing & layout
- 4px base grid. Card padding **20px**, compact **16px**; section gaps **24px**;
  control inner padding **12px**. Min interactive target **44px** (sidebar items
  are `min-h 44`).
- **Fixed shell:** left sidebar `256px` (collapsible to `72px`), sticky header
  `64px` with `backdrop-blur`. Content scrolls under both.
- Dashboards use responsive auto-fit grids (stat tiles `minmax(220px,1fr)`; main
  region splits `2fr / 1fr`).

### Corners, borders, elevation
- **Radius 8px** (`--radius`) for cards/panels/buttons; 6px inputs; 4px chips;
  full-round pills/badges/status-dots/avatars.
- **Borders are hairline** (1px, slate-800 in dark / slate-200 in light) — they do
  the structural work; cards rarely rely on heavy shadow.
- **Shadows are soft and low:** `--shadow-card` (subtle 2-layer) with
  `--shadow-card-hover` lift on interactive cards. A teal **glow**
  (`0 0 20px rgba(20,184,166,.25)`) is reserved for *live* elements only — the HK
  logo, online status dots, the health progress bar. Don't use glow as decoration.

### Motion & states
- Quiet and utilitarian. Transitions are short (150–200ms) color/transform fades.
- **Hover:** interactive cards lift `translateY(-2px)` + warm border to teal;
  buttons darken one step (teal→`#0d9488`); ghost/outline fill with the teal-50
  tint; table rows highlight to the inset surface.
- **Focus:** 2px teal ring (offset via a 3px soft halo on inputs).
- **Active/press:** color change, no scale gimmicks.
- The only looping animations: a 200ms accordion, a refresh-spinner, a gentle
  status-dot pulse for live polling. No bounces, no parallax, no big entrances.

### Logo
Text monogram **"HK"** in a teal rounded-square (10px radius) with the teal glow,
locked up beside "HUAKAI 华凯 / 控制台". There is no graphical logomark in the repo;
recreate the monogram from tokens.

---

## ICONOGRAPHY

- **Library: [Lucide](https://lucide.dev)** — the frontend imports `lucide-react`.
  Stroke icons, 2px weight, rendered at **16px** (`size-4`) inline with labels and
  in StatCard chips; 12–14px for dense meta.
- Icons in active use across the console: `layout-dashboard`, `database`,
  `key-round`, `bar-chart-3`, `file-check-2`, `settings`, `shield-check`,
  `activity`, `clock-3`, `database-zap`, `dollar-sign`, `zap`, `gauge`,
  `heart-pulse`, `shield-alert`, `layers`, `refresh-cw`, `messages-square`,
  `send`, `plus`, `ellipsis`, `user-round`, `moon`, `chevron-left`.
- Color: icons inherit `currentColor` (muted slate by default); tinted to the
  semantic tone inside StatCard chips and panel titles (teal `primary-400`, amber
  for alerts).
- In HTML cards / UI kits, load Lucide from CDN
  (`https://unpkg.com/lucide@0.469.0/dist/umd/lucide.min.js`) and call
  `lucide.createIcons()`. **Do not** hand-roll SVG icons or use emoji as icons.
- No custom icon font or SVG sprite ships in the repo. No PNG icon assets exist.

---

## INDEX / MANIFEST

**Root**
- `styles.css` — entry point; `@import`s all tokens. Consumers link this one file.
- `tokens/` — `colors.css`, `typography.css`, `spacing.css`, `radius.css`, `shadows.css`, `motion.css`.
- `SKILL.md` — Agent-Skill manifest (usable in Claude Code).

**Components** (`window.HUAKAIDesignSystem_36f9be`)
- `components/core/` — `Button`, `Badge`, `StatusDot`, `Card` (+ `CardHeader` /
  `CardTitle` / `CardDescription` / `CardContent` / `CardFooter`), `Input`, `Label`.
- `components/data/` — `StatCard`, `Table` (+ `THead` / `TBody` / `TR` / `TH` / `TD`).
- Each component has a `.d.ts` (props), `.prompt.md` (usage), and its directory
  carries a `*.card.html` Design-System specimen.

**UI kits**
- `ui_kits/console/` — interactive HUAKAI operations console: Dashboard, Accounts,
  Chat debugger, Audit placeholder. `index.html` is the click-through entry and a
  **Starting Point**.
- `ui_kits/website/` — HUAKAI product **homepage** (hero + terminal visual,
  supported providers, capability grid, self-host CTA, footer). A *new* on-brand
  surface (no website exists in the repo); `index.html` is a **Starting Point**.

**Foundation specimen cards** (`guidelines/`)
- Colors: `color-primary`, `color-neutral`, `color-semantic`.
- Type: `type-scale`, `type-mono`.
- Spacing/Brand: `spacing`, `radius-elevation`, `brand-logo`.

The **Design System tab** renders every `@dsCard`-tagged file above, grouped as
Colors · Type · Spacing · Brand · Components · Console · Website.

---

## Notes & substitutions
- **Fonts:** intentionally the system stack — no font files to ship. The compiler
  may flag `PingFang SC` as missing a `@font-face`; that is expected (it's a
  built-in macOS CJK font used purely as a fallback, matching the real product).
- **Icons:** Lucide via CDN (matches the codebase's `lucide-react`); no substitution.
- **Assets:** the repo contains no logo/illustration/photo files, so none were
  copied — the HK monogram is reproduced from tokens.
