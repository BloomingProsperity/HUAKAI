# 2026-05-13 HUAKAI 前端 UI 美学调研 v2 - Codex

## 1. 约束复述与结论先行

本轮的核心约束不是“换一个更深的蓝”，而是 HUAKAI 必须从 sub2api 的蓝绿、teal、cyan、indigo、偏蓝 violet 视觉区间里完全退出。这里的 primary / brand 色只从暖紫、橙琥珀、玫红、偏黄苔绿、黑白单色里选，background / card / muted / border 也避免 `#F8FAFC` 这类冷调蓝白。

结论：我推荐 **方案 A - 华紫 Plum Console**。它的 primary 是 `#A21CAF` / HSL `294.7 72.4% 39.8%`，是明确偏玫紫的暖紫，不是 indigo；状态色仍然保留 success green、warning amber、danger red，不牺牲运营台高频状态识别。

## 2. v1 承接与本轮排除

v1 的 indigo A 虽然比 teal 更商业，但仍属于蓝紫视觉家族，Owner 已明确否定。v1 的 deep blue、electric blue、cyan、soft gray-blue 也全部退出本轮候选。黑白灰可以保留一套，因为它的 primary 饱和度接近 0，不会与 sub2api 的品牌色撞车。

本轮只讨论 token map，不研究 typography / spacing / radius，不启动 dev server，不读 sub2api decomposition。当前前端 CSS 里仍存在 teal primary、primary scale、chart stroke、button 和 hover 的硬编码色；这些是后续实施风险，本研究只给可替换 token 和 globals.css 片段。

## 3. 5 套全新 token map

说明：每套表格均给出 light + dark 的 hex 与 HSL。`danger` 与 `destructive` 同步，是为了兼容 shadcn/Tailwind 当前命名；`success` / `warning` / `danger` 是独立状态 token，不把 primary 当状态色复用。

### 方案 A - 华紫 Plum Console（推荐）
- 为什么 HUAKAI 该选这套：以 294.7° 暖紫作为主色，彻底离开 blue/teal/indigo；状态色保留绿/琥珀/红，运营含义不被品牌色占用。
- HUAKAI 视角适配度：最适合 HUAKAI 的商业 SaaS 工作台定位：既有中文品牌的温度，又不会变成开源 infra 的冷色监控屏。适合账号池、路由、用量、审计同时存在的密集界面。
- 风险 / 不适合的场景：紫色饱和度过高会有 AI 工具/创作者产品感，落地时要用暖白底和低饱和边框压住。

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| background | #fdf8fd | 300 55.6% 98.2% | #160815 | 304.3 46.7% 5.9% |
| foreground | #251124 | 303 37% 10.6% | #fff3fe | 305 100% 97.6% |
| card | #fffafe | 312 100% 99% | #221021 | 303.3 36% 9.8% |
| card-foreground | #251124 | 303 37% 10.6% | #fff3fe | 305 100% 97.6% |
| popover | #fffafe | 312 100% 99% | #221021 | 303.3 36% 9.8% |
| popover-foreground | #251124 | 303 37% 10.6% | #fff3fe | 305 100% 97.6% |
| primary | #a21caf | 294.7 72.4% 39.8% | #e879f9 | 292 91.4% 72.5% |
| primary-foreground | #ffffff | 0 0% 100% | #2b142a | 302.6 36.5% 12.4% |
| secondary | #f6e7f5 | 304 45.5% 93.5% | #351933 | 304.3 35.9% 15.3% |
| secondary-foreground | #351933 | 304.3 35.9% 15.3% | #fff3fe | 305 100% 97.6% |
| muted | #f8edf6 | 310.9 44% 95.1% | #2b142a | 302.6 36.5% 12.4% |
| muted-foreground | #7a5e75 | 310.7 13% 42.4% | #d7afd2 | 307.5 33.3% 76.5% |
| accent | #f4d9f2 | 304.4 55.1% 90.4% | #4d264a | 304.6 33.9% 22.5% |
| accent-foreground | #351933 | 304.3 35.9% 15.3% | #fff3fe | 305 100% 97.6% |
| border | #eccdea | 303.9 44.9% 86.5% | #4d264a | 304.6 33.9% 22.5% |
| input | #eccdea | 303.9 44.9% 86.5% | #4d264a | 304.6 33.9% 22.5% |
| ring | #a21caf | 294.7 72.4% 39.8% | #e879f9 | 292 91.4% 72.5% |
| success | #16a34a | 142.1 76.2% 36.3% | #4ade80 | 141.9 69.2% 58% |
| success-foreground | #ffffff | 0 0% 100% | #052e16 | 144.9 80.4% 10% |
| warning | #d97706 | 32.1 94.6% 43.7% | #fbbf24 | 43.3 96.4% 56.3% |
| warning-foreground | #211a14 | 27.7 24.5% 10.4% | #422006 | 26 83.3% 14.1% |
| danger | #dc2626 | 0 72.2% 50.6% | #f87171 | 0 90.6% 70.8% |
| danger-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |
| destructive | #dc2626 | 0 72.2% 50.6% | #f87171 | 0 90.6% 70.8% |
| destructive-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |

### 方案 B - 铜橙 Amber Ops
- 为什么 HUAKAI 该选这套：橙铜色和“华凯”的中文商业温度贴合，区别于开源 infra 常见的蓝绿冷调。
- HUAKAI 视角适配度：适合把 HUAKAI 做成偏商业、偏经营系统的产品，品牌暖度最强，中文名和橙铜主色天然顺。
- 风险 / 不适合的场景：warning 与橙系品牌接近，告警密集页要靠图标、文字和背景面区分，不适合把 warning 当唯一视觉信号。

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| background | #fff8f1 | 30 100% 97.3% | #18100a | 25.7 41.2% 6.7% |
| foreground | #26160c | 23.1 52% 9.8% | #fff7ed | 33.3 100% 96.5% |
| card | #ffffff | 0 0% 100% | #21160e | 25.3 40.4% 9.2% |
| card-foreground | #26160c | 23.1 52% 9.8% | #fff7ed | 33.3 100% 96.5% |
| popover | #ffffff | 0 0% 100% | #21160e | 25.3 40.4% 9.2% |
| popover-foreground | #26160c | 23.1 52% 9.8% | #fff7ed | 33.3 100% 96.5% |
| primary | #c2410c | 17.5 88.3% 40.4% | #fb923c | 27 96% 61% |
| primary-foreground | #ffffff | 0 0% 100% | #431407 | 13 81.1% 14.5% |
| secondary | #f8eadc | 30 66.7% 91.8% | #342216 | 24 40.5% 14.5% |
| secondary-foreground | #342216 | 24 40.5% 14.5% | #fff7ed | 33.3 100% 96.5% |
| muted | #fbf1e6 | 31.4 72.4% 94.3% | #2a1c12 | 25 40% 11.8% |
| muted-foreground | #7b6654 | 27.7 18.8% 40.6% | #d6bea9 | 28 35.4% 75.1% |
| accent | #f3dcc6 | 29.3 65.2% 86.5% | #4b3322 | 24.9 37.6% 21.4% |
| accent-foreground | #342216 | 24 40.5% 14.5% | #fff7ed | 33.3 100% 96.5% |
| border | #ead5bf | 30.7 50.6% 83.3% | #4b3322 | 24.9 37.6% 21.4% |
| input | #ead5bf | 30.7 50.6% 83.3% | #4b3322 | 24.9 37.6% 21.4% |
| ring | #c2410c | 17.5 88.3% 40.4% | #fb923c | 27 96% 61% |
| success | #16a34a | 142.1 76.2% 36.3% | #4ade80 | 141.9 69.2% 58% |
| success-foreground | #ffffff | 0 0% 100% | #052e16 | 144.9 80.4% 10% |
| warning | #ca8a04 | 40.6 96.1% 40.4% | #fde047 | 50.4 97.8% 63.5% |
| warning-foreground | #26160c | 23.1 52% 9.8% | #422006 | 26 83.3% 14.1% |
| danger | #be123c | 345.3 82.7% 40.8% | #fb7185 | 351.3 94.5% 71.4% |
| danger-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |
| destructive | #be123c | 345.3 82.7% 40.8% | #fb7185 | 351.3 94.5% 71.4% |
| destructive-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |

### 方案 C - 玫红 Executive Rose
- 为什么 HUAKAI 该选这套：玫红主色有强品牌记忆，适合把 HUAKAI 从“网关工具”拉到商业运营平台。
- HUAKAI 视角适配度：适合需要强差异化、强品牌识别的第一版公开演示；在导航、CTA、关键数字上会比黑白和橙色更有记忆。
- 风险 / 不适合的场景：primary 已占用红色附近，danger 必须改成燃橙/棕橙并强制配合 icon/文案，否则高危状态不如传统红直觉。

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| background | #fff7f8 | 352.5 100% 98.4% | #19090f | 337.5 47.1% 6.7% |
| foreground | #2a1117 | 345.6 42.4% 11.6% | #fff1f3 | 351.4 100% 97.3% |
| card | #ffffff | 0 0% 100% | #251019 | 334.3 39.6% 10.4% |
| card-foreground | #2a1117 | 345.6 42.4% 11.6% | #fff1f3 | 351.4 100% 97.3% |
| popover | #ffffff | 0 0% 100% | #251019 | 334.3 39.6% 10.4% |
| popover-foreground | #2a1117 | 345.6 42.4% 11.6% | #fff1f3 | 351.4 100% 97.3% |
| primary | #e11d48 | 346.8 77.2% 49.8% | #fb7185 | 351.3 94.5% 71.4% |
| primary-foreground | #ffffff | 0 0% 100% | #4c0519 | 343.1 87.7% 15.9% |
| secondary | #fbe8ec | 347.4 70.4% 94.7% | #3a1825 | 337.1 41.5% 16.1% |
| secondary-foreground | #3a1825 | 337.1 41.5% 16.1% | #fff1f3 | 351.4 100% 97.3% |
| muted | #fdf0f3 | 346.2 76.5% 96.7% | #30131e | 337.2 43.3% 13.1% |
| muted-foreground | #7f6269 | 345.5 12.9% 44.1% | #d8b4bd | 345 31.6% 77.6% |
| accent | #f5d5dc | 346.9 61.5% 89.8% | #562539 | 335.5 39.8% 24.1% |
| accent-foreground | #3a1825 | 337.1 41.5% 16.1% | #fff1f3 | 351.4 100% 97.3% |
| border | #efccd5 | 344.6 52.2% 86.9% | #562539 | 335.5 39.8% 24.1% |
| input | #efccd5 | 344.6 52.2% 86.9% | #562539 | 335.5 39.8% 24.1% |
| ring | #e11d48 | 346.8 77.2% 49.8% | #fb7185 | 351.3 94.5% 71.4% |
| success | #16a34a | 142.1 76.2% 36.3% | #4ade80 | 141.9 69.2% 58% |
| success-foreground | #ffffff | 0 0% 100% | #052e16 | 144.9 80.4% 10% |
| warning | #a16207 | 35.5 91.7% 32.9% | #facc15 | 47.9 95.8% 53.1% |
| warning-foreground | #2a1117 | 345.6 42.4% 11.6% | #422006 | 26 83.3% 14.1% |
| danger | #c2410c | 17.5 88.3% 40.4% | #fb923c | 27 96% 61% |
| danger-foreground | #ffffff | 0 0% 100% | #431407 | 13 81.1% 14.5% |
| destructive | #c2410c | 17.5 88.3% 40.4% | #fb923c | 27 96% 61% |
| destructive-foreground | #ffffff | 0 0% 100% | #431407 | 13 81.1% 14.5% |

### 方案 D - 苔绿 Lime Ledger
- 为什么 HUAKAI 该选这套：苔绿/酸橙比 emerald/teal 更偏黄，能保留“增长、通过、容量”联想但不落入 sub2api 蓝绿色。
- HUAKAI 视角适配度：适合强调容量、增长、账号池健康的产品叙事；比 teal 更生长感，和 sub2api 主色距离足够远。
- 风险 / 不适合的场景：绿色主色天然会抢 success 语义，本方案把 success 改为紫色，认知成本最高，只建议在 Owner 明确想要绿色品牌时使用。

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| background | #fbfbf2 | 60 52.9% 96.7% | #101408 | 80 42.9% 5.5% |
| foreground | #1d2111 | 75 32% 9.8% | #f8ffe9 | 79.1 100% 95.7% |
| card | #ffffff | 0 0% 100% | #171d0d | 82.5 38.1% 8.2% |
| card-foreground | #1d2111 | 75 32% 9.8% | #f8ffe9 | 79.1 100% 95.7% |
| popover | #ffffff | 0 0% 100% | #171d0d | 82.5 38.1% 8.2% |
| popover-foreground | #1d2111 | 75 32% 9.8% | #f8ffe9 | 79.1 100% 95.7% |
| primary | #65a30d | 84.8 85.2% 34.5% | #a3e635 | 82.7 78% 55.5% |
| primary-foreground | #ffffff | 0 0% 100% | #1d260d | 81.6 49% 10% |
| secondary | #eef3d6 | 70.3 54.7% 89.6% | #24300f | 81.8 52.4% 12.4% |
| secondary-foreground | #24300f | 81.8 52.4% 12.4% | #f8ffe9 | 79.1 100% 95.7% |
| muted | #f2f5e6 | 72 42.9% 93.1% | #1d260d | 81.6 49% 10% |
| muted-foreground | #697151 | 75 16.5% 38% | #c4d0a1 | 75.3 33.3% 72.4% |
| accent | #e2edbd | 73.8 57.1% 83.5% | #354719 | 83.5 47.9% 18.8% |
| accent-foreground | #24300f | 81.8 52.4% 12.4% | #f8ffe9 | 79.1 100% 95.7% |
| border | #dce7b9 | 74.3 48.9% 81.6% | #354719 | 83.5 47.9% 18.8% |
| input | #dce7b9 | 74.3 48.9% 81.6% | #354719 | 83.5 47.9% 18.8% |
| ring | #65a30d | 84.8 85.2% 34.5% | #a3e635 | 82.7 78% 55.5% |
| success | #a21caf | 294.7 72.4% 39.8% | #e879f9 | 292 91.4% 72.5% |
| success-foreground | #ffffff | 0 0% 100% | #3b0a3d | 297.6 71.8% 13.9% |
| warning | #d97706 | 32.1 94.6% 43.7% | #fbbf24 | 43.3 96.4% 56.3% |
| warning-foreground | #1d2111 | 75 32% 9.8% | #422006 | 26 83.3% 14.1% |
| danger | #dc2626 | 0 72.2% 50.6% | #f87171 | 0 90.6% 70.8% |
| danger-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |
| destructive | #dc2626 | 0 72.2% 50.6% | #f87171 | 0 90.6% 70.8% |
| destructive-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |

### 方案 E - 暖黑白 Monochrome SaaS
- 为什么 HUAKAI 该选这套：几乎无色相，彻底避开 blue/teal/indigo，适合把品牌记忆让给中文名、数据密度和交互质量。
- HUAKAI 视角适配度：最稳、最少争议，适合企业内控台和需要压低审美风险的版本；状态色也最容易保持传统语义。
- 风险 / 不适合的场景：差异化弱，容易靠近 Vercel/Notion 式通用 SaaS；如果没有强 logo 或插画资产，会显得“高级但不记名”。

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| background | #faf9f6 | 45 28.6% 97.3% | #0d0c0b | 30 8.3% 4.7% |
| foreground | #181716 | 30 4.3% 9% | #f5f4f0 | 48 20% 95.1% |
| card | #ffffff | 0 0% 100% | #171615 | 30 4.5% 8.6% |
| card-foreground | #181716 | 30 4.3% 9% | #f5f4f0 | 48 20% 95.1% |
| popover | #ffffff | 0 0% 100% | #171615 | 30 4.5% 8.6% |
| popover-foreground | #181716 | 30 4.3% 9% | #f5f4f0 | 48 20% 95.1% |
| primary | #171717 | 0 0% 9% | #f5f5f5 | 0 0% 96.1% |
| primary-foreground | #ffffff | 0 0% 100% | #171717 | 0 0% 9% |
| secondary | #efeee9 | 50 15.8% 92.5% | #242321 | 40 4.3% 13.5% |
| secondary-foreground | #242321 | 40 4.3% 13.5% | #f5f4f0 | 48 20% 95.1% |
| muted | #f3f2ee | 48 17.2% 94.3% | #1f1e1c | 40 5.1% 11.6% |
| muted-foreground | #706d66 | 42 4.7% 42% | #b9b5ab | 42.9 9.1% 69.8% |
| accent | #e7e4dc | 43.6 18.6% 88.4% | #37342f | 37.5 7.8% 20% |
| accent-foreground | #242321 | 40 4.3% 13.5% | #f5f4f0 | 48 20% 95.1% |
| border | #dedbd2 | 45 15.4% 84.7% | #37342f | 37.5 7.8% 20% |
| input | #dedbd2 | 45 15.4% 84.7% | #37342f | 37.5 7.8% 20% |
| ring | #171717 | 0 0% 9% | #f5f5f5 | 0 0% 96.1% |
| success | #16a34a | 142.1 76.2% 36.3% | #4ade80 | 141.9 69.2% 58% |
| success-foreground | #ffffff | 0 0% 100% | #052e16 | 144.9 80.4% 10% |
| warning | #d97706 | 32.1 94.6% 43.7% | #fbbf24 | 43.3 96.4% 56.3% |
| warning-foreground | #181716 | 30 4.3% 9% | #422006 | 26 83.3% 14.1% |
| danger | #dc2626 | 0 72.2% 50.6% | #f87171 | 0 90.6% 70.8% |
| danger-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |
| destructive | #dc2626 | 0 72.2% 50.6% | #f87171 | 0 90.6% 70.8% |
| destructive-foreground | #ffffff | 0 0% 100% | #450a0a | 0 74.7% 15.5% |

## 4. 推荐方案

推荐 **方案 A - 华紫 Plum Console**。

理由：

1. primary hue `294.7°`，在允许的纯紫 / 深紫范围内，视觉上明显不是 blue、teal、cyan、indigo。
2. 暖紫 + 偏紫白底色比冷白/蓝灰更贴合“华凯”的中文品牌温度，同时仍然保留商业 SaaS 工作台的克制感。
3. success / warning / danger 可以继续使用绿色、琥珀、红色传统语义，运营人员扫账号健康、告警、错误时不需要重新学习状态色。
4. 暗色模式是紫黑而不是 slate-teal，不会回到硬核监控屏或开源 infra 面板的视觉语言。
5. 和 v1 的 indigo 区分足够明确：v1 `#4F46E5` 接近 239° 蓝紫，本方案 `#A21CAF` 是 294.7° 玫紫，中间隔着完整的 violet/blue-violet 区间。

不推荐把 B/C/D 作为默认的原因：B 的 primary 和 warning 接近，C 的 primary 和 danger 语义冲突，D 的 primary 和 success 语义冲突。E 很安全，但品牌记忆不足，更适合作为企业白标主题而不是 HUAKAI 默认主题。

## 5. 可粘贴 globals.css 字符串

用途：替换 `frontend/app/globals.css` 里现有的 `:root`、`.dark`、`@theme inline` 颜色变量段。它不会自动改掉 `frontend/tailwind.config.ts` 里的 teal primary scale，也不会自动改掉组件里的 hard-coded `#14b8a6` / `#0d9488` / chart stroke；完整落地还要同步这些位置。当前已观察到的风险点包括 `frontend/app/globals.css:15`、`frontend/app/globals.css:125`、`frontend/app/globals.css:153`、`frontend/app/globals.css:196`、`frontend/app/globals.css:236`、`frontend/tailwind.config.ts:36`、`frontend/components/dashboard/TrendChart.tsx:49`。

```css
@layer base {
  :root {
    --background: 300 55.6% 98.2%; /* #fdf8fd */
    --foreground: 303 37% 10.6%; /* #251124 */

    --card: 312 100% 99%; /* #fffafe */
    --card-foreground: 303 37% 10.6%; /* #251124 */

    --popover: 312 100% 99%; /* #fffafe */
    --popover-foreground: 303 37% 10.6%; /* #251124 */

    --primary: 294.7 72.4% 39.8%; /* #a21caf */
    --primary-foreground: 0 0% 100%; /* #ffffff */

    --secondary: 304 45.5% 93.5%; /* #f6e7f5 */
    --secondary-foreground: 304.3 35.9% 15.3%; /* #351933 */

    --muted: 310.9 44% 95.1%; /* #f8edf6 */
    --muted-foreground: 310.7 13% 42.4%; /* #7a5e75 */

    --accent: 304.4 55.1% 90.4%; /* #f4d9f2 */
    --accent-foreground: 304.3 35.9% 15.3%; /* #351933 */

    --destructive: 0 72.2% 50.6%; /* #dc2626 */
    --destructive-foreground: 0 0% 100%; /* #ffffff */

    --success: 142.1 76.2% 36.3%; /* #16a34a */
    --success-foreground: 0 0% 100%; /* #ffffff */
    --warning: 32.1 94.6% 43.7%; /* #d97706 */
    --warning-foreground: 27.7 24.5% 10.4%; /* #211a14 */
    --danger: 0 72.2% 50.6%; /* #dc2626 */
    --danger-foreground: 0 0% 100%; /* #ffffff */

    --border: 303.9 44.9% 86.5%; /* #eccdea */
    --input: 303.9 44.9% 86.5%; /* #eccdea */
    --ring: 294.7 72.4% 39.8%; /* #a21caf */

    --color-bg: #fdf8fd;
    --color-bg-muted: #fffafe;
    --color-fg: #251124;
    --color-fg-muted: #7a5e75;
    --color-fg-subtle: #a68aa0;
    --color-border: #eccdea;
    --color-border-strong: #d7afd2;
    --color-accent-blue: #a21caf; /* legacy name, intentionally not blue */
    --color-accent-green: #16a34a;
    --color-accent-purple: #a21caf;
    --color-semantic-success: #16a34a;
    --color-semantic-warning: #d97706;
    --color-semantic-warning-bg: #fff7ed;
    --color-semantic-warning-fg: #92400e;
    --color-semantic-warning-border: #fed7aa;
    --color-semantic-danger: #dc2626;
    --color-semantic-danger-fg: #b91c1c;
  }

  .dark {
    --background: 304.3 46.7% 5.9%; /* #160815 */
    --foreground: 305 100% 97.6%; /* #fff3fe */

    --card: 303.3 36% 9.8%; /* #221021 */
    --card-foreground: 305 100% 97.6%; /* #fff3fe */

    --popover: 303.3 36% 9.8%; /* #221021 */
    --popover-foreground: 305 100% 97.6%; /* #fff3fe */

    --primary: 292 91.4% 72.5%; /* #e879f9 */
    --primary-foreground: 302.6 36.5% 12.4%; /* #2b142a */

    --secondary: 304.3 35.9% 15.3%; /* #351933 */
    --secondary-foreground: 305 100% 97.6%; /* #fff3fe */

    --muted: 302.6 36.5% 12.4%; /* #2b142a */
    --muted-foreground: 307.5 33.3% 76.5%; /* #d7afd2 */

    --accent: 304.6 33.9% 22.5%; /* #4d264a */
    --accent-foreground: 305 100% 97.6%; /* #fff3fe */

    --destructive: 0 90.6% 70.8%; /* #f87171 */
    --destructive-foreground: 0 74.7% 15.5%; /* #450a0a */

    --success: 141.9 69.2% 58%; /* #4ade80 */
    --success-foreground: 144.9 80.4% 10%; /* #052e16 */
    --warning: 43.3 96.4% 56.3%; /* #fbbf24 */
    --warning-foreground: 26 83.3% 14.1%; /* #422006 */
    --danger: 0 90.6% 70.8%; /* #f87171 */
    --danger-foreground: 0 74.7% 15.5%; /* #450a0a */

    --border: 304.6 33.9% 22.5%; /* #4d264a */
    --input: 304.6 33.9% 22.5%; /* #4d264a */
    --ring: 292 91.4% 72.5%; /* #e879f9 */

    --color-bg: #160815;
    --color-bg-muted: #221021;
    --color-fg: #fff3fe;
    --color-fg-muted: #d7afd2;
    --color-fg-subtle: #a68aa0;
    --color-border: #4d264a;
    --color-border-strong: #6d3768;
    --color-semantic-warning-bg: rgba(251, 191, 36, 0.14);
    --color-semantic-warning-fg: #fbbf24;
    --color-semantic-warning-border: rgba(251, 191, 36, 0.36);
    --color-semantic-danger-fg: #f87171;
  }
}

@theme inline {
  --color-background: hsl(var(--background));
  --color-foreground: hsl(var(--foreground));
  --color-card: hsl(var(--card));
  --color-card-foreground: hsl(var(--card-foreground));
  --color-popover: hsl(var(--popover));
  --color-popover-foreground: hsl(var(--popover-foreground));
  --color-primary: hsl(var(--primary));
  --color-primary-foreground: hsl(var(--primary-foreground));
  --color-secondary: hsl(var(--secondary));
  --color-secondary-foreground: hsl(var(--secondary-foreground));
  --color-muted: hsl(var(--muted));
  --color-muted-foreground: hsl(var(--muted-foreground));
  --color-accent: hsl(var(--accent));
  --color-accent-foreground: hsl(var(--accent-foreground));
  --color-destructive: hsl(var(--destructive));
  --color-destructive-foreground: hsl(var(--destructive-foreground));
  --color-success: hsl(var(--success));
  --color-success-foreground: hsl(var(--success-foreground));
  --color-warning: hsl(var(--warning));
  --color-warning-foreground: hsl(var(--warning-foreground));
  --color-danger: hsl(var(--danger));
  --color-danger-foreground: hsl(var(--danger-foreground));
  --color-border: hsl(var(--border));
  --color-input: hsl(var(--input));
  --color-ring: hsl(var(--ring));

  --color-primary-50: #fdf4ff;
  --color-primary-100: #fae8ff;
  --color-primary-200: #f5d0fe;
  --color-primary-300: #f0abfc;
  --color-primary-400: #e879f9;
  --color-primary-500: #d946ef;
  --color-primary-600: #c026d3;
  --color-primary-700: #a21caf;
  --color-primary-800: #86198f;
  --color-primary-900: #701a75;
  --color-primary-950: #4a044e;

  --color-accent-50: #fdf8fd;
  --color-accent-100: #f8edf6;
  --color-accent-200: #f4d9f2;
  --color-accent-300: #eccdea;
  --color-accent-400: #d7afd2;
  --color-accent-500: #a68aa0;
  --color-accent-600: #7a5e75;
  --color-accent-700: #5f365a;
  --color-accent-800: #4d264a;
  --color-accent-900: #351933;
  --color-accent-950: #160815;

  --radius-sm: calc(var(--radius) - 4px);
  --radius-md: calc(var(--radius) - 2px);
  --radius-lg: var(--radius);
  --shadow-card: 0 1px 3px rgba(37, 17, 36, 0.05), 0 1px 2px rgba(37, 17, 36, 0.07);
  --shadow-card-hover: 0 10px 40px rgba(37, 17, 36, 0.10);
  --shadow-glass: 0 8px 32px rgba(37, 17, 36, 0.08);
  --shadow-glow: 0 0 20px rgba(162, 28, 175, 0.24);
}
```

## 6. 落地风险与不在范围

- 只改 token 不等于完成视觉分家。当前 `tailwind.config.ts` 还有 teal primary scale，`globals.css` component layer 还有 teal button / hover，`TrendChart` 还有 teal stroke；后续实施必须一起扫掉。
- 本轮没有改代码，没有跑构建，没有做浏览器截图，因此不声称视觉已落地，只给可执行的 token 决策。
- 本轮没有读取任何非 MIT reference source，也没有复制参考项目 UI source / schema / file structure，clean-room 风险为低。
- 安全风险低：只做视觉 token 研究，不碰 auth、billing、quota、database schema、deployment、secrets。

## Owner 摘要

做了什么：按 v2 非蓝绿约束产出 5 套全新 HUAKAI UI token map，并推荐华紫 Plum Console。改了哪些文件：目标文件为 `docs/research/2026-05-13-frontend-ui-aesthetic-v2-codex.md`，另按防死要求在 `/tmp/codex-ui-aesthetic-v2.txt` 分节追加草稿。为什么这样做：紫主色能彻底避开 sub2api 蓝绿/indigo，又不牺牲运营台状态色语义。有没有功能缩水：没有，只是研究与 token 方案。有没有 clean-room 风险：低，未读 reference source、未复制外部实现。有没有安全风险：低，未触碰安全敏感文件。需要 Owner 确认：是否采用方案 A 作为默认品牌主题；如果采用，下一步要确认是否允许同步修改 `globals.css`、`tailwind.config.ts` 和硬编码 teal 组件。下一步建议：让前端执行一个小 patch，把 teal primary scale、chart stroke、button/hover、shadow-glow 全部切到方案 A，并用截图验证 light/dark。
