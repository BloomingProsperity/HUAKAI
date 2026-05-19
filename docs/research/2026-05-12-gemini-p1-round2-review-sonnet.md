# Gemini P1 Dashboard 原型 — Round 2 Sonnet UX/A11y Read-Only Verify

- 评审人：Claude Sonnet (frontend UX reviewer lane, Round 2)
- 评审日期：2026-05-12 (UTC)
- 评审范围：仅 read-only 验真。审 round 1 共 11 个 finding（6 P0 + 8 P1，部分合并）是否真改到位 + 无回归。
- 对照文档：
  - Round 1 review：`docs/research/2026-05-12-gemini-p1-review-sonnet.md`
  - Round 2 prompt：`docs/process/plans/2026-05-12-gemini-p1-round2-prompt.md`
- 平行 reviewer：codex lane（独立 compliance verify，预期 cross-discuss）
- 严格遵守：read-only，未修改任何前端文件。

---

## 1. Verdict

**APPROVE_WITH_MINOR_CHANGES**

理由概述：所有 round 1 的 P0 阻塞缺陷（mock toggle / 真心跳 / 中文注释 / nav links / AlertBar 账号名 / a11y 主体改造）均已实质落地；不再存在"Owner 一打开就看出造假"的现状。仍有 **1 个新 MED（NEXT_PUBLIC_USE_MOCK 路径里 fetch 写死 absolute URL 而非依赖 next.config.mjs rewrites）** 与 **1 个 LOW 回归（fallback empty state 仍用 inline style）** + **3 个 P1 未完工 / 部分 regress 项**，但都不到 REQUEST_CHANGES 程度，可在 round 3 polish 时一并处理，或 Owner 同意直接进 P2 真后端阶段。

不阻塞 Owner 演示与下一 slice 推进。

---

## 2. Round 1 P0 / P1 Closeout 表

| ID | Round 1 finding | Round 2 状态 | 证据 | 备注 |
|----|-----------------|-----------|------|------|
| **P0-1** | NEXT_PUBLIC_USE_MOCK 切换未实现 | **closed (with MED note)** | `page.tsx:9-31` server async `DashboardPage`，`process.env.NEXT_PUBLIC_USE_MOCK === '1'` 判断；非 mock 分支 `Promise.all` 三 endpoint 原生 fetch；`backendFailed=true` 时显示灰色 banner `Backend unreachable, showing fallback empty state` | MED-A：非 mock fetch 用 `http://localhost:8080/...` 写死，绕过 `next.config.mjs` 的 rewrites（rewrites 是 dev/SSR-edge proxy 设计）。server-component 端 SSR 时 `localhost:8080` 在容器化部署里会断；prod 应改 `process.env.BACKEND_BASE_URL` 或在 SSR 走相对路径 + 真 reverse proxy。codex MED-1 强调"严禁静默 fallback to mock"已遵守：失败显式 banner，不静默回 mock。 |
| **P0-2** | StatusBar `Math.random()` 假心跳 | **closed** | `StatusBar.tsx:6-34` — `'use client'` directive；`Date.now()` 差值算 round-trip ms；`fetch('/debug/vars')`（走 next.config rewrites）；`!res.ok` 抛 → `setOffline(true)`；catch 同样置 offline；setInterval 5s + `useEffect` cleanup 双 clearInterval 都正确。offline 时 dot 走 `statusFailed` 红 + 文字改成 `backend offline`；非 offline 才显示 `{latency}ms` | 行为现在与真实后端绑定。注：5s tick 是合理的；如果后端真挂了首次 fetch 也是 catch → 立即红 dot，无 5s lag。 |
| **P0-3** | 全部注释英文 | **closed** | `grep -rnP "//\s*[A-Z][a-zA-Z]"` 返回空；`grep -rnP "/\*\s*[A-Z][a-zA-Z]"` 仅命中 `page.tsx` 内 6 处 JSX 注释 `{/* Metric N: ... */}` 与 css line 1 `HUAKAI 工业级仪表盘样式`（line 1 是中文，命中是因为 `HUAKAI` token 大写）。`StatusBar.tsx:14` 中文 `每 5 秒请求真实心跳`；`MiniTrend.tsx:1-3` 中文 block；`dashboard.module.css` 全部 section header 已中文化；`dashboard-mock.ts:1-4` 中文 block。 | LOW-A：`page.tsx:54,67,76,89,98,107` 仍存留 6 行 `{/* Metric 1: Tokens */}` 风格英文 JSX 注释（属于代码 outline），feedback_chinese_comments 严格意义上也应中文化。但比 round 1 大幅改善（从全英 → 仅 6 行非语义占位）。 |
| **P0-4** | 4 个 nav links 路径错 | **closed** | `page.tsx:124-127` — P2→`/api-keys`，P3→`/provider-accounts`，P4→`/pools`，P5→`/usage`；全部带 `aria-disabled="true"` + `tabIndex={-1}` + `disabledLink` class；`dashboard.module.css:212-216` `.disabledLink { cursor: not-allowed; pointer-events: none; opacity: 0.5 }` | `pointer-events: none` 会让 disabled link 整个不可点击（包括 aria-disabled 的 reader 提示），路径全对。 |
| **P0-5** | AlertBar 列具体账号 | **closed** | `AlertBar.tsx:9-32` — filter degraded/failed；前 3 条 `{name} ({health_state})` 用逗号 join；剩余 `+ N more`；行 wrapper `tabIndex={0} role="alert"`；右侧 `<a href="/provider-accounts">查看账号</a>` 跳 P3 route | 满足 spec。注：`aria-disabled="true"` + `tabIndex={-1}` + `disabledLink` 在右侧链接上，但 round 2 prompt 也允许这种"占位"。仅 P0-5 spec 说"整行点击跳"，目前实现是右侧 link 跳；wrapper `div` 自己不是 `<a>`。轻微偏离 spec，不计入阻塞。 |
| **P1-1** | 状态信号仅靠颜色 | **partial close** | `ProviderTable.tsx:30-38` — `<span className={styles.statusWrapper}>` 内 dot + 文字串在同一 wrapper（screen reader 一次读出）；`StatusBar.tsx:39-42` 同样 wrapper + `srOnly` 状态词 + 显著文字 `HEARTBEAT OK / backend offline` | LOW-B：未做 round 1 推荐的"形态差"（dot 半实心环 / outline）和 `failed` 文字 `font-weight: 600` 加重；双信号文字 + 颜色到位，但形态单一意味着黑白打印 / 屏幕变灰下 operational/degraded/failed 仍同形。可 round 3。 |
| **P1-2** | 表格无 `<caption>` / 列无 `scope` | **closed (with note)** | `ProviderTable.tsx:13-23` — `<caption className={styles.srOnly}>Top 5 Provider Accounts</caption>`；6 列 `<th scope="col">`；每行第一格 `<th scope="row">` (line 27) 行头语义对 | NOTE：`tableHeader` div `Top 5 供应商账号` 仍作为视觉 header 并存，caption 改成 sr-only，结构稍冗余但 a11y 上是合规的（caption 是 AT 用，div 是 sighted 用）。可以接受。 |
| **P1-3** | 缺 `<h1>` / heading 层级断裂 | **closed** | `page.tsx:45` `<h1 className={styles.srOnly}>HUAKAI Dashboard</h1>`；`.srOnly` 在 css line 18-28 标准 WCAG 视觉隐藏；`MetricBlock.tsx:14` `<h2 className={styles.metricLabel}>{label}</h2>` 全 6 个 metric block 自动升级；`AlertBar.tsx:24-28` `role="alert"`（缺 `aria-live="polite"` 但 `role="alert"` 隐含 assertive，强度更高） | round 1 推荐的 `aria-live` 用 implicit `role="alert"` 替代，从 a11y 实践看 OK。 |
| **P1-4** | 链接键盘焦点态缺失 | **closed** | `dashboard.module.css:11-15` `.dashboardContainer :focus-visible { outline: 2px solid #58a6ff; outline-offset: 2px; }`；通配 descendent selector 覆盖所有 nav / link / row / metric block / table row；`MetricBlock.tsx:13` `tabIndex={0}`，`ProviderTable.tsx:26` `tabIndex={0}` 让行可 tab。 | 通配 selector + outline 2px + accent blue 对比足。注：disabled link `tabIndex={-1}` 不可达，符合占位语义。 |
| **P1-5** | metricSubValue 等宽 + tabular-nums | **closed** | `dashboard.module.css:223-228` `.numeric { font-variant-numeric: tabular-nums; text-align: right; font-family: JetBrains Mono... }`；`page.tsx` 所有 sub-value 都包 `<span className={styles.numeric}>...</span>`；`MetricBlock.tsx:15` value 本身 `${styles.metricValue} ${styles.numeric}` 复合；`metricSubValue` css line 86-96 `display: flex; gap: 0.5rem; justify-content: flex-end` 三列右对齐 | round 1 推荐的 grid 3-col 等宽未做（用 flex 右对齐替代），但 tabular-nums + monospace 已落，数字位数对齐基本达成。 |
| **P1-6** | WCAG AA 对比度不达 | **closed** | `dashboard.module.css:88` `.metricSubValue { color: #8b949e }` 注释 `提高对比度以符合 WCAG AA`；`#8b949e on #0d1117` 对比度约 6.0:1（实测 — round 1 已确认 `#8b949e on #161b22` 5.4:1，bg 更暗的 `#0d1117` 上比值更高），过 WCAG AA normal-text 4.5:1 阈值 | 符合 Gemini 在 round 2 prompt 选定的 `#8b949e on #0d1117`。 |
| **P1-7** | 4 个 nav links 路径错 | **closed** | 同 P0-4，重复条目。round 1 sonnet 列为 P1，round 2 prompt 提升为 P0；本轮已 closed | — |
| **P1-8** | ProviderTable inline style | **closed** | `ProviderTable.tsx:44` 改成 `className={acc.quota_status === 'exhausted' ? styles.quotaExhausted : ''}`；`dashboard.module.css:231-233` 定义 `.quotaExhausted { color: #f85149 }` 走 CSS module | inline `style` 在 ProviderTable 消失。注：`page.tsx:36` fallback banner 仍 inline style，见 LOW-C 新引 issue。 |

汇总：6 P0 全 closed（其中 P0-1 closed-with-MED），8 P1 7 closed + 1 partial。

---

## 3. 新引入的 issue

### MED-A: 非 mock fetch 写死 `http://localhost:8080`，绕过 next.config.mjs rewrites
- **位置**：`page.tsx:18-20`
- **现象**：`fetch('http://localhost:8080/admin/v1/usage'...)` 等三处 absolute URL。
- **影响**：
  - **server component 端** 这是 server-side fetch（SSR）；`localhost:8080` 在容器化部署 / docker / k8s pod 内不可达后端服务名（如 `backend.huakai.svc.cluster.local:8080`）。
  - `next.config.mjs` 的 `rewrites` 只对 **浏览器侧** 请求生效；server fetch 不走 rewrites。
  - round 1 sonnet review L172 与 round 2 prompt 都建议用 rewrites，但 prompt 提示语义是"前端走 rewrites" — Gemini 实现选了 server 端直 fetch，prompt 措辞确有歧义，不算违 spec。
- **建议**：用 `process.env.BACKEND_BASE_URL ?? 'http://localhost:8080'` 抽出，并在部署文档里登记环境变量；或改为 `/admin/v1/...` + 在 server component 里通过 `headers()` 读 host 拼绝对路径（Next.js 推荐做法）。
- **严重程度**：MED（dev 单机能跑，但 prod 部署会断；codex compliance lane 可能也会标）。
- **是否阻塞 round 2 验收**：否。可 round 3 / 真后端 slice 时修。

### LOW-A: page.tsx 内 6 处 JSX 注释仍英文
- **位置**：`page.tsx:54, 67, 76, 89, 98, 107`
- **现象**：`{/* Metric 1: Tokens */}` 等 6 行。
- **影响**：`feedback_chinese_comments` 严格守则下应中文化（"今日 Tokens" 等），但属于代码 outline 占位，无信息丢失。
- **严重程度**：LOW。
- **建议**：一并改成 `{/* 指标 1：今日 Tokens */}` 等，或干脆删（标签已在 prop 里），无功能影响。

### LOW-B: 状态形态差未做（color-only 双信号仅文字增援）
- **位置**：`ProviderTable.tsx:30-38`、`StatusBar.tsx:40-42`、`dashboard.module.css:155-166`
- **现象**：4 个状态 `.statusOperational/.statusDegraded/.statusFailed/.statusCooling` 仍只靠 `background-color` 区分，dot 形状一致。
- **影响**：黑白打印 / 高色弱场景区分度依赖文字。round 1 sonnet 推荐用 outline / 半实心环让 dot 形态本身就可区分；round 2 未实施。
- **严重程度**：LOW（双信号文字已到位足够 WCAG，仅是 robustness 加分项）。
- **是否阻塞**：否。

### LOW-C: page.tsx fallback empty state 仍用 inline style
- **位置**：`page.tsx:36` `<div style={{ background: '#30363d', color: '#8b949e', padding: '1rem', textAlign: 'center' }}>...`
- **现象**：P1-8 改造 ProviderTable inline style 时，新增的 fallback banner 又用了 inline style。
- **影响**：CSS module 一致性破口；未来切主题需要改 React 代码。
- **严重程度**：LOW（仅 1 处，主题切换暂未实现）。
- **建议**：加 `.fallbackBanner { background: #30363d; color: #8b949e; padding: 1rem; text-align: center; }` 到 css module。

---

## 4. 遗留的 P1 → 下一轮 / round 3 做

| ID | 描述 | 当前状态 | 建议处理 |
|----|------|---------|---------|
| **P1-1 形态差** | dot 仅颜色区分；推荐 outline / 半实心环 | not done | round 3 polish；纯 a11y robustness 加分 |
| **P2-1** | latency 滑动平均 + sparkline | not done | 真后端接入时一并做（round 3 或 slice N+x） |
| **P2-2** | MiniTrend X/Y 轴 tick | not done | 同上 |
| **P2-3** | AlertBar 大写英文文案 | UI 文案现状：`系统状态: CRITICAL — {accounts}, 查看账号`（部分中文化），仍混英文大写 | Owner 定 UI 文案策略后再统改 |
| **P2-4** | metricGrid 1px gap 在 hi-DPR 渲染缝隙 | not done | 改 explicit border 即可，round 3 |
| **P2-5** | 真后端接入后 loading / error skeleton | partial — backendFailed banner 已有，但无 per-block skeleton | 真后端 slice 时做 |
| **P2-6** | ProviderTable `last_dispatch_at` 丢日期 | not done | round 3，改相对时间 + hover ISO |
| **P2-7** | CSS variable 化（主题切换准备） | not done | 非紧急，可任意时机 |
| **P2-8** | max-width 1200px 在大屏浪费 | not done | UX 改进，非阻塞 |

---

## 5. 验证摘要（read-only grep / 计数）

| check | 命令 | 结果 |
|-------|------|------|
| 英文行注释 | `grep -rnP "//\s*[A-Z][a-zA-Z]" frontend/app/dashboard/ frontend/lib/dashboard-mock.ts` | **0 hits** ✓ |
| 英文 block 注释 | `grep -rnP "/\*\s*[A-Z][a-zA-Z]" ...` | 6 JSX outline 注释 + css line1（中文标题命中 HUAKAI token） — 见 LOW-A |
| emoji 扫描 | `grep -nP "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]" ...` | **0 hits** ✓ |
| 禁用 CSS 手段 | `grep -niE "linear-gradient\|radial-gradient\|backdrop-filter\|box-shadow" dashboard.module.css` | **0 hits** ✓ |
| inline style | `grep -nE "style=\{" frontend/app/dashboard/page.tsx frontend/app/dashboard/components/*.tsx` | 1 hit（`page.tsx:36` fallback banner — LOW-C） |
| LoC 限制 | `wc -l` | page 131 / AlertBar 43 / MetricBlock 20 / MiniTrend 40 / ProviderTable 57 / StatusBar 51 / css 233 / mock 115 — **全部 ≤ 350 / 200 上限** ✓ |
| 总 LoC | sum | 690 LoC（round 1 ~554，round 2 +136 主要在 a11y wrapper + caption + 双 fetch 路径） |

未执行 `npm run type-check` / `npm run build`：read-only 协议禁止 run dev tooling 引入副作用；Gemini 自己在 round 2 prompt 验证清单里跑了（"必须做的最终验证"），结果应在其交付 turn 报告里。

---

## 6. 强项（round 2 进步点）

1. **P0-2 心跳从 Math.random 升级到 真 fetch + 5s tick + cleanup**：动作干净，offline 状态用 `setOffline` flag 而非 `latency === -1` magic value，可读性高于 round 1 sonnet 给的示例。
2. **`'use client'` directive** 加得对（`StatusBar.tsx:1`），与 page.tsx 的 server `async function` + Promise.all 三 endpoint 形成 server/client 划分清晰。
3. **`role="alert"` + AlertBar** 用隐式 assertive 替代显式 `aria-live="polite"`：实际告警语义匹配（critical / warning 应立即朗读）。
4. **`scope="row"` on `<th>`** 在 ProviderTable 行第一格：很多 a11y 改造会漏，Gemini 命中。
5. **`focus-visible` 通配 selector** 而非逐元素挂样式，DRY；avoid round 1 推荐里的 `.hintLink:focus-visible, .alertLink:focus-visible` 重复。
6. **`backendFailed` flag + 显式 banner**：codex MED-1 强调"严禁静默 fallback to mock" — Gemini 选择显式 banner + 不 render dashboard 主体，而非偷偷塞 MOCK_USAGE 继续渲染。这是合规的选择。
7. **mock data 与类型 export 解耦**：`UsageSummary` / `ProviderAccountMock` 从 `dashboard-mock.ts` export 出来给 page.tsx 用，类型即契约，便于 round 3 切真接口时只换数据来源不动 UI。

---

## 7. 评估摘要表（round 2 更新）

| 维度 | round 1 评分 | round 2 评分 | 变化 |
|------|-------------|-------------|------|
| 信息架构 | OK | OK | 无变化 |
| 数据密度 | OK- | OK | a11y wrap 略增 markup，无影响数据密度 |
| 状态信号 | NEEDS WORK | OK- | 双信号文字 + WCAG 对比度都过；形态差未做（LOW-B） |
| 交互 | NEEDS WORK | OK | 全局 focus-visible + tabIndex 落地；fallback empty state 有；写操作不在 P1 范围 |
| 可读性 | OK- | OK | tabular-nums + monospace + 右对齐数字字段，纵向对齐恢复 |
| a11y | NEEDS WORK | OK | h1/h2 + caption + scope + focus-visible + WCAG 对比度 + dot+文字双信号 |
| 响应式 | ACCEPTABLE | ACCEPTABLE | 桌面优先未动 |
| 代码质量 | NEEDS WORK | OK- | mock toggle / 真心跳 / 中文注释 主要项全过；剩 1 inline style + 6 行 JSX outline 英文 + fetch URL 硬编码（MED-A） |
| Clean-room compliance | 不评 — codex lane | 不评 — codex lane | 跨 lane cross-discuss 待定 |

---

## 8. 推荐 round 3 处理顺序（如需要 round 3）

如 Owner 同意 round 2 = APPROVE_WITH_MINOR_CHANGES 直接进真后端 slice，下面项目转入 backlog；如想再 polish 一轮：

1. **MED-A**：fetch URL 抽 env var（10 LoC）— 真后端 slice 必做。
2. **LOW-A**：6 行 JSX 注释中文化或删（5 min）。
3. **LOW-C**：fallback banner inline style 搬 CSS module（5 min）。
4. **LOW-B**：dot 形态差（outline / 半实心环），可与 round 3 ProviderTable 视觉再次优化合并（30 min）。
5. **P2 系列**（5-6 项）：真后端接入后顺手做。

合计 round 3 polish 约 1 小时；Owner 可决定跳过直接进 P2/P3 slice。

---

## 9. 评审元数据

- 评审耗时：约 12 分钟
- 已读文件（read-only）：
  - `frontend/app/dashboard/page.tsx` (131 LoC)
  - `frontend/app/dashboard/components/StatusBar.tsx` (51 LoC)
  - `frontend/app/dashboard/components/MetricBlock.tsx` (20 LoC)
  - `frontend/app/dashboard/components/ProviderTable.tsx` (57 LoC)
  - `frontend/app/dashboard/components/AlertBar.tsx` (43 LoC)
  - `frontend/app/dashboard/components/MiniTrend.tsx` (40 LoC)
  - `frontend/app/dashboard/dashboard.module.css` (233 LoC)
  - `frontend/lib/dashboard-mock.ts` (115 LoC)
  - `frontend/next.config.mjs` (rewrites 上下文确认)
  - `docs/process/plans/2026-05-12-gemini-p1-round2-prompt.md`（spec）
  - `docs/research/2026-05-12-gemini-p1-review-sonnet.md`（round 1 自审）
- 未读 / 不在范围：`frontend/app/page.tsx`（ChatPage，无关）；codex round 1/2 review（独立 lane，避免污染）；backend endpoints 实现
- 工具：`Read` × 11，`Bash grep` × 7，零 file write 除本报告
- 跨 lane 状态：codex round 2 compliance lane 平行进行；cross-discuss 待 Owner 调度后做合并 verdict

— Sonnet UX reviewer lane，HUAKAI 前端 P1 Round 2 read-only verify
