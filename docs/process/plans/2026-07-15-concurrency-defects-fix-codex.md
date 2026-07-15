# 2026-07-15 重并发降级缺陷独立修复计划（Codex）

> 本文是 Codex 独立稿。撰写过程中未读取
> `docs/process/plans/2026-07-15-concurrency-defects-fix-claude.md`；在 Claude/Codex
> 两稿交叉讨论、Owner 裁定并形成无后缀综合计划前，不执行本文中的生产代码改动。

## 0. 元数据与真实性声明

| 项目 | 内容 |
| --- | --- |
| Owner directive | “为三个既有重并发降级缺陷写一份独立修复计划（只写计划文档，不改任何生产代码）” |
| 计划产物 | `docs/process/plans/2026-07-15-concurrency-defects-fix-codex.md` |
| HUAKAI 基线 | `bbd471c55361046b96c453051e6a072f0fc935d9` |
| 独立性 | 未读取同主题 Claude 计划；本文只基于 Owner brief、HUAKAI 源码/测试/契约和三个参考仓当前默认分支源码 |
| Clean-room lane | `specifier`；本会话读过非 MIT 参考源码，后续不得复用为本修复的 implementer lane |
| 参考仓新鲜度 | 三仓 HEAD 均为 2026-07-09/10，且均由 `origin/main` 包含，未超过 30 天陈旧线 |
| Observed regions | 20 个源码/测试/契约区域（见 §10） |
| Inferences | 5 项，均显式标成“推断”或“建议”，不冒充当前行为 |
| Open questions | 4 项，集中在重试预算、清理超时和统计门槛，见 §8 |

真实性边界：缺陷 1 与缺陷 3 的给定机制均被当前源码直接确认；缺陷 2 的后果仍可成立，
但 Owner brief 中“`Release` 单次无重试、失败仅告警”的机制描述不符合当前 HEAD。当前实现已经有
3 次整事务重试、失败补偿入队和默认启动的分钟级补偿 worker；只有重试耗尽且补偿交接失败/未运行时，
才会退化到 30 分钟 lease。证据见 §4.1。本文按当前真实机制规划残余缺陷，不把历史描述当现状。

## 1. 范围、硬约束与成功标准

### 1.1 范围

未来实施稿允许触及的职责范围：

- `backend/internal/quota`：`Reserve` 错误分类、`Release` 专用事务重试策略、补偿交接和相关测试。
- `backend/internal/quotaenforce`：只做组合链路回归验证；除非综合计划证明必要，不改变对外组合语义。
- `backend/internal/billing`：`Abort` 专用重试预算、耗尽后的 lease 加速资格标记和相关测试。
- `backend/internal/gatewayhttp`、`backend/cmd/gateway`：只补契约/E2E 断言，不改变 HTTP 状态码、稳定错误码或头字段。
- `backend/sql/queries`：只允许增加或收紧现有表上的守卫式 `UPDATE` 查询；不改表、列、索引、约束或迁移。

明确不在范围：

- 不改 schema，不新增 migration，不引入运行时依赖。
- 不降低 `DefaultClaimLeaseWindow`；当前 30 分钟是为覆盖最长 600 秒流式请求及结算余量，直接缩短会误杀在途请求并造成少计费（`backend/internal/billing/claim_gate.go:42-52`）。
- 不改变任何对外 HTTP 状态码、稳定错误码、`Retry-After` 或 `X-Huakai-Abort-Failed` 契约；现有头由统一错误出口写入（`backend/internal/gatewayhttp/chat_completions_error.go:103-107`）。
- 不翻转 `HUAKAI_QUOTA_RECONCILER_ENABLED` 或其他默认开关；配额补偿器当前缺省即启用（`backend/cmd/gateway/wiring.go:279-288`）。
- 不删除非瞬时基础设施错误的 quota fail-closed，不删除 billing/quota sweeper，不把业务错误纳入盲重试。
- 本计划任务本身只新增本文，不改生产代码、不运行格式化改写、不提交 git commit。

### 1.2 必须持续成立的不变量

1. `40001`/`40P01` 只能在整笔 Serializable 事务回滚后重跑；不能在已经中止的同一事务中重试单条查询。HUAKAI 的事务包装在闭包报错时回滚，在闭包成功后才提交（`backend/internal/quota/pg_store.go:51-68`）。
2. quota 的普通存储故障继续生成精确 `quota_fail_closed` 决策；该稳定码由本地常量与构造器定义（`backend/internal/quota/service.go:20-28,769-778`）。
3. billing `Abort` 的 claim 状态、hold 释放、账务事件、usage evidence 与 pool slot 释放仍在同一 Tx2 中原子提交（`backend/internal/billing/settler.go:309-371,376-460`）。
4. 未决 post-delivery settlement 继续阻止零成本中止；候选 SQL 和 Tx2 内复核两层都保留（`backend/sql/queries/balance_holds.sql:81-95`；`backend/internal/billing/settler.go:338-355`）。
5. 正常无冲突路径仍只跑一次事务；新增预算只作用于已识别的瞬时冲突，耗尽/恢复标记只作用于异常路径。
6. 现有“终结后 claim/quota/slot 不悬挂”验收义务不能用“偶发降级”永久豁免（`docs/11_ACCEPTANCE_TEST_MATRIX.md:390`）；钱账原子 Tx2 与 quota reserve/settle 能力仍属 `Implemented Better`（`docs/03_FEATURE_PARITY_MATRIX.md:44,50`）。

### 1.3 总体成功标准

- 缺陷 1：三个吞错点（新 claim 的 reservation 读、policy 解析、复活路径 policy 解析）遇到 `40001`/`40P01` 时都进入既有整事务重试；非重试错误仍精确 fail-closed。
- 缺陷 2：低于新 `Release` 专用预算的瞬时冲突最终释放成功且不留重复副作用；预算耗尽后，即使调用者未传 `ReservationID`，也能按 tenant+claim 守卫式解析 reservation，并用不受请求取消影响的短清理上下文落补偿状态/任务；任务交接失败时也把 reservation 提前变为下一轮 stale sweep 的候选。正常生产条件下冻结窗口从 30 分钟降到不超过两个 1 分钟 worker tick（建议验收上限 125 秒）。
- 缺陷 3：可吸收范围内的 Tx2 冲突不再打出 abort-failed 头；真正耗尽时仍返回原错误并保留原头，同时只把仍为 `reserving` 的 claim 提前变成下一轮 lease sweep 候选。正常生产条件下 hold 冻结从最长约 30 分钟降到不超过两个 30 秒 tick（建议验收上限 65 秒）。
- 所有主修测试都有“亲手破坏守卫必红”的记录；断言同时检查期望好值、次数与持久化终态，禁止只写 `!= bad`。
- `go test`、真 PostgreSQL 集成测试、重并发 E2E、`-race`（适用包）和 code budget 门通过；无 migration、无依赖变更、无默认开关变化。

### 1.4 时间估算

| 工作段 | Agent 有效工时 | 预计墙钟时间 |
| --- | ---: | ---: |
| 基线复现、冲突分布与测试注入器 | 2–3 小时 | 3–5 小时（含真 PG 重复跑） |
| 缺陷 1 测试、修复、三次 mutation | 3–4 小时 | 约半天 |
| 缺陷 2 热重试、恢复交接、reconciler 回归 | 5–7 小时 | 约 1 天 |
| 缺陷 3 热重试、lease 加速、钱账/HTTP 回归 | 5–7 小时 | 约 1 天 |
| 全量门、30 轮 soak、review 修正与报告 | 3–5 小时 | 4–8 小时 |
| **合计** | **18–26 小时** | **约 3 个工作日，另受 CI/PG 资源排队影响** |

估算只覆盖综合计划批准后的实现与验证，不包含两份独立计划的交叉讨论等待；若真 PG 定点 deadlock
无法稳定复现，应按 §8 改用 unit 的 `40P01` 硬门，不用无限调时序消耗工时。

## 2. 共用设计原则

### 2.1 先减少打穿，再缩短兜底窗口

两层职责不能混在一起：

- 热路径重试只处理 `40001`/`40P01`，每次重开完整事务，以降低降级契约的触发率。
- 仍然耗尽时，不伪造成功、不吞错误；只持久化“尽快由既有 worker/sweeper 再处理”的资格。
- worker/sweeper 仍是最终裁决者，继续复核 claim 状态、幂等终态和 post-delivery 保护。

### 2.2 建议的内部重试参数（待 §8 裁定）

| 路径 | 当前 | 本独立稿建议 | 理由 |
| --- | --- | --- | --- |
| quota `Reserve` | 3 次总尝试，5ms 线性退避加至多 3ms jitter（`backend/internal/quota/service.go:26-28,78-87,178-187`） | 不改预算，只修错误可见性 | S1 根因不是预算，而是闭包吞错 |
| quota `Release` | 与 `Reserve` 共用 3 次总尝试（`backend/internal/quota/service_settle.go:331-348`） | 独立为 6 次总尝试；base 5ms、cap 100ms、decorrelated jitter | 终结动作幂等且整事务回滚；与 reserve 解耦，避免扩大准入延迟 |
| billing `Abort` | 与 billing reserve/settle 共用 6 次总尝试，base 2ms、cap 50ms（`backend/internal/billing/retry.go:13-20`；`backend/internal/billing/settler_retry.go:31-53`） | 独立为 9 次总尝试；仍用 base 2ms、cap 50ms、decorrelated jitter | 最坏新增 sleep 上界约 150ms（从 5 段到 8 段、每段 cap 50ms），仅影响已冲突的终结路径 |

参数不是凭空宣称“足够”：实施前必须先按 §7 记录当前同负载基线；若 p99 延迟或冲突分布不支持上述值，
在综合计划中调整，但“独立策略、有限预算、仅重试两个 SQLSTATE、整事务重跑”四点不得退让。

### 2.3 清理上下文

补偿交接不能继续无条件复用已经取消/到期的请求 `ctx`。建议仅在主事务已经失败、需要落恢复资格时，
用 `context.WithoutCancel(ctx)` 保留 trace/value，再包 1 秒 timeout；该上下文只允许做守卫式状态标记和任务入队，
不能继续请求上游、不能直接结算金额、不能改变原返回错误。1 秒是候选值，需由 §8 裁定。

## 3. 缺陷 1（S1）：`Reserve` 将瞬时事务冲突伪装成永久 quota 429

### 3.1 根因确认（Observed）

1. `Reserve` 外层确有 3 次事务循环，但它只能看到事务闭包返回的 `err`；只有该值被识别为 `40001`/`40P01` 才重试，耗尽后才返回 `RetryableError`（`backend/internal/quota/service.go:78-81,178-187,738-740`）。
2. 新 claim 路径读取 reservation 时，除 `ErrNoRows` 外的所有错误都会构造 fail-closed deny，随后闭包返回 `nil`；因此读阶段的瞬时 PG 冲突永远到不了外层分类器（`backend/internal/quota/service.go:82-111`）。
3. policy 解析失败也用同样方式生成 deny 并返回 `nil`；解析器原样上抛策略查询错误，而查询带 `FOR UPDATE`，确实可能成为事务冲突发生点（`backend/internal/quota/service.go:114-120`；`backend/internal/quota/policy.go:32-46`；`backend/sql/queries/quota.sql:20-34`）。
4. released/expired reservation 的复活路径又有一个同类吞错点：policy 解析错误被转换为 deny，第三返回值却是 `nil`（`backend/internal/quota/service.go:614-623`）。
5. 事务层本身没有吞错：闭包非空错误会 rollback 并原样返回，commit 错误也会原样返回（`backend/internal/quota/pg_store.go:55-67`）。所以修复边界应在上述三个错误分类点，而不是改 `WithTx`。
6. 现有单元测试只在闭包完整成功后由 fake `WithTx` 注入冲突；其 reservation 读取始终返回 `ErrNoRows`，没有覆盖读/解析阶段的吞错（`backend/internal/quota/service_test.go:334-398,865-876,933-935`）。现有真 PG 测试覆盖的是不同 claim 争同一窗口后的冲突，也没有定点覆盖三个吞错位置（`backend/internal/quota/service_integration_test.go:618-713`）。

### 3.2 首选修法

在三个点位先分类，再决定是否 fail-closed：

1. `GetReservationByClaimForUpdate` 返回非 `ErrNoRows` 错误时：若为 `40001`/`40P01`，闭包直接返回原错误；否则保持当前 `failClosedDecision`、`DenyError` 和 `return nil`。
2. 新 claim 的 `ResolvePolicies` 返回错误时：同样先把两个瞬时 SQLSTATE 原样交给外层；其余错误完全保持当前 fail-closed。
3. `reactivateExistingReservation` 的 `ResolvePolicies` 返回错误时：瞬时 SQLSTATE 通过第三返回值向上传递，且不构造 deny；其余错误保持当前 deny 结果。
4. 不改外层 `isPgRetryableTxConflict`、重试次数、稳定错误码、audit 语义或 claim-race 分支。重试必须从新事务重新读取 reservation/policy，不能在同一事务里重试单条查询。

建议用一个只返回“应交给事务重试吗”的极小本地 helper 统一三个分支，但 helper 不得同时构造 HTTP/业务结果；
这样可以减少以后新增吞错点，又不会把 retry policy 和 fail-closed 决策耦合。

### 3.3 备选与不采用项

- 备选 A：三个点位分别写显式 `isPgRetryableTxConflict` 分支。改动最小，但未来容易漏第四个转换点；若综合稿优先最小 diff，可采用。
- 备选 B：引入携带 deny 与 cause 的内部 typed outcome。表达力强，但对三个分支而言重构面过大，本稿不建议首轮采用。
- 不采用：在 `GetReservation...`/policy query 内部重试。Serializable 事务一旦收到 `40001`/`40P01` 已不可继续，单查询重试会违反整事务重跑要求。
- 不采用：对所有数据库错误重试。它会延迟真正的权限、连接、约束或配置故障，并削弱 fail-closed 纵深。

### 3.4 判别性测试与亲手变异

| ID | 层级 | 注入/步骤 | 必须精确断言 | 亲手变异后为何变红 |
| --- | --- | --- | --- | --- |
| AT-CD1-001 | unit | 表驱动在 reservation 读取点分别注入一次 `40001`、`40P01`，下一事务返回 `ErrNoRows` 并完成准入 | `Allowed=true`、`Decision.Code=quota_reserve_allowed`、事务恰 2 次、只持久化 1 个 reservation/1 次 allow audit、0 次 fail-closed | 把新 guard 改回 `return nil` 后第一次即得到 deny，`Allowed` 与次数断言红 |
| AT-CD1-002 | unit | 在新 claim policy 查询点按相同两种 SQLSTATE 注入，再成功 | 完整事务重跑；最终 allow；第一次事务没有可见 reservation/window/audit 副作用 | 删 policy guard 后变为假 429，红 |
| AT-CD1-003 | unit | seed released/expired reservation，在复活 policy 查询点先冲突后成功 | 最终状态精确为 `reserved`、`IdempotencyHit=false`、策略/窗口按新请求重建一次 | 复活 helper 再次吞错时返回 deny，红 |
| AT-CD1-004 | unit | 三个点位各让冲突持续到 3 次预算耗尽 | 错误 `IsRetryable=true`、`IsDenied=false`、result 为空、事务恰 3 次、无 `quota_fail_closed` audit | 任一点吞错都会变成 deny 且只跑 1 次，红 |
| AT-CD1-005 | unit | 三个点位分别注入普通错误和非重试 PG 状态（如 `23514`） | 精确 `DenyError`、`Decision.Code=quota_fail_closed`、事务恰 1 次 | 若误改成“所有错误重试”，次数/错误类型红；守住 fail-closed |
| AT-CD1-006 | integration_pg | 在测试用 `beginTx` 包装器中先建立 Serializable snapshot，再并发更新既有 reservation，使第一次 `SELECT ... FOR UPDATE` 命中真实 `40001`，第二次成功 | 最终允许/复用结果符合 seed，数据库无 fail-closed audit，事务确实重开 | 去掉 guard 后真 PG 路径返回 deny，红 |

`40P01` 的整事务分类由 unit 表驱动确定性覆盖；真 PG deadlock 若需要不稳定的时序，不作为唯一合入门。
实施者必须实际临时破坏三个 guard 各一次、运行对应测试、保存 RED 摘要，再恢复代码得到 GREEN；不能只在注释里声称 mutation。

### 3.5 风险、缓解与回滚

- 风险：把非瞬时错误误认成可重试，增加延迟。缓解：分类器仍只接受精确 `40001`/`40P01`，AT-CD1-005 守住负集合。
- 风险：重试后重复 reservation/window/audit。缓解：每次冲突必须回滚整事务，测试检查好值和精确计数，不只检查“没有错误”。
- 风险：只修新 claim 漏掉复活链。缓解：AT-CD1-003 独立定点覆盖。
- 回滚：无 schema/data 迁移，可单独回退三个分类 guard；但这会重新打开 S1，只能作为紧急止血并立即恢复，不能作为长期降级方案。

## 4. 缺陷 2（S2）：quota `Release` 冲突耗尽后 reservation/window 长时间虚占

### 4.1 根因确认与 brief 校正（Observed）

当前 HEAD 的真实链路如下：

1. `quotaenforce.Abort` 先完成 billing abort，再调用 quota `Release`；quota 失败除 not-found 外会向调用者返回，不是简单在这一层“仅告警”（`backend/internal/quotaenforce/settler.go:178-196`）。
2. quota `Release` 已经通过 `runQuotaFinalizationWithRetry` 跑完整事务；它不是单次 `withStore`。该 helper 当前与 reserve 共用 3 次预算，冲突耗尽会返回 `RetryableError`（`backend/internal/quota/service_settle.go:167-175,231-242,331-348`）。
3. `Release` 在事务中依次释放窗口、并发槽、reservation 并写 audit；任一步失败都会使整个事务回滚，因此可以安全地重跑完整事务（`backend/internal/quota/service_settle.go:176-227,452-480`）。
4. 失败后会先尝试把 reservation 标为 `reconciliation_needed`，再立即插入 `release_after_abort` job；两步都发生在失败事务之外，但继续使用原请求 `ctx`（`backend/internal/quota/service_settle.go:663-710`）。几乎所有非 not-found/mismatch/非法状态错误都进入该补偿分支（`backend/internal/quota/service_settle.go:786-797`）。
5. 生产补偿 worker 缺省开启，每分钟同时重放任务和扫 stale reservation（`backend/internal/quota/reconciliation_worker.go:11-17,102-114`；`backend/cmd/gateway/wiring.go:1281-1289`）。因此“必等 30 分钟”不是当前默认生产行为。
6. 重并发 E2E 显式设置 `HUAKAI_QUOTA_RECONCILER_ENABLED=false`，并在 5 秒后把仍为 `reserved` 的行计为可容忍降级；该测试环境会刻意绕过分钟级恢复（`backend/cmd/gateway/account_slot_concurrency_e2e_test.go:163-181,400-438`）。现有 binding E2E 最多容忍 2 个悬挂与 2 个 abort 冻结（`backend/cmd/gateway/binding_concurrency_e2e_test.go:119-150`）。
7. 若三次热重试耗尽，且原 `ctx` 已取消导致“标记 + 入队”失败，或 worker 被关闭/持续故障，reservation 的默认 lease 与 billing claim 同为 30 分钟，才会退到 lease sweep（`backend/internal/quotaenforce/settler.go:18-23,68-84`；`backend/internal/billing/claim_gate.go:42-52`）。
8. 生产组合层只按 tenant+claim 调 `Release`，没有传 `ReservationID`（`backend/internal/quotaenforce/settler.go:188-192`）；当前失败分支却把请求中的零值 ID 传给补偿函数，且只有 ID 非零才执行 reservation 标记（`backend/internal/quota/service_settle.go:231-239,671-679`）。所以即使失败前已经读到 row，当前主调用链也可能跳过 `reconciliation_needed` 标记。

因此当前根因应表述为：**3 次、窄 jitter 的共享短预算在重并发下仍可耗尽；主调用不传
`ReservationID`，使现有标记可能跳过；耗尽后的恢复交接复用请求 `ctx`，且 reservation lease 未被提前到期，
交接失败时会落入 30 分钟兜底窗口。**这是对现状的可验证修复目标。

### 4.2 首选修法

分四层，均不删除现有兜底：

1. **`Release` 专用重试策略。** 将 quota finalization helper 接受内部 policy；只给 `Release` 使用建议的 6 次总尝试和 decorrelated jitter，`Reserve` 保持 3 次，`Settle`/cache-hit 首轮不随意扩大。只识别 `40001`/`40P01`，每次仍经 `WithTx` 重跑完整事务。
2. **恢复准备按 claim 定位，不信任可选 ID。** 新增一条本地守卫式查询：以 tenant+claim 为必需键，可选非零 reservation ID 只作附加一致性守卫；允许当前状态为 `reserved`/`reconciliation_needed`，原子地标成 `reconciliation_needed`、把 `lease_expires_at` 缩到不晚于 DB 当前时间，并返回真实 reservation ID。并发方已经把 row 推到 `settled`/`released` 时 no-row 视为“无需恢复”，不得反向改写终态。这样主调用的零 ID 不会让标记静默 no-op。
3. **持久恢复交接脱离请求取消。** 热重试真正失败后，以 1 秒 bounded cleanup context 执行“按 claim 准备恢复 → 用返回的真实 ID 入队”两步；每一步自身只对 `40001`/`40P01` 做最多 3 次短重试。无论交接成功与否，向上返回的主错误仍是原 `Release` 错误（可用 `errors.Join` 附加交接错误，但稳定类型/外部码不变）。
4. **准备成功即具备 stale 资格。** 若后续 job 入队成功，job 查询优先重放；若入队失败，分钟级 stale sweep 会在 billing claim 已终态且无待处理 job、无 post-delivery 恢复时接手。当前 stale SQL 已具备这些防误扫条件（`backend/sql/queries/quota.sql:581-612`），不需要 schema。

补充可观测性只加低基数计数：`operation=release`、`sqlstate`（固定枚举）、`outcome=retry_success|exhausted|handoff_queued|handoff_stale_only|handoff_failed`；禁止 tenant/claim 标签。

### 4.3 备选与不采用项

- 备选 A：只把 3 次增至 6 次，不改补偿交接。改动小，但请求取消/入队失败仍可能直接落入 30 分钟；不满足“缩短冻结窗口”的完整目标。
- 备选 B：热重试耗尽后用 detached context 再直接跑一次 `Release`。它会在客户端生命周期外继续执行重事务，并把恢复与请求 goroutine 耦合，本稿不建议。
- 不采用：全局缩短 30 分钟 lease。会误扫仍在运行的长流，违反 §1.1。
- 不采用：新增恢复表/消息中间件。已有 reconciliation job + stale sweep 已可表达所需状态，新增 schema/依赖超出约束。
- 不采用：把 `Release` 失败吞成成功。这样会隐藏窗口虚占并改变现有 abort-failed 可观测契约。

### 4.4 判别性测试与亲手变异

| ID | 层级 | 注入/步骤 | 必须精确断言 | 亲手变异后为何变红 |
| --- | --- | --- | --- | --- |
| AT-CD2-001 | unit | `Release` 前 5 次事务依次返回 `40001`/`40P01`，第 6 次成功 | 恰 6 次完整事务、最终 `released`、一个 release audit、窗口 reserved=0、槽 released；业务错误只跑 1 次 | 把预算还原为 3 或删重试，最终状态/次数红 |
| AT-CD2-002 | integration_pg | 用测试 trigger + 非事务 sequence 在 release audit 写点让前 5 次抛 `40001`，第 6 次通过 | 真 PG 最终原子释放；sequence=6；重复调用是 idempotency hit 且计数不变 | 任一中间副作用未回滚或预算不足均红 |
| AT-CD2-003 | unit/integration_pg | 像真实 `quotaenforce.Abort` 一样令请求 `ReservationID=0`；让 6 次都冲突，并在最后一次冲突时取消原请求 `ctx`；cleanup store 可成功 | 主返回仍为 retryable/原 cause；按 tenant+claim 返回真实 ID，reservation 精确为 `reconciliation_needed` 且 lease≤DB now；job 携真实 ID、状态 `queued`、`next_run_at≤now` | 继续只信请求零 ID或改回原 `ctx` 时，标记/入队任一断裂，断言红 |
| AT-CD2-004 | integration_pg | 热重试耗尽；标记成功、入队故障；随后移除故障并驱动 `ReconciliationWorker.RunOnce` | 无 job 时 stale 段在两 tick 上限内把 aborted claim 对应 reservation 变 `released`，窗口/槽归零 | 标记不提前 lease 时 worker 处理数=0、状态仍悬挂，红 |
| AT-CD2-005 | integration_pg | 热重试耗尽且成功入队；下一轮 job replay | job `succeeded`、reservation `released`，stale 段不重复处理，audit 恰一条 | 去掉 job 排除/幂等守卫会重复效果，红 |
| AT-CD2-006 | e2e_concurrency | 保持 reconciler disabled，定点注入少于新预算的冲突 | 5 秒内 `quotaStuckReservations` 增量精确为 0，而不是“≤2” | 将预算变回 3 后出现已知悬挂，红 |
| AT-CD2-007 | recovery | seed 未决 post-delivery settlement，再提前 lease 并跑 stale sweep | processed=0，reservation/window/slot 全保持，待恢复完成后才允许收敛 | 删除 SQL 排除或复核会错误释放，红 |

真 PG trigger/function/sequence 只建在临时测试库并在 cleanup 删除，不进入 migration。实施者必须至少亲手执行四类变异：
预算降回 3、恢复准备只信请求零 ID、恢复交接改回原 `ctx`、删除提前 lease；各自对应 AT-CD2-001/003/003/004 必须 RED，再恢复 GREEN。

### 4.5 风险、缓解与回滚

- 风险：更多热重试放大数据库压力。缓解：仅两个 SQLSTATE、仅 `Release`、有 cap/jitter/ctx、记录 attempt 分布；若 p99 超门，先调预算而非删除恢复层。
- 风险：按 claim 解析命中错误 reservation。缓解：tenant+claim 是既有幂等定位键，查询必须带 tenant；请求 ID 非零时再做一致性守卫，终态 row 不更新（`backend/sql/queries/quota.sql:215-237,333-346`）。
- 风险：提前 stale 后错误释放仍在运行的请求。缓解：动作只发生在 billing abort 已成功而 quota finalization 失败之后；stale SQL还要求 billing claim 终态并排除未决 post-delivery 恢复（`backend/sql/queries/quota.sql:589-610`）。
- 风险：标记成功但入队失败形成双恢复入口。缓解：有 queued/running/failed job 时 stale SQL排除，终态 `Release` 本身幂等（`backend/internal/quota/service_settle.go:187-229`）。
- 风险：PG 全不可用时 detached handoff 仍失败。此时无法诚实保证分钟级恢复；原 30 分钟 lease 与后续 sweeper 继续是最后兜底，必须告警而非伪成功。
- 回滚：恢复当前 3 次 finalization 策略、原请求 `ctx` 和不改 lease 的标记查询；无需数据回滚。已被提前标记的行只会由既有守卫式 reconciler 收敛，不会产生新格式数据。

## 5. 缺陷 3：billing Tx2 abort 重试耗尽导致声明式冻结

### 5.1 根因确认（Observed）

1. `Abort` 外层确实用完整 Tx2 重试器包住 `abortOnce`（`backend/internal/billing/settler.go:296-304`）。重试器只识别 `40001`/`40P01`，当前 `reserveRetryMax=5`，即首次加 5 次重试、共 6 次；耗尽返回最后一个原始 PG 错误（`backend/internal/billing/retry.go:13-33`；`backend/internal/billing/settler_retry.go:31-53`）。
2. Tx2 锁住 claim 并要求状态为 `reserving`，随后更新 aborted、释放 hold、写 billing event/usage evidence、释放 pool slot并提交；任一冲突回滚全部效果（`backend/internal/billing/settler.go:323-371,376-460`）。
3. 六次全部失败后，claim/hold 没有部分提交，因而保持 `reserving`/`held`；gateway 用原错误设置 `X-Huakai-Abort-Failed`（`backend/internal/gatewayhttp/chat_completions_error.go:103-107`）。重并发 E2E 也明确把该头计为冻结，并允许 claim/hold 留待 sweeper（`backend/cmd/gateway/account_slot_concurrency_e2e_test.go:37-45,454-527`）。
4. lease sweeper 每 30 秒选择 lease 已过期的 `reserving` claim，逐条再次调用 `Abort`；单行失败不阻断同批其他行（`backend/internal/billing/lease_sweep.go:16-18,103-145`）。但 claim 的正常 lease 在 reserve 时是 30 分钟，故刚打穿的行尚不具备候选资格（`backend/internal/billing/claim_gate.go:42-52`；`backend/sql/queries/balance_holds.sql:81-95`）。
5. 当前 retry unit 已覆盖“两次冲突后成功”“业务错误不重试”“6 次耗尽原错返回”，真 PG integration 只覆盖一次注入冲突后成功，没有覆盖耗尽后的恢复窗口（`backend/internal/billing/retry_test.go:49-120`；`backend/internal/billing/settler_integration_test.go:491-529`）。

### 5.2 首选修法

1. **Abort 独立重试 policy。** 保留现有共用 retry engine 的整事务、context-aware、decorrelated jitter 语义，但让调用方传入 policy；`Abort` 采用建议的 9 次总尝试，`Settle` 与 reserve 保持当前预算。正常路径仍只有一次 Tx2。
2. **耗尽后只加速 lease，不伪造终态。** `Abort` 收到仍可识别为 `40001`/`40P01` 的最终错误时，以 1 秒 bounded cleanup context 执行单条守卫式更新：tenant/id 匹配、`status='reserving'`，把 `lease_expires_at` 缩到不晚于 DB 当前时间。更新不释放 hold、不改 claim status、不写账务事件、不碰金额。
3. **保留原错误与头。** 加速成功或失败都返回原 Tx2 错误；gateway 继续写相同 `X-Huakai-Abort-Failed`。加速失败只追加低基数日志/指标，不用新错误覆盖主错误。
4. **仍由原 sweeper 最终裁决。** 下一轮 sweeper 继续经现有候选 SQL排除 post-delivery 恢复，再进入 `Abort` 的行锁与状态复核；多副本仍靠 `FOR UPDATE SKIP LOCKED` 和 claim 状态守卫裁决（`backend/sql/queries/balance_holds.sql:81-95`；`backend/internal/billing/settler.go:323-355`）。

这项设计把“正常 lease 必须覆盖长流”和“已明确终止但 abort 耗尽应尽快回收”分开，不改变全局 30 分钟默认值。

### 5.3 备选与不采用项

- 备选 A：仅提高 Abort 重试预算。能降低头的命中率，但真正耗尽仍冻结 30 分钟，不完整。
- 备选 B：耗尽后直接用 detached context 再跑 `abortOnce`。可更快，但会在返回路径外执行完整钱账 Tx2，且继续冲击热点，本稿不建议。
- 备选 C：内存延迟队列。进程崩溃即丢、多副本不可见，不可作为 money-path 恢复保证。
- 不采用：复用 `usage_record_dlq` 增加新事件种类。它混淆“已交付结算恢复”和“零交付中止恢复”，且可能需要 schema/运营语义扩展。
- 不采用：直接把 claim 改成 `aborted` 或直接释放 hold。那会绕过 Tx2 的账务事件、usage evidence、slot release 与二次防御，破坏原子性。

### 5.4 判别性测试与亲手变异

| ID | 层级 | 注入/步骤 | 必须精确断言 | 亲手变异后为何变红 |
| --- | --- | --- | --- | --- |
| AT-CD3-001 | unit | 前 8 次交替返回 `40001`/`40P01`，第 9 次成功 | 恰 9 次 Tx2、8 次 sleep；业务错误仍恰 1 次；context cancel 立即停止 | 把 Abort policy 还原 6 次后错误返回，红 |
| AT-CD3-002 | integration_pg | 在 abort event 写点用 sequence 让前 6 次 `40001`、第 7 次成功（精确越过旧预算） | 无 abort-failed；claim=`aborted`、hold=`released`、held=0、slot released、event/usage 各恰一 | 还原旧预算后第 6 次耗尽，状态/次数红 |
| AT-CD3-003 | HTTP + integration_pg | 连续 9 次都注入冲突，触发真实 gateway abort 出口 | HTTP status/body/稳定码与基线逐字节一致；`X-Huakai-Abort-Failed` 仍存在；紧接返回时 claim=`reserving`、hold=`held`，lease≤DB now | 若吞掉错误或改头契约，HTTP断言红；若直接伪终态，立即状态断言红 |
| AT-CD3-004 | integration_pg | AT-CD3-003 后移除 trigger，驱动 `LeaseSweeper.SweepOnce`（另做真实 ticker 版本） | 一轮/至多两 tick 内 claim=`aborted`、hold=`released`、held=0、slot/quota slot 释放，账务证据恰一 | 删除 lease 加速后候选数=0、状态仍冻结，红 |
| AT-CD3-005 | failure | 让 lease 加速 UPDATE 自身失败 | 返回错误仍是原最后 PG conflict；头不变；lease 仍是原未来值；日志/指标记 `expedite_failed` | 若清理错误覆盖主错，错误身份/头红；守住 30 分钟原兜底 |
| AT-CD3-006 | recovery safety | seed 未决 post-delivery settlement，再让 lease 到期并跑 sweeper | 候选被排除；claim/hold/slot 不动。恢复行变 delivered 后才可中止 | 删除候选排除或 Tx2 内复核会错误零成本中止，红 |
| AT-CD3-007 | concurrency | 两个 sweeper 并发处理同一提前到期 claim，并与迟到真实终结竞争 | 只有一个终态提交；version/事件/usage/hold 释放均恰一；另一方得到幂等/状态哨兵 | 去掉状态守卫或锁后出现重复计数，红 |
| AT-CD3-008 | e2e soak | 固定数据/并发度跑修前后各 30 轮 | 确定性“6 冲突”场景头数从 1 降到 0；自然负载头率不高于基线且目标至少下降 50%；任何仍带头的行 65 秒内恢复 | 仅改 E2E 容忍阈值而不修逻辑，确定性注入与恢复时限仍红 |

实施者必须至少亲手执行三类变异：Abort policy 还原 6 次、删除 lease 加速、让加速错误覆盖主错误；
AT-CD3-002/004/005 必须分别 RED。测试 trigger 仍只存在于临时测试库。

### 5.5 风险、缓解与回滚

- 风险：多三次冲突重试增加拒绝响应尾延迟。缓解：只在两个 SQLSTATE 上发生，sleep cap 不变；以 §7 的 p95/p99 和 header-rate 双门裁定。
- 风险：错误提前到期仍在途 claim。缓解：只有调用者已进入 terminal `Abort` 且完整 Tx2 真正耗尽后才更新；UPDATE 还要求状态仍是 `reserving`，sweeper 再做两层 post-delivery 防御。
- 风险：所有耗尽请求同时提前到期造成 sweeper burst。缓解：现有 batch=100、`SKIP LOCKED`、30 秒 tick 保持；补充每轮 processed/error 与 oldest age 指标，不扩大 batch 默认值（`backend/internal/billing/lease_sweep.go:16-18`）。
- 风险：加速 UPDATE 也遇到 PG 故障。缓解：它是 best effort，不能覆盖原错误；原 30 分钟 lease/sweeper 仍在。
- 回滚：回退 Abort 专用 policy 和守卫式 lease UPDATE 即可；无 schema/data 回滚。已提前到期但尚未清扫的 claim 仍只会走原 sweeper，不会绕过账务 Tx2。

## 6. 三个参考仓的等价机制结论（specifier lane）

以下只提炼行为证据，不采用上游命名、schema、代码结构或算法实现。三仓均无 HUAKAI 三项缺陷所涉及的
“Serializable 完整 reserve/abort Tx + claim/hold/quota lease + 双层 sweeper”同构机制。

| 参考仓（当前默认分支） | 等价性结论 | 一句话源码证据 | 对 HUAKAI 计划的作用 |
| --- | --- | --- | --- |
| sub2api `12d811bd76572836d6df6e1fa8aa5ff91be3b12e` | **无等价（仅局部相似）** | 已读计费与图像冻结金路径各自只观察到一次 begin→effects→commit；另有批任务在 10 分钟 stale 后尝试释放，失败再入工作队列，但没有所读 HUAKAI 式 SQLSTATE 整事务热重试。证据：`Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/repository/usage_billing_repo.go:35-62,140-171`；`...:backend/internal/service/batch_image_billing_recovery.go:38-102`；`...:backend/internal/service/batch_image_worker_runtime.go:103-114` | 只支持“终结失败必须有后台再处理路径”这一场景要求；不移植实现 |
| new-api `246d62aa5ed3ba2a4728322c269c180a016dc9cd` | **无等价（仅局部相似）** | 已读退款路径中，一类非幂等余额返还明确单次执行，另一类有幂等保护的订阅返还做 3 次短重试；异步退款失败只记日志，未观察到 claim lease/sweeper 或完整 Serializable abort Tx。证据：`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/billing_session.go:81-122`；`...:service/funding_source.go:57-63,111-138` | 只支持“必须先证明幂等与事务回滚，才可增加重试”的约束；不复制重试实现 |
| CLIProxyAPI `26d45fd46a2d2911adef14772465131066dae465` | **无等价** | 本次扫描的用量边界只观察到请求完成后的 usage 回调、usage 入 Redis 列表和模型 quota-exceeded 标记，未观察到预扣 hold/claim abort/lease recovery 生命周期。证据：`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/pluginapi/types.go:1100-1102,1255-1270`；`...:internal/home/client.go:867-875`；`...:sdk/cliproxy/model_registry.go:11-19` | 无可复用机制；HUAKAI 修法完全由本地钱账/配额不变量推导 |

Clean-room 结论：参考仓只作为“存在或不存在某类恢复结果”的证据，不提供 HUAKAI 代码。本文首选修法均从
HUAKAI 自己的事务边界、状态守卫、worker 与验收契约推导；后续实现必须换全新 clean implementer 会话，
且不得重新读取本节非 MIT 源码。该隔离符合本项目“reference 是证据而非源码提供者”的规则（`docs/05_CLEAN_ROOM_POLICY.md:5-20,22-48,58-69`）。

## 7. 预执行清单与具体执行顺序

### 7.1 Owner 批准综合计划前

1. 保持本稿与 Claude 独立稿隔离；由主持者在两稿都完成后列出 agreements/conflicts/gaps。
2. Owner 裁定 §8 四个决策点，形成无后缀综合计划；未形成综合稿前不得实施。
3. 为实施工作启动新的 clean implementer 会话；该会话只读 HUAKAI 综合计划和本地源码，不读非 MIT 参考仓。

### 7.2 实施前基线

1. 记录 `git status --short`、HEAD、Go/toolchain、PG 版本；确认只 stage 计划内文件，绝不覆盖用户改动。
2. 用当前代码跑 quota/billing unit、`integration_pg`、`e2e_concurrency`；固定 seed、并发度、请求数与机器资源。
3. 自然负载各跑 30 轮，记录：`40001`/`40P01` 次数、每次 attempt 数、quota 悬挂数、abort-failed 头数、恢复年龄、p50/p95/p99。
4. 验证临时 trigger/sequence 注入器 cleanup；测试完成后查询 `pg_trigger`/`pg_proc` 确认无残留。
5. 运行 code budget 门；优先在现有内聚文件补小 helper，测试按职责建文件，禁止继续膨胀无关 god-file。

### 7.3 建议的闭合增量顺序

1. **增量 A（缺陷 1 测试先行）**：先加 AT-CD1-001..006 并证明至少一个在旧代码上 RED；再加三个错误透传 guard；跑 quota unit + 真 PG；做三次 mutation。
2. **增量 B（缺陷 2 热路径）**：先加可注入的 quota finalization policy 测试；实现 `Release` 独立 budget/jitter；确认 `Settle`/cache-hit 默认未变。
3. **增量 C（缺陷 2 恢复）**：先加 canceled-context、job-fail→stale-sweep 测试；再实现 bounded cleanup context、标记时提前 lease；跑 reconciler 多副本/未决交付保护回归。
4. **增量 D（缺陷 3 热路径）**：先加“6 冲突后第 7 次成功”的真 PG 测试；再拆 Abort policy；确认旧 budget mutation RED。
5. **增量 E（缺陷 3 恢复）**：先加 header/state/sweep 判别测试；再增加守卫式 lease 加速；跑 post-delivery、双 sweeper、迟到 settle 竞态回归。
6. **增量 F（整链路）**：运行 deterministic fault E2E 与 30 轮 soak；比较基线并按 §1.3 判定，不允许只提高容忍阈值。
7. 每个未来代码增量按项目规则 stage 后运行 Codex read-only review，S0/S1 清零再提交；本计划任务自身禁止 commit。

### 7.4 建议验证命令（未来实施会话执行）

- `cd backend && go test ./internal/quota ./internal/quotaenforce ./internal/billing`
- `cd backend && go test -race ./internal/quota ./internal/quotaenforce ./internal/billing`
- `cd backend && HUAKAI_DATABASE_URL=... go test -tags=integration_pg ./internal/quota ./internal/billing`
- `cd backend && HUAKAI_DATABASE_URL=... go test -tags=e2e_concurrency ./cmd/gateway -run 'Binding|AccountSlot' -count=30`
- 运行仓库既有 standard unit/codebudget 门；若命令由 Makefile/CI 统一定义，以仓库入口为准。

## 8. 交叉裁定时的四个决策点

1. **重试预算：**是否接受 quota `Release` 6 次、billing `Abort` 9 次的初始值；若不接受，必须给出同负载 attempt 分布与 p99 证据，不能退回“重试越少越安全”的直觉。
2. **cleanup timeout：**是否接受 1 秒 bounded detached context；候选范围 500ms–2s。无论选值如何，不允许无限 detached goroutine。
3. **恢复时限：**是否采用 quota 125 秒、billing 65 秒（各两个 tick 加余量）作为生产条件门；PG 持续不可用时应被明确排除并进入告警演练，而非假装可保证。
4. **soak 统计门：**本稿建议确定性注入为硬门，自然负载 30 轮以“不高于基线且目标下降 ≥50%”为趋势门；若样本基线为 0，确定性门优先，自然 soak 只做 no-regression。

## 9. 总体爆炸半径、失败模式与回滚门

| 失败模式 | 爆炸半径 | 预防/检测 | 回滚门 |
| --- | --- | --- | --- |
| 错把业务/永久错误纳入重试 | quota 准入和 billing abort 尾延迟；可能形成 DB 放大 | 精确 SQLSTATE 负集合、attempt 指标、业务错误恰一次测试 | 任一非 `40001/40P01` 被重试即 S1，停止合入 |
| 整事务重跑产生重复钱账/窗口副作用 | 余额、usage、audit、slot 正确性 | 真 PG trigger 在中后段注入；断言每个好值和精确一次 | 任一重复或部分提交为 S0/S1，回退该增量 |
| 提前 lease 误扫在途/已交付请求 | 用户被少计费、slot 被过早释放 | terminal Abort 前置条件、status guard、候选 SQL + Tx2 二次保护、竞态测试 | 任一误扫为 S0，立即回退 lease 加速 |
| detached cleanup 泄漏/越权 | 连接池、关机、trace 生命周期 | 1 秒硬 timeout、无 goroutine、只做两类 DB 写、shutdown 测试 | 超时后仍活跃或执行主业务即 S1 |
| worker storm | quota/billing DB 热点 | 保持现有 tick/batch/`SKIP LOCKED`，观察 oldest age/processed/error | p99 或池饱和越过既有 SLO则调低热预算/分批，不删 sweeper |
| 只改 E2E 容忍值掩盖缺陷 | 线上继续悬挂 | deterministic mutation gate + 恢复时限，不接受仅阈值变更 | mutation 不红即测试无效，阻塞合入 |

总体回滚是按缺陷拆开的纯代码/查询回退，不涉及 schema。回滚不能删除 fail-closed 或 sweeper，不能关闭现有
quota reconciler 默认值，也不能清理/改写既有账务数据。若只回退热重试，恢复加速仍可安全保留；若只回退
恢复加速，则现有 30 分钟 lease 语义自然接管，但必须恢复高优先级告警并保留缺陷为未关闭。

## 10. 源码覆盖证明、推断与开放问题

### 10.1 Observed regions（20）

1. quota `Reserve` 主循环、三个吞错点、冲突分类与 fail-closed：`backend/internal/quota/service.go:68-192,614-623,738-778`。
2. quota Serializable 事务和 reservation lock read：`backend/internal/quota/pg_store.go:49-68,209-221`。
3. policy 解析与 `FOR UPDATE`：`backend/internal/quota/policy.go:32-46`；`backend/sql/queries/quota.sql:5-34`。
4. quota reserve unit retry coverage/fake 注入位置：`backend/internal/quota/service_test.go:334-398,865-876,933-935`。
5. quota reserve 真 PG 并发窗口测试：`backend/internal/quota/service_integration_test.go:618-713`。
6. quota `Release` 事务、当前重试与窗口释放：`backend/internal/quota/service_settle.go:167-242,331-365,452-480`。
7. quota 补偿标记/入队与错误选择：`backend/internal/quota/service_settle.go:663-710,786-797`；`backend/sql/queries/quota.sql:338-346`。
8. quota stale sweep 查询和 store：`backend/sql/queries/quota.sql:581-612`；`backend/internal/quota/pg_store_sweep.go:66-95`。
9. quota 组合 abort/lease：`backend/internal/quotaenforce/settler.go:18-23,61-85,178-196`。
10. quota worker/reconciler 与生产接线：`backend/internal/quota/reconciliation_worker.go:11-17,102-114`；`backend/internal/quota/reconciler.go:91-139,213-224,304-312`；`backend/cmd/gateway/wiring.go:279-288,1281-1289`。
11. quota release/idempotency/slot 现有测试：`backend/internal/quota/service_settle_integration_test.go:212-292`；`backend/internal/quota/pg_store_concurrency_release_integration_test.go:11-88`。
12. billing Tx2 retry engine/policy：`backend/internal/billing/settler_retry.go:9-55`；`backend/internal/billing/retry.go:13-52`。
13. billing Abort 完整 Tx2：`backend/internal/billing/settler.go:296-304,309-371,376-462`。
14. billing lease sweep/header/E2E：`backend/internal/billing/lease_sweep.go:16-18,103-145`；`backend/sql/queries/balance_holds.sql:81-95`；`backend/internal/gatewayhttp/chat_completions_error.go:103-107`；`backend/cmd/gateway/account_slot_concurrency_e2e_test.go:37-45,454-527`。
15. billing retry/真 PG 现有测试：`backend/internal/billing/retry_test.go:49-120,163-190`；`backend/internal/billing/settler_integration_test.go:491-529`。
16. 重并发 E2E 对 quota/reconciler/降级的现状：`backend/cmd/gateway/account_slot_concurrency_e2e_test.go:163-181,400-438`；`backend/cmd/gateway/binding_concurrency_e2e_test.go:119-162`。
17. sub2api 一次性事务与批任务恢复：`Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/repository/usage_billing_repo.go:35-62,140-171`；`...:backend/internal/service/batch_image_billing_recovery.go:38-102`；`...:backend/internal/service/batch_image_worker_runtime.go:103-114`。
18. new-api 退款与局部重试：`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/billing_session.go:81-122`；`...:service/funding_source.go:57-63,111-138`。
19. CLIProxyAPI 用量/模型 quota 边界：`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/pluginapi/types.go:1100-1102,1255-1270`；`...:internal/home/client.go:867-875`；`...:sdk/cliproxy/model_registry.go:11-19`。
20. 本地契约与 clean-room 规则：`docs/03_FEATURE_PARITY_MATRIX.md:44,50`；`docs/11_ACCEPTANCE_TEST_MATRIX.md:390`；`docs/05_CLEAN_ROOM_POLICY.md:5-20,22-48,58-69`。

### 10.2 Inferences（5）

1. **推断：**三个吞错点先透传两个瞬时 SQLSTATE 后，既有外层循环即可重开事务；依据是事务层原样返回闭包错误和外层已存在分类器（§3.1）。
2. **推断：**quota `Release` 使用独立 6 次 decorrelated jitter 会比当前 3 次窄线性 jitter 降低同波重撞率；必须用 §7 基线验证，不宣称预先证明幅度。
3. **推断：**补偿标记同时提前 lease，在 job 入队失败但 PG 已恢复时，会让下一分钟 stale sweep 接手；依据是当前 stale SQL的 lease/claim/job 三重条件（§4.1）。
4. **推断：**billing claim 在 Abort 耗尽后提前 lease，会把正常恢复窗口从约 30 分钟缩到下一个 30 秒 tick；持续 PG 故障不在保证内（§5.1）。
5. **推断：**9 次 Abort 尝试在当前 50ms sleep cap 下可吸收比旧 6 次更长的冲突波，同时把新增 sleep 理论上界控制在约 150ms；实际 Tx 执行耗时仍须测量。

### 10.3 Open questions（4）

1. 相同硬件/PG 配置下，quota release 与 billing abort 的 attempt 分布是否支持 6/9 次候选值？
2. cleanup timeout 应取 500ms、1s 还是 2s，才能兼顾连接池压力与取消后持久交接？
3. 自然负载的 30 轮样本是否足以稳定估计 header/stuck rate，还是需提升到 100 轮非阻塞 soak？
4. 真 PG `40P01` 定点构造是否能在 CI 保持确定性；若不能，是否接受 unit 精确覆盖 `40P01`、真 PG 只定点覆盖 `40001`？

## 11. Owner 交叉裁定摘要

本独立稿确认：缺陷 1 是三个错误转换点吞掉瞬时冲突的 S1，首选最小修复是只把
`40001`/`40P01` 交回既有整事务循环，普通故障继续 fail-closed；缺陷 2 的 brief 机制在当前 HEAD 已部分过时，
真实剩余问题是 3 次短重试仍会耗尽、零 `ReservationID` 会跳过标记且补偿交接受请求取消影响，建议用
`Release` 独立预算、按 claim 恢复准备、bounded cleanup context、提前 reservation lease 四层处理；缺陷 3
建议用 Abort 独立预算降低头命中率，并在真正耗尽后只提前
claim lease，由原 30 秒 sweeper 完成钱账终态，原头、fail-closed 和两类 sweeper 均不删除。观察事实来自 20 个
区域；5 项性能/恢复效果是待测试推断；4 个参数/统计问题需要与 Claude 独立稿交叉讨论后由 Owner 裁定。
全案不改 schema、不翻默认行为、不缩功能；参考三仓均无等价机制，只提供局部场景证据，未向 HUAKAI 搬运实现。

Source files read: HUAKAI `backend/internal/quota/service.go`, `backend/internal/quota/pg_store.go`, `backend/internal/quota/policy.go`, `backend/internal/quota/service_settle.go`, `backend/internal/quota/pg_store_sweep.go`, `backend/internal/quota/reconciliation_worker.go`, `backend/internal/quota/reconciler.go`, `backend/internal/quotaenforce/settler.go`, `backend/internal/quota/service_test.go`, `backend/internal/quota/service_integration_test.go`, `backend/internal/quota/service_settle_integration_test.go`, `backend/internal/quota/pg_store_concurrency_release_integration_test.go`, `backend/internal/quota/reconciler_integration_test.go`, `backend/internal/quota/reconciler_sweep_integration_test.go`, `backend/internal/billing/claim_gate.go`, `backend/internal/billing/retry.go`, `backend/internal/billing/settler_retry.go`, `backend/internal/billing/settler.go`, `backend/internal/billing/lease_sweep.go`, `backend/internal/billing/retry_test.go`, `backend/internal/billing/settler_integration_test.go`, `backend/internal/gatewayhttp/chat_completions_error.go`, `backend/cmd/gateway/wiring.go`, `backend/cmd/gateway/account_slot_concurrency_e2e_test.go`, `backend/cmd/gateway/binding_concurrency_e2e_test.go`, `backend/sql/queries/quota.sql`, `backend/sql/queries/balance_holds.sql`, `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/05_CLEAN_ROOM_POLICY.md`, `docs/11_ACCEPTANCE_TEST_MATRIX.md`; sub2api `backend/internal/repository/usage_billing_repo.go`, `backend/internal/service/batch_image_billing_recovery.go`, `backend/internal/service/batch_image_worker_runtime.go`, `backend/internal/service/batch_image_processor.go`; new-api `service/billing_session.go`, `service/funding_source.go`; CLIProxyAPI `sdk/pluginapi/types.go`, `internal/home/client.go`, `sdk/cliproxy/model_registry.go`
Lane: specifier
Agent: OpenAI Codex GPT-5 `/root`
UTC timestamp: 2026-07-15T02:12:08Z
