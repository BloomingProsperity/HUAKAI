你是 HUAKAI 前端 Gemini，第 3 轮修补。

Round 2 你交付后，sonnet + codex 双 reviewer 拍板 = REQUEST_CHANGES。还有 4 件 P0/MED/LOW 残留要修。

参考 review 文档：
- docs/research/2026-05-12-gemini-p1-round2-review-sonnet.md
- docs/research/2026-05-12-gemini-p1-round2-review-codex.md

只修以下 4 件，**不要碰其他东西**：

## Fix 1（P0-3 残留）：page.tsx 11 处英文 JSX 注释翻中文

codex 列出的 11 行（精确）：
- frontend/app/dashboard/page.tsx 行 46, 49, 52, 54, 67, 76, 89, 98, 107, 119, 122

把这 11 行（以及其它 `{/* English */}` 类 outline 注释）都翻成中文。保留 JSX 注释语法 `{/* */}`。

## Fix 2（P0-6 残留）：page.tsx fallback banner inline style 移到 CSS module

sonnet 报：page.tsx 的 "Backend unreachable" banner 用了 inline `style={{...}}`，与项目"无 inline style"约束冲突。

修改：
- 在 dashboard.module.css 加一个 `.fallbackBanner` class
- page.tsx 把 banner 改用 `<div className={styles.fallbackBanner}>`
- 视觉保持不变（左侧 4px accent 条 / 无背景色 / 文字告知）

## Fix 3（MED-A）：page.tsx fetch 写死 URL → 相对路径走 rewrites

sonnet 报：page.tsx:18-20 的 fetch 用了 `http://localhost:8080/...` 硬编码，绕过 next.config.mjs 的 rewrites，生产部署会断。

修改：所有 fetch URL 改用相对路径：
- `/admin/v1/usage` (不带 host)
- `/admin/v1/provider-accounts?limit=5`
- `/debug/vars`

next.config.mjs 的 rewrites 在 dev / prod 都会自动转发，不需要 `http://localhost:8080` 前缀。

## Fix 4（LOW-B）：状态 dot 加几何区分

sonnet 报：5 种状态 dot 现在只靠颜色区分（healthy / degraded / failed / quota_exhausted / cooling_down）。色盲不友好。

修改：每种状态用**颜色 + 形状** 双信号：
- healthy → 实心圆 ●
- degraded → 实心三角 ▲
- failed → 实心方块 ■
- quota_exhausted → 空心圆 ○
- cooling_down → 实心菱形 ◆

这些都是 Unicode 几何字符，**不是 emoji**（重要：不要用 ✅❌⚠️ 这类）。文字标签保留（双信号：形状 + 文字）。

## 必须做的最终验证

- `cd /home/codex/HUAKAI/frontend && npm run type-check < /dev/null 2>&1 | tail -10` 报告 0 error
- `npm run build < /dev/null 2>&1 | tail -20` 报告 build 通过
- `grep -P "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]" frontend/app/dashboard/ -r 2>&1` 报告无 emoji 命中
- `grep -P "style=\{" frontend/app/dashboard/page.tsx` 报告 0 命中（inline style 收尾）
- `grep -P "http://localhost" frontend/app/dashboard/page.tsx` 报告 0 命中（硬编码 URL 收尾）

## 回报模板

```
Round 3 - Vertex AI gemini-2.5-pro

Files changed:
- frontend/app/dashboard/page.tsx (XXX → YYY LoC)
- frontend/app/dashboard/dashboard.module.css (XXX → YYY LoC)
- (其它如有)

Fixes:
- Fix 1 (P0-3 残留): [N 行注释中文化]
- Fix 2 (P0-6 残留): [.fallbackBanner moved to CSS]
- Fix 3 (MED-A): [3 fetch URL 改相对路径]
- Fix 4 (LOW-B): [5 状态 dot 加几何字符]

Verifications:
- type-check: PASS/FAIL
- build: PASS/FAIL
- emoji scan: 0 hits
- inline style scan: 0 hits in page.tsx
- localhost hardcode scan: 0 hits in page.tsx

Outstanding: (本轮不修的)
```

约束：
- 仅改 page.tsx + dashboard.module.css，其他不动
- 不引新 npm 包
- 不改 next.config.mjs
- 中文注释 / 英文标识符

## Round 3 补充：Owner 看到的渲染问题（2026-05-12）

Owner 浏览器打开 http://localhost:3000/dashboard 时反馈：
- 看到的是 "Backend unreachable, showing fallback empty state" banner（说明 backendFailed = true 路径触发）
- 整页看上去没有暗色主题应用、上方 layout.tsx 全局 nav 显示

根因分析：HUAKAI 后端 admin/v1/* 路由当前是 P2-P5 阶段的占位，**实际 endpoint 还没就绪**，所以非 mock 路径必然 fetch 失败 → 触发 fallback banner → Owner 看不到完整设计。

请在 Round 3 顺手做：

### Fix 5（dev UX）：调整 useMock 默认值
将 `process.env.NEXT_PUBLIC_USE_MOCK === '1'` 改为 `process.env.NEXT_PUBLIC_USE_MOCK !== '0'`（默认走 mock，仅当显式 `='0'` 才尝试真后端）。注释里清楚说明 HUAKAI 后端 P2-P5 阶段尚未就绪，dev 期间默认 mock 让 dashboard 设计可独立呈现。

### Fix 6（视觉应用确认）：确保 dashboard 在 layout.tsx 全局 chrome 之下也能正确呈现 HUAKAI 工业控制台风格
- 全局 globals.css 已有 body `background: #0d1117` 暗色底，dashboard 应能直接 inherit
- 如果 dashboardContainer 内部需要补 explicit `background` 或 padding 调整确保视觉一致，请加
- 顶部 layout.tsx 的 `<header>` 全局 nav 不要动（那是其他人维护的全局 chrome），但你可以在 dashboard 内部加一个 `<h2>` 或 section title 让视觉有清晰隔断
- 整页应有清晰的工业控制台 vibe（暗色 + 边框分层 + 数据密度 + 几何符号状态），不要混杂浅色或卖萌元素
