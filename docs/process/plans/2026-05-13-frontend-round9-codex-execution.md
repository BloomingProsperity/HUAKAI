# 2026-05-13 Frontend Round 9 Codex Execution

| Owner directive | "你是 HUAKAI 项目前端 owner... read 后执行... 不要问 Owner，按你判断走。" |
| Scope | In: `frontend/` P1 Dashboard shell, Tailwind/shadcn styling path, dashboard page/components, verification report in `/tmp`. Out: backend logic, auth, billing, quota enforcement, database schema, production deploy, `LICENSE`. |
| Success criteria | `/dashboard` renders one sidebar + one main content surface, Tailwind utilities visibly apply, P1 dashboard shows Chinese status header, 6 metrics, trend chart, provider table, and alert area; `npm run type-check`, `npm run build`, and HTTP 200 check pass. |
| Time estimate | 1-2 hours wall clock; one Codex implementation/verification pass. |
| Blast radius | Medium frontend-only risk: root layout affects all Next routes; dashboard route/component edits affect P1 view. No backend or data persistence risk. |
| Failure modes | Tailwind v4/shadcn mismatch keeps CSS from compiling; duplicate route layouts persist; chart container still resolves to zero/negative size; strict TS catches recharts or lucide typing drift; existing unrelated frontend pages rely on global layout assumptions. Mitigation: verify logs, simplify app shell, use stable Tailwind entrypoint, keep chart height explicit, run type/build/curl checks. |
| Decision points | If Tailwind v4 remains broken after minimal fixes, downgrade to standard Tailwind v3 only if needed; this changes dev dependencies and must be recorded. No high-risk files should be touched. |
| Pre-execution checklist | Read `docs/RULES.md`; read Round 9 brief; read current `frontend/app`, `frontend/components`, `frontend/lib`, Tailwind/PostCSS/shadcn configs; read Sub2API decomposition sections 1, 2, 7, 8, 9, 10; run requested dev-server smoke command; diagnose before editing. |
| Concrete execution order | 1. Capture current failure from Next dev/log/HTML. 2. Fix style pipeline and duplicate layout. 3. Rebuild dashboard with React/shadcn/lucide/recharts, Chinese UI, no AI emoji/chatbot icon. 4. Verify type-check, build, HTTP 200, HTML/CSS evidence, and visual/smoke check. 5. Write `/tmp/codex-frontend-round9.txt`. |
