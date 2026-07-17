# 2026-07-13 Operator 首页真数据缺陷修复（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “修 HUAKAI operator 首页大屏(frontend/src/features/dashboard/,你刚按 spec 重构的)3 个亲检缺陷,真数据栈 5178/8084(admin@huakai.ai/Huakai#2026dev/租户1)。” |
| Scope | **包含**：`frontend/src/features/dashboard/` 内账号池总数/健康分解、价目模型数、零请求可用率空态的接线与纯映射测试；必要时小幅调整共享卡片测试。**不包含**：后端、数据库、鉴权、计费、配额、样式重构、提交 commit。 |
| Success criteria | 账号池大数来自 `GET /admin/v1/pools?tenant_id=N` 列表长度；健康分解仍只来自 `channel-health/summary.by_state`，无健康上报时徽标明确显示“未上报”；模型数来自公开 `listPricing` 数组长度且注记“按价目模型数”；零请求时网关可用率显示与不可用卡同形的空值态；每处均有取错字段/值时必红的 Vitest；指定真栈核验通过；`npx vitest run` 与 `npm run build` 全绿。 |
| Time estimate | 约 25–45 分钟墙钟时间，单 agent 实作与验证约 40–70 分钟。 |
| Blast radius | 仅 operator 首页资源卡、顶部统计卡及其 dashboard 数据接线；错误可能造成首页数值误报或局部卡片降级，但不改变服务端数据。 |
| Failure modes | 列表响应不是预期形状：通过 API/映射测试锁定；健康 `total=0` 被误当 0/0：用非零真实池数 + 空健康状态判别；模型数组取错：锁定长度及文案；零请求仍格式化百分比：锁定 `requests=0` 分支；现有脏工作树被覆盖：只做小补丁并逐文件审 diff；真栈不可达：保留本地测试证据并如实报告。 |
| Decision points | 无需中途 Owner 决策：均为已明确授权的中风险前端小修；若发现必须改后端、鉴权核心或新增运行时依赖，则停止并请 Owner 确认。 |

## Pre-execution checklist

1. 读取 `AGENTS.md`、`docs/RULES.md` 与 `frontend-ops-ui-review` 技能，确认 Owner Start Gate 已满足。
2. 检查工作树，保存并避开 Owner/前序 agent 的未提交改动。
3. 独立读取 dashboard、现有 pools API、`listPricing`、`StatCard` 与相关测试，不读取非 MIT 参考源码。
4. 等待 Claude 独立计划，比较一致点、冲突点与互补缺口，写合并权威计划后才执行。
5. 先补判别测试并确认目标分支能锁定错误值，再实施最小代码改动。
6. 用真数据栈核验租户 1 的池列表、健康摘要、公开价目与零请求展示所需输入；不得在报告或日志中回显密码/token。
7. 跑 dashboard 定向测试、前端全量 `npx vitest run`、`npm run build`。
8. 检查最终 diff 与 `git status`，不 stage、不 commit，按 Owner Summary Rule 用中文报告。

## 具体执行顺序

1. 将账号池列表计数与健康摘要拆成独立加载态：总数只取列表 `items.length`；健康摘要成功且有上报量时才计算可用/异常，否则两个分解徽标显示“未上报”。
2. 将模型加载替换为 `listPricing(signal).then(items => items.length)`，同步模型统计与资源卡说明为“按价目模型数”。
3. 在网关可用率映射中先判断 `totals.requests === 0`，返回 `value="—"`、`hint="暂无请求"` 的诚实空值态；非零请求保持现有百分比。
4. 补 API 接线与纯映射判别测试，确保 23 池、空健康上报、价目长度/文案、零请求空态任一算错都会失败。
5. 执行真栈核验、定向测试、全量测试与构建，并记录真实结果和残余风险。
