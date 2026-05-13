# HUAKAI P1 Dashboard — Gemini Round 5（Round 4 minor closeout）

你是 HUAKAI 前端唯一 design + code owner（Gemini 3.1 Pro Preview via Vertex AI）。

## Owner 反馈（隐式：sonnet + codex Round 4 review）

Round 4 你交付的 5-file SaaS 美学重设计 Owner 没看实物前（dev server 还在 3000 端口跑），sonnet + codex 同时给 **APPROVE_WITH_MINOR_CHANGES**。

两人共同点的 5 条 MEDIUM 必须本轮修，另有几条 LOW 可顺手清。

## 本轮 必修 5 条

### MEDIUM-1：CSS 注释翻中文（违反 HUAKAI 中文注释规则）

`frontend/app/dashboard/dashboard.module.css` 至少以下 13 处英文注释 / section header：

- 第 1-2 行 `[REDESIGN] Modern SaaS Dashboard Styles`
- 第 4 行 `Inspired by Vercel...`
- 第 20 行 `Top Navigation (Header)`
- 第 94 行 `Screen-reader only`
- 第 102 行 `No hover effect since it's disabled`
- 第 167, 234, 248, 258, 263, 279, 320, 344, 357, 364 各 section 分隔

`frontend/app/dashboard/page.tsx:35` 也有 1 处英文注释。

**修法**：全部翻中文。**品牌名保留英文**（Vercel / Helicone / Linear / Stripe / Supabase 不翻），如：

```css
/* 顶部导航 — 借鉴 Vercel sticky header + Linear vertical divider */
```

### MEDIUM-2：StatusIndicator label 改双语

`frontend/app/dashboard/components/StatusIndicator.tsx:13-17` 当前：

```ts
label: 'Operational' / 'Degraded' / 'Failed' / 'Cooling Down' / 'Unknown'
```

与 MetricGrid 的双语风格（`今日成本 (Today's Cost)`）不一致。**给运营者读中文**。

**修法（推荐）**：双语形态，中文在前。

```ts
label: '正常 (Operational)' / '降级 (Degraded)' / '失败 (Failed)' / '冷却中 (Cooling Down)' / '未知 (Unknown)'
```

### MEDIUM-3：Header ping 加 AbortController + timeout

`frontend/app/dashboard/components/Header.tsx:14-25` 当前 `ping()` 每 5s 直打 `/debug/vars`，无 timeout / 无 AbortController。后端真挂起时 fetch 默认无 timeout 会无限挂；多 tab 打开会放大 QPS。

**修法**：

```ts
useEffect(() => {
  const tick = async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 5_000);
    try {
      const t0 = performance.now();
      const r = await fetch(getApiUrl('/debug/vars'), { signal: ctrl.signal });
      if (!r.ok) throw new Error('not ok');
      const t1 = performance.now();
      setLatency(Math.round(t1 - t0));
      setHealthy(true);
    } catch {
      setHealthy(false);
      setLatency(null);
    } finally {
      clearTimeout(timer);
    }
  };
  tick();
  const id = setInterval(tick, 5_000 + Math.random() * 1_000); // jitter
  return () => clearInterval(id);
}, []);
```

注意 import `getApiUrl from '../../../lib/api/huakai'` 替换硬编码 `/debug/vars`（MEDIUM-4 一起解决）。

### MEDIUM-5：AlertBar 抽 FAILED_STATES 常量

`frontend/app/dashboard/components/AlertBar.tsx:11-20` 当前字符串字面量 `'failed'` / `'degraded'` 直接比对，未用 `HealthState` 类型常量。union 扩展时静默漏统计。

**修法**：

```ts
import { HealthState } from './StatusIndicator';

const FAILED_STATES: HealthState[] = ['failed'];
const DEGRADED_STATES: HealthState[] = ['degraded', 'cooling_down'];

// 使用：
const failed = accounts.filter(a => FAILED_STATES.includes(a.health_state)).length;
const degraded = accounts.filter(a => DEGRADED_STATES.includes(a.health_state)).length;
```

### LOW 顺手（可选；但顺手做收益高）

- `dashboard.module.css:262-265` / `257-260` 硬编码十六进制色 `#f0b952` / `#ff8984` 走 `var(--color-semantic-*)` token（在 `globals.css` 加 token）
- `ProviderTable.tsx:38` 裸字符串 `'exhausted'` 改 import `QuotaStatus` union
- `Header.tsx:7` `useState(new Date())` 用 `useState<Date | null>(null)` + effect 内 setState 首值，避免 SSR hydration mismatch

## globals.css / layout.tsx 越界确认

sonnet 注意到 `frontend/app/globals.css` + `frontend/app/layout.tsx` 已 modified（工作树脏），不在 Round 4 brief 声明范围。请 commit 前**用一句话解释**这是本轮顺修（如加 design token）还是历史脏残留。如是历史残留请重置。

## 锁定不变

- Next.js 14 App Router + TypeScript strict
- 自写 component / 不引第三方 UI 库
- Tailwind utility + CSS module
- 0 AI emoji（Unicode geometric shapes 仍允许）
- 0 inline style
- type-check + build 必须 PASS

## 验证（你自跑）

```bash
cd /home/codex/HUAKAI/frontend
npm run type-check < /dev/null 2>&1 | tail -3   # PASS
npm run build < /dev/null 2>&1 | tail -8        # PASS

# 中文注释守门
grep -nP '^\s*//[^/].*[A-Za-z]{4,}' frontend/app/dashboard/page.tsx frontend/app/dashboard/components/*.tsx | grep -v -E '(http|getApiUrl|HUAKAI|HealthState|ProviderAccountMock|getDashboardMock)' | head
grep -nP '^\s*/\*.*[A-Za-z]{4,}' frontend/app/dashboard/dashboard.module.css | grep -v -E '(Vercel|Helicone|Linear|Stripe|Supabase|HUAKAI)' | head   # 仅品牌名英文 OK

# Round 4 verifications 再跑一遍确认无回归
grep -rP "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{2700}-\x{27FF}\x{2B00}-\x{2BFF}]" frontend/app/dashboard/ frontend/lib/api/huakai.ts frontend/lib/dashboard-mock.ts   # 0
grep -P "style=\{" frontend/app/dashboard/page.tsx frontend/app/dashboard/components/*.tsx   # 0
```

## 输出报告

```
Round 5 — Minor changes closeout

What I changed and why:
[3-5 句话]

Files changed: [列表]

Round 4 minor changes 状态:
- MEDIUM-1 CSS 13 处英文注释: [done / list 翻译 sample 2-3 行]
- MEDIUM-2 StatusIndicator label 双语: [done / 5 个 label 实际文本]
- MEDIUM-3 Header ping AbortController + timeout + jitter: [done / how]
- MEDIUM-4 Header /debug/vars 走 getApiUrl: [done]
- MEDIUM-5 AlertBar FAILED_STATES 抽常量: [done / 实际形态]
- LOW-1 design token 化: [done / skipped because 原因]
- LOW-3 ProviderTable QuotaStatus union: [done / skipped 原因]
- LOW-5 Header useState SSR mismatch: [done / skipped 原因]

globals.css / layout.tsx 越界说明:
[一句话]

Verifications:
- type-check: PASS / FAIL
- build: PASS / FAIL
- 中文注释 grep 残留: 0 / N
- emoji: 0
- inline style: 0

Outstanding（你自评下一轮可继续）:
- ...
```

直接做。如有歧义按你判断走。
