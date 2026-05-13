# HUAKAI 前端 — Round 6

## 项目介绍

HUAKAI 是 MIT 协议的 AI gateway + 账号池 + 运营平台。

- **谁用**：运营者（Owner / SRE / 客服）。**不是** end-user，**不是** 开发者。
- **干啥用**：运营者自备多家 AI 订阅（Anthropic Pro / Gemini Advanced / OpenAI Plus 等），HUAKAI 把它们池化成逻辑容量，end-user 通过 HUAKAI 签发的 API Key 消费。
- **运营者需要在 dashboard 看到**：
  - 实时知道平台健康 / 容量 / 谁要爆
  - 上游账号哪个挂了、哪个限流、哪个 cooling
  - 异常告警（账号 cooling / quota exhausted / 后端心跳丢）
  - 当前指标（今日 token / 成本 / 请求数 / 延迟 p50/p95/p99 / 并发 / cache hit / 健康账号比例）
- **后续会有 P2-P5**：账号池管理、API Key 管理、计费、用户、设置。当前只做 P1 总览。

## Owner 历史发言（你过去几轮听到的）

- "禁止AI风格，禁止使用AI表情"
- "这个布局好丑。还是借鉴下别的项目吧"（撤回 layout 禁抄；现在可借鉴 Vercel / Linear / Stripe / Helicone 等）
- "所有的设计由Gemini来啊 你们只提供后端的功能给他就行了"
- "让Gemini放开思维"
- "禁止走gemini studio得路径" → 你现在跑的是 Vertex global region；3.1-pro-preview 配额耗光后 Owner 允许回滚到 gemini-2.5-pro 订阅版，本轮就是 2.5-pro

## Owner 这一轮态度

> "他做前端的时候，你继续完善后端"

Owner 已经看到 Round 4 的实物（dev server 在 :3000）。他没列具体要改什么。让你**自己读代码 + 启动 / 看实物 + 自己判断**什么地方该改、什么风格还能更好。

## 你的输入资源

- `frontend/app/dashboard/` 全部源码（你做了 4 轮，应该熟）
- `frontend/lib/dashboard-mock.ts`（mock 数据）
- `frontend/lib/api/huakai.ts`（API helper）
- `frontend/app/globals.css` + `frontend/app/layout.tsx`
- `docs/research/2026-05-12-frontend-brief-huakai-summary.md`（885 行 HUAKAI 完整脑图）
- `docs/research/2026-05-12-frontend-brief-market-sonnet.md` + `-codex.md`（市场参考）
- dev server 在 `:3000` 应该还跑着；浏览器看实物

## 输出报告（自由格式）

```
Round 6 — Gemini 自主打磨

你看了什么 / 启动了什么 / 决定改什么 / 为什么

Files changed: [...]

Verification: type-check / build / 你自己想跑的命令
```

直接开始。
