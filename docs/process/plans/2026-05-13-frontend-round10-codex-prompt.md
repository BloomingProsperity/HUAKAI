# HUAKAI 前端 — Round 10（Codex 接手 v2，scope 收窄）

## Round 9 已完成的部分（**不要重做**）

Round 9 codex 中途死掉，但**已完成**了一件事：

✅ `frontend/app/globals.css` — Tailwind v4 正确入口已搞好：
- `@import "tailwindcss";` + `@config "../tailwind.config.ts";`
- `@theme inline { ... }` — v4 CSS-first 主题 token（primary teal #14b8a6 + accent slate）
- `<a>` reset / `box-sizing` reset / body bg/font 都到位
- HSL CSS 变量 light + dark dual-theme

**这个文件不要再改**。只 read 一次理解 token 体系即可。

## Round 9 没做完的部分（**Round 10 的任务**）

❌ `frontend/app/dashboard/page.tsx` — 仍是 Round 8 残留:
- 英文 title (`Today's Cost` / `Requests` / `P95 Latency`)
- 用了 `<StatCard>` + `<TrendChart>` + shadcn `<Table>` 但**没有 sidebar/header layout 包裹**
- 6 metric cards 分成 4+2 两行（应该是单行 6 列 或 3+3 两行）
- 中文 UI 文案完全没有

❌ `frontend/app/layout.tsx` — 没有 AppLayout 包 sidebar
❌ `frontend/components/layout/{AppLayout,Sidebar,Header}.tsx` — Round 8 写的简陋版本
❌ dev server 没起来 verify
❌ type-check / build 没跑

## 你（Round 10 codex）的任务（按顺序）

### 第 1 步（必须先做）：写 stub 报告占位

```bash
echo "Round 10 进行中 - 启动于 $(date)" > /tmp/codex-frontend-round10.txt
```

**这样即使你中途死掉，stdout 还能让 Claude 看到你的进度**。每完成一个文件就 `>>` 追加日志。

### 第 2 步：read 必要文件（限定范围）

```
frontend/app/globals.css          (Round 9 已修好 — 仅 read 理解 token)
frontend/app/layout.tsx           (要改)
frontend/app/dashboard/page.tsx   (要重写)
frontend/app/dashboard/layout.tsx (要决定保留或删)
frontend/components/layout/*.tsx  (要改 — Round 8 简陋版)
frontend/components/dashboard/*.tsx (要 review，可能要改)
frontend/components/ui/*.tsx      (shadcn 4 个 — 不动)
frontend/lib/dashboard-mock.ts    (mock 数据 — 不动)
frontend/lib/utils.ts             (cn helper — 不动)
frontend/tailwind.config.ts       (v3 形态但配合 v4 import — 不动除非真有问题)
frontend/postcss.config.js        (@tailwindcss/postcss — 不动)
```

**不要 read** 1274 行的 sub2api decomp doc 完整内容 —— Round 9 已死过一次，可能就是 context 撑爆。如要参考就 grep / sed 指定行号读。

### 第 3 步：重写 4 个文件

#### a) `frontend/app/layout.tsx` —— root layout 包 AppLayout

中文 metadata + AppLayout 包 children（sidebar + 主内容两栏）。

#### b) `frontend/components/layout/Sidebar.tsx`

- 顶部 logo "HUAKAI" + 副标题"控制台"
- 5 个 nav 项（仅 P1 Dashboard 可用，其它 disabled/灰）：
  - 总览 (Dashboard) — active
  - 账号池 (Accounts) — disabled
  - API Keys — disabled
  - 用量 (Usage) — disabled
  - 设置 (Settings) — disabled
- 底部小字版本号或环境标识
- 宽 w-64，固定左侧
- 用 `lucide-react` 图标
- 中文文案

#### c) `frontend/components/layout/Header.tsx`

顶部状态条：
- 左：时间（`new Date().toLocaleString('zh-CN')`，client component）
- 中：后端心跳（mock：绿点 + "已连接"）
- 右：用户头像占位 / 主题切换占位

#### d) `frontend/app/dashboard/page.tsx` —— 重写

完整 P1 Dashboard 总览页，**全部中文文案**：

1. **顶部状态条**（h1 + 时间 + 数据更新时间）
2. **6 个核心指标卡**（3 列 × 2 行 或 6 列单行 lg 屏，sm 屏 2 列）：
   - 今日 Token 用量 → `usage.tokens_today`
   - 今日成本 → `$usage.cost_usd`
   - 请求数 → `usage.request_count`
   - P95 延迟 → `usage.latency_p95`ms
   - 并发数 → `usage.in_flight`
   - 缓存命中率 → `usage.cache_hit_ratio` * 100%
3. **24h 趋势图**（用 `<TrendChart>` Round 8 已有的 recharts，确保 height 给死值 `h-[280px]` 避 -1 warning）
4. **Top 5 Provider Accounts 表格**（shadcn Table，中文表头）
5. **健康账号比例**（简单进度条 + "X / Y 健康"文案）

mock 数据从 `@/lib/dashboard-mock.ts` 读。

### 第 4 步：删除 Round 8 残留

```
frontend/app/dashboard/dashboard.module.css   # Round 7 残留，删
frontend/app/dashboard/components/             # Round 6/7/8 mix 残留 dir，删
frontend/app/dashboard/layout.tsx              # 重复 layout，删（root layout.tsx 包就够了）
```

### 第 5 步：启 dev server + verify

```bash
cd /home/codex/HUAKAI/frontend
rm -rf .next
nohup npx next dev -p 3000 > /tmp/next-dev-round10.log 2>&1 &
sleep 10
curl -s -o /tmp/dashboard-round10.html -w "HTTP %{http_code}\n" http://localhost:3000/dashboard
tail -30 /tmp/next-dev-round10.log
grep -oE "(bg-primary|text-primary|grid-cols-|gap-6|space-y-6|rounded-lg)[a-z0-9-]*" /tmp/dashboard-round10.html | sort -u | head -20
```

期望:
- HTTP 200
- Tailwind classes 在 rendered HTML 里出现（说明 CSS bundle 生效）
- no "0 styles applied" hint
- no chart container -1 warning（chart 给死 height）

### 第 6 步：type-check + build

```bash
cd /home/codex/HUAKAI/frontend
npm run type-check 2>&1 | tail -20
npm run build 2>&1 | tail -30
```

两个都过。

### 第 7 步：写最终报告

把 `/tmp/codex-frontend-round10.txt` 完整覆盖（中文）：

```
Round 10 — Codex 接手 P1 Dashboard（v2）

接手时 Round 9 留下的状态:
- globals.css 已完成 (Tailwind v4 正确入口)
- dashboard/page.tsx / layout.tsx / Sidebar 等仍是 Round 8 状态
- dev server 没起

What I changed:
1. frontend/app/layout.tsx - [简述]
2. frontend/components/layout/Sidebar.tsx - [简述]
3. ...

Files deleted:
- frontend/app/dashboard/dashboard.module.css
- ...

Verification:
- HTTP 200 GET /dashboard: yes/no
- Tailwind classes in HTML: [列举 5 个]
- npm run type-check: pass/fail
- npm run build: pass/fail
- chart container warning: gone/still here

如有未解决问题: [列举]

最终评估: 可继续 / 需 Round 11 / 大返工
```

## 不变约束

- Next.js 14 App Router + TypeScript strict（不换）
- 前端目录 `frontend/`
- 第三方库可引（shadcn-ui / lucide-react / recharts 已装；可加 sonner / @tanstack/react-table 等如需）
- **禁** AI emoji / chatbot 气泡 / 机器人 icon / "AI-powered" 文案
- Unicode geometric (●▲■◆○) 不算 emoji 可用
- 中文 UI 文案 + 中文注释
- inline style 禁（用 Tailwind utility class）
- clean-room CLAUDE.md #11 — 可借鉴 sub2api 模式但不复制 Vue 代码片段进 HUAKAI git

## 防死提示

Round 9 codex 大约 7 min 后中途死掉（没写报告就退出）。Round 10 防御策略：

1. **先写 stub 报告**（第 1 步 echo），死了也有记录
2. **每完成一个文件就 `>>` 追加日志**，例如:
   ```bash
   echo "[$(date)] Sidebar.tsx 完成" >> /tmp/codex-frontend-round10.txt
   ```
3. **不要一次读太多大文件** —— sub2api decomp 1274 行别整读
4. **Tool call 之间不要堆超长 plan/thought**

直接开始。不要再问我。
