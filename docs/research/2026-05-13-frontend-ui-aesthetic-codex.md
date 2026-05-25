# 2026-05-13 HUAKAI 前端 UI 美学调研 - Codex

## 1. 现状诊断

当前 `frontend/app/globals.css` 的 light 模式是 `--background: 0 0% 100%`、`--card: 0 0% 100%`、`--primary: 172.6 80.4% 40.0%`，也就是纯白页面、纯白卡片、高饱和 teal 按钮/焦点环。问题不只是“白”，而是页面底、卡片底、popover 底没有气氛差异，层级只能靠浅边框支撑；同时 `secondary` / `muted` / `accent` 都是同一个浅蓝灰，导致 UI 在大面积 dashboard 上显得平、冷、像默认模板。teal `#14b8a6` 又接近成功绿语义，容易让主操作色和健康/成功状态混在一起。依据：`frontend/app/globals.css:6-32`。

dark 模式的 `--background`、`--card` 都落在深蓝灰 `222.2 47.4% 11.2%`，`secondary`、`muted`、`border` 都落在 slate-800 附近，再配 teal，整体会偏“开源 infra 工具 / tailwind demo / 终端控制台”。HUAKAI 的目标是运营控制台 + 商业 SaaS，不应只追求技术感；更稳的方向是低饱和中性底、轻微页面/卡片分层、主强调色与语义色分离，让账号、额度、日志、账单这类高密度信息先可读，再高级。

## 2. 市场参考扫一遍

取色说明：下表的底色是基于两份 2026-05-12 market brief 对公开 dashboard / docs 截图的归纳取色，属于可借鉴的近似色，不声明为对方私有源码 token，也没有复制任何 UI 源码。参考依据包括 Vercel 的“黑白单色 + border 灰阶”、Linear 的“暗紫/暗灰/暗黑”、Stripe 的“紫色品牌 + 白底 + 灰阶”、Helicone 的“偏白底 + 灰 + 蓝紫强调”、Resend 的“黑底 + border-white/5 极细边”、Tremor 的“neutral dashboard palette with blue default”。对应证据见 `docs/research/2026-05-12-frontend-brief-market-sonnet.md:147-180,221-233` 与 `docs/research/2026-05-12-frontend-brief-market-codex.md:91-117,199-207`。

| Ref | 截图 / 参考 URL | Light 底色 | Dark 底色 | 主色（强调） | 调性标签 | 对 HUAKAI 的价值 |
|---|---|---|---|---|---|---|
| Vercel | https://vercel.com/docs/dashboard-features/overview | `#FAFAFA` / `0 0% 98%` | `#000000` / `0 0% 0%` | 黑白 + 少量语义状态 | 极简、严肃、边框分层 | 学“少即是多”：页面底不要纯白刺眼，靠边框和灰阶做层级 |
| Linear | https://linear.app/docs/conceptual-model | `#F7F8FA` / `220 23% 97.5%` | `#08090A` / `210 11% 3.5%` | `#5E6AD2` 靛紫 | keyboard-first、低噪声、暗色成熟 | 给 HUAKAI 命令面板、日志列表、drawer 提供低干扰工作台气质 |
| Stripe Dashboard | https://docs.stripe.com/dashboard/basics | `#F6F9FC` / `210 45% 97.6%` | `#0A2540` / `208 73% 14.5%`（品牌深色近似） | `#635BFF` 紫蓝 | 金融级、商业、密度高 | 高密度表格和账单/额度页适合用偏冷白 + 紫蓝强调，可信但不花 |
| Helicone | https://www.helicone.ai/ / https://us.helicone.ai/dashboard | `#F8FAFC` / `210 40% 98%` | `#0B1020` / `223 49% 8%` | 蓝/绿/紫状态色 | AI infra、观测、请求中心 | 可借鉴信息架构，不建议照抄高饱和蓝绿；HUAKAI 应更商业、更少开源工具味 |
| Resend | https://resend.com/docs/dashboard/logs/introduction | `#FFFFFF` / `0 0% 100%` | `#000000` / `0 0% 0%` | 单色 + restrained blue/green status | 黑底极简、精致、低噪声 | 暗色可借鉴极细边和微弱叠层，但不要复制大圆角玻璃软感 |
| Tremor | https://www.tremor.so/ / https://npm.tremor.so/ | `#FFFFFF` / `0 0% 100%` | `#09090B` / `240 10% 3.9%` | `#2563EB` blue-600 | KPI/chart 中性仪表盘 | KPI 卡、图表、BarList 可借鉴蓝色默认，但主 shell 仍要 HUAKAI 自己定调 |

## 3. 备选方案 3-5 套

### 方案 A - 柔和石墨 + 靛蓝（推荐）

| 模式 | background | card | foreground | muted | border |
|---|---|---|---|---|---|
| light | `#F7F8FA` / `220 23.1% 97.5%` | `#FFFFFF` / `0 0% 100%` | `#111827` / `220.9 39.3% 11%` | `#6B7280` / `220 8.9% 46.1%` | `#E2E8F0` / `214.3 31.8% 91.4%` |
| dark | `#070A0F` / `217.5 36.4% 4.3%` | `#0F141B` / `215 28.6% 8.2%` | `#F8FAFC` / `210 40% 98%` | `#94A3B8` / `215 20.2% 65.1%` | `#222B36` / `213 22.7% 17.3%` |

Primary 强调色：light 用 `#4F46E5` / `243.4 75.4% 58.6%`，dark 用 `#818CF8` / `234.5 89.5% 73.9%`。不保留 teal；把成功绿留给健康/成功状态，主操作改成商业 SaaS 更常见的 indigo。

应用场景：适合作为 Round 11 默认。账号、quota、logs、billing 都能在中性底上保持可读，indigo 对导航选中、主按钮、focus ring 足够明确。

风险：dark 模式比当前更接近黑底，需要确保图表网格线不要过暗；现有 teal 相关 `--color-primary-*` 色阶若继续使用，会出现双主色，需要同步清理或保留为旧兼容。

### 方案 B - Vercel 式黑白灰

| 模式 | background | card | foreground | muted | border |
|---|---|---|---|---|---|
| light | `#FAFAFA` / `0 0% 98%` | `#FFFFFF` / `0 0% 100%` | `#09090B` / `240 10% 3.9%` | `#71717A` / `240 3.8% 46.1%` | `#E4E4E7` / `240 5.9% 90%` |
| dark | `#000000` / `0 0% 0%` | `#0A0A0A` / `0 0% 3.9%` | `#FAFAFA` / `0 0% 98%` | `#A1A1AA` / `240 5% 64.9%` | `#27272A` / `240 3.7% 15.9%` |

Primary 强调色：light 用 `#18181B` / `240 5.9% 10%`，dark 用 `#FAFAFA` / `0 0% 98%`，几乎单色。teal 全部降级为语义/图表色。

应用场景：适合想把 HUAKAI 做得最克制、最“高端工具”的路线，特别适合项目/租户切换、settings、API key 管理。

风险：品牌记忆点弱；主按钮和普通文本都接近黑白，需要依赖布局、icon、状态色，否则第一眼会像 Vercel/shadcn 默认组合。

### 方案 C - 温和石英白 + 深海蓝

| 模式 | background | card | foreground | muted | border |
|---|---|---|---|---|---|
| light | `#F7F5F0` / `42.9 30.4% 95.5%` | `#FFFFFF` / `0 0% 100%` | `#1C1917` / `24 9.8% 10%` | `#78716C` / `25 5.3% 44.7%` | `#E7E5E4` / `20 5.9% 90%` |
| dark | `#11100E` / `40 9.7% 6.1%` | `#1A1815` / `36 10.6% 9.2%` | `#FAFAF9` / `60 9.1% 97.8%` | `#A8A29E` / `24 5.4% 63.9%` | `#2D2A24` / `40 11.1% 15.9%` |

Primary 强调色：light 用 `#365A8C` / `214.9 44.3% 38%`，dark 用 `#93C5FD` / `211.7 96.4% 78.4%`。不保留 teal，改成偏沉稳的蓝。

应用场景：适合 Owner 觉得纯白刺眼、希望更“商业后台 / 财务系统 / Notion-ish”的路线，账单、额度、审计页面会显得温和。

风险：暖底如果用多了会偏 beige/文档工具，不适合强调“AI Gateway infra”的硬度；需要用冷色图表和深色导航压住。

### 方案 D - 深墨 Ops + 电蓝

| 模式 | background | card | foreground | muted | border |
|---|---|---|---|---|---|
| light | `#F8FAFC` / `210 40% 98%` | `#FFFFFF` / `0 0% 100%` | `#0F172A` / `222.2 47.4% 11.2%` | `#64748B` / `215.4 16.3% 46.9%` | `#E2E8F0` / `214.3 31.8% 91.4%` |
| dark | `#05070A` / `216 33.3% 2.9%` | `#0B0F14` / `213.3 29% 6.1%` | `#F8FAFC` / `210 40% 98%` | `#94A3B8` / `215 20.2% 65.1%` | `#1F2937` / `215 27.9% 16.9%` |

Primary 强调色：light 用 `#2563EB` / `221.2 83.2% 53.3%`，dark 用 `#60A5FA` / `213.1 93.9% 67.8%`。teal 不保留；主色从 AI infra 的青绿换成云控制台常见蓝。

应用场景：适合 dark-first 运维中心，日志、provider health、routing、alerts 会显得更像专业监控工具。

风险：如果默认 dark，会继续有“硬核 infra”感；若 Owner 已经反感深蓝灰，D 只能作为 dark-first 备选，不建议做 light 默认。

### 方案 E - 软灰蓝 + Cyan Focus

| 模式 | background | card | foreground | muted | border |
|---|---|---|---|---|---|
| light | `#F6F8FB` / `216 38.5% 97.5%` | `#FFFFFF` / `0 0% 100%` | `#0B1220` / `220 48.8% 8.4%` | `#667085` / `220.6 13.2% 46.1%` | `#DDE3EA` / `212.3 23.6% 89.2%` |
| dark | `#080D12` / `210 38.5% 5.1%` | `#0F1720` / `211.8 36.2% 9.2%` | `#F8FAFC` / `210 40% 98%` | `#98A2B3` / `217.8 15.1% 64.9%` | `#243040` / `214.3 28% 19.6%` |

Primary 强调色：light 用 `#0284C7` / `200.4 98% 39.4%`，dark 用 `#38BDF8` / `198.4 93.2% 59.6%`。不是保留 teal，而是转到 sky/cyan，和 success green 拉开。

应用场景：适合 observability、logs、实时请求探索页，会比 teal 更清爽，也比 indigo 更技术。

风险：如果图表、状态、链接也都用 blue/cyan，容易变成单一冷色系；需要强制 success/warning/danger 独立。

## 4. 推荐 1 套 + 理由

推荐方案 A“柔和石墨 + 靛蓝”作为 Round 11 默认。理由：1. light 背景从纯白降到 `#F7F8FA`，卡片仍为白，层级立刻清楚；2. indigo 主色和 success green 分离，降低状态误读；3. dark 从 slate-teal 开源感改成近黑石墨，更像成熟 SaaS 工作台；4. 与 shadcn Card / Badge / Table 的 token 结构天然兼容；5. primary `#4F46E5` 配白字对比度约 6.29:1，普通按钮文字可过 WCAG AA。

## 5. 实施提示

如 Owner 选方案 A，可以先只替换 `frontend/app/globals.css` 的 `:root` 与 `.dark` 中下列 shadcn token。注意：当前文件还有 `--color-bg`、`--color-accent-*` 这类自定义 token；如果页面组件仍直接引用这些 hex，也应同步成同一调性，否则会出现“新 indigo + 旧 teal”的双主色。

```css
:root {
  --background: 220 23.1% 97.5%;
  --foreground: 220.9 39.3% 11%;

  --card: 0 0% 100%;
  --card-foreground: 220.9 39.3% 11%;

  --popover: 0 0% 100%;
  --popover-foreground: 220.9 39.3% 11%;

  --primary: 243.4 75.4% 58.6%; /* #4F46E5 */
  --primary-foreground: 0 0% 100%;

  --secondary: 210 40% 96.1%;
  --secondary-foreground: 220.9 39.3% 11%;

  --muted: 210 40% 96.1%;
  --muted-foreground: 220 8.9% 46.1%;

  --accent: 210 40% 96.1%;
  --accent-foreground: 220.9 39.3% 11%;

  --destructive: 0 84.2% 60.2%;
  --destructive-foreground: 0 0% 100%;

  --border: 214.3 31.8% 91.4%;
  --input: 214.3 31.8% 91.4%;
  --ring: 243.4 75.4% 58.6%;

  --color-bg: #f7f8fa;
  --color-bg-muted: #ffffff;
  --color-fg: #111827;
  --color-fg-muted: #6b7280;
  --color-fg-subtle: #9ca3af;
  --color-border: #e2e8f0;
  --color-border-strong: #cbd5e1;
  --color-accent-blue: #4f46e5;
  --color-accent-green: #16a34a;
  --color-accent-purple: #7c3aed;
}

.dark {
  --background: 217.5 36.4% 4.3%;
  --foreground: 210 40% 98%;

  --card: 215 28.6% 8.2%;
  --card-foreground: 210 40% 98%;

  --popover: 215 28.6% 8.2%;
  --popover-foreground: 210 40% 98%;

  --primary: 234.5 89.5% 73.9%; /* #818CF8 */
  --primary-foreground: 217.5 36.4% 4.3%;

  --secondary: 213.8 27.6% 11.4%;
  --secondary-foreground: 210 40% 98%;

  --muted: 213.8 27.6% 11.4%;
  --muted-foreground: 215 20.2% 65.1%;

  --accent: 213.8 27.6% 11.4%;
  --accent-foreground: 210 40% 98%;

  --destructive: 0 62.8% 30.6%;
  --destructive-foreground: 210 40% 98%;

  --border: 213 22.7% 17.3%;
  --input: 213 22.7% 17.3%;
  --ring: 234.5 89.5% 73.9%;

  --color-bg: #070a0f;
  --color-bg-muted: #0f141b;
  --color-fg: #f8fafc;
  --color-fg-muted: #94a3b8;
  --color-fg-subtle: #64748b;
  --color-border: #222b36;
  --color-border-strong: #334155;
  --color-accent-blue: #818cf8;
  --color-accent-green: #22c55e;
  --color-accent-purple: #a78bfa;
}
```

## 6. 不在范围

- 不改 layout / 组件结构；本报告只讨论底色、卡片色、文字色、边框色、primary 强调色。
- 不改 typography；没有建议引入 Inter、Geist、Sohne、Domaine 或任何新字体。
- 不改 spacing / radius / shadow；Resend 的玻璃毛、圆角、阴影路线只作为反例边界，不进入实施建议。
- 不读 sub2api decomp 1274 行；这里只基于当前 `globals.css` 和两份 market brief 做审美判断。
- 不运行 npm install / dev server；没有启动或依赖 `http://localhost:3000/dashboard`。
- 没有读取参考项目源码，也没有复制非 MIT 项目的 UI 源码、组件结构、注释或私有 token 文件；clean-room 风险低。
