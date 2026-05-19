你是 HUAKAI 项目签约的前端工程师 Gemini（本轮升级到 gemini-3-pro-preview）。

第一轮 P1 Dashboard 你已交付（文件在 /home/codex/HUAKAI/frontend/app/dashboard/ 与 frontend/lib/dashboard-mock.ts），HUAKAI 双 reviewer（codex 合规 lane + sonnet UX lane）做了 review，合并 verdict = REQUEST_CHANGES。本轮你只需要修以下清单，不重写整页。

完整 review 文档：
- docs/research/2026-05-12-gemini-p1-review-codex.md
- docs/research/2026-05-12-gemini-p1-review-sonnet.md

合并的 P0（必修）清单（修完才能交 Owner 看）：

## P0-1：实装 NEXT_PUBLIC_USE_MOCK 切换
**位置**：frontend/app/dashboard/page.tsx
**现状**：直接 import `MOCK_USAGE / MOCK_PROVIDER_ACCOUNTS / MOCK_CHART_DATA` 当数据，env flag 无效。
**期望**：
- 在 server component 内读 `process.env.NEXT_PUBLIC_USE_MOCK === '1'`
- mock 分支返回当前常量
- 非 mock 分支调真实 backend `/admin/v1/usage` + `/admin/v1/provider-accounts?limit=5` + `/debug/vars`（用原生 fetch）
- 非 mock 分支若 fetch 失败 → 返回受控 empty state（页面顶部加灰色横条 "Backend unreachable, showing fallback empty state"），**不要静默继续 mock**

## P0-2：StatusBar 心跳改真实 ping
**位置**：frontend/app/dashboard/components/StatusBar.tsx:8,13
**现状**：`Math.random()` 生成假延迟，永远显示假"OK 24ms"，即使后端已挂。
**期望**：
- 每 5 秒 fetch `/debug/vars`（用 next.config.mjs rewrites 自动转 :8080），记录 round-trip 毫秒
- 显示真延迟数字
- 后端 unreachable 时显示 "backend offline" + dot 颜色变红
- useEffect cleanup setInterval

## P0-3：所有注释翻成中文
**位置**：所有 .tsx / .css 文件（StatusBar.tsx:12, MiniTrend.tsx:1-3, dashboard.module.css headers, dashboard-mock.ts:1-4 等）
**HUAKAI 规则**：仓库内新写注释一律中文，标识符 / enum 字面值保持英文。
**期望**：所有英文注释翻成中文，保留语义。

## P0-4：4 个导航 link 修正
**位置**：page.tsx 底部"Bottom Navigation Hints"
**现状**：P2 + P3 都指向 `/accounts`（错），P4 / P5 路径也需要核对。
**期望**：
- P2 → `/api-keys`
- P3 → `/provider-accounts`
- P4 → `/pools`
- P5 → `/usage`
- 这些路由暂不存在，是占位链接，给 anchor 链 + `aria-disabled="true"` + cursor not-allowed

## P0-5：AlertBar 列具体异常账号
**位置**：frontend/app/dashboard/components/AlertBar.tsx
**现状**：只显示 "1 FAILED, 2 DEGRADED ACCOUNTS DETECTED"，没具体账号名。
**期望**：
- 从 props 接收 accounts 列表，派生 degraded/failed 子集
- 显示前 3 条："{account_name} ({health_state})" 用逗号分隔
- 超过 3 条加 "+ N more"
- 整行点击跳 `/provider-accounts` 路由（aria-disabled 同 P0-4）

## P0-6（合并 sonnet a11y）：基本 a11y 改造
**位置**：page.tsx + components/*.tsx + dashboard.module.css

a11y 改造点：
1. **heading 层级**：page.tsx 加一个 `<h1>HUAKAI Dashboard</h1>` (sr-only 视觉隐藏可，但 DOM 必须有)。MetricBlock 内子标题用 `<h2>` 或 `<h3>`。
2. **表格 a11y**：ProviderTable 加 `<caption>Top 5 Provider Accounts</caption>` + 表头 `<th scope="col">`，行头 `<th scope="row">`。
3. **状态双信号**：状态 dot 不能只靠颜色。每个 dot 旁边必须有文字（"healthy"/"degraded"/"failed"），且文字与 dot 在 DOM 内连续（同 `<span>` 包裹），方便 screen reader 一次读出。
4. **数字对齐**：所有数字字段（sub-value、in_flt/cap、cache hit %、latency ms 等）用 monospace 字体 + `text-align: right`，确保位数对齐。CSS 选择器加 `.numeric { font-variant-numeric: tabular-nums; }`。
5. **focus-visible**：全局 `:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }`，nav link / 按钮 / table row 必须有键盘焦点态。
6. **WCAG AA 对比**：sub-value 文字色（之前 sonnet 量了 #484f58 on #0d1117 ≈ 3.0:1，不达标）改成 ≥ 4.5:1 的灰阶值。
7. **inline style 移走**：所有内联 style（如 sonnet 提到的 quota color）移到 dashboard.module.css 用 className 控制。
8. **跳过外链 emoji 渲染**：双确认无 emoji 字符。

## 实施纪律

- **不允许新增 npm 依赖**
- **不允许修改 next.config.mjs**
- **不允许动 frontend/app/page.tsx**（那是 P11 LiveChat 测试 wedge 的现状文件）
- **必须做的最终验证**（你自己在你的 turn 末尾跑）：
  - `cd /home/codex/HUAKAI/frontend && npm run type-check < /dev/null 2>&1 | tail -10` 报告 0 error
  - `npm run build < /dev/null 2>&1 | tail -20` 报告 build 通过
  - `grep -P "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]" frontend/app/dashboard/ -r 2>&1` 报告无 emoji 命中
  - `grep -iE "linear-gradient|radial-gradient|backdrop-filter" frontend/app/dashboard/dashboard.module.css` 报告无命中
- 文件 LoC 上限：page ≤ 350，组件 ≤ 200。

## 回报模板（按这格式回）

```
Round 2 - Gemini 3 Pro Preview

Files changed:
- frontend/app/dashboard/page.tsx (XXX → YYY LoC)
- ...

P0 fixes:
- P0-1 mock toggle: [implemented / how]
- P0-2 real ping: [implemented / how]
- P0-3 中文注释: [translated N comments]
- P0-4 nav links: [4 routes corrected]
- P0-5 alert bar names: [implemented]
- P0-6 a11y: [list items addressed]

Verifications:
- type-check: PASS/FAIL
- build: PASS/FAIL
- emoji scan: 0 hits
- CSS禁用手段: 0 hits
- LoC compliance: PASS/FAIL

Outstanding:
- [P1 items skipped for round 3]
```

直接做，不要询问澄清。如有歧义按你判断走，在 Outstanding 里说明。
