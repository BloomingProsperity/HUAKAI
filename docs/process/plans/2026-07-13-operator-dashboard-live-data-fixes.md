# 2026-07-13 Operator 首页真数据缺陷修复（合成权威计划）

> 本计划由 Claude 与 Codex 两份独立计划交叉讨论后合成。Owner 原指令已明确数据源、展示语义、验证门和“不 commit”，两份计划无冲突、无新增待裁决点，按既有授权执行。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “修 HUAKAI operator 首页大屏……3 个亲检缺陷……中文注释报告,不 commit……每处改动补/改 vitest 测试(值算错必红);npx vitest run 全绿+npm run build 绿后报告。” |
| Scope | **In：**`frontend/src/features/dashboard/` 内账号池库存/健康分解、公开价目模型数、零请求可用率空态的接线、映射与测试；复用既有 groups/models API。**Out：**后端、schema、鉴权核心、计费/配额、依赖、部署、其他页面重构、`LICENSE`、commit。 |
| Success criteria | 池主数严格取租户池列表 `items.length`；健康徽标仅取 `by_state`，无上报显示“未上报”；模型卡取 `listPricing` 长度并注记“按价目模型数”；零请求显示 `—` + “暂无请求”，非零请求且零成功率仍显示 `0.00%`；各缺陷有错误值必红测试；全量 Vitest 与 build 通过；尽可能完成真栈只读核验；不 commit。 |
| Time estimate | 约 45–75 分钟墙钟，单 agent 约 1–1.5 小时。 |
| Blast radius | Operator 首页顶部指标、资源概览账号池/模型卡及健康待办组合；不改变服务端数据和契约。 |
| Failure modes | 健康总数继续冒充库存、空健康推成 0/0 或全可用、健康失败吞掉池主数、旧 `/v1/models` 仍被调用、价目长度/标签错误、零请求与全失败混淆、脏工作树被覆盖、沙箱阻断真栈 socket。缓解方式为分源独立状态、判别性夹具、最小 patch、逐文件 diff、脱敏且只读的真栈尝试及诚实报告。 |
| Decision points | 当前无；若必须改后端/鉴权/schema、加依赖，或真实响应违背现有 HUAKAI 类型，则停止并请 Owner 确认。 |

## 双计划对照

- **一致点：**复用 `listPools` 和 `listPricing`；库存与健康分源；空态不伪造 0；先补判别测试；跑全量测试和构建；不 commit。
- **冲突点：**无。
- **互补缺口：**Claude 补充了“非零请求且零成功率仍显示 `0.00%`”以及健康未上报不能阻塞其他待办；Codex 补充了健康端点失败时仍保留池库存主数。三点全部纳入执行。

## Pre-execution checklist

1. 已读取项目规则、UI 评审技能、目标代码和既有 API；不读取任何参考项目源码。
2. 已检查脏工作树；仅在既有未提交 dashboard 重构上做可审计小补丁，不覆盖其他改动。
3. 先补池列表 23 vs 健康 0、健康失败/空上报、价目长度与说明、零请求/非零全失败的判别测试。
4. 账号池库存和健康使用独立加载态；待办只消费已上报健康，不因池健康缺失阻塞其他已知待办。
5. 使用现有 AbortSignal 模式，不新增依赖；代码注释只用中文描述 HUAKAI 自身机制。
6. 跑 dashboard 定向 Vitest、前端全量 `npx vitest run`、`npm run build`、`git diff --check`。
7. 对 5178/8084 做只读脱敏核验；若沙箱禁止 socket，不伪造结果，明确记录。
8. 最终检查状态并按八项 Owner Summary Rule 中文汇报，不 stage、不 commit。

## 执行顺序

1. 增加/调整测试，使旧实现因错误数据源、错误字段或错误空态而失败。
2. Operator 首页复用 `listPools(tenantId, 200, signal)`，池主数取 `items.length`；健康摘要独立加载。
3. 池资源映射接受“库存数 + 可选健康摘要”；仅在已知健康状态合计大于 0 时计算可用/异常，否则两枚徽标显示“未上报”。
4. 待办合并改为告警、账号、池健康各自独立贡献；池健康未上报时不生成虚假池异常结论。
5. 模型接线复用 `listPricing`，取数组长度；顶部和资源卡说明明确“按价目模型数”。
6. 可用率先判断 `totals.requests`：零请求返回 `—`/“暂无请求”，其余才格式化成功率。
7. 执行定向、全量、构建、diff 与尽可能的真栈验证；修复本改动引入的失败。
8. 中文报告实际证据、风险、未验证项和下一步，不 commit。
