# 2026-05-12 HUAKAI 前端市场 brief（Codex lane）
## 元数据
- Lane: Codex frontend research / specifier-style market brief.
- Date: 2026-05-12 UTC.
- Independence: 本 lane 未读取 sonnet brief，也不依赖 sonnet 结论。
## 1. 24+ Reference UI 拆解
### 1. Helicone
- URL: https://www.helicone.ai/ ; docs evidence: https://docs.helicone.ai/getting-started/platform-overview
- Recency: GitHub `Helicone/helicone` pushed_at `2026-05-05T23:10:28Z`, within 90 days; public docs crawled within the last week.
- Top nav: marketing homepage top nav + product dashboard left nav; public copy lists Dashboard, Requests, Segments, Sessions, Users, HQL, Prompts, Datasets, Playground, Monitor, Rate Limits.
- Home/main layout: landing hero is product-first, then dashboard screenshots; dashboard mental model is request table + metrics + drilldown.
- Color strategy: dark ops base with vivid blue/green/accent highlights; feels “AI infra console” rather than classic SaaS pastel.
- Typography: public pages use modern sans; exact product font not confirmed, so do not copy.
- Dark/i18n/a11y: dark visual language visible; i18n not confirmed; docs provide image alt text, but full a11y posture not confirmed.
- HUAKAI 借鉴: Logs/Requests should be first-class, not hidden under analytics; make the gateway table the center of the product.
### 2. LiteLLM Proxy UI
- URL: https://docs.litellm.ai/ ; product page: https://www.litellm.ai/
- Recency: GitHub `BerriAI/litellm` pushed_at `2026-05-12T01:39:12Z`, within 90 days; license not asserted by GitHub API, no source copied.
- Top nav: docs/product navigation emphasizes Proxy Server, virtual keys, teams, spend, models, admin dashboard.
- Home/main layout: operational admin panel pattern; user lands in management surfaces, not a marketing-like overview.
- Color strategy: utilitarian, neutral, likely light-first with status colors for spend/access; visual polish is secondary to task coverage.
- Typography: standard docs/app sans; exact UI font not confirmed.
- Dark/i18n/a11y: admin UI can be disabled/configured per docs; i18n/a11y posture not confirmed from public docs.
- HUAKAI 借鉴: Treat virtual keys, teams, budgets, and spend as adjacent controls in one operator workflow.
### 3. Portkey AI
- URL: https://portkey.ai/docs/product/observability/logs ; feature overview: https://docs1.portkey.ai/docs/introduction/feature-overview
- Recency: GitHub `Portkey-AI/gateway` pushed_at `2026-03-25T09:33:55Z`, within 90 days; MIT.
- Top nav: product modules center on AI Gateway, Observability/Logs, Prompt Library, Guardrails, Agents.
- Home/main layout: log list with click-to-open side panel; log detail links back to config/prompt context; replay is a visible action.
- Color strategy: clean SaaS neutral with clear active/inactive status states; status column carries gateway behavior such as cache/retry/fallback/load balance.
- Typography: docs/app sans; exact dashboard font not confirmed.
- Dark/i18n/a11y: docs do not prove i18n; shareable log URLs and detail panels are strong collaboration affordances.
- HUAKAI 借鉴: Every log row should expose route/fallback/cache status and open a right drawer with replay and trace links.
### 4. Langfuse
- URL: https://langfuse.com/ ; docs: https://langfuse.com/docs/
- Recency: GitHub `langfuse/langfuse` pushed_at `2026-05-11T20:43:23Z`, within 90 days; public site states open-source/MIT, GitHub API returned no license assertion.
- Top nav: app surface appears lifecycle-based: Observability, Prompts, Evaluation, Platform.
- Home/main layout: workflow loop from traces to prompts to evals; dashboard tabs include trace details, sessions, timeline, users, agent graphs, analytics, evals.
- Color strategy: light product docs with orange/neutral brand accents; operational UI appears data-dense but less dark-ops than Helicone.
- Typography: modern sans; exact font not confirmed.
- Dark/i18n/a11y: self-host/open-source posture strong; i18n and a11y not fully confirmed from public evidence.
- HUAKAI 借鉴: Connect observability rows to prompt versions and eval outcomes instead of treating logs as dead records.
### 5. Braintrust
- URL: https://www.braintrust.dev/ ; docs: https://www.braintrust.dev/docs/evaluate
- Recency: proprietary SaaS; no canonical dashboard repo pushed_at; official docs crawled last week / four days ago.
- Top nav: eval-first information architecture: Evaluations, Playgrounds, Experiments, datasets, scorers.
- Home/main layout: comparison tables and diff views are central; public docs describe filter bar, column visibility, table, summary views, aggregate scores.
- Color strategy: polished neutral enterprise SaaS with restrained accent use.
- Typography: modern sans; exact font not confirmed.
- Dark/i18n/a11y: no public proof of i18n; keyboard and table affordances are documented indirectly via playground/eval workflows.
- HUAKAI 借鉴: For routing changes, offer experiment-style compare views: baseline route vs new route, cost/latency/error deltas.
### 6. PostHog AI / LLM Analytics
- URL: https://posthog.com/docs/llm-analytics ; product site: https://posthog.com/posthug
- Recency: GitHub `PostHog/posthog` pushed_at `2026-05-12T01:41:04Z`, within 90 days; GitHub API returned no license assertion.
- Top nav: PostHog uses product-suite navigation: Product Analytics, Session Replay, Web Analytics, Error Tracking, Experiments, Feature Flags, Logs, CDP, Workflows, PostHog AI.
- Home/main layout: app library and dashboard modules; LLM Analytics is presented as a product inside Product OS, not a separate console.
- Color strategy: playful brand-strong yellow/orange plus neutral product surfaces; higher personality than enterprise infra tools.
- Typography: modern sans; exact font not confirmed.
- Dark/i18n/a11y: open-source/self-host posture; i18n/a11y not fully confirmed from public evidence.
- HUAKAI 借鉴: Put AI usage analytics next to product/user dimensions so operators can correlate cost with users and features.
### 7. new-api / QuantumNous new-api
- URL: https://github.com/Calcium-Ion/new-api
- Recency: GitHub redirected metadata to `QuantumNous/new-api`, pushed_at `2026-05-11T03:25:37Z`, within 90 days; AGPL-3.0; source-read was specifier-only.
- Source evidence: `QuantumNous/new-api@ba474393fbb91ad7bb66e6c9693da8d0550eedfb:web/classic/package.json:6`, `...:web/classic/src/App.jsx:90`, `...:web/classic/src/components/layout/SiderBar.jsx:71`, `...:web/classic/src/components/layout/headerbar/index.jsx:67`.
- Top nav: hybrid shell with sticky top header and configurable left console navigation; routes cover channel, token, logs, models, deployments, playground, subscriptions, settings.
- Home/main layout: dense admin console with tables, modals, charts, setup wizard, playground, and model-pricing surfaces.
- Color strategy: Semi UI + Tailwind; light/dark support visible in shell classes; more functional than brand-refined.
- Typography: component-library default sans; package uses Semi UI, Vite, Tailwind, i18next, charts.
- Dark/i18n/a11y: explicit i18n dependencies and theme toggle paths observed; a11y not evaluated.
- HUAKAI 借鉴: Keep admin-heavy account/channel/model surfaces reachable in one sidebar, but simplify naming and reduce modal sprawl.
### 8. one-api
- URL: https://github.com/songquanpeng/one-api
- Recency: GitHub pushed_at `2026-01-09T03:26:43Z`, outside 90 days, mark stale-but-stable; MIT; default branch commit observed `8df4a267...`.
- Source evidence: `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:web/air/package.json:5`, `...:web/air/src/App.js:57`, `...:web/air/src/components/SiderBar.js:37`, `...:web/air/src/components/HeaderBar.js:65`.
- Top nav: older hybrid app shell with horizontal header plus left navigation; routes cover channels, tokens, logs, users, settings, data detail, chat/drawing when enabled.
- Home/main layout: classic CRUD console; tables and edit pages dominate.
- Color strategy: Semi UI / older React dashboard look; practical but visually dated.
- Typography: default component-library sans; no evidence of a bespoke type system.
- Dark/i18n/a11y: dark toggle observed; i18n package not visible in sampled `air` package; a11y not evaluated.
- HUAKAI 借鉴: Preserve “simple CRUD is fast” ergonomics but modernize density, filtering, and detail drawers.
### 9. all-api-hub
- URL: https://github.com/qixing-jk/all-api-hub
- Recency: GitHub pushed_at `2026-05-11T16:43:46Z`, within 90 days; GitHub API license noassertion; source-read was shallow UI shell only.
- Source evidence: `qixing-jk/all-api-hub@3ef71a49d31dc46af75aabe8b9cc52f8a83937b3:package.json:64`, `...:src/entrypoints/options/App.tsx:73`, `...:src/entrypoints/options/components/Sidebar.tsx:27`, `...:src/components/AppLayout.tsx:29`.
- Top nav: extension/options app with header, collapsible left sidebar, search dialog, and provider-wrapped layout.
- Home/main layout: settings/asset-management console; content sits in a central rounded panel with side navigation and command-like search.
- Color strategy: Tailwind/shadcn-like neutral + blue active state; supports light/dark class patterns.
- Typography: Tailwind/system sans; component dependencies include Radix, TanStack Query/Table, ECharts, i18next, zod, React 19.
- Dark/i18n/a11y: explicit i18n and theme providers observed; sidebar has aria labels in sampled source.
- HUAKAI 借鉴: Add command/search entry points for settings-heavy ops workflows, but avoid oversized rounded panels for HUAKAI dense views.
### 10. Vercel Dashboard
- URL: https://vercel.com/docs/dashboard-features/overview ; project docs: https://vercel.com/docs/projects/project-dashboard
- Recency: proprietary dashboard; official docs last updated `2025-09-24`; `vercel/vercel` repo pushed_at `2026-05-12T01:50:04Z` but not dashboard source.
- Top nav: top-level team/project scope selector plus top/right search; project dashboard uses tabs for Overview, Deployments, Logs, Storage, Settings.
- Home/main layout: project grid/list, then project-specific detail pages with cards, tables, logs, and settings.
- Color strategy: mono/neutral, very low saturation; status dots carry semantic color.
- Typography: Vercel brand uses Geist family publicly; HUAKAI should not copy, but can borrow compact numeric rhythm.
- Dark/i18n/a11y: docs support rich images; i18n not assessed; dashboard supports search/filter/list-grid preference.
- HUAKAI 借鉴: Use scope selector + global search + project/account grid as a clean entry model.
### 11. Linear
- URL: https://linear.app/docs/conceptual-model ; inbox docs: https://linear.app/docs/inbox
- Recency: proprietary SaaS; official docs crawled four days ago; no public dashboard repo pushed_at.
- Top nav: minimal sidebar + keyboard navigation + Cmd/Ctrl-K command menu; docs emphasize contextual commands.
- Home/main layout: list/board/timeline views with display options, filters, grouping, sidebars, contextual menus.
- Color strategy: muted dark/neutral with subtle brand highlights; avoids chart-heavy dashboards.
- Typography: refined modern sans; exact app font not confirmed.
- Dark/i18n/a11y: keyboard-first workflows are first-class; i18n not confirmed.
- HUAKAI 借鉴: Add a command palette for “find key, open log, replay request, disable account, create route.”
### 12. Stripe Dashboard
- URL: https://docs.stripe.com/dashboard/basics ; analytics docs: https://docs.stripe.com/payments/analytics
- Recency: proprietary SaaS; docs crawled within two months; localized docs exist for multiple locales.
- Top nav: left sidebar primary navigation with account/product sections, shortcuts, and search.
- Home/main layout: home page is customizable widget dashboard; money pages use dense tables, filters, exports, and detail records.
- Color strategy: neutral/light enterprise with purple brand accent and strong semantic status colors.
- Typography: Stripe uses a polished custom/brand sans stack publicly; exact dashboard font not used as dependency.
- Dark/i18n/a11y: broad localization visible in docs; keyboard shortcut help is documented.
- HUAKAI 借鉴: For billing/quota, copy the workflow pattern: table -> filter/export -> detail -> audit trail, not the visual style.
### 13. Cloudflare Dashboard
- URL: https://developers.cloudflare.com/fundamentals/concepts/accounts-and-zones/ ; analytics docs: https://developers.cloudflare.com/analytics/types-of-analytics/
- Recency: proprietary dashboard; docs published/crawled in April-May 2026; no public dashboard repo pushed_at.
- Top nav: account/zone scope model with sidebar switching between account-level and zone-level products.
- Home/main layout: product console with analytics/logs/security/configuration split by resource scope.
- Color strategy: neutral enterprise with orange brand accents and clear warning/security states.
- Typography: modern sans; exact dashboard font not confirmed.
- Dark/i18n/a11y: dashboard language can be changed per profile docs; a11y not fully confirmed.
- HUAKAI 借鉴: Make tenant/account/provider scope explicit at all times; scope confusion is a production-risk UI bug.
### 14. Supabase Studio
- URL: https://supabase.com/docs/guides/local-development/overview ; logs docs: https://supabase.com/docs/guides/platform/logs
- Recency: GitHub `supabase/supabase` pushed_at `2026-05-12T01:51:18Z`, within 90 days; Apache-2.0.
- Top nav: project dashboard with left product navigation: database, auth, storage, functions, logs, settings, and related tools.
- Home/main layout: Studio combines table editor, SQL editor, logs explorer, settings, and product-specific panels.
- Color strategy: dark-capable neutral with green brand accent; developer-console feel.
- Typography: modern sans and mono for SQL/logs; exact font not critical.
- Dark/i18n/a11y: local Studio exists; logs explorer and SQL-like query affordances documented.
- HUAKAI 借鉴: Logs should support SQL-like or structured query mode for advanced operators, behind guardrails.
### 15. Resend
- URL: https://resend.com/docs/dashboard/logs/introduction ; domains docs: https://resend.com/docs/dashboard/domains/introduction
- Recency: proprietary SaaS; docs crawled four days ago / last month; no public dashboard repo pushed_at.
- Top nav: resource navigation around Emails, Domains, Logs, API Keys, Broadcasts, Contacts, Settings.
- Home/main layout: clean resource tables with details pages, filters, exports, and linked logs.
- Color strategy: minimal monochrome SaaS with restrained blue/green status; very low visual noise.
- Typography: modern sans; exact font not confirmed.
- Dark/i18n/a11y: docs expose copyable CLI/API paths; i18n not confirmed.
- HUAKAI 借鉴: API key pages should show last-used, permission scope, linked logs, and export with minimal ceremony.
### 16. PostHog
- URL: https://posthog.com/posthug ; GitHub: https://github.com/PostHog/posthog
- Recency: GitHub `PostHog/posthog` pushed_at `2026-05-12T01:41:04Z`, within 90 days.
- Top nav: broad product suite with app library; navigation favors product modules over pure resource CRUD.
- Home/main layout: dashboards, insights, replay, flags, experiments, surveys, logs, and AI assistant are unified in one product OS.
- Color strategy: playful yellow/orange brand layer over dense analytics UI.
- Typography: informal but readable product sans; exact font not confirmed.
- Dark/i18n/a11y: self-hostable; i18n/a11y not fully verified.
- HUAKAI 借鉴: Keep “assistant asks over product data” as a right rail or command action, not a separate chatbot island.
### 17. Sentry
- URL: https://docs.sentry.dev/product/issues/ ; trace docs: https://docs.sentry.io/product/explore/traces/
- Recency: GitHub `getsentry/sentry` pushed_at `2026-05-12T01:47:30Z`, within 90 days; GitHub API license noassertion.
- Top nav: issue/performance/explore-centric observability console; saved searches and query-to-dashboard are core.
- Home/main layout: issue inbox table -> issue detail with stack/event/context/sidebar; trace explorer adds query charts and sample tables.
- Color strategy: neutral/dark observability style with purple accent and severity/status colors.
- Typography: modern sans plus mono for stack/event data.
- Dark/i18n/a11y: docs show keyboard/search workflows indirectly; i18n not confirmed.
- HUAKAI 借鉴: Treat provider failures as triageable “issues” with regression/escalation status, not only log rows.
### 18. Datadog
- URL: https://docs.datadoghq.com/llm_observability/ ; product page: https://www.datadoghq.com/product/llm-observability/
- Recency: proprietary SaaS; docs crawled last month/week; no public dashboard repo pushed_at.
- Top nav: huge observability platform navigation; LLM Observability adds traces, clusters, dashboards, experiments/playground.
- Home/main layout: traces and clusters are paired with operational dashboards and quality/security evaluation surfaces.
- Color strategy: enterprise observability dark/neutral with purple brand and strong chart palettes.
- Typography: modern enterprise sans; exact font not confirmed.
- Dark/i18n/a11y: broad platform likely supports enterprise accessibility standards, but not proven from fetched docs.
- HUAKAI 借鉴: Pair LLM trace browsing with cluster/group views for repeated failure patterns and cost anomalies.
### 19. Grafana Cloud
- URL: https://grafana.com/docs/grafana-cloud/visualizations/dashboards/ ; AI observability: https://grafana.com/docs/grafana-cloud/machine-learning/ai-observability/
- Recency: GitHub `grafana/grafana` pushed_at `2026-05-12T01:14:56Z`, within 90 days; AGPL-3.0, no source read.
- Top nav: Grafana Cloud platform shell with dashboards, Explore, drilldowns, alerts, and data sources.
- Home/main layout: panels organized in rows/tabs; Explore is query-first; AI Observability adds conversations, traces, eval rules, prebuilt dashboards.
- Color strategy: dark observability default with strong categorical chart colors.
- Typography: modern sans plus mono/query editors.
- Dark/i18n/a11y: dark mode is a product norm; i18n/a11y not assessed.
- HUAKAI 借鉴: Offer “Dashboard” for glance and “Explore” for unrestricted operator investigation.
### 20. shadcn/ui
- URL: https://ui.shadcn.com/docs/components ; GitHub: https://github.com/shadcn-ui/ui
- Recency: GitHub `shadcn-ui/ui` pushed_at `2026-05-10T16:36:26Z`, within 90 days; MIT; 114k+ stars observed via GitHub API/search.
- Top nav: docs/sidebar component catalog plus registry/distribution model.
- Home/main layout: component docs, examples, preview/code tabs, copy-and-own workflow.
- Color strategy: neutral/monochrome by default; theme tokens allow brand overlays.
- Typography: modern docs sans; product does not force a specific app font.
- Dark/i18n/a11y: dark mode docs, chart a11y layer, RTL mention, Radix/Base primitives.
- HUAKAI 借鉴: Use shadcn as owned component source, but wrap into HUAKAI-specific ops components.
### 21. Tailwind UI / Catalyst
- URL: https://tailwindcss.com/plus/ui-kit ; docs: https://catalyst.tailwindui.com/docs
- Recency: Catalyst is paid/proprietary source; `tailwindlabs/headlessui` pushed_at `2026-04-13T16:12:31Z`, within 90 days; MIT for Headless UI.
- Top nav: docs + component kit; app layouts include sidebar and stacked layouts.
- Home/main layout: high-quality app UI kit with forms, tables, dialogs, navigation, layouts.
- Color strategy: restrained neutral, strong contrast, careful light/dark adjustments.
- Typography: Tailwind default sans strategy; exact demo font not treated as dependency.
- Dark/i18n/a11y: docs state keyboard accessible and screenreader-conscious; dark mode supported.
- HUAKAI 借鉴: Steal the engineering posture: own code, tight APIs, dense-but-readable tables.
### 22. Tremor
- URL: https://www.tremor.so/ ; docs: https://npm.tremor.so/
- Recency: GitHub `tremorlabs/tremor` pushed_at `2025-10-10T13:08:24Z`, outside 90 days, mark stale-but-stable; Apache-2.0.
- Top nav: component/docs/catalog focused on charts and dashboard blocks.
- Home/main layout: KPI cards, charts, trackers, bar lists, date/range filters.
- Color strategy: neutral dashboard palette with blue default and semantic chart colors.
- Typography: Tailwind dashboard scale: small labels, compact body, larger metrics.
- Dark/i18n/a11y: docs state accessible components; theming docs include light/dark token sets.
- HUAKAI 借鉴: Use Tremor-like primitives for KPI/charts only; keep navigation and tables in HUAKAI system.
### 23. Mantine
- URL: https://mantine.dev/ ; AppShell docs: https://mantine.dev/core/app-shell/
- Recency: GitHub `mantinedev/mantine` pushed_at `2026-05-11T16:49:46Z`, within 90 days; MIT.
- Top nav: docs/sidebar component library; AppShell supports header/nav/aside/footer layout.
- Home/main layout: component library rather than app; strong coverage for forms, shell, charts, data display.
- Color strategy: theme-driven neutral/brand system with light/dark built in.
- Typography: Mantine theme typography; exact app font left to implementer.
- Dark/i18n/a11y: official pages state dark theme support; help docs state WAI-ARIA/keyboard/focus testing posture.
- HUAKAI 借鉴: Keep AppShell concepts in mind, but shadcn+Tailwind gives HUAKAI more ownership and lower lock-in.
### 24. Park UI
- URL: https://park-ui.com/docs/introduction ; GitHub: https://github.com/chakra-ui/park-ui
- Recency: GitHub `chakra-ui/park-ui` pushed_at `2026-04-10T23:05:11Z`, within 90 days; MIT.
- Top nav: docs/sidebar component catalog across buttons, data display, disclosure, forms, layout, navigation, overlays, typography.
- Home/main layout: code-distribution component system; supports multiple JS frameworks through Ark UI/Panda CSS.
- Color strategy: clean neutral system with Radix Colors influence; good for design-token discipline.
- Typography: system-design typography components rather than fixed product font.
- Dark/i18n/a11y: docs emphasize accessible Ark UI foundations; i18n not confirmed.
- HUAKAI 借鉴: Park UI validates “open code + design tokens” direction, but HUAKAI should stay on Tailwind/shadcn for ecosystem depth.
### 25. Magic UI
- URL: https://magicui.design/docs/components ; GitHub: https://github.com/magicuidesign/magicui
- Recency: GitHub `magicuidesign/magicui` pushed_at `2026-05-11T04:21:55Z`, within 90 days; MIT.
- Top nav: animated components catalog; shadcn CLI install path.
- Home/main layout: visual effects, animated lists, cards, hero/video/terminal effects, not an ops dashboard system.
- Color strategy: high-motion, marketing-strong, often gradient/glow-heavy.
- Typography: demo-driven; not an ops typography source.
- Dark/i18n/a11y: theme toggler component exists; a11y of motion effects must be reviewed per component.
- HUAKAI 借鉴: Use sparingly for onboarding/empty states only; avoid animated decoration in core ops pages.
### 26. Aceternity UI
- URL: https://ui.aceternity.com/ai-recommendations ; docs: https://ui.aceternity.com/explore
- Recency: no canonical public repo pushed_at verified for the component catalog; official catalog page crawled within three weeks; mark recency as public-doc-current but source-recency-unknown.
- Top nav: component exploration and AI-readable catalog.
- Home/main layout: animated component/block marketplace; focus is cinematic hero/cards/backgrounds.
- Color strategy: brand-strong, motion-heavy, high-contrast dark visuals.
- Typography: demo-specific; not suitable as HUAKAI base typography.
- Dark/i18n/a11y: motion-heavy components require reduce-motion and contrast review before production use.
- HUAKAI 借鉴: Borrow only the idea of AI-readable component catalogs, not the aesthetic for admin ops.
## 2. HUAKAI 前端栈推荐
- Owner-requested target: Next.js 14 + Tailwind + shadcn/ui + Tremor or Recharts + TanStack Table + TanStack Query + Zustand + react-hook-form + zod.
- Important constraint: existing HUAKAI rule TS-002 currently locks frontend to TypeScript + React + Vite + TanStack + Tailwind.
- Recommendation wording: treat Next.js 14 as a product brief recommendation that requires Owner/DR confirmation before replacing Vite.
- Safe equivalent if DR-004 remains: React + Vite + Tailwind + shadcn/ui + TanStack stack gives 90% of the same UI outcome.
- Default component strategy: shadcn/ui as source-owned primitives; no opaque component framework lock-in.
- Charts: Recharts as first choice for custom charts; Tremor only for fast KPI/chart blocks if its API stays compatible.
- Tables: TanStack Table mandatory for logs/accounts/channels/models because column visibility, sorting, pagination, row selection, virtualization hooks matter.
- Server state: TanStack Query for API fetch/cache/retry/invalidation; avoid ad hoc `useEffect` fetches in tables.
- Local UI state: Zustand for sidebar collapse, command palette state, unsaved filters, live-log preference, assistant panel mode.
- Forms: react-hook-form + zod for API keys, provider accounts, quotas, route rules, plugin config, alert rules.
- Validation: zod schemas should be generated/aligned from OpenAPI where possible; hand-written schemas only for client-only form refinements.
- Routing: if Next.js approved, use App Router with route groups by ops domain; if Vite retained, use React Router with route-level lazy loading.
- Data contracts: OpenAPI remains source of truth; frontend types should be codegen, not manually reconstructed from examples.
- Icons: lucide-react default; do not invent custom SVG icons for common actions.
- Date/time: use dayjs or date-fns with explicit timezone display for logs/audit; relative time must show exact timestamp on hover.
- Virtualization: use TanStack Virtual or react-virtuoso for request logs, audit, usage, provider health events.
- Command palette: cmdk or shadcn command component for global action/search.
- Accessibility: Radix/shadcn base plus explicit keyboard tests for command palette, drawers, tables, destructive confirmations.
- Internationalization: keep i18n-ready string boundary even if first release is Chinese/English only.
- Theming: CSS variables + Tailwind tokens; no hard-coded chart colors outside central theme.
- Build risk: adding Next.js is a high-risk stack change under project rules; docs-only recommendation does not authorize implementation.
## 3. Design System 推荐
- Overall tone: dark ops console, not marketing site.
- Base background: slate/neutral dark, with slightly raised panels and clear borders.
- Primary hue: indigo/blue-indigo for selected navigation, primary buttons, query focus, active route.
- Neutral scale: neutral/slate gray for text, borders, table rows, panels, code blocks.
- Status colors: success green, warning amber, danger red, info cyan/blue.
- Severity mapping: errors and disabled provider accounts must use danger; degraded health uses warning; healthy uses success; unknown uses neutral/info.
- Typography primary: Inter or a metrically similar open sans.
- Typography mono: JetBrains Mono for API keys, trace IDs, request IDs, model IDs, JSON, logs, SQL/HQL.
- Type scale: compact; dashboard headings should be 18-24px, table text 12-14px, metric numerals 24-32px.
- Letter spacing: 0 by default; do not use negative tracking.
- Border radius: 6px default; 8px max for repeated cards/modals; avoid pill-heavy admin UI.
- Spacing: Tailwind default scale; dense tables use 8-12px cell padding.
- Layout density: primary pages should fit table + filters + summary row in first viewport on desktop.
- Card usage: use cards for repeated resource summaries and modals; do not wrap whole page sections in cards.
- Drawers: right drawer for log/request/account detail; left drawer for filters only when filters exceed one row.
- Top bar: tenant/workspace selector, global search, date range, environment, user/help.
- Sidebar: resource navigation with icon+label; collapsible to icons; selected state high contrast.
- Right assistant rail: optional on L5 layout; should be operational assistant tied to current resource.
- Charts: muted axes, strong tooltip contrast, no rainbow defaults; chart palette capped and semantic.
- Tables: sticky header, density toggle, column visibility, saved views, row detail drawer, bulk actions.
- Empty states: actionable and compact; no giant illustrations in ops pages.
- Loading states: skeleton rows/cards, not full-page spinners except auth/setup.
- Destructive actions: confirmation modal with typed resource name for high-risk ops; audit event preview.
- Secrets: redacted by default; reveal requires permission + audit reason.
- Mobile: readable inspection and emergency operations; full admin table editing can be desktop-first.
- a11y: focus rings visible in both themes; all icon buttons need labels/tooltips; command palette keyboard-only usable.
- i18n: all labels externalized; tables reserve width for Chinese and English; status text never color-only.
## 4. 5 个 Layout 候选
### L1 Helicone-like: sidebar + filter drawer + 主区 table/chart
```text
┌─────────────────────────────────────────────────────────────────────┐
│ Top: Workspace ▾  Env ▾  Search / Request ID           User ▾       │
├──────────────┬──────────────────────────────────────────────────────┤
│ Sidebar      │ Dashboard / Requests                                 │
│ Dashboard    │ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐                 │
│ Requests     │ │Cost  │ │Reqs  │ │Err % │ │P95   │                 │
│ Accounts     │ └──────┘ └──────┘ └──────┘ └──────┘                 │
│ Routing      │ ┌─────────────────────────────┐ ┌────────────────┐  │
│ Logs         │ │ latency/cost chart           │ │ filter drawer  │  │
│ Settings     │ └─────────────────────────────┘ │ status/model   │  │
│              │ ┌─────────────────────────────┐ │ tenant/key     │  │
│              │ │ request table                │ │ provider      │  │
│              │ │ row -> right detail drawer   │ └────────────────┘  │
└──────────────┴──────────────────────────────────────────────────────┘
```
- Strength: best fit for observability-first gateway.
- Risk: can become too table-heavy without saved views.
- HUAKAI use: default candidate for Logs, Usage, Observability.
### L2 Linear-like: 极简侧栏 + Cmd+K 中心
```text
┌─────────────────────────────────────────────────────────────────────┐
│ HUAKAI                         [Cmd+K Search actions...]    User    │
├──────────┬──────────────────────────────────────────────────────────┤
│ ▣ Dash   │ Page title                         View ▾  Filter ▾      │
│ ▣ Accts  │ ┌──────────────────────────────────────────────────────┐ │
│ ▣ Keys   │ │ grouped list/table or board                          │ │
│ ▣ Logs   │ │ keyboard-first row navigation                         │ │
│ ▣ Audit  │ │ selected row opens inline/right detail                │ │
│          │ └──────────────────────────────────────────────────────┘ │
│          │ Command palette handles: jump, create, disable, replay   │
└──────────┴──────────────────────────────────────────────────────────┘
```
- Strength: fast for expert operators.
- Risk: discoverability for new admins.
- HUAKAI use: good if command palette is treated as core, not shortcut garnish.
### L3 Vercel-like: 顶 nav + 卡片 grid
```text
┌─────────────────────────────────────────────────────────────────────┐
│ HUAKAI  Accounts  Usage  Routing  Logs  Plugins       Search  User  │
├─────────────────────────────────────────────────────────────────────┤
│ Scope: Tenant ▾       Add account +      Date range ▾               │
│ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ │
│ │ Account card │ │ Account card │ │ Provider     │ │ Route group  │ │
│ │ health/cost  │ │ rate/quota   │ │ health       │ │ status       │ │
│ └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘ │
│ Recent events / deployments / config changes                         │
└─────────────────────────────────────────────────────────────────────┘
```
- Strength: approachable overview and project/account selection.
- Risk: insufficient density for incident response.
- HUAKAI use: good for first Dashboard and account hub landing, not deep logs.
### L4 Stripe-like: 左侧深 nav + dense table + 详情 drawer
```text
┌──────────────┬──────────────────────────────────────────────────────┐
│ Dark nav     │ API Keys                                             │
│ Home         │ Search...  Status ▾  Owner ▾  Export  Create +       │
│ Accounts     │ ┌────────────────────────────────────────────────┐   │
│ API Keys     │ │ key alias | owner | scope | last used | spend   │   │
│ Usage        │ │ key alias | owner | scope | last used | spend   │   │
│ Billing      │ │ key alias | owner | scope | last used | spend   │   │
│ Logs         │ └────────────────────────────────────────────────┘   │
│ Settings     │                         ┌────────────────────────┐   │
│              │                         │ Detail drawer          │   │
│              │                         │ requests, audit, edit  │   │
└──────────────┴─────────────────────────┴────────────────────────┘   │
```
- Strength: strongest for billing, keys, customers, audit.
- Risk: feels heavy if every page is table-first.
- HUAKAI use: Accounts, API Keys, Quota, Audit, Billing-equivalent pages.
### L5 Mixed: 左 sidebar + 右 sidebar assistant chat for ops 操作
```text
┌─────────────────────────────────────────────────────────────────────┐
│ Top: scope/search/date/env                                          │
├──────────┬──────────────────────────────────────────┬──────────────┤
│ Left nav │ Main ops surface                         │ Assistant    │
│ Dash     │ ┌──── health row ────┐                   │ Context:     │
│ Accts    │ ┌───────────────────┐│                   │ selected log │
│ Routing  │ │ table/chart/logs  ││                   │              │
│ Logs     │ └───────────────────┘│                   │ Suggested:   │
│ Audit    │ detail drawer overlays main if needed     │ replay       │
│          │                                          │ disable key  │
│          │                                          │ create alert │
└──────────┴──────────────────────────────────────────┴──────────────┘
```
- Strength: differentiates HUAKAI with operator assistant tied to current context.
- Risk: assistant can hide core controls or encourage unsafe actions.
- HUAKAI use: candidate for Provider Health, Routing, Live Chat, incident/debug pages.
## 5. HUAKAI 必备页面列表（≥12）
### 1. Dashboard
```text
┌──────────────────────────────────────────────────────┐
│ KPI: cost | requests | errors | p95 | active tenants  │
├──────────────────────┬───────────────────────────────┤
│ Cost/latency chart    │ Provider health summary       │
├──────────────────────┴───────────────────────────────┤
│ Recent incidents / top routes / quota warnings        │
└──────────────────────────────────────────────────────┘
```
- Controls: date range, tenant scope, environment, refresh, saved dashboard.
- Must show: cost, requests, error rate, latency, provider/account health.
- Pattern: L1/L3 hybrid.
### 2. Accounts
```text
┌──────────────────────────────────────────────────────┐
│ Accounts  Search  Provider ▾ Status ▾ Owner ▾ Add +   │
├──────────────────────────────────────────────────────┤
│ account | provider | health | quota | spend | owner   │
│ row click -> right drawer                             │
└──────────────────────────────────────────────────────┘
```
- Controls: TanStack table, filters, column visibility, bulk tag, add/edit drawer.
- Must show: owner, provider, account status, last activity, limits, risk badges.
- Safety: secrets redacted; disable requires confirmation and audit reason.
### 3. API Keys
```text
┌──────────────────────────────────────────────────────┐
│ API Keys  Search  Scope ▾ Last used ▾ Create key +    │
├──────────────────────────────────────────────────────┤
│ alias | tenant | scope | models | last used | spend   │
│ drawer: reveal-once, rotate, linked logs, audit       │
└──────────────────────────────────────────────────────┘
```
- Controls: create modal, scope picker, model access multi-select, rotate/revoke.
- Must show: permission scope, last used, request count, spend, anomaly status.
- Borrowing: Resend/Stripe resource table with linked logs.
### 4. Usage
```text
┌──────────────────────────────────────────────────────┐
│ Usage  Tenant ▾ Key ▾ Model ▾ Date range ▾ Export     │
├──────────────────────┬───────────────────────────────┤
│ token/cost charts     │ breakdown by provider/model   │
├──────────────────────┴───────────────────────────────┤
│ usage records table                                  │
└──────────────────────────────────────────────────────┘
```
- Controls: date range, group by, export, compare period, saved view.
- Must show: prompt/completion/total tokens, cost, cache hit, provider charge.
- Pattern: Grafana/Tremor chart discipline plus Stripe export workflow.
### 5. Quota
```text
┌──────────────────────────────────────────────────────┐
│ Quota policies  Tenant ▾ Status ▾ Create policy +     │
├──────────────────────────────────────────────────────┤
│ policy | subject | window | limit | used | action     │
│ drawer: thresholds, grace, alerts, audit preview      │
└──────────────────────────────────────────────────────┘
```
- Controls: policy builder, warning/danger thresholds, dry-run preview.
- Must show: quota window, remaining amount, enforcement mode, next reset.
- Safety: changes affect enforcement; mark implementation as high-risk later.
### 6. Channels
```text
┌──────────────────────────────────────────────────────┐
│ Channels  Provider ▾ Region ▾ Status ▾ Test selected  │
├──────────────────────────────────────────────────────┤
│ channel | provider | account pool | health | latency  │
│ drawer: credentials status, models, route bindings    │
└──────────────────────────────────────────────────────┘
```
- Controls: health test, model sync, tag filter, batch enable/disable.
- Must show: account pool relationship, failover readiness, last probe.
- Borrowing: new-api/one-api breadth, modernized into safer drawers.
### 7. Models
```text
┌──────────────────────────────────────────────────────┐
│ Models  Provider ▾ Capability ▾ Price known ▾ Sync    │
├──────────────────────────────────────────────────────┤
│ model | provider | modality | context | price | route │
│ drawer: aliases, pricing, capability payload          │
└──────────────────────────────────────────────────────┘
```
- Controls: capability filters, alias editor, provider sync, price override.
- Must show: model capability, pricing confidence, route availability.
- Pattern: table with compact badges; no decorative model cards.
### 8. Routing
```text
┌──────────────────────────────────────────────────────┐
│ Routing  Route group ▾ Simulate request               │
├──────────────────────┬───────────────────────────────┤
│ route rules table     │ route graph / fallback chain  │
├──────────────────────┴───────────────────────────────┤
│ simulation result: chosen account, fallback, cost     │
└──────────────────────────────────────────────────────┘
```
- Controls: rule editor, drag order only if backed by explicit priority, simulate, dry-run.
- Must show: fallback chain, cache/retry policy, safety gates, owner.
- Borrowing: Portkey status clarity + Braintrust compare experiments.
### 9. Plugins
```text
┌──────────────────────────────────────────────────────┐
│ Plugins  Search  Category ▾ Enabled ▾                 │
├──────────────────────────────────────────────────────┤
│ plugin | category | state | version | permissions     │
│ drawer: config, events, audit, rollback               │
└──────────────────────────────────────────────────────┘
```
- Controls: enable/disable, config form, permission review, test hook.
- Must show: plugin state, required permissions, last run, failure count.
- Safety: plugin enablement should preview permissions and audit impact.
### 10. Logs
```text
┌──────────────────────────────────────────────────────┐
│ Logs  Query... Status ▾ Model ▾ Route ▾ Date ▾        │
├──────────────────────────────────────────────────────┤
│ time | status | model | key | route | cost | latency  │
│ row -> right drawer: request, response, trace, replay │
└──────────────────────────────────────────────────────┘
```
- Controls: query bar, filters, column picker, live tail, export, replay.
- Must show: request ID, tenant, key, model, route, provider account, cost, latency.
- Borrowing: Portkey right drawer + Helicone request-centric nav.
### 11. Live Chat
```text
┌──────────────────────────────────────────────────────┐
│ Live Chat / Playground  Model ▾ Route ▾ Key ▾         │
├──────────────────────┬───────────────────────────────┤
│ conversation panel    │ params / headers / trace      │
│ streaming output      │ generated curl / SDK snippets │
└──────────────────────┴───────────────────────────────┘
```
- Controls: model picker, route selector, parameter sliders, prompt variables.
- Must show: streaming tokens, cost estimate, chosen provider/account, trace link.
- Safety: playground requests must be tagged and separable from production traffic.
### 12. Observability
```text
┌──────────────────────────────────────────────────────┐
│ Observability  Dashboard | Explore | Traces | Issues  │
├──────────────────────┬───────────────────────────────┤
│ charts / clusters     │ trace or issue detail         │
├──────────────────────┴───────────────────────────────┤
│ saved investigations / alerts                         │
└──────────────────────────────────────────────────────┘
```
- Controls: dashboard tabs, query editor, issue grouping, save as alert/widget.
- Must show: repeated failures, clusters, provider incidents, route regressions.
- Borrowing: Grafana Explore + Sentry Issues + Datadog clusters.
### 13. Audit
```text
┌──────────────────────────────────────────────────────┐
│ Audit  Actor ▾ Resource ▾ Action ▾ Date ▾ Export      │
├──────────────────────────────────────────────────────┤
│ time | actor | action | resource | before/after       │
│ drawer: diff, request context, approval chain         │
└──────────────────────────────────────────────────────┘
```
- Controls: immutable filters, export, diff viewer, link to affected resource.
- Must show: actor, tenant, action, resource, reason, before/after, request ID.
- Safety: every dangerous UI action should point here after completion.
### 14. Settings
```text
┌──────────────────────────────────────────────────────┐
│ Settings  Search settings...                         │
├──────────────┬───────────────────────────────────────┤
│ Org          │ selected settings form                 │
│ Members      │ validation summary                     │
│ Security     │ save / discard / audit preview         │
│ Billing      │                                       │
└──────────────┴───────────────────────────────────────┘
```
- Controls: settings search, tabs/sections, forms with zod validation.
- Must show: unsaved state, permission requirements, audit preview for risky changes.
- Pattern: all-api-hub settings search idea, but denser and less rounded.
### 15. Provider Health
```text
┌──────────────────────────────────────────────────────┐
│ Provider Health  Provider ▾ Region ▾ Auto-refresh     │
├──────────────────────┬───────────────────────────────┤
│ health matrix         │ incident / probe detail       │
├──────────────────────┴───────────────────────────────┤
│ probes table: latency, errors, quota, last success    │
└──────────────────────────────────────────────────────┘
```
- Controls: auto-refresh, probe now, mute alert, incident timeline.
- Must show: provider/account health, probe status, fallback readiness, affected routes.
- Borrowing: Cloudflare scope clarity + Sentry issue triage.
### 16. Alerts / Reports
```text
┌──────────────────────────────────────────────────────┐
│ Alerts  Metric ▾ Scope ▾ Status ▾ Create alert +      │
├──────────────────────────────────────────────────────┤
│ alert | metric | threshold | scope | channel | state  │
│ drawer: history, recipients, test notification        │
└──────────────────────────────────────────────────────┘
```
- Controls: threshold builder, recipient picker, Slack/email/webhook, test.
- Must show: last trigger, current value, muted status, owner.
- Borrowing: Helicone reports/alerts pattern, but with HUAKAI audit hooks.
## Open Questions
- OQ-1: 是否正式重开 DR-004，把 Vite 改成 Next.js 14；当前 brief 不能替代 Owner/DR 决策。
- OQ-2: HUAKAI 首发是否需要中英双语，还是仅保持 i18n-ready 边界。
- OQ-3: Live Chat/Playground 是否允许真实 provider account 调用，还是先用 sandbox/mock。
- OQ-4: Operator assistant 是否可以执行危险动作，还是只生成建议和 deep links。
- OQ-5: 是否需要把 Observability 的 query language 作为 Day 1 功能，还是先做 filters + saved views。
- OQ-6: 是否引入 Tremor 运行时依赖，还是只用 Recharts + shadcn chart wrapper。
## Clean-room Tail
- Source files read: `new-api/web/classic/package.json`; `new-api/web/classic/src/App.jsx`; `new-api/web/classic/src/components/layout/SiderBar.jsx`; `new-api/web/classic/src/components/layout/headerbar/index.jsx`.
- Source files read: `one-api/web/air/package.json`; `one-api/web/air/src/App.js`; `one-api/web/air/src/components/SiderBar.js`; `one-api/web/air/src/components/HeaderBar.js`.
- Source files read: `all-api-hub/package.json`; `all-api-hub/src/entrypoints/options/App.tsx`; `all-api-hub/src/entrypoints/options/components/Sidebar.tsx`; `all-api-hub/src/components/AppLayout.tsx`.
- Lane: specifier / frontend research.
- Agent: GPT-5 Codex, local Codex lane.
- UTC timestamp: 2026-05-12T01:58:51Z.
- Chinese summary: 本文件的真实观察来自公开官网/文档、GitHub 元数据和三个 UI 仓库的浅层 shell/navigation/package 区域；合理推断主要集中在颜色策略、typography 倾向和 HUAKAI layout 借鉴；open question 共 6 个。未复制任何参考项目代码或美术，非 MIT/AGPL 项目只作为 specifier 观察，不进入实现路径。
