# HUAKAI 控制台 — UI kit

A high-fidelity, click-through recreation of the HUAKAI operations console (the
admin/ops surface of the self-hosted AI Gateway). Recreated from the project's
own Next.js frontend (`frontend/app/*`, `frontend/components/*`).

## Run
Open `index.html`. It loads React + Babel + the design-system bundle (`_ds_bundle.js`)
+ Lucide icons, then mounts the app.

## Navigation (two-portal IA)
`nav.js` defines the full internal information architecture as two portals, switched
via the segmented control under the logo:

- **运营台 (ops)** — 运营总览 · 账号池 · 路由与分组 · 用户与租户 · 模型与定价 · 计费运营 ·
  用量分析 · 内容运营 · 风控 · 监控告警 · 审计 · 系统维护 (each a collapsible group of sub-pages).
- **用户门户 (user)** — 概览 · API Key · 用量 · 模型与渠道 · 钱包与订单 · 邀请返利 · 账户.

The sidebar is a grouped, collapsible tree (group headers expand/collapse; the active
group auto-expands); the header shows a portal › group › page breadcrumb.

## Surfaces
Three IA nodes route to fully-built views; every other node renders a candid
`Placeholder` (`占位` badge, "不会用本地假数据补齐") so the whole structure is navigable.
- **运营总览 / Dashboard** (`Dashboard.jsx`) — KPI stat grid, 24h cache-hit trend, Top-5
  account table, alert + health-ratio panels. The header "刷新" spins on click.
- **账号池 › 上游账号 / Accounts** (`Accounts.jsx`) — provider account list with health-filter
  chips, search field, and a "新增账号" action.
- **用户门户 › API Key › Playground / Chat** (`Chat.jsx`) — gateway prompt debugger with a faked SSE stream.

## Chrome
`Shell.jsx` = fixed light sidebar (HK monogram, portal switch, grouped nav tree, status
footer) + sticky backdrop-blur header (breadcrumb, backend heartbeat chip, admin avatar).

## Theme
The console renders a **clean light data-admin theme** (sky-tinted neutral background,
white cards, crisp hairlines, brand **teal** accent) via a scoped `:root` override in
`index.html` — the design-system tokens themselves are untouched. (Earlier this kit was
dark; switched to light per request. Easy to revert by restoring `class="dark"` and
removing the override.)

## Composition
Built entirely from the design-system primitives via
`window.HUAKAIDesignSystem_36f9be` — `Card`, `StatCard`, `Badge`, `Button`,
`Input`, `Label`, `StatusDot`, `Table`/`TR`/`TD`. Icons are Lucide (`icons.jsx`).
Mock data lives in `data.js`; navigation IA in `nav.js`. Nothing here talks to a real backend.

> Most IA nodes are navigable placeholders — the three views above are fully built;
> the rest are reserved for pages/endpoints not yet implemented in the real frontend.
