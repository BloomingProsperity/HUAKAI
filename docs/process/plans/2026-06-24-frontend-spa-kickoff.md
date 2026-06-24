# 前端 SPA 重建启动计划(2026-06-24)

## 背景

旧 Next.js 实验前端已移除(归档 `archive/frontend-nextjs-pre-vite`),主线 `frontend/` 干净。
《docs/frontend/2026-06-24-源码梳理与前端编写方案.md》(41 Agent 纯源码梳理产出)是 WHAT 的蓝图——
9 个功能域、每域 P0/P1/P2 模块与功能清单。本计划是 HOW:把前端从零搭起并逐切片点亮。

栈决策(已定,见 frontend-stack-decision):**React 18 + Vite 5 + TypeScript SPA**,`go:embed`
单二进制分发(沿 sub2api=Vue+Vite、new-api=React 两家 embed 静态 SPA 进单 Go 二进制的范式)。

## 反克隆设计基线(硬约束)

调研定论:"克隆味"在 6 个默认 token(主色/圆角/阴影/字体/图标/间距)而非布局。故:
- **离默认 token**:自有 `src/styles/tokens.css`(玉青主色、偏大圆角、暖灰中性、柔光阴影、中文优先字体),禁魔法色值。
- **管线即导航**:导航按中转站数据管线(账号池→路由→Key→用量计费→…)组织,非"功能清单侧栏";导航命名避开 `Sidebar.tsx`(用 `PipelineNav`)。
- **Cmd-K 签名交互**:顶栏留命令面板入口(本切片占位,后续接全局快捷导航)。
- **双形壳**:运维台 / 终端用户台双形态(后续据登录身份切换,本切片先单壳)。

## 切片序列

- **切片 0 地基(本 PR)**:Vite+React+TS 脚手架 builds green;外壳(顶栏+管线导航+布局)+ 路由骨架(8 域挂占位页)+ API 客户端基座(`lib/api.ts` fetch 封装/错误归一/混合鉴权)+ 设计 token + 控制台总览页。**不碰 Go**(embed 接线触网关 router=§6 collision,留后)。
- **切片 1+ P0 模块**:按方案文档第四节 P0 顺序逐域点亮,每域一个或多个切片:
  1. 账号中心(列表+多维筛选 → 新建向导 account-modes 驱动 → 凭据获取流 → 详情五面)
  2. 路由与池(池组渠道 → 账号路由属性 → 模型绑定+选号策略 → 渠道健康 → 选号审计)
  3. API Key(列表 → 创建+一次性明文 → 详情/编辑/撤销 → 每-Key 控制面)
  4. 用量与计费(钱包/充值/订单/兑换 + 用量统计)
  5. 用户租户 / 6. 模型定价 / 7. 系统 / 8. 安全审计
- **embed 切片(Owner-gated 部署前置)**:`backend/internal/webui` go:embed dist + 网关 router 接线(no-op by default,`-tags embed` 才编入),清洁重做不并旧 `webui-embed-infra` 分支(那条基于移除前快照)。

## 落地纪律

每域模块切片:worktree → 读后端真实端点(源码实证,禁记忆)→ 接 `lib/api.ts` → 类型化 DTO → 组件 →
build/typecheck 绿 → 提交。clean-room:借鉴 sub2api(LGPL)/new-api(AGPL)的细颗粒 UX 一律功能性重写,
不复制其前端标识符。后端能力已就绪,前端=把已有能力暴露 + 选择性借鉴竞品 UX。

## 风险

- npm 依赖:vite 5 dev 链有 esbuild dev-server advisory(GHSA-67mh,**仅影响 `npm run dev`**,生产是
  网关服务静态 dist、无 esbuild server),不强升 vite8 破坏性大版本。
- 旧分支(feat/frontend-admin-* 等)基于移除前快照、含旧 Next.js 页,**作移植参考不直接 merge**,避免把旧栈带回。
