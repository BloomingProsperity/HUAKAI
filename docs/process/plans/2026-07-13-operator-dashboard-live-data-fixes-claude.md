# 2026-07-13 Operator 首页大屏真数据缺陷修复（Claude 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “修 HUAKAI operator 首页大屏(frontend/src/features/dashboard/,你刚按 spec 重构的)3 个亲检缺陷,真数据栈 5178/8084(admin@huakai.ai/Huakai#2026dev/租户1)。中文注释报告,不 commit: 1. 账号池卡显 0 但库里有 23 个真池:大数改取 GET /admin/v1/pools?tenant_id=N 列表计数(前端已有 pools api 消费点,照现有模式);可用/异常分解仍用 channel-health/summary by_state,无健康数据时分解徽标显\"未上报\"而非 0/0。 2. 在线模型卡\"—数据暂不可用\":/v1/models 是 API-key 鉴权 session 调不到。照 frontend/src/features/dashboard/DashboardMetrics.tsx 的做法退化用公开价目(listPricing)长度,label 註\"按价目模型数\"。 3. 网关可用率 0.00%:totals.requests 为 0 时不显 0.00%(误导像全挂),显\"暂无请求\"空值态(StatCard 支持的空值形态,与\"在线模型\"卡同款)。 每处改动补/改 vitest 测试(值算错必红);npx vitest run 全绿+npm run build 绿后报告。” |
| Scope | **In：**`frontend/src/features/dashboard/` 内 Operator 首页的数据访问、状态组合、纯映射与 Vitest；必要时复用 `frontend/src/features/groups/api.ts` 的 `listPools`、`frontend/src/features/models/api.ts` 的 `listPricing`，不另造重复 API 模式；以 5178/8084、租户 1 做只读真数据核验。**Out：**后端、数据库/schema、鉴权核心、计费/配额、生产部署、依赖、其他页面重构、`LICENSE`、提交 commit。 |
| Success criteria | 账号池主数取租户池列表的真实 `items.length`，租户 1 显示 23；可用/异常仅取健康汇总 `by_state`，无任何可判定健康状态时两项分解显示“未上报”，不得把列表总数减去空健康数据伪造成可用；在线模型两张相关卡均取公开价目数组长度，标签或说明明确“按价目模型数”；请求总数为 0 时网关可用率以 `—` + “暂无请求”展示，请求数大于 0 且成功率为 0 时仍必须显示 `0.00%`；每个缺陷有能因错误端点、错误字段或错误分支而失败的判别性 Vitest；`npx vitest run` 全绿，`npm run build` 全绿；不 commit。 |
| Time estimate | 约 45–75 分钟墙钟：现状与真数据基线 10–15 分钟，代码和测试 20–35 分钟，全量验证与 UI 复核 15–25 分钟；单 agent 约 1–1.5 小时。 |
| Blast radius | 主要影响 Operator 首页顶部指标、资源概览账号池卡和账号池健康待办的展示；复用现有 groups/models API，不改变服务端契约。若状态组合错误，可能把端点失败伪装成零、把未上报健康误报为正常，或因一个健康端点失败连带隐藏已成功取得的池总数。 |
| Failure modes | 见下节；核心原则是“库存总数”和“健康上报”分源、分状态，任何失败都不伪造为 0。 |
| Decision points | 当前无新增 Owner 决策点：Owner 已指定三个数据源和展示语义。若真栈返回的池列表响应并非既有 `{items: [...]}`、单页存在截断（本次租户 1 的 23 条不触发）、或公开价目含重复条目导致“模型数”口径不再等于数组长度，则停止扩大改动，携带原始响应结构（脱敏）请 Owner 决定契约/去重口径。 |

## 已核对的本地事实与实现边界

- 当前 `OperatorOverview.tsx` 把 `channel-health/summary.total` 同时当账号池库存总数和健康分解总数，因此健康表无数据时会把真实库存误报为 0。
- 现有 `groups/api.ts` 已提供 `listPools(tenantID, limit, signal)`，调用 `GET /admin/v1/pools` 并返回 `{items}`；本修复应复用它或以同一消费模式封装，避免重复端点常量与响应类型。
- 当前 Operator 首页调用 `/v1/models`；现有 `DashboardMetrics.tsx` 已通过公开 `listPricing(signal)` 并取数组长度，符合 Owner 指定的 session 可达退化口径。
- 当前 `gatewayAvailabilityStat` 无条件格式化 `success_rate`，没有先用 `totals.requests` 判断分母是否存在。
- 本工作只读 HUAKAI 内部代码、spec、测试与规则，未读取任何参考项目源码，不触发 clean-room lane guard；实现与代码注释均只描述 HUAKAI 自身行为。

## 失败模式与缓解

| 失败模式 | 缓解与判别测试 |
| --- | --- |
| 继续使用健康汇总的 `total` 作为账号池主数 | 夹具故意设置“池列表 23 条、健康汇总 `total=0`”，断言资源卡主值严格为 `23`；错误取值必红。 |
| 无健康记录时把 23 个池全部推成“可用”或显示 `0/0` | 健康 `by_state={}`（以及所有已知状态合计为 0）的夹具断言可用/异常徽标均为“未上报”；禁止用库存总数减异常数计算可用。 |
| 健康汇总失败连带让池库存卡整体不可用 | 库存与健康状态独立组合；库存成功而健康失败时仍显示主数，分解显示“未上报”。API/组件层测试覆盖该分支。 |
| 账号池异常待办在健康未上报时误报或漏掉其他待办 | 纯映射接受可选健康数据；未上报时仅不生成池健康待办，告警和账号待办仍按已知真数据生成。若实现不改待办签名，则至少确保不会将“未上报”解释为 0 个异常并宣称健康。 |
| 公开价目请求成功但仍显示旧标签或仍请求 `/v1/models` | API/组件测试断言调用 `listPricing` 对应公开端点且值为数组长度；映射测试断言卡片说明含“按价目模型数”。 |
| 价目数组长度计算错、误取某个字段或去重 | 使用长度与对象内容无关且 ID 有意重复/不同的夹具，断言 Owner 指定的“列表长度”口径；错误计算必红。 |
| 无请求误显示 `0.00%` | 构造 `requests=0` 且 `success_rate='0'`，断言 `value='—'`、提示“暂无请求”。 |
| 真正全失败的窗口也被当成“暂无请求” | 构造 `requests>0`、`success_rate='0'`，断言仍显示 `0.00%`，保证分支只看请求分母。 |
| 真栈核验泄露凭据或改变数据 | 只使用 Owner 提供的账号做登录及 GET；凭据不写入计划、测试、日志或仓库，不输出 session/admin token；不调用 POST/PATCH/DELETE。 |
| 修改越界或覆盖用户未提交工作 | 开始前检查 `git status --short` 与目标文件 diff；仅编辑本计划批准的 dashboard 文件及必要测试，保留并报告已有改动。 |

## Pre-execution checklist

1. 等待 Claude/Codex 两份独立计划完成并由主 agent 对比 agreements、conflicts、gaps；Owner 批准合成计划后才开始实现。
2. 重新读取 `AGENTS.md`、`docs/RULES.md`、本计划及合成计划，确认 start gate、中文注释/报告、Truth-First、不 commit 和每 commit 互审规则；本任务明确不 commit，因此完成前不执行 `git add`/`git commit`。
3. 检查 `git status --short`、目标文件现有 diff，识别并保留 Owner/其他 agent 的未提交改动。
4. 读取 `OperatorOverview.tsx`、`api.ts`、`overview.ts` 及其测试；读取现有 `groups/api.ts`/types、`models/api.ts` 和 `DashboardMetrics.tsx`，确认复用接口和 AbortSignal 模式。
5. 在真数据栈确认前端 5178、后端 8084 可达；用 Owner 给定账号进入租户 1，仅做登录和 GET 基线核验。记录脱敏结果：池列表条数、健康 `by_state` 是否空、价目数组长度、usage `totals.requests`；不得记录密码、Cookie、token 或完整敏感响应。
6. 先写/调整失败测试，分别钉死池列表 23 vs 健康 0、健康未上报、公开价目长度与标签、零请求空值以及非零请求全失败；先确认测试能在旧实现上暴露缺陷。
7. 实现最小修复，不新增 runtime dependency，不动后端、schema、auth、billing、quota 或部署文件；新增/修改的注释一律中文。
8. 先跑 dashboard 定向 Vitest，再跑前端全量 `npx vitest run` 与 `npm run build`。
9. 回到 5178 以租户 1 人工复核三处：账号池主数 23 且健康未上报诚实展示、在线模型按价目计数、零请求可用率为“暂无请求”；保留只读、脱敏证据。
10. 最终检查 `git diff --check`、`git diff --stat`、`git status --short`，确认无 commit，并按 Owner Summary Rule 用中文报告测试命令和真栈核验结果。

## 具体执行顺序

1. **建立可复现基线。** 在真栈分别读取池列表、渠道健康汇总、公开价目和 usage overview，确认亲检现象与响应形状；若响应契约与现有 HUAKAI 类型不一致，先停在证据层，不猜字段。
2. **先补账号池判别测试。** API 测试钉死 `GET /admin/v1/pools?tenant_id=1` 及 signal；映射测试将库存总数和健康汇总拆开，断言 23/真实 `by_state`，并覆盖空 `by_state`/健康请求失败时的“未上报”。
3. **改账号池数据模型和接线。** 主数使用 `listPools(...).items.length`；健康分解继续消费 `channel-health/summary.by_state`。两源独立加载或以可保留部分成功的组合状态汇合，避免健康端点失败吞掉库存。`poolResource` 不再把健康汇总 `total` 当库存，也不以库存减空健康状态推导“可用”。
4. **校正相关待办语义。** 只有健康 `by_state` 有可判定上报时才计算异常池；未上报不生成虚假的 0 异常结论，同时不阻塞告警、账号等已有真数据待办。
5. **先补模型判别测试，再改接线。** 移除 Operator 首页对 `/v1/models` 的依赖，复用 `listPricing(signal)`，严格取响应数组长度；顶部和资源卡的标签/说明统一注明“按价目模型数”，不宣称在线健康状态。
6. **先补可用率边界测试，再改映射。** `totals.requests===0` 返回 StatCard 现有空值形态（`—`、提示“暂无请求”）；仅在请求数大于 0 时格式化 `success_rate`。同时保留“请求数大于 0、成功率为 0 → 0.00%”反例测试。
7. **定向验证。** 在 `frontend/` 运行 dashboard 相关 Vitest（至少 API、overview/映射及新增组件测试）；对每个新测试做一次判别性复核，确认若恢复旧端点/旧字段/旧分支会失败，而非只测“不是坏值”。
8. **全量验证。** 在 `frontend/` 运行 `npx vitest run`，随后运行 `npm run build`；任何失败先定位是否由本 diff 引入，不以跳过、放宽断言或删除测试换绿。
9. **真栈 UI 复核。** 刷新 5178 Operator 首页，核对租户 1 的三处显示及浏览器网络请求路径；若真数据与单测夹具不同，优先修正夹具/实现以匹配真实 HUAKAI 契约，禁止伪造“已验证”。
10. **收尾报告，不 commit。** 报告实际修改文件、每项展示前后、定向/全量测试与 build 结果、真栈脱敏观察、功能/clean-room/安全风险、是否有待 Owner 确认项；明确仓库仍为未提交状态。

## 计划完成态

本文件仅是 Claude 独立计划，不执行上述实现，也不修改其他文件。待 Codex 独立计划完成后，由主 agent 做差异对照并形成 Owner 可审批的合成计划。
