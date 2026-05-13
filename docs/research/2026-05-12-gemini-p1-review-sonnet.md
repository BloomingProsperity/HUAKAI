# Gemini P1 Dashboard 原型 — Sonnet UX/Usability/A11y Review

- 评审人：Claude Sonnet (frontend UX reviewer lane)
- 评审日期：2026-05-12 (UTC)
- 评审范围：`/home/codex/HUAKAI/frontend/app/dashboard/**` + `frontend/lib/dashboard-mock.ts`
- 对照 brief：`docs/plans/2026-05-12-frontend-gemini-brief.md` §A、§E、§G
- 评审维度：**仅 UX / usability / a11y / 代码质量**；clean-room compliance 由 codex lane 独立评
- 平行 reviewer：codex lane（另一会话，评 compliance / 视觉原创性 / brief §A 三条硬约束）

---

## 1. Verdict

**REQUEST_CHANGES**

理由概述：视觉气质方向是对的（边框分层 / 等宽数字 / 无渐变 / 无 emoji / 暗色单色 + 单 accent），但有 **3 个 P0 阻塞缺陷** 会让 Owner 一打开页面立即看出"功能没做完"：

1. brief §G "Mock 数据约束" 明确要求 `NEXT_PUBLIC_USE_MOCK === '1'` 切换，**当前实现完全没接这个开关**，mock 硬编码进 page。
2. brief §G "Dashboard P1 内容" 第 1 条要求"与后端心跳延迟"，**当前实现是 `Math.random()` 假数据**，心跳值与后端无任何关联，Owner 一旦悬停看 5 秒就知道是装的。
3. brief §H + Owner 全局约束要求中文注释；**所有组件 + CSS 注释全部英文**（`/* HUAKAI Industrial Dashboard Styles */`、`// Simulate heartbeat latency fluctuation`、`// Ratios are 0 to 1`）。

此外 a11y 层面 6 个 P1 问题（颜色独信号 / 表头语义 / 键盘焦点 / heading 层级 / aria-label 缺失 / 数字千分位无 `aria` 朗读控制）。修完 P0 + P1 后可 APPROVE_WITH_MINOR_CHANGES。

---

## 2. Critical findings (P0 — 必须修完才能给 Owner 看)

### P0-1 NEXT_PUBLIC_USE_MOCK 开关未实现

- **文件**：`frontend/app/dashboard/page.tsx:7`
- **现状**：直接 `import { MOCK_USAGE, MOCK_PROVIDER_ACCOUNTS, MOCK_CHART_DATA } from '../../lib/dashboard-mock';` 然后 `const usage = MOCK_USAGE;`。完全没读 `process.env.NEXT_PUBLIC_USE_MOCK`，也没占位的真实 fetch 路径。
- **期望**：brief §G "Mock 数据约束" 写明 `process.env.NEXT_PUBLIC_USE_MOCK === '1'` 切换、真实接入留 TODO。这是 Owner 验收要看的明确指标 — 没切换意味着无法在切换到接 backend 时验证 fallback 行为。
- **修改建议**：抽 `frontend/lib/dashboard-data.ts`（或同位置工厂函数）：
  ```ts
  // 数据来源切换：mock vs 真实 backend
  export async function loadDashboardUsage(): Promise<UsageSummary> {
    if (process.env.NEXT_PUBLIC_USE_MOCK === '1') return MOCK_USAGE;
    // TODO(P1-接入): const r = await fetch('/admin/v1/usage', { headers: { 'X-Admin-Token': ... } });
    throw new Error('real backend not wired yet');
  }
  ```
  然后 `page.tsx` 改成 `async function DashboardPage()` 并 `await loadDashboardUsage()`。Provider table 和 chart 同模式。

### P0-2 心跳延迟是 Math.random() 假数据

- **文件**：`frontend/app/dashboard/components/StatusBar.tsx:8、13`
- **现状**：
  ```tsx
  const [latency, setLatency] = useState(24);
  const latencyTimer = setInterval(() => setLatency(20 + Math.floor(Math.random() * 10)), 5000);
  ```
  完全本地生成，**不打任何 backend endpoint**。Owner 拔掉 backend 它还会变 — 误导性强于"硬编码 24ms"。
- **期望**：brief §G 第 1 条要求"与后端心跳延迟（数字 ms，用等宽字体）"。这是状态条核心信号；伪造比缺失更糟，因为它会让运营者在真故障时仍然看见 "HEARTBEAT OK" + 24ms。
- **修改建议**：要么真打 `/debug/vars`（或 brief D 列的任一 endpoint）做 `performance.now()` 差值，要么在 mock 模式下显式标注 `latency: '— (mock)'` 并把 dot 改成 `statusCooling` 灰色。最低限度也要让 USE_MOCK=0 时走 fetch 路径：
  ```tsx
  useEffect(() => {
    if (process.env.NEXT_PUBLIC_USE_MOCK === '1') {
      setLatency(null); // 文字显示 "—"
      return;
    }
    const tick = async () => {
      const t0 = performance.now();
      try { await fetch('/admin/v1/healthz', { cache: 'no-store' }); setLatency(Math.round(performance.now() - t0)); }
      catch { setLatency(-1); /* 显示 OFFLINE */ }
    };
    tick(); const id = setInterval(tick, 5000); return () => clearInterval(id);
  }, []);
  ```

### P0-3 全部注释英文

- **文件**：`StatusBar.tsx:12`（`// Simulate heartbeat latency fluctuation`）、`MiniTrend.tsx:1-3、22`（`/** Industrial Mini Trend Chart ... */`、`// Ratios are 0 to 1`）、`dashboard.module.css:1、11、34、78、116、130、148、166`（全部英文 section header）、`dashboard-mock.ts:1-4`（`/** Dashboard P1 Mock Data for HUAKAI / Based on brief 2026-05-12 */`）
- **现状**：100% 英文注释。
- **期望**：CLAUDE.md global memory `feedback_chinese_comments`（Owner 2026-05-06）："仓库内新写注释一律中文；标识符 / enum 字面值保持英文"。
- **修改建议**：把所有注释中文化，标识符 / className / CSS class 名保留英文。例如 `/* HUAKAI 工业控制台样式 */`、`// 心跳延迟波动模拟`、`// ratio 取值范围 0 到 1`。

---

## 3. Recommended improvements (P1 — Show Owner 前最好修，否则不能 APPROVE)

### P1-1 状态信号仅靠颜色

- **文件**：`ProviderTable.tsx:29-36`、`dashboard.module.css:117-128`
- **现状**：health 状态 dot 是 6×6 实心圆 + 颜色，紧跟纯文字 `operational` / `degraded` / `failed`。文字本身是英文小写 token，**色觉障碍用户**或**色弱 + 小字号**双重压力下，degraded（金 #d29922）vs operational（绿 #3fb950）在 0.8rem 字号下区分度不足。
- **期望**：brief §E.1 #3 "状态信号即颜色"是指颜色用于状态而非装饰，但 a11y 上必须文字 + 颜色双信号，且文字本身要有形态差。
- **修改建议**：(a) dot 加 ARIA 隐藏 + 状态文字加 `aria-label="status: degraded"`；(b) 在 dot 形状上也做区分（例：operational 实心、degraded 半实心环、failed 实心 + 4px 红色 outline），让黑白打印或屏幕变灰也可读；(c) 至少给文字加 `font-weight: 600` 当 `failed` 时。

### P1-2 表格无 `<caption>` / 列无 `scope`

- **文件**：`ProviderTable.tsx:11-22`
- **现状**：标题 `Top 5 Provider Accounts` 是 `<div className={styles.tableHeader}>`，不是 `<caption>`。表头 `<th>` 没有 `scope="col"`。
- **期望**：WCAG H39 + H63；屏幕阅读器以 caption 关联表语义、scope 让单元格定位列。
- **修改建议**：
  ```tsx
  <table className={styles.compactTable}>
    <caption className={styles.tableHeader}>Top 5 Provider Accounts</caption>
    <thead>
      <tr>
        <th scope="col">Name</th>
        ...
  ```
  移除外层 `<div className={styles.tableContainer}>` 包裹 `<div>` 标题的 hack。

### P1-3 缺 `<h1>` / heading 层级断裂

- **文件**：`page.tsx:13-101`
- **现状**：整页**没有任何 `<h1>` / `<h2>`**。dashboard 主区入口屏幕阅读器无法定位"主标题"。MetricBlock 的 `label`（`Today Tokens` 等）是 `<div>`，不是 `<h3>`。
- **期望**：WCAG 1.3.1 + 2.4.6；NOC / SCADA 控制台依然要让 AT 用户能 "skip to section"。
- **修改建议**：
  - StatusBar 上方加 `<h1 className="sr-only">HUAKAI Operations Dashboard</h1>`（视觉隐藏，AT 可读）
  - MetricBlock 把 `<div className={styles.metricLabel}>` 改为 `<h2 className={styles.metricLabel}>`（小字号 uppercase 不影响视觉）
  - ProviderTable caption 自然作为 `<h2>` 等价物
  - AlertBar 加 `role="alert"` + `aria-live="polite"`

### P1-4 链接看起来像普通文字，键盘焦点无可见态

- **文件**：`dashboard.module.css:148-164`、`page.tsx:96-99`
- **现状**：`.hintLink` 只有 hover 改色，**没有 `:focus-visible` 样式**。键盘 Tab 到 `API KEYS [P2]` 时无任何视觉反馈（依赖浏览器默认 outline，在暗色背景 #0d1117 上常被压成几乎不可见）。alertLink 同样无 focus 态。
- **期望**：brief §E.1 #5 "键盘可达"；WCAG 2.4.7 Focus Visible。
- **修改建议**：
  ```css
  .hintLink:focus-visible,
  .alertLink:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }
  .hintLink:hover { color: #58a6ff; text-decoration: underline; }
  ```
  另外底部 4 个链接全部指向错误路径（见 P1-7），先 fix 路径再做 focus 态。

### P1-5 数字密集块缺等宽 + 缺数字对齐

- **文件**：`page.tsx:32-36、52-58`（IN/OUT/CACHE、p50/p95/p99 子值）、`dashboard.module.css:65-70`（`.metricSubValue`）
- **现状**：metricSubValue 没 `font-family` 等宽继承；token 数 `1,245,080` 和 `854,200` 在普通字体下宽度不一致，扫读时无法纵向对齐。`p50:450ms` `p95:1200ms` `p99:2500ms` 同样问题。
- **期望**：brief §E.3 "等宽字体用于：数字 / token / id / timestamp / hash"；§E.1 #1 数据密度优先 — 不对齐就丧失密度优势。
- **修改建议**：
  ```css
  .metricSubValue {
    font-family: 'JetBrains Mono', 'Source Code Pro', monospace;
    font-variant-numeric: tabular-nums;
    font-size: 0.7rem;
    color: #8b949e; /* 当前 #484f58 在 #0d1117 上对比度 ~3.0:1, 未过 WCAG AA 4.5:1 — 见 P1-6 */
  }
  ```
  另外 IN/OUT/CACHE 三段建议改 `display: grid; grid-template-columns: 1fr 1fr 1fr;` 使三列等宽，否则浮在一行会跟 cost 那个单值块比例失衡。

### P1-6 颜色对比度未过 WCAG AA

- **文件**：`dashboard.module.css:67`（`.metricSubValue { color: #484f58 }`）、`:32`（`.statusBar` 顶部 ts/tz `color: #8b949e` on `#161b22`）
- **现状**：实测对比度：
  - `#484f58` (sub-value text) on `#0d1117` (block bg)：**约 3.0 : 1**（不过 WCAG AA 4.5:1 normal text）
  - `#8b949e` on `#161b22`：约 5.4 : 1（过）
  - `#3fb950` (operational dot) vs `#0d1117` 表格背景：8.7:1（过，但 dot 6px 太小不参与 text rule，仍 OK）
  - `#d29922` (degraded) vs `#0d1117`：6.7:1（过）
- **期望**：WCAG 2.1 AA：normal text 4.5:1，large 3:1。SubValue 字号 0.75rem (~12px) 属 normal text。
- **修改建议**：把 `.metricSubValue color` 改成 `#8b949e`（与 metricLabel 一致），或更亮的 `#a0a8b3`。

### P1-7 底部导航 4 个链接全部指向错误路径

- **文件**：`page.tsx:96-99`
- **现状**：
  ```tsx
  <a href="/accounts">API KEYS [P2]</a>
  <a href="/accounts">PROVIDER ACCOUNTS [P3]</a>
  <a href="/bindings">POOL & CHANNELS [P4]</a>
  <a href="/selection">USAGE & BILLING [P5]</a>
  ```
  P2 和 P3 都指向 `/accounts`（重复），P4 指 `/bindings`、P5 指 `/selection` — brief §C 中根本没有 `/bindings` 或 `/selection` 这种路由，且 P2 P3 应该是不同页（brief §C 明确 P2 = api-key 管理、P3 = provider-account 管理）。
- **期望**：路由命名跟 brief §C 一致。即便 P2-P5 还没建页，链接也应指向 placeholder route。
- **修改建议**：
  ```tsx
  <a href="/api-keys">API KEYS [P2]</a>
  <a href="/provider-accounts">PROVIDER ACCOUNTS [P3]</a>
  <a href="/pools">POOL &amp; CHANNELS [P4]</a>
  <a href="/usage">USAGE &amp; BILLING [P5]</a>
  ```
  并在每个 href 旁加注释 "TODO: 路由待 P2-P5 实现"。注意 `&` 在 JSX text 里建议写 `&amp;`。

### P1-8 ProviderTable 行内 inline style

- **文件**：`ProviderTable.tsx:41`
- **现状**：`<span style={{ color: acc.quota_status === 'exhausted' ? '#f85149' : 'inherit' }}>` — 用 inline style 表达状态色，绕开 CSS module。
- **期望**：状态色作为状态信号应集中在 CSS module，便于将来切主题。
- **修改建议**：增加 `.quotaExhausted { color: #f85149 } .quotaActive { color: #c9d1d9 }` 并改 `className`。这也跟 P1-1 双信号要求一致 — 仅红色不够，建议加 `[!]` 前缀文字或 `font-weight: 600`。

---

## 4. Nice to have (P2 — round-2 改)

### P2-1 StatusBar `Math.random()` 即便修了 P0-2 也建议给 latency 一个**滑动平均** + 历史 mini-sparkline，让运营者看到趋势而非瞬时值，符合 brief §E.1 "数据密度"原则。约 +20 LoC。

### P2-2 MiniTrend 无 X / Y 轴标签、无 hover tooltip。brief §G 第 2 条说"折线最长 24h"，但当前 SVG 没标 0:00 / 12:00 / 24:00 时间锚。建议增加 2-3 个 tick 文字（等宽字体 0.6rem）。或 round-2 接入真实数据后再说。

### P2-3 AlertBar 文字是英文大写 `SYSTEM STATUS: CRITICAL — ... DEGRADED ACCOUNTS DETECTED.`。这条 SCADA 气质对，但全大写 + 标点跨语种用户读起来累。建议改为 `系统状态: 严重 — 2 个降级 / 1 个故障账号`（注释中文规则；但 UI 文案策略未在 brief 明确，可保留英文，仅说大小写）。Owner 可定。

### P2-4 metricGrid 用 `gap: 1px; background: #30363d` 实现"内部分隔线"，在 high-DPR 屏 (devicePixelRatio > 1.5) 可能出现 sub-pixel 渲染导致缝隙不均。建议改成显式 `border-right: 1px solid #30363d; border-bottom: 1px solid #30363d` 配合 `:nth-child(3n) { border-right: none }` — 更可控。

### P2-5 整页**没有 loading / error 状态**。当前因为 mock 是同步常量所以无问题，但 P0-1 修完接入异步数据后必须设计：loading skeleton、error banner（与 AlertBar 共用样式）、empty state（"暂无活跃账号"）。建议同时立 round-2。

### P2-6 ProviderTable `last_dispatch_at` 用 `toLocaleTimeString` 只显示时分秒，**丢日期**。如果一个账号 8 小时没派发，列里会显示 "09:15:20" 跟其他刚派发的 "10:32:12" 看起来都是今天，运营者误判。建议改为 `2 min ago` 类相对时间 + hover 显示 ISO 完整时间戳。

### P2-7 没有 dark/light 切换实现。brief §E.3 "暗色或亮色主题二选一" — 选了暗色 OK，但建议把所有颜色提到 `:root { --bg-base: #0d1117; ... }` CSS 变量，给未来切换留位。

### P2-8 dashboard 容器 `max-width: 1200px` 在 4K / 2K 大屏上左右大量留白，与 brief §E.1 #1 数据密度优先冲突。SCADA 控制台一般占满 viewport。建议 `max-width: min(1600px, 100vw - 4rem)` 或干脆无 max-width，靠 grid 自适应。

---

## 5. Strengths (做得好)

1. **整体气质命中**。无渐变、无 backdrop-blur、无圆角 > 4px（实际全是 0 圆角）、无 box-shadow、无 emoji、配色单色 + 单 accent（#58a6ff 蓝） — brief §A、§E.2 全过。
2. **等宽字体落地**。`.metricValue`、`.mono`、StatusBar latency 都正确使用 JetBrains Mono fallback。
3. **status dot 6px 实心圆 + 文字**符合 brief §E.3 状态指示规范。dot 颜色与状态绑定（operational 绿 / degraded 金 / failed 红 / cooling 灰）映射清晰。
4. **AlertBar 条件渲染**正确：`degradedCount === 0 && failedCount === 0` 时 `return null`，符合 brief "仅当任一账号 health_state ∈ {degraded, failed} 时出现"。左侧 4px accent 条 + 透明背景符合"无背景色横向通栏"要求。
5. **MiniTrend 自己用 SVG 写**，没引 Recharts/visx（brief 允许但能不引最好），40 行干净。
6. **组件拆分粒度合理**：StatusBar / MetricBlock / ProviderTable / AlertBar / MiniTrend 各司其职，没有大单文件膨胀（最大 page.tsx 103 LoC，远低于 350 上限；MiniTrend 40 LoC、AlertBar 19 LoC）。
7. **StatusBar `useEffect` cleanup 正确**（`return () => { clearInterval(timer); clearInterval(latencyTimer); }`），无 setInterval leak。
8. **mock 数据真实感强**：`anthropic-pro-01` / `openai-plus-team` / `azure-gpt4-east` 命名贴合 brief §B "运营者自备多个上游订阅"语境，degraded/failed/exhausted 状态多样，能验证 AlertBar 触发路径。
9. **金额双币 RMB ≈ 304.45 / USD 42.58** 接近 1:7.14，符合当下汇率量级，没用 1:7 整数装。

---

## 6. 评估摘要表

| 维度 | 评分 | 关键问题 |
|------|------|----------|
| 信息架构 | OK | hierarchy 清晰；状态条 → 告警 → 6 指标 → 表 → 导航 的扫读路径合理 |
| 数据密度 | OK- | 6×3 grid + 表 6 列 + status bar 总数据点约 50-60，达标；max-width 1200 在大屏浪费（P2-8） |
| 状态信号 | NEEDS WORK | dot+文字双信号有，但 hash sub-value 色对比不足（P1-6）；exhausted inline style（P1-8）；色觉无障碍弱（P1-1） |
| 交互 | NEEDS WORK | 键盘焦点态完全缺（P1-4）；写操作未实现可不评；目前无 loading/error 路径（P2-5） |
| 可读性 | OK- | 字号 / 字重对比够；数字未启用 tabular-nums 影响对齐（P1-5） |
| a11y | NEEDS WORK | 缺 h1 / h2（P1-3）；表无 caption / scope（P1-2）；对比度未过（P1-6）；alert 无 aria-live（P1-3） |
| 响应式 | ACCEPTABLE | 桌面端锁定 OK（brief 允许）；移动端会破，但 brief §E 允许桌面优先 |
| 代码质量 | NEEDS WORK | 组件拆分好；mock 切换未实现（P0-1）；心跳假数据（P0-2）；全英注释（P0-3） |
| Clean-room compliance | **不评 — codex lane** | — |

---

## 7. 验收清单对照 (brief §G 末段)

| brief 清单项 | 状态 | 说明 |
|--------------|------|------|
| 无渐变 / 无 backdrop-blur / 无 box-shadow > 4px / 无圆角 > 6px | PASS | grep 全文件 0 命中 |
| 无 emoji 字符 | PASS | 0 命中 |
| 无第三方 component 库 import | PASS | 仅 React / Next；未引 Recharts |
| 数字字段使用等宽字体 | PARTIAL | metricValue 有；metricSubValue 无（P1-5） |
| 状态颜色仅用于状态信号 | PASS | accent 蓝仅用于 link / latency / trend stroke |
| 全页面 Tab 键可达 | PARTIAL | 可达但无 focus-visible 样式（P1-4） |
| 不像 Helicone/Vercel/Linear/Stripe/Supabase | **codex lane 判** | 不在我评审范围 |
| 不像 ChatGPT/Claude/Gemini/Perplexity 设置页 | **codex lane 判** | 不在我评审范围 |
| 视觉给出"工业控制台"气质 + 文字理由 | PARTIAL | 视觉到位；**理由说明文档未交付**（brief §I 要求） |

---

## 8. 推荐 round-2 修改顺序

1. P0-1 P0-2 P0-3 一并修（约 +60 LoC，1-2 小时）
2. P1-1 P1-3 P1-4 P1-6 一组 a11y fix（+40 LoC，1 小时）
3. P1-2 P1-5 P1-7 P1-8 一组 polish（+30 LoC，1 小时）
4. P2 全部留待 round-2 或接入真实数据时再做

修完 P0 + P1，重新交给我 + codex 二审。我这边按 APPROVE_WITH_MINOR_CHANGES 处理；若 codex compliance 也过则交付 Owner。

---

## 9. 评审元数据

- 评审耗时：约 15 分钟
- 已读文件：
  - `frontend/app/dashboard/page.tsx` (103 LoC)
  - `frontend/app/dashboard/components/StatusBar.tsx` (32 LoC)
  - `frontend/app/dashboard/components/MetricBlock.tsx` (20 LoC)
  - `frontend/app/dashboard/components/ProviderTable.tsx` (54 LoC)
  - `frontend/app/dashboard/components/AlertBar.tsx` (19 LoC)
  - `frontend/app/dashboard/components/MiniTrend.tsx` (40 LoC)
  - `frontend/app/dashboard/dashboard.module.css` (170 LoC)
  - `frontend/lib/dashboard-mock.ts` (116 LoC)
  - `docs/plans/2026-05-12-frontend-gemini-brief.md` (全文)
- 未读 / 不在范围：`frontend/app/page.tsx`（ChatPage，无关）、`frontend/next.config.mjs`、backend endpoints
- 跨 lane 等待：codex compliance review 完成后做 cross-discuss

— Sonnet UX reviewer lane，HUAKAI 前端 P1 第一轮独立评审
