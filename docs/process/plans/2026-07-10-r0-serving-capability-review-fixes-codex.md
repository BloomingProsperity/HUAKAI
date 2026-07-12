# 2026-07-10 R0 serving capability 提交前 review 修复（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “【R0 修复轮】按提交前 review 清 2 条 S1 + 2 条 S2（代码侧）”；本工作单实际指派代码问题 F2、F3、F5，并明确禁 commit、禁 push。 |
| Scope | **包含**：为 serving capability contract 增加显式车道并按车道计算 blocking 站点；逐项核对全部 released family；收窄 disabled provider 的闭合旁路；拒绝非 canonical family 写入；补充判别测试；仅更新 `AT-R0-MATRIX-001` 的 F2 期望；新增 pre-existing 路由缺口 deferred 记录。**不包含**：F1/F4 文档修复、CAP-003/004/005 状态列、pool SQL、数据库 schema、路由算法、commit、push。 |
| Success criteria | `replicate_image` 按 image 车道在真实所需站点齐备时为 ready，chat 三站点仍报告但不阻断；所有 released family 均有核查证据；contract-only 且未 ready 的 disabled 写入返回 422，canonical disabled 写入维持 201；大小写/格式非 canonical 值返回 400；三处守卫各有可变异判别测试；用户指定四组质量门全部通过。 |
| Time estimate | 墙钟约 60–100 分钟；单 agent 约 1.5–2.5 工程小时，主要耗时在 released family 依赖核查、全库质量门与定向变异验证。 |
| Blast radius | `ServingCapabilityContract` 的构造与所有评估调用；管理员 provider 创建/更新语义；全部 released family 的 enable/readiness 判定；运维站点报告；验收矩阵文字。若失败，可能错误放行无法 dispatch 的 provider，或继续阻断已发布图片渠道。 |
| Failure modes | 1. image 车道误放宽 adapter/pool-vendor/transport：用缺站点单测阻断；2. 默认车道零值导致旧 contract 被放宽：零值定义为 `chat_hcsf` 并覆盖测试；3. contract-only/canonical 判定凭猜测：直接读取 `registrydefault` 注册集合并做表驱动测试；4. 只测 HTTP 状态、不证明错误原因：同时断言 code/reason；5. 测试 fixture 与生产 wiring 不一致：复用生产 catalog/wiring；6. 文档并发覆盖：只改 MATRIX-001 精确文本并在最终 diff 中核对。 |
| Decision points | Owner 已在工作单确定关键决策：image 车道仅放宽 chat 三站点；F3 不改 pool SQL；F5 采用“拒收”倾向；禁 commit/push。若核查发现某个 released family 需要新车道或依赖与指令冲突，先停下报告，不自行扩大路由语义。 |

## Pre-execution checklist

1. 读取当前 working tree 与协调锁，保留 Claude 的既有修改。
2. 读取 serving capability contract、evaluate、测试及默认 registry，列出全部 released family、canonical 集合与 runtime 依赖。
3. 读取 `imageshttp` 的实际请求、响应、transport、pool 使用点，确认 image 车道 blocking 集合。
4. 读取 admin mutation handler 的校验顺序与现有 wiring 测试，确定 400/422/201 的判别边界。
5. 先补会失败的 F2/F3/F5 测试，再实现最小修复。
6. 运行定向测试；分别临时变异三处车道/旁路/canonical 守卫并确认指定测试变红，随后恢复并复跑。
7. 精确更新 MATRIX-001 与 deferred 记录，确认未改 CAP-003/004/005 状态列和 F1/F4 文档。
8. 设置 `GOFLAGS=-buildvcs=false`，依次亲跑 build、vet、指定 test、codebudget、quality-gate。
9. 查看最终 diff、协调状态并 release，占用结束后输出中文报告；不 commit、不 push。

## Concrete execution order

1. 建立 released family × lane × station × runtime dependency 核查表。
2. 在 contract 类型中引入默认安全的 `ServingLane`，为 image family 显式标注 `image`。
3. 让 evaluate 始终报告全部站点，但只由当前车道的 blocking 站点决定 ready。
4. 在 mutation 校验层加入 canonical 精确值守卫，并将 disabled 旁路限定为 canonical family。
5. 补齐 servingcapability 单测与 adminhttp wiring 判别测试。
6. 完成文档精确修订、deferred 登记、变异验证和全量质量门。

## 独立性声明

本计划依据 Owner 本次工作单独立形成，起草前未读取任何同描述的 Claude 计划。后续若存在 Claude 独立计划，由 Claude/Owner 负责交叉讨论与综合；本计划不冒充 synthesized plan。
