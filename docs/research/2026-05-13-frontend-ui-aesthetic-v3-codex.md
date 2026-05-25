# 2026-05-13 HUAKAI 前端 UI 美学调研 v3 - Codex

## 1. v3 硬约束复述

本轮只做纯设计 token exercise，不重新对比 market references，不 fetch refs，不读取 sub2api decomposition。v3 的核心不是“找一个更不蓝的蓝”，而是直接退出 Owner 已否定的蓝、青、靛、紫、绿视觉区间。

本文件采用以下规则：

- primary 只允许玫红粉红、橙琥珀、棕铜焦糖、沙金、黑白深灰单色。
- background / card 不使用冷调蓝白，统一用暖白、奶白、烟灰、深棕或近黑。
- success 不使用绿色，也不复用 primary；推荐用暖灰、蓝灰或低饱和石墨，并强制配合 checkmark icon。
- warning 在橙系 / 金系 primary 方案里不再使用传统亮 amber，而改成棕黄、烟灰、低饱和暖褐，并配合 triangle icon。
- danger 在玫红 primary 方案里不使用亮红撞色，而改成深棕红 / 血色 / 低亮度 oxblood，并配合 destructive icon、确认文案和高对比文字。

## 2. 结论先行

推荐 **方案 A - 炭玫瑰 Rose Carbon**。

它的 primary 是 `#C2185B` / HSL `336.4 78% 42.7%`，dark primary 是 `#F95D91` / HSL `340 92.9% 67.1%`。两个 primary 都落在允许的玫红粉红区间，不进入 blue / teal / cyan / indigo / violet / purple / plum / magenta / fuchsia，也不进入任何绿色区间。

这套方案对 v2 两个核心风险的处理最清楚：

- primary 玫红不承担 danger。danger 改为低亮度深棕红 `#5A1A13`，视觉上更接近“危险封印 / destructive confirmation”，不是普通品牌色。
- success 不用绿色。success 改为暖石墨 `#5B5A54`，必须与 checkmark icon、成功文案和浅背景 badge 一起使用。
- warning 不用传统亮 amber。warning 使用棕金 `#9A6B16`，和 primary 在 hue、亮度、语义位置上分开。
- 四色可区分依赖的是 hue + saturation + luminance + icon，不靠单一 hue 硬扛。

## 3. Token Map 方案

HSL 统一写作 `h s% l%`，可直接转换为 shadcn / Tailwind CSS variables 的值。

### 方案 A - 炭玫瑰 Rose Carbon（推荐）

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| primary | #C2185B | 336.4 78% 42.7% | #F95D91 | 340 92.9% 67.1% |
| background | #FFF8F6 | 13.3 100% 98.2% | #130C0D | 351.4 22.6% 6.1% |
| card | #FFFFFF | 0 0% 100% | #1E1415 | 354 20% 9.8% |
| foreground | #241517 | 352 26.3% 11.2% | #FFF4F0 | 16 100% 97.1% |
| muted | #F4ECE9 | 16.4 33.3% 93.5% | #2A2020 | 0 13.5% 14.5% |
| border | #E4D2CC | 15 30.8% 84.7% | #4B3836 | 5.7 16.3% 25.3% |
| success | #5B5A54 | 51.4 4% 34.3% | #C9C1B2 | 39.1 17.6% 74.3% |
| warning | #9A6B16 | 38.6 75% 34.5% | #D6A84F | 39.6 62.2% 57.5% |
| danger | #5A1A13 | 5.9 65.1% 21.4% | #D97757 | 14.8 63.1% 59.6% |

**四色综合区分证明：** primary 是高饱和玫红；success 是低饱和暖石墨，几乎不靠 hue；warning 是棕金；danger 是低亮度深棕红。四者在 light 模式下分别对应“品牌 CTA / 成功确认 / 注意 / 破坏性危险”，不会把 primary 当 danger，也不会把 success 当 primary。

**适配评估：** 这套最适合 HUAKAI 的商业 SaaS + 运营控制台定位。玫红提供明确品牌记忆，炭色和暖白底把它压回企业工作台，不会变成消费娱乐产品。中文“华凯”的气质更像“稳、亮、有经营感”，Rose Carbon 比橙色更有记忆，比黑白更有品牌边界。

**风险 + 不适合场景：** 如果页面大量使用红色类插画或营销素材，primary 与内容资产可能互相抢。高危操作必须固定使用 danger token、destructive icon、确认弹窗和明确文案，不能只靠颜色。

### 方案 B - 燃铜 Copper Control

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| primary | #B45309 | 26 90.5% 37.1% | #F97316 | 24.6 95% 53.1% |
| background | #FFF8F1 | 30 100% 97.3% | #120D09 | 26.7 33.3% 5.3% |
| card | #FFFCF8 | 34.3 100% 98.6% | #1E1711 | 27.7 27.7% 9.2% |
| foreground | #24150B | 24 53.2% 9.2% | #FFF3E8 | 28.7 100% 95.5% |
| muted | #F3E9DE | 31.4 46.7% 91.2% | #2B2119 | 26.7 26.5% 13.3% |
| border | #E2CFBA | 31.5 40.8% 80.8% | #4B392B | 26.2 27.1% 23.1% |
| success | #56534E | 37.5 4.9% 32.2% | #C7C0B5 | 36.7 13.8% 74.5% |
| warning | #6F5A3B | 35.8 30.6% 33.3% | #C9A36A | 36 46.8% 60.2% |
| danger | #7F1D1D | 0 62.8% 30.6% | #E26D5C | 7.6 69.8% 62.4% |

**四色综合区分证明：** primary 是高饱和燃铜橙；warning 故意降饱和，转为烟熏褐，不用传统亮 amber；success 是低饱和暖灰；danger 是血红低亮度。primary 与 warning 虽同属暖区，但 warning 更灰、更暗，必须配 triangle icon 和 warning label，不作为 CTA 色使用。

**适配评估：** 这套最贴“华凯”的汉字暖度和经营感，适合账号池、账单、额度、经营指标突出的 SaaS。它比蓝绿 infra 更像商业控制台，也比玫红更稳。

**风险 + 不适合场景：** 告警很多的页面要谨慎，因为 primary 与 warning 都在暖色域。若首页或仪表盘需要大量 warning badge，这套会显得偏黄褐，适合用在更偏财务、经营、账户管理的版本。

### 方案 C - 沙金 Gold Ledger

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| primary | #A16207 | 35.5 91.7% 32.9% | #F5C451 | 42.1 89.1% 63.9% |
| background | #FFF9EA | 42.9 100% 95.9% | #141008 | 40 42.9% 5.5% |
| card | #FFFCF2 | 46.2 100% 97.5% | #20180C | 36 45.5% 8.6% |
| foreground | #21180A | 36.5 53.5% 8.4% | #FFF7E1 | 44 100% 94.1% |
| muted | #F4EAD2 | 42.4 60.7% 89% | #2E2514 | 39.2 39.4% 12.9% |
| border | #E4D0A6 | 40.6 53.4% 77.3% | #544126 | 35.2 37.7% 23.9% |
| success | #4B5563 | 215 13.8% 34.1% | #C7CCD2 | 212.7 10.9% 80.2% |
| warning | #6B6258 | 31.6 9.7% 38.2% | #C4B8A6 | 36 20.3% 71% |
| danger | #8B1E1E | 0 64.5% 33.1% | #F0705F | 7 82.9% 65.7% |

**四色综合区分证明：** primary 是高饱和沙金；success 使用低饱和蓝灰作为状态 token，不参与品牌；warning 是暖烟灰，不再是金黄；danger 是深血色。primary 与 warning 的 hue 虽接近暖区，但 warning 饱和度只有 `9.7%`，在视觉上更像操作提示背景，不像品牌 CTA。

**适配评估：** 适合把 HUAKAI 做成“经营账本 / 用量账本 / 账户资产”的方向。沙金让产品有资产管理和商业沉稳感，适合账单、额度、消耗、充值、成本等模块。

**风险 + 不适合场景：** 大面积使用会有“财务系统”感，不适合强调技术中台、路由调度、开发者工具的版本。success 使用蓝灰，虽然不是 primary，但如果 Owner 希望全系统零蓝色，这套要替换为方案 A 的暖石墨 success。

### 方案 D - 焦糖 Umber Console

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| primary | #8A5A2B | 29.7 52.5% 35.5% | #C58B5A | 27.5 48% 56.3% |
| background | #FAF6F0 | 36 50% 96.1% | #100D0B | 24 18.5% 5.3% |
| card | #FFFCF7 | 37.5 100% 98.4% | #1C1612 | 24 21.7% 9% |
| foreground | #211812 | 24 29.4% 10% | #F9F1E8 | 31.8 58.6% 94.3% |
| muted | #F0E7DC | 33 40% 90.2% | #2A211B | 24 21.7% 13.5% |
| border | #D8C7B3 | 32.4 32.2% 77.5% | #4C3A2C | 26.2 26.7% 23.5% |
| success | #5F6368 | 213.3 4.5% 39% | #C4CDD8 | 213 20.4% 80.8% |
| warning | #6F6254 | 31.1 13.8% 38.2% | #CBBBA5 | 34.7 26.8% 72.2% |
| danger | #8A1C1C | 0 66.3% 32.5% | #E36C5B | 7.5 70.8% 62.4% |

**四色综合区分证明：** primary 是中饱和焦糖棕；success 是低饱和蓝灰；warning 是低饱和暖灰褐；danger 是深血色。primary 比 warning 更有品牌色浓度，warning 更像提示外壳；success 与 warning 靠冷暖和 icon 区分。

**适配评估：** 这套最像成熟 B2B 内控台，适合长时间盯数据、筛选账号、查看日志和审计。它不会给用户强烈“营销产品”感，更像可靠的经营系统。

**风险 + 不适合场景：** 品牌记忆比玫红弱。棕色体系如果插图、图表、badge 都跟着偏棕，容易显旧；图表色必须另设多 hue 辅助色，不能只从棕色 scale 里取。

### 方案 E - 暖黑白 Mono Seal

| token | light hex | light HSL | dark hex | dark HSL |
|---|---:|---:|---:|---:|
| primary | #1A1A1A | 0 0% 10.2% | #F2F2F2 | 0 0% 94.9% |
| background | #FAF8F3 | 42.9 41.2% 96.7% | #0D0C0B | 30 8.3% 4.7% |
| card | #FFFFFF | 0 0% 100% | #171615 | 30 4.5% 8.6% |
| foreground | #181716 | 30 4.3% 9% | #F5F3EF | 40 23.1% 94.9% |
| muted | #F0EEE8 | 45 21.1% 92.5% | #242321 | 40 4.3% 13.5% |
| border | #DDD8CD | 41.2 19% 83.5% | #3A3732 | 37.5 7.4% 21.2% |
| success | #4B5563 | 215 13.8% 34.1% | #C7CCD2 | 212.7 10.9% 80.2% |
| warning | #8A6B2E | 39.8 50% 36.1% | #D6A84F | 39.6 62.2% 57.5% |
| danger | #7F1D1D | 0 62.8% 30.6% | #EF7A6A | 7.2 80.6% 67.6% |

**四色综合区分证明：** primary 是无色相黑白；success 是低饱和蓝灰；warning 是棕金；danger 是深血色。primary 与三种状态色不存在 hue 抢占，因为 primary sat 为 `0%`。

**适配评估：** 这套适合白标、企业内控、审计优先的 HUAKAI 版本。它最稳，也最不容易被 Owner 再次判定为蓝、紫或绿。品牌识别不靠颜色，而靠以下系统语言补足：更紧凑的 8px grid、方正 iconography、左侧导航强信息层级、中文“华凯”字标、关键 CTA 的实心黑白反差、微暖背景 tint。

**风险 + 不适合场景：** 品牌记忆仍然弱。没有强 logo、强图标系统和稳定 spacing 时，会靠近通用 SaaS / Notion / Vercel 风格，不适合作为默认首发主题，但适合企业白标或 high-control 模式。

## 4. 推荐方案与理由

推荐 **方案 A - 炭玫瑰 Rose Carbon**。

1. **完全避开 Owner 明确否定区间。** primary light `336.4°`、dark `340°`，不是蓝、青、靛、紫、绿，也不贴近 v1 的 indigo 或 v2 的华紫。
2. **状态色冲突最少。** success 是暖石墨，warning 是棕金，danger 是深棕红；primary 不复用任何状态语义。
3. **品牌记忆强于黑白，运营风险低于橙/金。** 橙系和金系天然容易撞 warning；黑白缺记忆。Rose Carbon 在两者之间更均衡。
4. **符合 HUAKAI 中文商业 SaaS 调性。** 暖白底 + 炭色结构 + 玫红主色，比冷色 infra 更有“华凯”的品牌温度，同时仍适合账号、路由、账单、审计等密集控制台。
5. **后续实施成本可控。** 只需要把 primary scale、ring、CTA、chart emphasis、status badge token 化；danger 可以映射到 destructive，success/warning 作为新增语义变量接入。

## 5. 可粘贴 globals.css 片段

以下片段按方案 A 生成，包含 primary、background、card、foreground、muted、border，以及 success / warning / danger / destructive。success 不能单独靠颜色表达，组件层应配 checkmark icon；warning 应配 triangle icon；danger 应配 destructive icon 与确认文案。

```css
@layer base {
  :root {
    --background: 13.3 100% 98.2%; /* #FFF8F6 */
    --foreground: 352 26.3% 11.2%; /* #241517 */

    --card: 0 0% 100%; /* #FFFFFF */
    --card-foreground: 352 26.3% 11.2%; /* #241517 */

    --popover: 0 0% 100%; /* #FFFFFF */
    --popover-foreground: 352 26.3% 11.2%; /* #241517 */

    --primary: 336.4 78% 42.7%; /* #C2185B */
    --primary-foreground: 0 0% 100%; /* #FFFFFF */

    --secondary: 16.4 33.3% 93.5%; /* #F4ECE9 */
    --secondary-foreground: 352 26.3% 11.2%; /* #241517 */

    --muted: 16.4 33.3% 93.5%; /* #F4ECE9 */
    --muted-foreground: 8.6 9.3% 44.1%; /* #7B6966 */

    --accent: 14 62.5% 90.6%; /* #F6DFD8 */
    --accent-foreground: 352 26.3% 11.2%; /* #241517 */

    --success: 51.4 4% 34.3%; /* #5B5A54 */
    --success-foreground: 0 0% 100%; /* #FFFFFF */

    --warning: 38.6 75% 34.5%; /* #9A6B16 */
    --warning-foreground: 13.3 100% 98.2%; /* #FFF8F6 */

    --danger: 5.9 65.1% 21.4%; /* #5A1A13 */
    --danger-foreground: 0 0% 100%; /* #FFFFFF */

    --destructive: 5.9 65.1% 21.4%; /* #5A1A13 */
    --destructive-foreground: 0 0% 100%; /* #FFFFFF */

    --border: 15 30.8% 84.7%; /* #E4D2CC */
    --input: 15 30.8% 84.7%; /* #E4D2CC */
    --ring: 336.4 78% 42.7%; /* #C2185B */
  }

  .dark {
    --background: 351.4 22.6% 6.1%; /* #130C0D */
    --foreground: 16 100% 97.1%; /* #FFF4F0 */

    --card: 354 20% 9.8%; /* #1E1415 */
    --card-foreground: 16 100% 97.1%; /* #FFF4F0 */

    --popover: 354 20% 9.8%; /* #1E1415 */
    --popover-foreground: 16 100% 97.1%; /* #FFF4F0 */

    --primary: 340 92.9% 67.1%; /* #F95D91 */
    --primary-foreground: 340 50% 11.8%; /* #2D0F19 */

    --secondary: 0 13.5% 14.5%; /* #2A2020 */
    --secondary-foreground: 16 100% 97.1%; /* #FFF4F0 */

    --muted: 0 13.5% 14.5%; /* #2A2020 */
    --muted-foreground: 15.6 25.7% 79.4%; /* #D8C4BD */

    --accent: 5.7 16.3% 25.3%; /* #4B3836 */
    --accent-foreground: 16 100% 97.1%; /* #FFF4F0 */

    --success: 39.1 17.6% 74.3%; /* #C9C1B2 */
    --success-foreground: 351.4 22.6% 6.1%; /* #130C0D */

    --warning: 39.6 62.2% 57.5%; /* #D6A84F */
    --warning-foreground: 351.4 22.6% 6.1%; /* #130C0D */

    --danger: 14.8 63.1% 59.6%; /* #D97757 */
    --danger-foreground: 351.4 22.6% 6.1%; /* #130C0D */

    --destructive: 14.8 63.1% 59.6%; /* #D97757 */
    --destructive-foreground: 351.4 22.6% 6.1%; /* #130C0D */

    --border: 5.7 16.3% 25.3%; /* #4B3836 */
    --input: 5.7 16.3% 25.3%; /* #4B3836 */
    --ring: 340 92.9% 67.1%; /* #F95D91 */
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
  --color-success: hsl(var(--success));
  --color-success-foreground: hsl(var(--success-foreground));
  --color-warning: hsl(var(--warning));
  --color-warning-foreground: hsl(var(--warning-foreground));
  --color-danger: hsl(var(--danger));
  --color-danger-foreground: hsl(var(--danger-foreground));
  --color-destructive: hsl(var(--destructive));
  --color-destructive-foreground: hsl(var(--destructive-foreground));
  --color-border: hsl(var(--border));
  --color-input: hsl(var(--input));
  --color-ring: hsl(var(--ring));
}
```

## 6. 落地注意事项

- 不要把 success 做成绿色，也不要把 checkmark 只靠色块表达；成功态必须是 `success token + checkmark icon + 成功文案`。
- 方案 A 的 danger 不是亮红，而是深棕红 / 血色；删除、封禁、密钥失效等 destructive 操作必须使用 danger/destructive token。
- 图表不要从 primary 单色 scale 推导。运营控制台图表应另建 chart palette，避免把玫红扩散成整屏粉色。
- 背景不要回到 `#F8FAFC`、`#F1F5F9` 这类冷蓝白；本轮使用暖白和炭黑是为了避免视觉上回到 v1。
- 黑白方案可以保留为白标 / 高控制模式，但不建议作为 HUAKAI 默认主题。
