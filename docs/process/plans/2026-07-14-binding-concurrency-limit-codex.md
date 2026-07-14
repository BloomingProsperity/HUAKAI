# 2026-07-14 绑定级并发硬上限独立计划（Codex）

> 状态：**独立草案，等待 Claude 交叉讨论、综合计划与 Owner 裁定；禁止据此直接实施。**
>
> 独立性声明：本计划是在未读取任何同名 `-claude.md` 计划的前提下形成。本文只读取 HUAKAI 内部规则、代码、迁移、测试与项目文档；没有重新读取任何非 MIT 借鉴项目源码。关于三镜行为的描述仅采用 Owner 本次任务简报给出的结论，不新增外部事实断言。

| 项目 | 内容 |
| --- | --- |
| Owner 指令 | “绑定级 `MaxParallelRequests` 并发上限真生效……本轮只写计划停下”；“先写你的独立计划……写完计划就停下等 Claude 综合裁定（不要直接实现）” |
| 范围 | 规划 `model_pool_bindings.max_parallel_requests` 从已存储字段变为 PostgreSQL 全局硬并发闸；规划 0183 非破坏性向上迁移、选号透传、原子占槽、429 与已有 claim/hold 回滚复用、结算/断连/lease 回收、防泄漏与真 PostgreSQL 并发测试；评估所有生产 endpoint 与配置 UI 的完整覆盖。本文不实施。 |
| 成功标准 | 双计划综合前，把硬上限的线性化点、钱账不变量、状态派生计数、端点覆盖、迁移边界、并发与变异测试、回滚/观测要求及 Owner 决策点写清；不得把“外层 `COUNT` 预检”误当成可抗并发的最终硬闸。 |
| 时间估算 | 本轮独立计划约 1 小时。Owner 批准后的完整实现预计 16–24 代理工时、约 2–3 个工作日墙钟时间；若本片只允许 chat 首发而把其他端点列为后续，后端可缩至约 10–16 代理工时，但这不满足“绑定级全局上限”完整语义，需 Owner 明示接受。 |
| 爆炸半径 | 最高：数据库迁移、所有走 pool selector 的请求、binding 配置更新、账号槽获取、claim/hold 回滚、成功/失败结算、lease sweep、429 客户端契约、运营配置 UI、PASR/default 五种选号模式以及多实例网关共享的 PostgreSQL 热路径。 |
| 失败模式 | 超卖、永久占满、钱账 hold 泄漏或重复扣、跨租户串计数、配置被 UI 静默清空、错误退化为 500/503、数据库故障时 fail-open、锁序死锁/热点、只接 chat 造成旁路、弱测试在 goroutine 尚未真正并发时误绿、生成 SQL 镜像与源 SQL 漂移。详见下文。 |
| 决策点 | D1 0183 schema；D2 两层闸与事务线性化；D3 全端点还是 chat-only；D4 `NULL/0` 语义；D5 前端配置入口；D6 429 契约；D7 配置变更的生效边界；D8 是否需要额外运行时开关。均须在综合计划中明确。 |
| 执行前检查 | 先完成 Claude/Codex 计划差异表与综合稿；Owner 批准所有高风险决策；准备隔离的真 PostgreSQL 数据库；确认迁移编号仍为 0183；确认工作树与 codebudget 基线；分小闭环实施并跑钱账、并发、迁移、竞态和 reviewer-lane 门。 |

## 1. 本轮边界与硬约束

本轮只允许新增本计划文件，明确不做以下动作：

- 不改 schema、Go、SQL 查询镜像、前端、测试或任何运行时代码。
- 不运行迁移、不启动服务、不跑 `sqlc generate`。
- 不执行 `git add`、`git commit`、`git checkout`、`git restore` 或 `git stash`。
- 不读取 Claude 的独立计划，不提前做顺序式审查。
- 不触碰 `Sidebar.tsx`、`LICENSE`、真实凭据、生产数据库或生产部署。
- 不新造计费/扣费/hold 释放逻辑；后续实现只能复用现有 `Reserve`、`abortReservation`、settler 与 acquisition 状态翻转。
- 所有新增 `.go` 注释、测试注释、计划/评审与 Owner 汇报均使用中文；代码注释不得出现借鉴项目名。

执行启动条件是：Claude 与 Codex 独立计划均完成，双方交叉讨论形成无后缀综合计划，Owner 明确批准综合计划及本文 D1–D8 的最终取舍。未满足时保持停止。

## 2. HUAKAI 现状证据与独立发现

### 2.1 已确认事实

1. `model_pool_bindings.max_parallel_requests` 已存在，允许 `NULL` 或非负整数，不需要重复加字段：`backend/sql/migrations/0008_model_registry.up.sql:159-165`。
2. registry 已把该值读入 `BindingMetadata`，但注释明确表示目前没有选号/gate/计费消费：`backend/internal/registry/registry.go:71-80`、`backend/internal/registry/postgres_registry.go:178-192`。
3. `SelectionRequest` 目前只透传 `BindingID`、RPM、TPM，没有并发上限：`backend/internal/pool/router/types.go:100-106`。chat 构造请求也只填这三项：`backend/internal/gatewayhttp/chat_completions_dispatch.go:503-528`。
4. chat 生命周期先 `ClaimGate.Reserve`，后 pool select：`backend/internal/gatewayhttp/chat_completions_dispatch.go:327-345`。因此任何 binding 并发拒绝都发生在钱账预留之后，必须走现有 abort 路径。
5. 账号槽的最终获取在 `DBSlotManager.acquireOnce` 的 `SERIALIZABLE` 事务内完成，当前顺序是更新账号 `in_flight_count`、插入 acquisition、提交：`backend/internal/pool/dispatcher/slot_manager.go:68-109`。
6. acquisition 的权威活跃态是 `status='acquired'`；成功、失败与孤儿回收都通过状态翻转终结：`backend/sql/migrations/0001_pool_routing.up.sql:172-195`、`backend/sql/queries/pool_slot_acquisitions.sql:19-26,48-83`。
7. 成功结算与 abort 已分别调用同一“释放 acquisition + 递减账号槽”原语：`backend/internal/billing/settler.go:251-260,439-454`。绑定计数若直接由 acquisition 状态派生，无需在这些 money 路径新增 decrement。
8. 断连后的 chat abort 已用 `context.WithoutCancel` 脱离请求上下文：`backend/internal/gatewayhttp/chat_completions_attempt.go:189-201`。本片应验证复用，不能另写一条释放支路。
9. 现有 binding RPM/TPM 包装器在 selector 外层装配：`backend/cmd/gateway/selector_wiring.go:98-123`；它的滚动窗口 `Check/Record` 语义不能复用于占用型并发。
10. 现有失败分类把 key/binding rate limit 合并为 429，但 abort reason 仍叫 `key_rate_limited`：`backend/internal/gatewayhttp/chat_completions_handler.go:950-967`。binding concurrency 需要显式契约，不能悄悄落入 500。
11. 管理 API 已能读写 `max_parallel_requests`，但前端 PATCH 构造器明确不回填它：`frontend/src/features/routing/selection.ts:64-80`。由于后端 PATCH 是整行覆盖，运营人员编辑其他 binding 字段时会把已有并发配置静默清空。这是本片激活字段后必须处理的真实配置风险。
12. 目前有多个生产 handler 直接构造 `SelectionRequest`：chat、completions、embeddings、images、rerank、audio。若只接 chat，其余入口可绕过同一 binding 的总上限。
13. 现有 `AT-R1A-007` 只证明 per-key 与账号槽并发恢复，没有覆盖 binding 维度：`docs/11_ACCEPTANCE_TEST_MATRIX.md:377`。`F-CONC-001` 仍标为 Open：`docs/03_FEATURE_PARITY_MATRIX.md:78`。
14. 当前最后一个迁移是 0182，因此计划中的下一编号 0183 在本次核对时可用；实施前必须再次检查，避免并行分支占号。

### 2.2 必须修正的 TOCTOU 风险

只在最外层执行下列逻辑并不能形成“硬上限”：

```text
读取 acquired 数量 → 判断小于 K → 进入账号选号 → 插入 acquisition
```

N 个并发事务可以同时读到相同旧值并全部通过，随后各自插入，最终超过 K。`SERIALIZABLE` 只有在读与写处于同一事务且冲突可被 PostgreSQL 识别/重试时才可能保护该不变量；把读放在 selector 外、写放在后续独立事务，不具备这个条件。

因此本计划建议的完整结构是“两层、同一事实源”：

1. **外层快速拒绝层**：在账号选号之前查询 `COUNT(*) WHERE binding_id=? AND status='acquired'`。饱和时尽早返回；它减少无意义选号和账号槽竞争，但不声称自己是硬上限线性化点。
2. **事务内权威硬闸**：在 `DBSlotManager.acquireOnce` 的同一事务中，先按 binding 串行化，再重新读取 active count；达到 K 则返回专用 sentinel，未达到才更新账号槽并插入带 `binding_id` 的 acquisition。事务提交才算获取成功。这里是唯一的 hard-cap 线性化点。

建议的串行化原语是“对该 binding 行取事务级行锁并读取当前限额”，或等价的、带固定命名空间的 PostgreSQL transaction advisory lock。综合计划必须只选一个，并给出锁序证明：binding 锁必须先于 provider account 更新；所有实现路径一致；事务结束自动释放。行锁更易与 tenant/启用状态校验和运营更新形成一致边界，优先推荐；若基于 registry 快照只对正限额 binding 加锁，则须接受 D7 的配置传播边界。

外层预检与事务内复核都只能读取 acquisition 状态，不得增加独立 binding 计数器，也不得调用 RPM/TPM 的 `Record`。

## 3. 目标不变量

后续实现与测试必须共同守住以下不变量：

1. 对任意 `(tenant_id, binding_id)`，当有效上限 `K>0` 时，已提交且 `status='acquired'` 的 acquisition 数量始终 `<=K`。
2. binding 上限跨 Provider Account 聚合；同一 binding 选到不同账号仍共用 K。
3. 不同 binding 互不占用彼此额度；tenant 维度必须校验，禁止跨租户串计数或锁碰撞造成错误拒绝。
4. `NULL`/`0` 的兼容语义由 D4 明确；推荐两者均表示不启用 binding 上限，保持现有部署不变。负数在 API 边界返回 4xx，不能依赖 DB CHECK 最终变成 5xx。
5. 计数只包含 `status='acquired'`。`released_success`、`released_failure`、`orphan_swept` 永不占额度。
6. 已超过 `lease_expires_at` 但尚未被 sweeper 真正翻转状态的行仍计入占用；不能仅凭时间提前忽略，否则在旧槽实际未回收时超发。
7. 上限调低到当前占用以下时，不杀死已在途请求；只拒绝新获取，直到状态派生数自然降到新上限以下。
8. acquisition 插入与 binding 硬闸、账号 `in_flight_count` 增量在同一事务：三者要么一起提交，要么一起回滚。
9. binding 拒绝发生在 claim/hold reserve 之后时，必须通过既有 abort 完整释放；不得留下 reserving claim、held balance、quota reservation 或 acquisition。
10. 成功请求只结算一次；失败/拒绝不收费；客户端断连仍在 detached context 中完成 abort；lease sweep 是崩溃后的最终回收纵深。
11. PostgreSQL 的 count/锁/事务出错必须 fail-closed，不得为了可用性绕过硬上限。
12. 多网关实例共享同一 PostgreSQL 时仍满足同一上限；进程内 mutex、内存计数或单实例测试不构成证明。
13. selector mode、PASR fallback 或 endpoint family 不得成为绕过路径。

## 4. 拟议范围

### 4.1 范围内

1. 0183 向上迁移：
   - `pool_slot_acquisitions` 新增可空 `binding_id bigint`。
   - 新增部分索引 `(binding_id, status) WHERE status='acquired'`。
   - 不改、不删任何现有列、约束或数据；存量行保持 `binding_id IS NULL`。
   - 配套 down 文件仅作为受控回滚定义；不得在非一次性数据库上试跑 destructive down。
2. SQL 源与手工生成镜像同步：
   - insertion 接受 nullable binding ID；读取模型包含该字段。
   - 按用户硬约束不运行 `sqlc generate`，只能手工同步必要的 generated Go，并用编译/测试防漂移。
3. 请求上下文：`SelectionRequest` 新增 binding max parallel 字段；由 registry metadata 透传。
4. 最外层 binding concurrency 快速拒绝层，置于账号候选/账号槽之前；与 RPM/TPM 包装器相邻但保持独立类型和独立语义。
5. `DBSlotManager` 事务内的 binding 串行化、状态 count 复核与带 binding ID 的 acquisition insert。
6. 专用 sentinel、HTTP 429 映射、稳定 client code/abort reason、必要的低基数日志与指标。
7. 复用现有 `classifyPoolSelectFailure`、`abortReservation`、settler、slot release 和 lease sweep；不新增钱账状态机。
8. 默认与 PASR 的全部生产模式；禁止 PASR fallback 重试绕开 binding 饱和结果。
9. 所有会进入共享 pool selector 且能命中 model binding 的生产端点。推荐一次覆盖 chat、completions、embeddings、images、rerank、audio；若 Owner 选 chat-only，必须把其余入口标为明确 `Mandatory Roadmap` 且不得宣称全局 binding cap 已完成。
10. 管理 API 的非负校验；前端至少保留已有 `max_parallel_requests`，并按 D5 决定是否同片提供可见输入框与说明。
11. 真 PostgreSQL 并发、money 生命周期、断连、sweeper、迁移、错误映射、配置保留和变异证红测试。
12. 实施后更新 `docs/11_ACCEPTANCE_TEST_MATRIX.md`、相关 parity/risk 状态与评审记录；只有全端点与真并发门通过后，才能把 binding 维度标为 PASS/Implemented。

### 4.2 范围外

- 不修改账号级 `provider_accounts.in_flight_count` 的既有实现。
- 不把 binding concurrency 改成等待队列；本片语义是饱和后立即 429。账号级队列行为保持原样。
- 不引入 Redis、独立计数表、内存计数器、后台 decrement worker 或新 runtime dependency。
- 不修改 RPM/TPM 滚动窗口算法，不用 `Record` 模拟占用。
- 不改 billing ledger schema、quota schema、auth core、计价公式或支付逻辑。
- 不重构整个 selector/gatewayhttp 大包；只做能守住职责预算的小闭环改动。
- 不在本片实现前端导航，不触碰 `Sidebar.tsx`。

## 5. 设计与生命周期

### 5.1 正常获取

```text
解析 binding metadata
  → ClaimGate.Reserve（既有）
  → 外层 binding concurrency 快速预检
  → 账号候选/策略选择
  → DBSlotManager 的 SERIALIZABLE 事务
       → 获取同 binding 的事务锁
       → COUNT(binding_id, status='acquired')
       → 已满：事务回滚并返回专用 sentinel
       → 未满：原子增加账号槽
       → 插入同一 acquisition（含 binding_id）
       → commit
  → 上游调用
```

外层检查与事务内检查之间允许结果变化；后者是权威。事务若因 serialization failure 重试，必须重新加锁、重新 count，不能复用旧判断。

### 5.2 饱和拒绝

```text
专用 binding concurrency sentinel
  → 禁止换同 binding 的其他账号重试
  → 禁止 PASR/default fallback 把它当普通选号失败再次尝试
  → classify 为 429
  → 复用 abortReservation（detached context）
  → claim/hold/quota 回滚
  → 无 acquisition 或事务内 acquisition 未提交
```

若外层预检通过但事务内硬闸拒绝，结果必须与外层直接拒绝完全相同。任何 abort 失败继续沿用现有“降级并显式暴露回滚失败”的机制，不另造 money 补偿。

### 5.3 成功、失败、断连与崩溃

- 成功：既有 settle 把同一 acquisition 翻为 `released_success`，binding count 自动减少。
- 普通失败/客户端断连：既有 abort 在 detached context 中把状态翻为 `released_failure`，binding count 自动减少。
- selector 在 acquisition 已提交后、claim writeback 前失败：必须继续复用既有幂等 release 闭包；状态不再是 `acquired` 后才视为回收。
- 网关崩溃：lease 过期后由现有 `LeaseSweeper.SweepOnce` 把合格孤儿翻为 `orphan_swept`，binding count 自动减少。
- release/settle/sweep 重复执行：既有 `status='acquired'` 条件保证只翻转一次，binding 侧不做第二份 decrement。

### 5.4 配置与运营语义

- 推荐 `NULL` 与 `0` 都为“无限制/未启用”，正整数才强制。
- 降低上限不终止现有请求；提高上限允许后续请求立刻使用新容量。
- 删除/禁用 binding 时，历史 acquisition 仍凭保存的 `binding_id` 正常释放；不得依赖 binding 行仍存在才能进行 settle/sweep。
- 运营更新必须审计旧值/新值，并展示生效语义。若本片不提供可见 UI，现有值至少必须 round-trip 保留，绝不能因编辑其他字段而归零。

## 6. schema 与查询计划

### 6.1 0183 up

计划文件：

- `backend/sql/migrations/0183_binding_concurrency_acquisition.up.sql`
- `backend/sql/migrations/0183_binding_concurrency_acquisition.down.sql`

向上迁移只执行：

1. `ADD COLUMN binding_id bigint NULL`。
2. 建部分索引 `(binding_id, status) WHERE status='acquired'`。

明确不做：数据回填、`NOT NULL`、独立计数列、触发器、修改现有 CHECK、删除/重命名字段。按 Owner 指定不额外加 FK；tenant 一致性由获取事务显式验证并由测试覆盖。

上线评估要记录索引构建锁影响。若生产表很大，普通 `CREATE INDEX` 的锁窗口与 `CREATE INDEX CONCURRENTLY` 是否符合当前迁移执行器的事务模型，需在综合计划中确认；不能未经验证把 `CONCURRENTLY` 塞入事务型迁移。

### 6.2 查询与生成镜像

- 修改 `backend/sql/queries/pool_slot_acquisitions.sql` 的 insert/必要 select，使 `binding_id` 成为 nullable 参数/结果。
- 手工同步 `backend/internal/db/billing/pool_slot_acquisitions.sql.go`、相关 model/querier 中真正必要的生成镜像；不运行 generator，不做无关机械改写。
- binding hard gate 的 lock/count 查询优先放在 slot manager 事务边界附近，避免给已接近包预算的 generated DB 包增加大量文件；具体落点要服从 codebudget 和依赖方向。
- count SQL 必须同时带 tenant 与 binding 限定，并显式 `status='acquired'`；不能让 `binding_id IS NULL` 的存量行进入 binding 计数。
- 实施前用 `EXPLAIN` 在有代表性数据量下确认命中部分索引；不得只凭索引存在即宣称热路径安全。

### 6.3 锁序

推荐统一锁序：

1. binding 事务锁/行锁；
2. 账号槽更新所需行锁；
3. acquisition insert；
4. commit。

不得出现另一条获取路径先锁账号再锁 binding。管理端只更新 binding 时，也要验证与获取路径的锁等待可控。deadlock/serialization error 必须按既有有界重试策略处理；达到重试上限后 fail-closed，不能绕过 limit。

## 7. 失败模式、缓解与证据

| 风险 | 触发方式 | 后果 | 计划中的缓解 | 必须留下的测试/观测证据 |
| --- | --- | --- | --- | --- |
| 外层 `COUNT` TOCTOU | N 个请求同时看到 count<K | 超过硬上限 | 事务内按 binding 串行化并重新 count，和 insert 同事务 | N>K 真并发，任意时刻恰 K 行 acquired |
| released 仍被计数 | count 漏掉 status predicate | 容量永久锁死 | 只数 acquired；部分索引同谓词 | settle/abort/sweep 后 count=0，后续可重占；变异③必红 |
| acquisition 未写 binding | insert 参数漏透传 | count 恒 0、无限超发 | request→slot manager→insert 全链路断言 | 变异④必红；直接查每个成功行 binding_id |
| 拒绝后 hold 泄漏 | sentinel 未走 classify/abort | 用户余额长期 held、claim reserving | 复用 classify + detached abort | 所有 429 请求 claim aborted、hold released、held=0；变异②必红 |
| 成功重复扣 | retry/fallback 重复 settle | 双倍 usage/billing event | binding 饱和作为 terminal；沿用幂等 claim/settle | 成功请求每个恰一 usage/event/capture |
| 断连释放失败 | abort 继承已取消 ctx | acquired/hold 泄漏 | 只调用现有 `WithoutCancel` abort | ctx cancel 后 released_failure、hold 清零 |
| 崩溃孤儿 | 进程无机会 abort | 长期占满 | 现有 lease sweep 翻 `orphan_swept` | 过期且可回收的真行 SweepOnce 后不再计数 |
| sweep 测试假阳性 | claim 仍 reserving 或有未交付 DLQ | sweeper 按设计跳过 | 构造真正满足现有 sweep WHERE 的孤儿 | 先证明候选资格，再断言状态变化 |
| DB 故障 fail-open | count/lock 超时被忽略 | 无上限服务 | 专用错误向上返回，禁止内层 select | 注入查询错误，断言无账号更新/无 acquisition/钱账 abort |
| 跨租户串计数 | 只按 binding 或错误 ID | 相互限流/泄露元数据 | tenant+binding 一致性验证 | 两 tenant 并发隔离；错误 tenant 请求 fail-closed |
| selector mode 旁路 | PASR fallback/某模式没包 gate | 模式切换后超发 | 核心硬闸放共享 DBSlotManager，包装器覆盖生产 dispatcher | 五种模式参数化测试，饱和均 terminal |
| endpoint 旁路 | 只在 chat 填 limit | 其他协议超发 | 全生产 selector caller 透传，或 Owner 明示分片并标 roadmap | 至少一个跨 endpoint 共享 K 的 E2E |
| UI 静默清空 | PATCH 不回填隐藏字段 | 运营改别项后限额失效 | 最少 round-trip；推荐可见输入 | 前端请求体与后端更新集成测试 |
| 锁热点/死锁 | 同 binding 高 QPS 串行、锁序不一 | 延迟尖峰/503 | 临界区只做 count+slot+insert；统一锁序；有界重试 | 高竞争压测、重试/延迟指标、无 deadlock |
| 降配语义不明 | K 从大改小 | 杀在途或继续超发 | 不杀在途，仅阻止新占槽 | active>K 降配测试，释放后逐步恢复 |
| 生成码漂移 | 手改 generated 与 SQL 不一致 | 编译过但运行 scan/参数错 | 最小同步 + 真 PG insert/read 测试 + diff 审查 | SQL/source/model 三方契约测试 |
| codebudget 回归 | 给已大包继续加文件 | 门红、职责恶化 | 不给已到文件预算的 router 随意加第 21 文件；优先在已有内聚文件或小职责子包落点 | `go test ./internal/codebudget` |

## 8. 真 PostgreSQL 并发测试矩阵

所有 money/concurrency 核心用例必须使用隔离的真 PostgreSQL 与真实事务，不接受纯 mock 替代。测试不能以调度运气制造并发：使用 barrier/可控上游阻塞，让 K 个成功请求在数据库中保持 `acquired`，确认状态后才放开其余 goroutine。断言最终 HTTP 数量之外，还必须直接查 claim、hold、quota、usage、billing event、account in-flight 与 acquisition。

| ID | 场景与前置 | 并发步骤 | 必须断言 | 主要证红点 |
| --- | --- | --- | --- | --- |
| BC-PG-001 | 同 tenant、同 binding，K>0，至少 K 个可用账号槽，N>K | N 个 goroutine 同时越过起跑 barrier | 数据库任意稳定采样点恰 K 行 `binding_id=X AND status='acquired'`；恰 K 个进入上游；N-K 个为 binding 429；无第 K+1 个成功 | 去掉 gate/事务复核必红 |
| BC-PG-002 | BC-PG-001 的 N-K 个拒绝请求都已 Reserve | 保持 K 个上游阻塞，等待拒绝路径完成 | 每个拒绝 claim 都 aborted；hold 已释放；quota reservation 无残留；无 acquisition；用户 `held` 回到仅包含 K 个在途所需值 | 去掉 abort 必红 |
| BC-PG-003 | K 个成功请求 | 解除上游阻塞并正常 settle | K 行均 `released_success`；active count=0；账号 in-flight 恢复；每请求恰一 usage/billing event/capture；新请求可再次占槽 | count 包含 released 必红 |
| BC-PG-004 | 已占槽请求发生上游失败/本地 abort | 并发触发失败终结 | 状态 `released_failure`；active count 递减；不收费；hold/quota 清理；释放后 replacement 成功 | 失败释放漏接必红 |
| BC-PG-005 | 请求已经取得 acquisition | cancel 请求 ctx，走 chat detached abort | 尽管父 ctx 已取消，状态仍为 `released_failure`，hold 与槽均释放；后续请求成功 | 移除 `WithoutCancel` 必红 |
| BC-PG-006 | 手工构造 lease 已过期、状态 acquired、binding_id 有值且满足 sweeper 既有候选条件 | 调 `LeaseSweeper.SweepOnce` | 状态 `orphan_swept`；binding active count 减少；账号 in-flight 同步减少；replacement 成功 | sweeper 未覆盖 binding 生命周期必红 |
| BC-PG-007 | 同 binding 绑定两个 Provider Account，账号各有余量，K 小于总余量 | 并发请求迫使选号跨账号 | 两账号 acquisition 合计不超过 K，而非每账号各 K | 错按 account+binding 分组必红 |
| BC-PG-008 | 同 tenant 两个不同 binding，各 K=1 | 两组同时占槽 | 每组各允许 1 个；A 饱和不拒绝 B | 锁键/WHERE 过宽必红 |
| BC-PG-009 | 两 tenant，各自 binding 与请求 | 同时竞争 | 计数、429、日志与结果均租户隔离；伪造 tenant/binding 组合 fail-closed | 缺 tenant 校验必红 |
| BC-PG-010 | `NULL` 与 `0` 上限 | N 个并发请求，账号容量足够 | 按 D4 的兼容语义均不受 binding cap；账号级 cap 仍独立生效 | 把 0 当零容量必红 |
| BC-PG-011 | active=K，运营把 K 降为 K-1，再升为 K+1 | 不终止旧请求，尝试新请求 | 降配不杀现有；新请求被拒；释放到阈值后恢复；升配后新增容量按 D7 生效 | 缓存/锁语义错误必红 |
| BC-PG-012 | count/lock SQL 注入错误或超时 | 请求已完成 Reserve 后进入 gate | fail-closed；不更新账号槽、不插 acquisition；claim/hold 被 abort；返回稳定非成功错误 | DB 错误被吞必红 |
| BC-PG-013 | 两个独立 selector/slot manager 实例共享同一 PG | 从两实例各发 N/2 请求 | 全局总数仍恰 K，不是每进程 K | 进程内 mutex/计数必红 |
| BC-PG-014 | default、PASR actual/disabled/shadow_only/enforce | 参数化并发打满 | 各生产模式都由共享事务硬闸限制；饱和不因 fallback 再选号 | 模式漏装配必红 |
| BC-PG-015 | 同一 binding 通过两种 endpoint family 发请求 | chat 与至少一种非 chat 请求共用 barrier | 两端点合计最多 K；429 和 money 清理一致 | chat-only 接线必红 |
| BC-PG-016 | 账号 cap < binding cap、账号 cap > binding cap 两组 | 并发请求 | 两个独立上限取较小者；binding 饱和给 binding 429，账号饱和保持既有 wait/fail 语义 | 错误分类/门顺序必红 |
| BC-PG-017 | 释放与新 acquire 在同 binding 高竞争交错 | 多轮 settle/abort/acquire | 任意时刻不超 K；最终 active=0；无负账号计数、无悬挂 hold | release/acquire 竞态必红 |
| BC-PG-018 | 存量 `binding_id IS NULL` acquisition | 升级后执行 count/sweep/release | 旧行继续按账号生命周期释放，不进入任意 binding count；升级无数据回填依赖 | 迁移兼容性必红 |

### 8.1 Owner 指定的四个最小变异红点

以下四项必须以可重复测试证明，不只写注释：

1. 去掉 binding gate/事务复核：BC-PG-001 的“恰 K 个”断言红。
2. binding 拒绝后不调用 abort：BC-PG-002 的残留 hold/claim 断言红。
3. count 不按 `status='acquired'` 过滤：BC-PG-003/004/006 的容量恢复断言红。
4. acquisition insert 不写 `binding_id`：BC-PG-001/007 的超发断言红。

另建议保留三项高价值变异：去掉事务内复核但保留外层 precheck；把 binding 饱和当 `ErrNoSlotAvailable` 允许 fallback；把 detached abort 改回请求 ctx。三者分别证明线性化、terminal 语义与断连防泄漏。

### 8.2 单元/契约测试

- 透传：registry metadata → endpoint execution → `SelectionRequest` → acquisition insert 参数逐层等值。
- wrapper：`K<=0` 惰性；正 K 饱和专用 sentinel；查询错误 fail-closed；内层 selector 在拒绝时调用次数为 0。
- slot manager：锁/count 必须发生在账号 increment 之前；拒绝时事务回滚；serialization retry 每轮重读 count。
- classifier：binding concurrency sentinel 精确映射 D6 的 429/client code/reason，并调用一次 abort。
- admin API：负数 4xx；`NULL/0/正数` round-trip；并发更新语义与审计。
- frontend：编辑其他字段不丢 `max_parallel_requests`；若提供输入框，空值/0/正数/非法/未改动均有判别性断言。
- SQL generated mirror：nullable scan/insert 与源 SQL 对齐。
- 所有测试的 winner/loser 必须具备真正区分特征；禁止“只断言不是坏值”、零值时 `t.Skip`、注释 N=100 实际 N 很小等弱测试。

## 9. 实施顺序（仅在综合计划获批后）

1. **建立前置证据**：再次确认 0183 未被占用、工作树归属、所有 selector caller、registry 更新传播方式、codebudget 当前基线和可用隔离 PG。把 D1–D8 的 Owner 结果写入综合计划。
2. **先写失败测试**：迁移契约、slot manager 原子 cap、分类/abort、真 PG N>K、状态释放与四个变异红点。确认旧代码确实红，避免测试只验证实现细节。
3. **迁移最小闭环**：只加 nullable 列与部分索引；在一次性数据库验证 0182→0183、存量行兼容与查询计划。down 只在一次性库按 Owner 许可验证。
4. **SQL/模型同步**：改 source SQL 与最小 generated 镜像，手工完成，不运行 sqlc；先让 acquisition round-trip 测试过。
5. **事务硬闸**：在账号 increment 前实现 binding 串行化、count、sentinel 和 insert binding ID；先通过 DBSlotManager 真 PG 并发测试。
6. **外层快速拒绝**：装配独立 binding concurrency selector；明确它不 `Record`、不负责最终线性化；保证全部 dispatcher mode 覆盖。
7. **端点透传与失败分类**：先 chat 闭环验证 reserve→429→abort，再按 D3 覆盖其余生产入口；饱和错误在 fallback 链中保持 terminal。
8. **运营配置**：补 API 非负校验、审计与 D5 选定的前端行为；不动 Sidebar。
9. **恢复纵深**：跑 settle/abort/disconnect/sweep 与 acquire/release 交错测试，核对 status、账号 in-flight、claim、hold、quota、usage、billing event。
10. **观测与文档**：添加不含敏感信息的低基数字段/指标；更新 acceptance/parity/risk，记录未完成项，不夸大状态。
11. **小闭环 review**：每个预定 commit 仅包含单一职责，暂存后按规则跑 Codex review；S0/S1 修复后才能落地。money/schema/跨 feature 完成时再跑完整 reviewer-lane。本文阶段不暂存、不评审 commit。

## 10. 文件影响预估

以下是综合计划获批后的候选，不代表本轮已授权修改：

- schema/SQL：`backend/sql/migrations/0183_*`、`backend/sql/queries/pool_slot_acquisitions.sql`。
- generated mirror：`backend/internal/db/billing/pool_slot_acquisitions.sql.go`、`backend/internal/db/billing/models.go`，以及确有签名变化的最小接口文件。
- selector/request：`backend/internal/pool/router/types.go`、现有 selector 包装器所在的内聚文件、`backend/internal/pool/dispatcher/slot_manager.go`、公共 sentinel 导出位置。
- production wiring：`backend/cmd/gateway/selector_wiring.go`。
- endpoint：chat dispatch/classifier，以及 D3 选中的 completions、embeddings、images、rerank、audio 透传/分类文件。
- admin/config：`backend/internal/modelbindingadminhttp/routes.go`、registry 过时注释与对应测试。
- frontend（D5）：`frontend/src/features/routing/selection.ts`、binding modal/types/tests；明确不改 `Sidebar.tsx`。
- tests：优先新增独立 `e2e_concurrency` binding 测试文件，复用现有真 PG fixture/money 断言；补 slot manager、lease sweep、迁移和端点契约测试。
- docs：acceptance matrix、feature parity/risk 与 implementation review 记录。

包结构约束：`internal/pool/router` 已接近/达到非测试文件预算，不应为一个小 wrapper 盲目新增第 21 个文件；可在职责一致且远低于 600 行的既有“预选号 admission wrapper”文件内加入独立类型，或在不造成 import cycle 的前提下建真正内聚子包。generated billing 包也接近行数预算，只做必要同步。最终以 `internal/codebudget` 门为准，不能扩大 baseline 逃避拆分。

## 11. 验证命令计划

所有 Go 命令先设置用户指定缓存：

```bash
export GOCACHE=/home/ubuntu/HUAKAI/.gocache
export GOTMPDIR=/home/ubuntu/HUAKAI/.gotmp
```

获批实施后的建议门，具体包名按最终改动收敛：

```bash
cd /home/ubuntu/HUAKAI/backend
go test ./internal/pool/... ./internal/modelbindingadminhttp/... ./internal/gatewayhttp/...
go test -race ./internal/pool/...
go test ./internal/codebudget
HUAKAI_DATABASE_URL='<隔离测试库>' go test -tags=integration_pg ./internal/pool/... ./internal/billing/... ./internal/registry/...
HUAKAI_DATABASE_URL='<隔离测试库>' go test -tags=e2e_concurrency ./cmd/gateway -run 'TestBindingConcurrency' -count=1 -timeout=20m
```

要求：money/concurrency 主测试若没有 `HUAKAI_DATABASE_URL` 应明确失败，而不是 `t.Skip` 后让门虚绿；测试日志不得打印 DSN 或凭据。迁移测试必须使用一次性数据库。前端按仓库现有 package manager 运行 routing 定向测试、类型检查与构建，不引入新依赖。

性能验收至少包含：饱和/未饱和两种路径的锁等待、count 查询计划、serialization retry 次数和 p95 延迟对比。不能用牺牲 hard-cap 正确性换取静默 fail-open。

## 12. 观测、回滚与上线保护

### 12.1 观测

- 结构化日志：tenant/binding 的非敏感 ID、cap、观测到的 active count、拒绝阶段（precheck/transaction）、claim ID、selector mode、endpoint family、数据库错误类别；不得记录 API key、上游凭据或请求正文。
- 指标：binding concurrency reject 总数、事务复核拒绝数、gate DB error、serialization retry exhausted、active count 查询/锁等待时延、abort failure。binding ID 作为 metrics label 可能形成高基数，默认只进日志或受控聚合，不直接成为无界 label。
- 审计：配置旧值/新值、操作者、tenant、时间；不把运行时每次拒绝塞进管理审计表造成写放大。

### 12.2 回滚

- 代码回滚必须先保证所有新版本写出的 acquisition 仍可由旧 settle/sweep 按现有 status 正常释放；新增 nullable 列不会破坏旧读写。
- 推荐先保留 0183 列/索引，只回滚运行时读取/强制逻辑；不要在有新数据的生产表上急删列。
- 0183 down 只作为受控 schema rollback 文件，不应成为紧急关闸手段。若 D8 选择额外开关，开关只能暂时停用新 admission，不能改变钱账/释放语义；停用时要有显式审计和告警。
- 如果新 gate 导致异常大面积拒绝，安全处置顺序是：确认配置与 DB 健康 → 调高/置空受影响 binding 上限 → 验证 active count/hold 回收 → 再考虑运行时回滚。不得直接删 acquisition 或手改 ledger。

## 13. Owner 决策点（综合计划必须逐项落槌）

### D1 — 是否批准 0183 非破坏性向上迁移【高风险，必须确认】

建议：批准只加 nullable `binding_id` 与指定部分索引，不回填、不加独立计数、不改现有约束/数据。确认 down 文件可存在，但生产不把 drop column 当普通紧急回滚。

### D2 — 是否批准“两层闸 + 事务内线性化”修正【正确性阻断项】

建议：批准。外层 count 仅快速拒绝；真正 hard cap 必须在 acquisition insert 同一事务中按 binding 串行化并复核。若只批准外层 wrapper，本计划判定不能诚实声称“硬并发上限”，应停止实施而非交付会超发的版本。

还需在综合稿选定具体串行化原语：优先 binding 行事务锁；若选择 advisory lock，必须给出无碰撞命名空间、tenant 校验、锁序与管理更新一致性证明。

### D3 — 哪些 endpoint 同片生效【功能完整性】

建议：所有能命中同一 model binding 并进入 pool selector 的生产入口同片覆盖：chat、completions、embeddings、images、rerank、audio。chat-only 会留下明确旁路；若 Owner 为缩小首片而接受，状态只能写“chat path safe equivalent/分片中”，其余必须进入 Mandatory Roadmap，不能把字段标为全局已生效。

### D4 — `NULL` 与 `0` 的语义【兼容性】

建议：两者均为不限并发，只有正整数启用；负数在 API 入口 4xx。这样与现有非负 CHECK 及默认存量配置兼容。若 0 被定义为“拒绝全部”，必须明确迁移/运营提示，否则现有 0 值会造成停服。

### D5 — 是否本片提供前端可见配置入口【Owner 特别要求】

建议：本片直接提供可见的非负数字输入、`NULL/0` 说明与当前生效值，因为字段一旦影响生产硬拒绝，运营不可见会增加事故风险。即使 Owner 决定可见入口延期，本片也必须最少修复 PATCH round-trip，防止编辑 priority/selection mode 时静默清空既有限额；该最小修复不涉及 Sidebar。

### D6 — 429 客户端契约【API 稳定性】

建议：新增明确的 `binding_concurrency_limit_exceeded`（最终命名遵循 clienterr 规范）和独立 abort reason，不复用文案错误的 `key_rate_limited`。`Retry-After` 因释放时间未知，可采用现有保守 1 秒并标明估算，或不发；Owner 需统一选择。任何选择都应在全部 endpoint 一致。

### D7 — 运营改限额何时生效【一致性】

建议：事务硬闸在有正限额的请求上锁定 binding 行并读取数据库当前值；已解析但尚未占槽的请求尽量尊重最新值。若为降低未限流 binding 的锁开销而仅信任 registry 快照决定是否进入硬闸，则从 `NULL/0→正数` 的生效要等 registry 传播，必须明确最大延迟、观测与测试，不能宣称瞬时生效。

### D8 — 是否需要额外运行时开关【上线策略】

建议：不增加全局 env 开关；`max_parallel_requests` 本身是逐 binding、默认惰性的 opt-in，双重开关容易造成“配置看似生效、运行时实际关闭”。若 Owner 因最高风险要求 kill switch，应默认开启且后台显式展示/审计关闭状态，并将关闭视为临时事故处置，而非长期旁路。

## 14. 双计划交叉讨论清单

Claude 与 Codex 独立稿完成后，应先做差异表，再写无后缀综合稿。至少逐项比较：

1. hard-cap 线性化点是否真的与 insert 同事务；是否有人只设计了外层 precheck。
2. 选的是 binding 行锁还是 transaction advisory lock；锁序、缓存传播与管理更新边界是否一致。
3. endpoint scope 是否完整；chat-only 是否被误称为全局完成。
4. `NULL/0`、降配、删除/禁用、过期未扫行的语义是否一致。
5. 429 code/reason、Retry-After 与 PASR/default fallback 是否一致。
6. UI 是可见入口还是保留-only；是否发现现有 PATCH 会清空隐藏字段。
7. 真 PG 测试是否有 barrier 和数据库中间态断言，还是只数最终 HTTP 结果。
8. money 断言是否覆盖 rejected/success/abort/disconnect/sweep，且没有新造计费补偿。
9. migration 的锁影响、down 策略、手工 generated 同步和 codebudget 是否有遗漏。
10. 观测是否避免高基数 metrics 与敏感信息泄漏。

冲突由 Owner 选择；任何一方独立发现的 gap 必须进入综合稿或记录明确的拒绝理由，不可静默丢弃。

## 15. 执行前最终清单

- [ ] Claude 独立计划已完成，双方未在独立起草阶段互看。
- [ ] 已形成协议/冲突/gap 差异表。
- [ ] 无后缀综合计划已写入并获 Owner 明确批准。
- [ ] D1–D8 均有结论；D1/D2/D3/D7 未留模糊词。
- [ ] 再次确认 migration 0183 未冲突。
- [ ] 真 PostgreSQL 隔离库可用，凭据不会进日志。
- [ ] 旧代码下 BC-PG-001/002/003/004 的关键测试先证红。
- [ ] 所有 production `SelectionRequest` caller 已列清。
- [ ] 锁序与事务 retry 行为已有书面证明。
- [ ] SQL source/generated 手工同步范围已固定，明确不跑 sqlc。
- [ ] codebudget、竞态、钱账、迁移、前端与完整 reviewer-lane 命令已进入执行单。
- [ ] 无 `LICENSE`、Sidebar、auth core、billing ledger/quota schema 或新 dependency 变更。

在以上清单完成前，Codex 停止实施。

## 16. 风险结论

- **功能缩水**：本计划不删任何功能；推荐全端点生效。若选择 chat-only，必须显式降格为阶段性交付并保留 Mandatory Roadmap，不能静默遗漏。
- **clean-room 风险**：本轮未读取外部借鉴源码，只基于 Owner 已给结论和 HUAKAI 内部事实规划；没有复制标识符、结构、注释、schema 或算法。后续代码注释只描述 HUAKAI 机制。
- **安全风险**：主要是错误配置导致 DoS、跨租户串计数、数据库故障 fail-open、锁热点与日志高基数/敏感信息。本文给出 fail-closed、tenant 校验、锁序、低基数观测与可恢复配置路径。
- **money 风险**：binding 拒绝处于 Reserve 之后，若分类/abort 漏接会冻结 hold；因此 money 真 PG 断言与 detached abort 是 S0/S1 级 landing gate，绝不以单元 mock 替代。

本计划到此停止，等待 Claude 综合裁定与 Owner 确认后再进入任何实现动作。
