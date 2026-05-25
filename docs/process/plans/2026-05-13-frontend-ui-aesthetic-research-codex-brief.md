# HUAKAI 前端 UI 美学调研 — Codex Brief

## 触发

Owner 看 Round 10 实物 `http://localhost:3000/dashboard` 后判定：**"这个底色好丑"** — 让 codex 调研 UI 设计美学，给底色 / 整体调性的备选方案。

## 当前态（你之前自己改的，需要诚实评估）

`frontend/app/globals.css` 当前色板（HSL CSS 变量）：

```css
--background: 0 0% 100%           /* light: 纯白 */
--foreground: 222.2 84% 4.9%      /* light: 近黑文字 */
--primary: 172.6 80.4% 40.0%      /* #14b8a6 teal 青 — 沿用 sub2api */
--accent: 210 40% 96.1%           /* light: 灰蓝近白 */
--secondary: 210 40% 96.1%
--muted: 210 40% 96.1%
--border: 214.3 31.8% 91.4%

/* dark theme */
--background: 222.2 47.4% 11.2%   /* dark: 深蓝灰 */
```

也就是说当前 **light = 纯白底 + 青绿点缀**，**dark = 深蓝灰底**。这是直接照 sub2api 色板的，Owner 觉得丑。

## 你的任务

**只做研究 + 出方案，不要改任何代码**。产出一份调研报告到 `docs/research/2026-05-13-frontend-ui-aesthetic-codex.md`，含：

### 1. 现状诊断（1-2 段）

- 当前 light 模式纯白 + teal 为什么显得"丑"？
- dark 模式深蓝灰是不是太工业 / 太"开源工具"感？
- 当前色板与 HUAKAI 调性（运营控制台 / 商业 SaaS）是否匹配？

### 2. 市场参考扫一遍（4-6 个 ref，每个含截图 URL + 底色取色 + 调性标签）

不要只看 sub2api。请读：

```
docs/research/2026-05-12-frontend-brief-market-sonnet.md   (869 行 / 15 dashboards)
docs/research/2026-05-12-frontend-brief-market-codex.md    (~1100 行 / 20+ ref UIs)
```

从里面挑你认为 **底色最值得借鉴** 的 4-6 个（如 Vercel、Linear、Stripe、Helicone、Tremor、Cursor、Raycast、Notion、Plausible、Resend 之类），列：

| Ref | Light 底色 | Dark 底色 | 主色（强调） | 调性标签 |
|---|---|---|---|---|
| Vercel | #FAFAFA | #000000 | 黑/白对比 | 极简严肃 |
| Linear | ... | ... | ... | ... |

可借鉴 hex / HSL 都写。

### 3. 备选方案 3-5 套（每套必须给完整 token map）

每套含：

- **方案名**（如"温和米白 + 深墨" / "纯黑暗系 + 紫强调" / "灰玻璃 + 蓝绿混" 等）
- **light 模式**：background / card / foreground / muted / border 5 个 token 的 hex 或 HSL
- **dark 模式**：同上 5 个 token
- **primary 强调色**：是否保留 teal / 换成什么 / 为什么
- **应用场景**：HUAKAI 控制台运营场景里这套是否合适，为什么
- **风险**：可读性 / WCAG 对比度 / 与现有组件 (shadcn-ui Card/Badge/Table) 适配

### 4. 推荐 1 套 + 理由（300 字内）

明确推荐其中 1 套作为 Round 11 默认。给 3-5 条理由。

### 5. 实施提示

如 Owner 选了某套，可直接把 token map 复制进 `frontend/app/globals.css` 的 `:root` 和 `.dark` 块——给出确切的 HSL 字符串（如 `--background: 30 8% 96%`），方便实施者 1 分钟改完。

### 6. 不在范围

- 不改 layout / 组件结构（只动色）
- 不改 typography（字体）
- 不改 spacing / radius / shadow
- 不读 sub2api decomp 1274 行（你之前读过，参考其 teal 色板即可）
- 不需要 npm install / dev server 起 — 纯研究

## 防死提示

- 第一件事就 echo stub 到 `/tmp/codex-ui-aesthetic-research.txt`
- 每完成一节就 `>>` 追加进度
- 调研可以读 ~/refs/ 下的项目 README / config（若有底色 token），不要进入大文件深挖

## 不变约束

- clean-room: 你可以读 sub2api / new-api / portkey 等参考项目的色板配置（公开 token，不算源码），但不要把整段 Vue / React 代码复制到产出文档
- 中文报告 + 中文表头
- AI emoji 禁、chatbot bubble 禁

直接开始。
