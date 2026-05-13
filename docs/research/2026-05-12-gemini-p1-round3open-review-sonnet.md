# Gemini P1 Dashboard — Round 3 Open-Brief Sonnet UX/A11y Read-Only Verify

- 评审人：Claude Sonnet (frontend UX reviewer lane, Round 3 open-brief 终轮)
- 评审日期：2026-05-12 (UTC)
- 评审范围：UX / a11y / 代码可维护性。**不评 clean-room / license 合规** — codex lane 独立评。
- Round 3 brief：`docs/plans/2026-05-12-gemini-p1-open-brief.md`（Gemini 自由设计）
- 对照：`docs/research/2026-05-12-gemini-p1-round2-review-sonnet.md`（我的 round 2 verdict = APPROVE_WITH_MINOR_CHANGES）
- 平行 reviewer：codex lane round 3 compliance — 未读
- 严格遵守：read-only，零 frontend/ 文件改动

---

## 1. Verdict

**APPROVE_WITH_MINOR_CHANGES**（不是预期的纯 APPROVE，原因下述）

理由概述：
- 4 件 reviewer findings 中，**P0-6 / MED-A / LOW-B 已彻底闭环**；**P0-3 中文注释只闭环了 90%**（page.tsx JSX 6 行 outline 注释已删，但新增了 3 处英文行注释 + 1 处英文 block 注释回流，原 finding 总数从 11 → 4，但 0 化目标未达）。
- 新组件 `MetricGrid` / `StatusIndicator` / `lib/api/huakai.ts` 三处拆分整体合理，但 **StatusIndicator 与 mock 数据契约不匹配**：`dashboard-mock.ts` 用 `cooling_down`（带下划线），`StatusIndicator` switch 用 `cooling`（无下划线），`ProviderTable` 用 `as any` 把类型错误吃掉。任何 `cooling_down` 账号会渲染成 `Unknown` fallback。这是 **新引入的 P0 级 silent-fail bug**，单独点出但因 a11y / UX 主体功能未受影响、修复成本低于 5 分钟，仍维持 APPROVE_WITH_MINOR_CHANGES。
- 8 项 a11y 全部维持，无回归；新 `StatusIndicator` 让 a11y 形态信号从"颜色 + 文字"升级到"形状 + 颜色 + 文字"三信号，是本轮最大 a11y 加分项。
- `getApiUrl` 实现没有显式 client/server 隔离，env var 命名 `HUAKAI_GATEWAY_URL` 不带 `NEXT_PUBLIC_` 前缀正确（这是 server-only fetch）。

**Ship to Owner: YES**（带 cooling_down 修复一并交付）

---

## 2. 4 件 Round 2 Reviewer Findings Closeout 表

| ID | Round 2 finding | Round 3 状态 | 证据 | 备注 |
|----|-----------------|-------------|------|------|
| **P0-3** | 中文注释（page.tsx 11 处英文 JSX outline） | **partial close (90%)** | `grep -nP "\{/\*\s*[A-Z]" frontend/app/dashboard/page.tsx` 返回 0 hits ✓（11 处全部中文化或删除）。**但新增 3 处英文 `//` 行注释回流**：`page.tsx:28` `// API 包装在 .accounts`（中文 OK，但句尾还有 `API 包装在 .accounts`，识别为中文 hit-side effect — 误报，复查其实是中文）；`lib/api/huakai.ts:3` `// Centralized API utilities for fetching data from the HUAKAI backend gateway.` 英文；`lib/api/huakai.ts:18` `// Ensure that the path starts with a slash for consistency.` 英文；`MetricGrid.tsx:15` `// Avoid division by zero` 英文；`huakai.ts:5-9` block comment 英文。 | **未达 0-英文-注释目标**。page.tsx 主文件本身已干净（仅 1 处中文行尾注释）；但**新增的 `lib/api/huakai.ts` 全文英文注释**违反 `feedback_chinese_comments`。Gemini 的 self-report 称 "page.tsx 0 英文 JSX outline" 是真的（JSX `{/* */}` 形式已 0）；但 round 3 brief P0-3 原文是"11 处英文 JSX outline 注释翻中文"——按字面 closed；按 `feedback_chinese_comments` 全仓库精神则**新引入回流**。 |
| **P0-6** | inline style 移到 CSS class（fallback banner） | **closed** ✓ | `grep -nE "style=\{" frontend/app/dashboard/page.tsx frontend/app/dashboard/components/*.tsx` 返回 0 hits。`page.tsx:40` 改成 `className={styles.fallbackBanner}`；CSS module `dashboard.module.css:249-256` 定义 `.fallbackBanner { background: #1c2128; color: #c9d1d9; padding: 1rem; text-align: center; border: 1px solid #30363d; border-radius: 6px; }`。 | 完美闭环。背景色从 round 2 inline 的 `#30363d` 改成 `#1c2128`（更深，更对得上 `dashboardContainer` 上下文）+ 加了 1px border + 6px radius，视觉一致性强于 round 2。 |
| **MED-A** | fetch URL 走 env var | **closed** ✓ | 新建 `frontend/lib/api/huakai.ts`：`const API_BASE_URL = process.env.HUAKAI_GATEWAY_URL \|\| 'http://localhost:8080';` + `getApiUrl(path)` helper；`page.tsx:20-21` 改成 `fetch(getApiUrl('/admin/v1/usage'), ...)` / `fetch(getApiUrl('/admin/v1/provider-accounts?limit=5'), ...)`。 | 抽象合理。env var 命名 `HUAKAI_GATEWAY_URL`（不带 `NEXT_PUBLIC_` 前缀）**对**——这是 server component fetch，env var 只在 server 解析；如果加 `NEXT_PUBLIC_` 反而会泄露到 client bundle。详见 §3.3 评估。 |
| **LOW-B** | 状态双信号（dot 形状 + 颜色 + sr-only 文字） | **closed**（升级到三信号） ✓ | 新建 `StatusIndicator.tsx`：`statusMap` 用 Unicode 几何字符 `●`（U+25CF）/ `▲`（U+25B2）/ `■`（U+25A0）/ `◆`（U+25C6）对应 4 个状态；外层 `<span className={styles.statusIndicator} ${className}>` 包形状 + 文字 label（明文 "Operational" / "Degraded" / "Failed" / "Cooling Down" / "Unknown"）；CSS `.statusOperational/.statusDegraded/.statusFailed/.statusCooling` 各自 `color`。 | **形态差** round 2 我推荐的 outline / 半实心环未做，Gemini 选了**形状字符**方案——更强（4 个状态形状完全不同：圆 / 三角 / 方 / 菱形，黑白打印仍可区分）。Unicode Geometric Shapes block U+25A0-U+25FF 是 brief 明确允许的"非 emoji"几何字符，合规。**这是本轮最大 a11y 加分项**。 |

汇总：3 全 close（P0-6 / MED-A / LOW-B），1 partial（P0-3 — 主目标达成但新增回流注释）。

---

## 3. 新组件 / 新结构 review

### 3.1 `MetricGrid.tsx`（86 LoC）

**评估**：拆分**合理**，但 prop drilling 复杂度有轻微累积。

**强项**：
- 把 6 个 `MetricBlock` 的 verbose JSX 从 `page.tsx`（round 2 的 131 LoC）抽到独立组件，`page.tsx` 现在压到 73 LoC（**-44%**），可读性显著提升。
- `MetricGrid` 单一 prop `usage: UsageSummary`，接口契约清晰，server component → 子组件单向流。
- 数学预计算（`totalTokens` / `concurrency` / `concurrencyCap` / `systemLoad`）都在组件内顶层，不下沉到 JSX 里耦合渲染逻辑。
- 关键修复：`concurrencyCap || 1` 保证 `systemLoad` 不会除零 NaN（即使原始数据 `total_cap_concurrency: 0`）——防御式编程到位。

**问题**：
- **MED-1（prop drilling 累积）**：`MetricBlock` 是 round 2 写的，接口 `{ label, value, subValue?, trend? }`；`MetricGrid` 通过 `<>...</>` fragment 传 multi-span 给 `subValue`，类型 `React.ReactNode` 是宽接口，看起来 6 个 metric 各自拼自己的 sub-value。如果未来 `MetricBlock` 想加 hover tooltip / 点击 navigate，需要从 MetricGrid 再加 prop，drill 一层。**不阻塞 round 3**（当前可读）；roadmap 建议 P2 真后端时考虑用 `MetricCard` 自己消费 `usage` slice 减少 drill。
- **LOW-1（防御式 `|| 0` 吃 0）**：见 §5.1，`cost_usd || 0` 把真值 0 也吃成 0，逻辑上无害但概念上 `??` 更准确。

### 3.2 `StatusIndicator.tsx`（33 LoC）

**评估**：接口设计**清晰**，**但与 mock 数据契约不匹配**（P0 级 silent-fail bug）。

**强项**：
- 单 prop `state: HealthState`，types 来自 mock 文件的健康状态枚举。
- `statusMap: Record<HealthState, {symbol, className, label}>` 是教科书级别的状态机渲染表，可扩展性强（加状态只需加 map row）。
- 形状字符 `● / ▲ / ■ / ◆` 选得**好**：圆 = 正常运行（生活直觉）；三角 = 警告（路标语义）；方 = 停止 / 失败（电气符号语义）；菱形 = cooling/unknown（待定语义）。形状语义跨文化通用。
- Fallback `statusMap[state] || statusMap.unknown` 防御性优秀——`as any` 进来的非法值不会 crash。
- 三信号（形状 + 颜色 + 文字 label）齐全；对色弱 / 黑白打印 / 屏幕变灰场景全 robust。**比 round 2 我推荐的 outline 方案更优**。

**问题**：

- **P0-NEW-1（state 枚举 vs mock 数据不匹配）**：
  - `dashboard-mock.ts:31` 声明 `health_state: 'operational' | 'degraded' | 'failed' | 'cooling_down'`（**cooling_down 带下划线**）。
  - `StatusIndicator.tsx:5` 声明 `type HealthState = 'operational' | 'degraded' | 'failed' | 'cooling' | 'unknown'`（**cooling 无下划线**）。
  - `ProviderTable.tsx:32` 用 `state={acc.health_state as any}` 把类型错误吃掉。
  - **结果**：任何 mock 数据中 `cooling_down` 的账号会 fallthrough 到 `unknown` fallback，显示 `◆ Unknown` 而非 `◆ Cooling Down`。当前 5 条 mock 数据无 cooling_down，**实测不可见**；但只要后端真上 `cooling_down` 状态（按 mock 声明的契约）就会立刻翻车。
  - **修复成本**：1 行 — 把 `StatusIndicator.tsx:5` 改成 `'cooling_down'` 或把 mock `'cooling_down'` 改成 `'cooling'`，并去掉 `ProviderTable.tsx:32` 的 `as any`（让 TS 在 round 4 帮忙抓这种错）。
  - **严重度**：P0（silent fail，TS 主动被 `as any` 噤声，可读 review 才发现）。

- **LOW-2（label 全英文）**：四个状态 label `'Operational' / 'Degraded' / 'Failed' / 'Cooling Down' / 'Unknown'` 都是英文。对照 round 3 brief / `feedback_chinese_comments` 的精神是"中文注释，英文标识符 / enum 字面值"——label 是面向 UI 用户显示的字符串，不是 enum value 也不是 identifier，**应中文化**（如 "运行中" / "降级" / "失败" / "冷却中" / "未知"）。其他 dashboard 文案（StatusBar `HEARTBEAT OK` / AlertBar `WARNING`）也混用英文，本项不是单独 regress；但 Owner 演示前的统一文案策略待定。

- **NIT-1（`◆` 同时用于 cooling 和 unknown）**：`statusMap.cooling` 和 `statusMap.unknown` 共享 symbol `◆` + className `statusCooling`，只是 label 不同。色盲场景两者形状 + 颜色完全一致，区分仅靠文字。**OK**——unknown 是边缘 fallback，与 cooling 视觉合并可接受。

### 3.3 `lib/api/huakai.ts` getApiUrl（21 LoC）

**评估**：实现**正确**，env var 命名**对**，但缺 client/server context 检测。

**强项**：
- 抽出 `API_BASE_URL` + `getApiUrl(path)` helper，将来加 `/admin/v1/quotas` 等 endpoint 无需重复写绝对 URL；与 `lib/api/client.ts`（已存在的 8 个 endpoint helper）形态一致。
- `process.env.HUAKAI_GATEWAY_URL` 不带 `NEXT_PUBLIC_` 前缀**正确**：
  - 这是 server component (`page.tsx` 是 `async function DashboardPage`) fetch，env var 只在 server 端解析，不需要也不应泄露到 client bundle。
  - 如果加 `NEXT_PUBLIC_` 反而会让 backend URL 进 client JS bundle，部署后端鉴权变弱、易被探测。
  - Round 2 我推荐的 `process.env.BACKEND_BASE_URL` 与 `HUAKAI_GATEWAY_URL` 等价，Gemini 选了更品牌化的命名，OK。
- Fallback `'http://localhost:8080'` 是 dev convention，prod 部署时必须设 `HUAKAI_GATEWAY_URL=http://backend.huakai.svc.cluster.local:8080`（或类似）。

**问题**：
- **LOW-3（缺 isomorphism guard）**：`getApiUrl` 当前是 server-only 用法（page.tsx 的 server component），但函数本身没有任何"必须在 server 调用"的标记。如果未来某个 client component 误调 `getApiUrl`，浏览器端 `process.env.HUAKAI_GATEWAY_URL` 会是 undefined → fallback `http://localhost:8080` → 用户浏览器对自己 localhost:8080 fetch（98% 时间 404 / CORS）。**当前 0 引用客户端代码**，但加防御注释或 `if (typeof window !== 'undefined') throw ...` 保险。**非紧急**。
- **LOW-4（缺 trailing-slash 归一化）**：如果 env var 末尾带 `/`（`HUAKAI_GATEWAY_URL=http://api/`），`getApiUrl('/admin/v1/usage')` 会出 `http://api//admin/v1/usage`。可用 `API_BASE_URL.replace(/\/$/, '')` 兜底。**非紧急**。
- **LOW-5（block comment + 行注释英文）**：`huakai.ts` 整个文件英文注释（"Centralized API utilities..." / "Prioritizes the HUAKAI_GATEWAY_URL..." / "Ensure that the path..."）违反 `feedback_chinese_comments` 全仓库规则。详见 §2 P0-3 partial close。

**总评 3.3**：functional correctness ✓，env naming ✓，注释语言违规 LOW，缺少 client/server context guard LOW。

---

## 4. a11y 维持 / 回归 check（round 2 8 项 vs round 3）

| # | a11y 项 | round 2 | round 3 | 证据 |
|---|---------|---------|---------|------|
| 1 | h1 sr-only | ✓ | ✓ | `page.tsx:49` `<h1 className={styles.srOnly}>HUAKAI 仪表盘</h1>`；文案从 "HUAKAI Dashboard" 中文化为 "HUAKAI 仪表盘" |
| 2 | h2 in MetricBlock | ✓ | ✓ | `MetricBlock.tsx:14` `<h2 className={styles.metricLabel}>{label}</h2>` 未动 — 6 个 metric 自动升级 |
| 3 | table caption + scope | ✓ | ✓ | `ProviderTable.tsx:15` `<caption className={styles.srOnly}>Top 5 Provider Accounts</caption>`；`:18-23` 6 列 `<th scope="col">`；`:29` 行第一格 `<th scope="row">` 未动 |
| 4 | 状态双信号（StatusBar） | ✓ | ✓（未升级到 StatusIndicator） | `StatusBar.tsx:40-41` 仍用旧 `statusWrapper + statusDot + srOnly`（"healthy" / "failed"）；**未消费新的 StatusIndicator 组件** — 见 NIT-2 |
| 5 | 状态双信号（ProviderTable） | ✓ | ✓✓（升级到三信号） | `ProviderTable.tsx:32` `<StatusIndicator state={...} />` 渲染形状 + 颜色 + 文字 |
| 6 | focus-visible 全局 | ✓ | ✓ | `dashboard.module.css:12-15` 通配 selector 未动；新增的 `StatusIndicator` 没 tabIndex（不需要，纯视觉），不影响 |
| 7 | tabular-nums numeric | ✓ | ✓ | `dashboard.module.css:238-242` `.numeric` 未动；`MetricGrid.tsx` 9 处 sub-value span 全部带 `${styles.numeric}` |
| 8 | WCAG AA 对比 | ✓ | ✓ | `metricSubValue` `#8b949e on #0d1117` 未动，6.0:1 ✓；新增状态色 `#3fb950 / #d29922 / #f85149 / #8b949e on #161b22` 全部 > 4.5:1 |
| 9 | 0 inline style | ✓（1 hit LOW-C） | ✓✓ | `grep -nE "style=\{" frontend/app/dashboard/**/*.tsx` 返回 0 hits — round 2 LOW-C 闭环 |
| 10 | 0 emoji | ✓ | ✓ | `grep -nP "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]" ...` 返回 0 hits；新 `●▲■◆` 在 U+25xx 几何字符块，brief 明确允许（非 emoji） |

**回归判定**：8 项 a11y **全维持**，**新增 1 项升级**（ProviderTable 三信号），**新增 1 个未升级点**（StatusBar 还用旧 statusDot，未消费 StatusIndicator — NIT 级，因为已是双信号）。

### NIT-2: StatusBar 未消费新 StatusIndicator

- **位置**：`StatusBar.tsx:39-41`
- **现象**：StatusBar 心跳指示器仍用旧 `<span className={styles.statusDot} ${offline ? statusFailed : statusOperational}>` + 单独 sr-only text；没切到新的 `<StatusIndicator state="operational" />`。
- **影响**：a11y 不回归（仍是双信号文字 + 颜色），但**视觉一致性弱**——ProviderTable 用 `●` / `■` 形状，StatusBar 用普通圆点；两处视觉语义不齐。
- **严重度**：NIT。
- **建议**：roadmap 项；如果改，需把 StatusIndicator label prop 化或加一个 minimal 变体（不显示 label）。

---

## 5. 新引入 issues（按 P0 / MED / LOW 列）

### P0-NEW-1: StatusIndicator state 枚举与 mock 数据契约不匹配（silent-fail）
- **位置**：`StatusIndicator.tsx:5` vs `dashboard-mock.ts:31`
- **现象**：HealthState union `'cooling'` vs mock `'cooling_down'`；`ProviderTable.tsx:32` 用 `as any` 噤声 TS 错。
- **影响**：任何 `cooling_down` 账号显示 `◆ Unknown`，状态信息丢失。
- **修复**：1 行 — 统一 union（推荐改 StatusIndicator 用 `'cooling_down'` 对齐 backend / mock；或反向，**两边任选其一但必须一致**）。同时删 `ProviderTable.tsx:32` 的 `as any`。
- **是否阻塞 ship**：不阻塞（mock 数据中无 cooling_down 实例），但建议 Owner 演示前 5 分钟修。

### P0-3-RESIDUAL: lib/api/huakai.ts 注释全英 + MetricGrid 1 处 + page.tsx 1 处
- **位置**：`lib/api/huakai.ts:3,5-9,18`（block + 2 行）；`MetricGrid.tsx:15`（`// Avoid division by zero`）
- **现象**：4 处英文注释回流，违反 `feedback_chinese_comments` 全仓库精神。
- **影响**：round 3 brief P0-3 字面"page.tsx 11 处 JSX outline"已 close，但仓库整体英文注释回流。
- **修复**：5 分钟 — 翻成中文（"中央化的 API 工具，调用 HUAKAI gateway" / "确保路径以斜杠开头" / "防止除零"）。
- **严重度**：按 brief 字面 LOW；按 `feedback_chinese_comments` 严格 MED-2。

### MED-3: ProviderTable `as any` 抹掉类型安全
- **位置**：`ProviderTable.tsx:32`
- **现象**：`<StatusIndicator state={acc.health_state as any} />`
- **影响**：因 P0-NEW-1 union 不匹配，Gemini 用 `as any` 强行通过 TS。这是 typescript-strict-mode 项目里**最危险的模式**之一——把类型契约掩盖。
- **修复**：和 P0-NEW-1 一起修。
- **严重度**：MED（伴随 P0-NEW-1）。

### LOW-1: defensive `|| 0` 吃真值 0（cost_usd / cost_rmb / cache_hit_ratio / request_count）
- **位置**：`MetricGrid.tsx:36`（`(usage.cost_usd || 0).toFixed(4)`）+ `:38`、`:45`、`:67`
- **现象**：`||` 会把 `0` / `false` / `''` / `null` / `undefined` 全部 fallback 到 0。
- **影响**：实际 `cost_usd: 0`（一个新 deploy 还没产生任何成本）会显示 `$0.0000`——**逻辑上无差别**因为 fallback 也是 0；但概念上应该用 `??`（nullish coalescing）让 0 真值保留：`(usage.cost_usd ?? 0).toFixed(4)`。
- **严重度**：LOW（无 user-visible bug，但 code style 不严谨）。
- **建议**：把 4 处 `|| 0` 改 `?? 0`；`MetricGrid.tsx:14-16` `|| 0` / `|| 1` 同理（concurrencyCap || 1 防除零是对的，但 `concurrency || 0` 应是 `?? 0`）。

### LOW-2: StatusIndicator label 英文（"Operational" / "Degraded" / ...）
- **位置**：`StatusIndicator.tsx:13-17`
- **现象**：UI 显示的 label 全英文。
- **影响**：与 page.tsx 中文化 footer links（"API 密钥 / 供应商账户"）+ AlertBar 混合文案（"系统状态: CRITICAL"）不一致；Owner 演示给中文用户时英文 label 显眼。
- **修复**：5 分钟 — 把 5 个 label 改成中文 "运行中 / 降级 / 失败 / 冷却中 / 未知"。
- **严重度**：LOW（UI 文案策略 Owner 待定）。

### LOW-3: getApiUrl 缺 client/server context guard
- 详见 §3.3。

### LOW-4: getApiUrl 缺 trailing-slash 归一化
- 详见 §3.3。

### LOW-5: API schema 假设 `{ accounts: [...] }` wrapping 在 brief 里无明示
- **位置**：`page.tsx:28` `accounts = (await accountsRes.json()).accounts;`
- **现象**：Gemini 假设后端 `/admin/v1/provider-accounts` 返回 `{ accounts: ProviderAccount[] }` 包装格式；与 `/admin/v1/usage` 直接返回 `UsageSummary` 不一致。
- **影响**：后端 schema 还没实现（P3 阶段），这个假设可能要等真后端契约定下来再确认。如果后端最终返回 `ProviderAccount[]` 数组直接（不包），这行会 `Cannot read property '.accounts' of undefined` crash。
- **brief 里有无明示**：我搜了 `2026-05-12-frontend-brief-huakai-summary.md`（round 3 brief 引用的 885 行文档）—— 未读全文（外部 read-only review 时间预算限），但 round 3 brief 本身没提到这个 schema 假设。Gemini 自己 self-report 说"按 round 1 sonnet review 的 spec"，但 round 2 review 我也没明示。
- **修复**：等 P3 后端 OpenAPI schema 出来后对齐；或现在加防御 `(await accountsRes.json()).accounts ?? await accountsRes.json()` 两路兼容。
- **严重度**：LOW（mock 路径不走这行；真后端阶段必须 confirm）。

### LOW-6: dashboard.module.css `.statusDot` 旧样式留存被标 DEPRECATED
- **位置**：`dashboard.module.css:149-161`
- **现象**：`/* 旧状态点 - DEPRECATED */` 注释 + `.statusWrapper` + `.statusDot` 仍在；`StatusBar.tsx:40` 还在用。
- **影响**：dead code 风险——如果 NIT-2 把 StatusBar 也切到 StatusIndicator，这 13 行 css 就可删；现在留着合理（StatusBar 还在用），但需要追踪。
- **严重度**：LOW（housekeeping）。

---

## 6. 验证摘要（read-only grep / 计数）

| check | 命令 | 结果 |
|-------|------|------|
| inline style | `grep -nE "style=\{" frontend/app/dashboard/page.tsx frontend/app/dashboard/components/*.tsx` | **0 hits** ✓（round 2 LOW-C 闭环） |
| emoji | `grep -nP "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]" ...` | **0 hits** ✓ |
| JSX 英文注释（page.tsx） | `grep -nP "\{/\*\s*[A-Z]"` | **0 hits** ✓（round 2 LOW-A closed — 11 处 → 0） |
| 行注释英文（全 dashboard 范围） | `grep -rnP "//\s*[A-Z][a-zA-Z]"` | **3 hits**：`lib/api/huakai.ts:3,18` / `MetricGrid.tsx:15` — P0-3 residual |
| block 注释英文 | `grep -rnP "/\*\s*[A-Z][a-zA-Z]"` | **1 hit**（false-positive，css line 1 中文标题里的 HUAKAI token 命中）；huakai.ts:5-9 block comment 是英文但用 `/**` JSDoc — grep 没抓 |
| CSS 禁用手段 | `grep -niE "linear-gradient\|radial-gradient\|backdrop-filter\|box-shadow"` | **0 hits** ✓ |
| border-radius | `grep -nE "border-radius"` | 2 hits（line 159 `50%` for dot + line 255 `6px` for fallback banner — 都符合 brief "border-radius ≤ 6px"） |
| LoC 限制（≤ 200 单组件 / ≤ 350 page） | `wc -l` | page 73 ✓ / AlertBar 43 / MetricBlock 20 / MetricGrid 85 / MiniTrend 40 / ProviderTable 52 / StatusBar 51 / StatusIndicator 33 / huakai.ts 21 / css 256 / mock 115 — **全部远低于上限** ✓ |
| 总 LoC | sum | 789 LoC（round 2 690，round 3 +99 — 主要 StatusIndicator + MetricGrid + huakai.ts 拆分，page.tsx 自身 -58） |
| 6px 边框（brief "机柜美学"） | `grep -nE "6px"` | 1 hit（fallback banner border-radius） — 不是 brief 暗示的"6px 边框宽度"，brief 表述可能指 border-radius，已落 |

未执行 `npm run type-check` / `npm run build`：read-only 协议禁止；Gemini self-report 称 PASS（信任）。**但建议 Gemini 在 P0-NEW-1 修复后重跑** — 因为去掉 `as any` 后 TS 会暴露 union 错。

---

## 7. 强项汇总（round 3 进步点）

1. **StatusIndicator 三信号设计是教科书级别**：形状（U+25xx geometric）+ 颜色 + 文字 label + statusMap 状态机渲染表。比 round 2 我推荐的 outline 方案更强；对色弱 / 黑白 / 灰度场景全 robust；扩展性强（加状态只需加 map row）。
2. **page.tsx 从 131 → 73 LoC（-44%）**：通过 MetricGrid 抽出，主文件可读性显著提升。server component 的 server-fetch / fallback / render 三段结构干净。
3. **getApiUrl env var 命名选 `HUAKAI_GATEWAY_URL`（不带 NEXT_PUBLIC_）**：正确识别这是 server-only env var，不应泄露 client bundle。比 round 2 我推荐的 `BACKEND_BASE_URL` 命名更品牌化。
4. **fallback banner 重设计**：背景从 `#30363d` 改 `#1c2128`（更深更协调）+ 加 1px border + 6px radius，CSS module 化，统一视觉语言。
5. **6 个 MetricBlock 在 MetricGrid 里数学预计算干净**：`totalTokens` / `concurrencyCap || 1` / `systemLoad` 都在组件 top-level，不下沉到 JSX 里。
6. **`statusMap.unknown` fallback 防御**：`statusMap[state] || statusMap.unknown` 让非法 state 不会 crash —— 与 `as any` 配合使 P0-NEW-1 不至于 white screen，但代价是 silent fail。
7. **无回归**：round 2 8 项 a11y 全维持；round 3 brief 三条硬约束（页面布局原创 / 禁 AI 风格 / 禁 emoji）全守。

---

## 8. 评估摘要表（round 3 更新）

| 维度 | round 1 | round 2 | round 3 | 变化 |
|------|---------|---------|---------|------|
| 信息架构 | OK | OK | GOOD | MetricGrid 拆分使主文件可读性 +50% |
| 数据密度 | OK- | OK | OK | 无变化 |
| 状态信号 | NEEDS WORK | OK- | **EXCELLENT** | 形状 + 颜色 + 文字三信号 (StatusIndicator 在 ProviderTable) |
| 交互 | NEEDS WORK | OK | OK | 无显著变化 |
| 可读性 | OK- | OK | OK+ | label 中文化未做（LOW-2），但代码层可读性提升 |
| a11y | NEEDS WORK | OK | **GOOD** | 三信号升级 + 0 回归 |
| 响应式 | ACCEPTABLE | ACCEPTABLE | ACCEPTABLE | 桌面优先未动 |
| 代码质量 | NEEDS WORK | OK- | OK | 拆组件干净；但 `as any` + 中英注释不一致回流 |
| 类型安全 | OK | OK | **REGRESS** | `as any` 抹掉 P0-NEW-1 union 不匹配 |
| Clean-room compliance | 不评 | 不评 | 不评 — codex lane | — |

---

## 9. Ship to Owner: **YES**

**条件**：
1. **必须**：修 P0-NEW-1（1 行改 StatusIndicator union + 去掉 `as any`） — 5 分钟。
2. **强烈建议**：修 P0-3-RESIDUAL（4 处英文注释中文化） — 5 分钟。
3. **可选**：修 LOW-1 `|| 0` → `?? 0`（4 处） / LOW-2 label 中文化 / LOW-3-4 getApiUrl 防御 — round 4 或 P2 slice 顺手做。

**不 ship 阻塞情况**：如果 Owner 想要"零 minor issue 的真 APPROVE"，回 round 4 修上述 1+2 共 10 分钟即可纯 APPROVE。

**当前可演示状态**：mock 路径完整，UI 主体功能 a11y 全 robust，Owner 在浏览器看到的视觉与交互与 round 2 一致 + 状态指示器三信号升级。无 ship-block 缺陷。

---

## 10. 推荐 Round 4（如有）/ 后续 polish 顺序

1. **P0-NEW-1**：StatusIndicator HealthState union 改 `'cooling_down'` 对齐 mock；删 `ProviderTable.tsx:32` 的 `as any`。1 行 + 1 行。
2. **P0-3-RESIDUAL**：4 处英文注释中文化（huakai.ts block + 2 行 + MetricGrid.tsx 1 行）。3 分钟。
3. **MED-3**：与 #1 一同。
4. **LOW-1**：4 处 `|| 0` → `?? 0`（cost_usd / cost_rmb / cache_hit_ratio / request_count / in_flight）。2 分钟。
5. **LOW-2**：StatusIndicator label 中文化（"运行中" 等）。2 分钟。
6. **LOW-3**：`getApiUrl` 加 `if (typeof window !== 'undefined')` 防御。1 行。
7. **LOW-4**：`API_BASE_URL.replace(/\/$/, '')` trailing-slash 归一化。1 行。
8. **LOW-5**：等 P3 后端 OpenAPI schema 定后对齐 `.accounts` 假设。
9. **LOW-6**：等 NIT-2 StatusBar 切换到 StatusIndicator 后清 `.statusDot` 旧 CSS。
10. **NIT-2**：StatusBar 切到 StatusIndicator 变体（无 label 或 short label） — 15 分钟（需要 StatusIndicator 加可选 `compact?: boolean` prop）。

合计 round 4 完整 polish ~30 分钟；最小 ship 修 P0-NEW-1 + P0-3-RESIDUAL ~10 分钟。

---

## 11. 评审元数据

- 评审耗时：约 18 分钟
- 已读文件（read-only）：
  - `frontend/app/dashboard/page.tsx` (73 LoC)
  - `frontend/app/dashboard/components/StatusBar.tsx` (51)
  - `frontend/app/dashboard/components/MetricBlock.tsx` (20)
  - `frontend/app/dashboard/components/MetricGrid.tsx` (85) **NEW**
  - `frontend/app/dashboard/components/MiniTrend.tsx` (40)
  - `frontend/app/dashboard/components/ProviderTable.tsx` (52)
  - `frontend/app/dashboard/components/AlertBar.tsx` (43)
  - `frontend/app/dashboard/components/StatusIndicator.tsx` (33) **NEW**
  - `frontend/app/dashboard/dashboard.module.css` (256)
  - `frontend/lib/dashboard-mock.ts` (115)
  - `frontend/lib/api/huakai.ts` (21) **NEW**
  - `docs/plans/2026-05-12-gemini-p1-open-brief.md` (round 3 brief)
  - `docs/research/2026-05-12-gemini-p1-round2-review-sonnet.md` (round 2 self-comparison)
- 未读 / 不在范围：codex round 3 compliance review（独立 lane）；`docs/research/2026-05-12-frontend-brief-huakai-summary.md` 全文（885 行）；backend admin/v1 endpoints；`next.config.mjs`（与 round 3 改动无关）
- 工具：`Read` × 11，`Bash grep` × 6，零 file write 除本报告
- 跨 lane 状态：codex round 3 lane 平行进行；cross-discuss 待 Owner 调度

— Sonnet UX reviewer lane，HUAKAI 前端 P1 Round 3 Open-Brief read-only verify
