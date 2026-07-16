# 2026-07-14 绑定级 Weight 选号加权 Codex 独立计划

> 独立性声明：本计划由 Codex 在未读取任何同描述符 Claude 计划的前提下独立形成。只读取了 HUAKAI 内部规则、路由/注册表/网关实现与现有测试；未读取任何参考项目源码。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI 绑定级 Weight 真生效 · 选号加权（碰核心选号链，不碰钱不碰 schema）”；“加权只在 Priority 相等的候选间进行，绝不跨优先级洗序”；“改完把实现留在工作树停下等审查”。 |
| Scope | **范围内**：`registry.BindingMetadata` → `router.PoolCandidateMeta` 的 `Priority/Weight` 透传；`DefaultRouter.Plan` 在同优先级层内生成加权无放回顺序；可注入固定 seed 的随机源及并发保护；判别测试、旧行为回归、注释同步与指定门禁。**范围外**：钱路径、quota/billing/auth、数据库 schema/迁移、SQL 生成物、依赖、前端、`Sidebar.tsx`、参考项目源码、git 暂存与提交操作。 |
| Success criteria | 1. `PoolCandidateMeta` 携带 `Priority int32`、`Weight int32`，网关翻译层逐字段透传。2. 同一 `Priority` 内首选概率与归一权重相符，`1:9` 固定 seed 大样本中重权候选约 90%。3. 较低优先级（数值更大）无论权重多大都不能越过较高优先级。4. `Weight<=0` 归一为 1，零权重候选存在稳定的非零命中。5. 输入切片不被修改；元数据缺失时保持旧候选顺序；attempt 数量、预算、`primary/same_pool_account_failover/cross_pool_fallback`、上游模型映射语义不变。6. 单候选与各 `SelectionMode` 值回归通过。7. 指定 build/vet/test/cmd-gateway/codebudget 门全绿。 |
| Time estimate | 实现与判别测试约 45–70 分钟；定向测试、race、build/vet 与门禁约 20–40 分钟；总墙钟约 1–2 小时，单 agent 工时约 1.5 小时。当前仅产出计划，执行时间从 Owner 批准综合计划后计算。 |
| Blast radius | 直接影响所有经 `DefaultRouter.Plan` 的多 pool 请求首选池与回退池顺序；不改变候选资格、池内账号选择、attempt 上限、重试结束类或结算。错误的分层会让低优先级池抢流量；随机源竞态会影响并发网关稳定性；翻译层漏字段会令功能继续为死配置。 |
| Failure modes | 见“风险与缓解”。核心缓解是：只在 `PoolCandidates` 副本的连续同优先级段内洗序；缺元数据候选不参与洗序；`Weight<=0` 归一为 1；总权重使用 `int64`；共享 `*rand.Rand` 由 mutex 保护；用精确值断言和固定 seed 分布测试防弱测试。 |
| Decision points | 1. **策略版本**：核心选号行为变化后，建议把 Router `SnapshotVersion` 从旧策略值升级为新的绑定加权策略值，避免审计中同一版本代表两种策略；需在 Claude/Codex 计划综合时确认具体版本字符串。2. **部分元数据**：本计划采用“缺失候选保持原位单例，其余仅对连续、元数据完整且 Priority 相等的小段加权”，既不跨层也不因单条缺失关闭其他合法同层加权；若另一计划提出全量回退，交由 Owner 裁决。除此之外无 schema、依赖或高风险决策点。 |
| Pre-execution checklist | 1. Owner/Claude 形成独立计划并与本计划做 agreements/conflicts/gaps 差异。2. Owner 批准综合计划。3. 再次记录 `git status --short`，确认并行工作树改动并避开非本任务文件。4. 核对 `route_plan.go`、`default_router.go`、`chat_completions_attempt.go`、对应测试当前内容与 codebudget。5. 确认 `registry.sql.go` 仍按 `priority ASC, id ASC`，`postgres_registry.go` 仍填入 `Priority/Weight`，仅验证、不修改。6. 先写判别测试并确认旧实现按预期变红，再做最小实现。7. 全程不运行 `git add/commit/checkout/restore/stash`。 |

## 1. 已核实的 HUAKAI 内部事实

- `backend/sql/queries/registry.sql` 与生成的 `backend/internal/db/registry/registry.sql.go` 已按 `priority ASC, id ASC` 返回有效绑定。
- `backend/internal/registry/postgres_registry.go` 已把查询行的 `Priority/Weight` 写入 `BindingMetadata`；无需修改解析逻辑、SQL 或 schema。
- `backend/internal/gatewayhttp/chat_completions_attempt.go` 当前只向 `PoolCandidateMeta` 透传 pool、上游模型和 `SelectionMode`，这里是字段丢失点。
- `backend/internal/router/default_router.go` 当前直接按 `PoolCandidates` 展开 attempt；预算为单池 2、两池及以上 3，reason 依赖展开后的首次/重复出现。
- `backend/cmd/gateway/wiring.go` 只创建一个 `DefaultRouter` 并复用；`math/rand.Rand` 本身不支持并发使用，因此生产随机源必须加锁。
- 现有 `pool/router` 已有同类权重归一与蓄水池抽样模式；本任务只复用 HUAKAI 自身的保证形式，不读取或翻译任何外部源码。

## 2. 拟修改文件

| 文件 | 计划改动 | 约束 |
| --- | --- | --- |
| `backend/internal/router/route_plan.go` | 给 `PoolCandidateMeta` 增加 `Priority`、`Weight`，把注释改为“registry 顺序提供硬分层、Router 仅在同层加权”。 | 生产注释全中文，不出现参考项目名。 |
| `backend/internal/gatewayhttp/chat_completions_attempt.go` | 翻译 `BindingMetadata.Priority/Weight`。 | 仅增加字段映射；不新增根包生产文件，避免继续扩大 `gatewayhttp`。 |
| `backend/internal/router/default_router.go` | 注入默认非固定 seed 的 `*rand.Rand`；增加 mutex；对候选副本分段执行加权无放回洗序；Plan 使用该副本展开。必要时升级策略版本。 | 不改预算、reason 判定、retry class、capability、模型 override。文件预计仍低于 600 行。 |
| `backend/internal/registry/registry.go` | 删除 `Weight`“无选号消费”的过时注释，改为真实消费说明。 | 不改类型和解析。 |
| `backend/internal/router/default_router_weighted_test.go`（新测试文件） | 放置绑定级加权、优先级隔离、零权重、单候选/SelectionMode、输入不变与并发安全测试。 | `_test.go` 不计生产 codebudget；测试注释全中文并写明变异证红点。 |
| `backend/internal/router/router_test.go` | 必要时为只验证上游模型覆盖的旧 fixture 补明确且不同的 Priority，避免它无意进入新增随机分支；同步策略版本期望。 | 不放宽任何旧断言。 |
| `backend/internal/gatewayhttp/chat_completions_dispatch_test.go` | 精确断言翻译后的 `Priority/Weight`。 | 变异删除任一透传字段时直接红。 |

不计划修改 `backend/sql/queries/registry.sql`、`backend/internal/db/registry/registry.sql.go`、迁移、OpenAPI、前端、依赖清单或 codebudget baseline。

## 3. 算法保证形式

1. 复制 `PoolCandidates`，绝不原地修改 `ResolvedModel` 输入。
2. 用现有“首条元数据胜出”规则按 `PoolGroupID` 建索引，继续供上游模型映射复用。
3. 线性扫描 registry 已排序的候选：
   - 找不到元数据的候选视为不可洗序单例，原位保留；
   - 从一条有元数据候选开始，只把后续**连续且 Priority 完全相等、同样有元数据**的候选纳入该层；
   - 不以 `SelectionMode` 开关绑定级权重，避免把另一个“池内账号选号”字段误用到 pool binding 层。
4. 对每个长度大于 1 的层做加权无放回排列：每个位置在尚未放置的候选里用蓄水池法按权重选一个并交换；`Weight<=0` 的有效权重为 1。
5. 整次排列持有随机源 mutex，既避免 data race，也避免一次 Plan 的随机序列被其他请求穿插。
6. Plan 后续仍使用原循环、`seenPools`、`attemptBudgetForPools` 与 reason 赋值，仅把索引来源从原候选改为加权后的候选副本。

该结构给出硬证据：任何 swap 的两个下标都属于同一个已验证 `Priority` 段，算法中不存在跨段 swap；因此低优先级候选不可能越过高优先级段。

## 4. 判别测试矩阵

| ID | 前置条件与步骤 | 精确期望 | 变异证红点 |
| --- | --- | --- | --- |
| BW-AT-01 字段消费链 | registry 结果含两条不同 `Priority/Weight` 的绑定，经 `routerResolvedModelFromRegistry` 翻译。 | 每个 `PoolCandidateMeta` 的 pool、模型、`SelectionMode`、`Priority`、`Weight` 全部精确相等。 | 删除 Priority 或 Weight 映射后，结构体精确值断言红。 |
| BW-AT-02 1:9 分布 | 两候选同 Priority，轻候选权重 1、重候选权重 9；固定 seed 连续执行大样本 Plan，统计 `Attempts[0]`。 | 重候选命中率落在窄容差（计划 88%–92%）且两边都命中。 | 删除加权/恢复固定顺序时重候选为 0% 或 100%；改成均匀时约 50%，均不在区间。 |
| BW-AT-03 硬优先级 | 高优先候选（数值小）权重 1，低优先候选权重极大；固定 seed 多次 Plan。 | 每次 `Attempts[0]` 都属于高优先层；完整前三 attempt 中高优先层整体位于低优先层之前。 | 把所有候选放进同一个加权集合后，低优先重权候选至少一次越层，断言红。 |
| BW-AT-04 零权重补底额 | 同 Priority 的零权重候选对权重 9 候选，固定 seed 大样本统计 primary。 | 零权重候选稳定命中约 10%（计划容差 8%–12%），绝非 0。 | 去掉 `<=0 → 1` 后为恒不命中或在总权重为零场景触发非法随机上界，测试红。 |
| BW-AT-05 单候选与 SelectionMode | 对 `""`、`strict_priority`、`priority_weighted` 分别给单候选及任意权重。 | 两次 attempt 仍为同 pool，reason 依次为 `primary`、`same_pool_account_failover`，预算仍为 2。 | 随机化错误改变长度、reason、预算或把 SelectionMode 当 binding 层开关时红。 |
| BW-AT-06 旧回退与输入不变 | 无 PoolMetadata / 部分缺元数据，并在 Plan 前后保存输入候选副本。 | 无元数据时严格保持旧顺序；缺元数据候选原位；输入切片逐项不变。 | 直接原地 Shuffle、把缺元数据零值当同层、或全局排序时红。 |
| BW-AT-07 并发随机安全 | 多 goroutine 共享一个 Router，重复规划同层候选；使用 `go test -race`。 | 无 race、无 panic，所有计划长度/候选集合合法。 | 去掉 mutex 后 race detector 红。 |

所有分布测试使用固定 seed、固定样本数和双边界，不用“结果不等于某个坏值”的弱断言，也不使用 `t.Skip` 掩盖零命中。

## 5. 具体执行顺序

1. 等待 Claude 独立计划；只在双方都完成后比较 agreements/conflicts/gaps，写综合计划并取得 Owner 批准。
2. 批准后记录工作树基线和相关文件 hash/差异，避开并行修改。
3. 先新增 BW-AT-01～07；运行定向测试，记录旧实现对 BW-AT-01/02/03/04 的真实失败，不伪造 RED 结果。
4. 在路由契约与网关翻译层补字段，并更新过时注释。
5. 在 `DefaultRouter` 内加入随机源注入、互斥与同层加权无放回排序；不抽取到无关包，不新增 runtime dependency。
6. 更新因新字段/新策略版本而需精确同步的旧 fixture；不弱化既有断言。
7. `gofmt` 后先跑 router 与翻译层定向测试，再跑 race、相关包、`cmd/gateway`、codebudget、build/vet。
8. 用 `git diff --check`、`git diff --name-only` 核实无迁移/前端/依赖/Sidebar/baseline 改动。
9. 运行 `git status --short` 并在最终中文报告逐项列出所有工作树改动；不 stage、不 commit，停下等审查。

## 6. 风险与缓解

| 风险 | 后果 | 缓解/验证 |
| --- | --- | --- |
| 全候选加权导致跨优先级 | 低优先级池抢占高优先级流量，违反硬分层 | 仅在相同 Priority 的连续段内 swap；BW-AT-03 反复验证。 |
| `Weight=0` 被当成禁用 | 配置存在但永久饿死 | `<=0` 归一为 1；BW-AT-04 要求非零且约 10%。 |
| `rand.Rand` 并发竞态 | data race、随机状态损坏或 panic | Router 内 mutex；BW-AT-07 加 `-race`。 |
| 修改输入候选切片 | 同一解析结果被其他消费者观察到随机顺序 | 始终复制；BW-AT-06 前后精确比对。 |
| 元数据部分缺失被零值误分组 | 未知优先级候选参与错误层 | 缺元数据候选原位单例，不读取零值 Priority/Weight。 |
| 加权首选改变后模型 override 错位 | 请求发往错误上游模型名 | 继续按 PoolGroupID 查元数据；旧模型 override 精确测试保持。 |
| 新随机行为让旧测试偶发 | CI flaky | 业务测试用构造器默认随机；需要精确顺序的测试显式固定 seed 或使用不同 Priority；所有分布测试固定 seed。 |
| 策略版本不升级 | 审计中同一版本对应两种选号语义 | 综合计划确认并升级 Router 策略版本，同时更新精确 stamp 测试。 |
| `chat_completions_attempt.go` 接近 600 行 | 增长撞 codebudget | 字段加入现有 literal，不新增 helper/文件；实现逻辑全部留在小型 `router` 包。 |

## 7. 计划门禁命令

从 `/home/ubuntu/HUAKAI/backend` 执行，统一使用 Owner 指定缓存：

```bash
export GOCACHE=/home/ubuntu/HUAKAI/.gocache
export GOTMPDIR=/home/ubuntu/HUAKAI/.gotmp

go test ./internal/router -count=1
go test -race ./internal/router -count=1
go test ./internal/router ./internal/gatewayhttp ./internal/registry -count=1
go test ./cmd/gateway -count=1
go test ./internal/codebudget -count=1
go build ./...
go vet ./...
```

仓库根目录补跑：

```bash
git diff --check
git status --short
```

若任何门失败，只修本任务引入的错误；若失败来自既有并行改动，保留原始命令与错误并如实报告，不使用禁用的 git 命令清理他人工作。

## 8. 交叉讨论时必须比较的点

- 双方是否都把 `Priority` 当硬边界，而非只作为初始排序提示。
- 双方是否都识别共享 Router 的 RNG 并发安全问题。
- 加权排列是“仅选 primary”还是“对同层生成完整无放回回退顺序”；本计划选择后者，符合“洗序”且让后续 fallback 也按剩余权重排序。
- 部分元数据时是全量回退还是缺失项原位单例；本计划选择后者。
- 是否升级 Router 策略版本，以及随机决策在现有审计字段下能否满足追溯要求。
- 是否有任何方案误把 `SelectionMode` 当成绑定层 Weight 的开关；本计划明确不耦合。
