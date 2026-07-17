# 2026-07-13 运营总览页部件级重构（综合执行计划）

> 状态：Owner 已在任务指令中拍板 Claude 部件级施工图并要求严格执行。本计划综合 Claude 施工图与 Codex 独立计划，作为一期实现的执行依据。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “按部件级 spec 1:1 重构 HUAKAI 运营总览页（operator 首页）……Owner 已拍板。” |
| Scope | 严格实现施工图一期 B–I 页面主体：5 指标卡；资源概览与待办 8:4；趋势、模型分布、快捷入口 5:4:3；告警与审计 5:7。扩展 `StatCard`，新增 `ResourceCard`、`Donut`、`Sparkline`、`DataListTable` 及测试；扩展 dashboard 纯映射与 API 接线。明确不做：A 全站顶栏、delta、模型健康拆分、小时成功率双轴、限流优化建议、告警级别拆分、维护建议、后端/数据库/鉴权修改、外部图表库。 |
| Success criteria | `npm run build` 与全量 Vitest 通过；每个新增组件有测试；每个部件映射至少一条字段/计算变异断言；真实栈上显示上游账号 4/5 可用、审计表有行、模型分布有环；无数据一律诚实空态。 |
| Time estimate | 约 2.5–4 小时墙钟时间，约 3–5 小时 agent 有效工作量。 |
| Blast radius | operator `/` 首页及共享 `StatCard`；新增组件无既有调用方。API 误接会导致局部空态，组件不兼容会影响既有统计卡。 |
| Failure modes | 字段误读：以 HUAKAI 类型、后端 handler 与真实响应校准；漏租户：所有相关 admin query 显式带 `tenant_id`；单端点拖垮全页：按数据源独立状态；0/失败混淆：分别映射 positive 空态与 unavailable 空态；SVG 异常：覆盖空、零、单点与占比归一化；窄屏溢出：12 列桌面布局配响应式断点。 |
| Decision points | 真实响应与施工图不符时不猜；若必须改后端、鉴权、数据库、依赖或生产配置则停下请示。本轮不触发这些高风险变更。 |

## 双计划交叉结论

### 共识

- 共享组件和纯映射先行，页面最后接线。
- 每块数据独立加载、独立失败，失败不能伪装成 0。
- 复用 `apiGet` 的 admin Bearer 选择；platform admin 的 `/admin/v1/*` 请求显式带 `tenant_id`。
- SVG 自绘，设计只用 `var(--hk-*)`，快捷入口只导航。
- 先定向测试，再全量测试与 build，最后用真实栈做浏览器验收。

### Claude 施工图补足 Codex 独立计划

- 锁定 B–I 每格端点、中文文案、下钻与一期退化方式。
- 锁定待办只并告警、账号、账号池三源；不显示不存在的限流建议。
- 锁定模型分布使用 leaderboard 的 `request_count`，审计使用 audit-events。

### Codex 独立计划补足施工图

- 明确共享组件向后兼容、异常 SVG 输入测试、0 与 unavailable 分离。
- 明确工作树保护、分块失败隔离、真实 curl 响应校准和窄屏回退。
- 明确不手写凭据、不修改后端/高风险文件、不提交 commit。

### 已消解的范围张力

- 施工图组件愿景中的 `TopBar`、`DualAxisTrend`、`delta` 属二期或全站壳层，不属于 Owner 本次明确交付清单；一期只保留现有页面壳并实现页面主体。
- `StatCard` 只新增 `icon` 与 `sparkline` 槽；不预造无数据源的 delta API。
- C1 支持不传两枚徽标，以诚实呈现在线模型总数。

## 预执行检查清单

1. 读取页面、共享组件、API client、类型、路由和测试配置，确认现有工作树改动。
2. 逐端点检查 HUAKAI 前端类型和后端 JSON 形状；用真实 curl 响应校准。
3. 建立 B–I 的映射函数与测试矩阵，确保每函数至少一个辨识字段断言。
4. 实现共享组件与定向测试，再接数据层和页面。
5. 运行定向 Vitest、全量 Vitest、build。
6. Playwright 登录 5178，断言 4/5、审计行、donut SVG，并保存截图。
7. 检查 diff、中文注释、令牌、无假数、无外部依赖、未触碰高风险文件。

## 执行顺序

1. 端点与类型核验。
2. `Sparkline`、`Donut`、`ResourceCard`、`DataListTable`、兼容扩展 `StatCard`，同步组件测试。
3. `overview.ts` 一部件一映射函数及变异敏感测试。
4. dashboard API 聚合接线与独立加载状态。
5. `OperatorOverview.tsx` 按四层栅格重写，空态/下钻接齐。
6. 测试、build、真实栈浏览器验收、最终中文报告。
