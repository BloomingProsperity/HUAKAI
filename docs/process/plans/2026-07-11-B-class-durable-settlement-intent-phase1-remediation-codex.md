# 2026-07-11 B 类持久结算意图阶段 1 对抗修复 Codex 独立计划

> 合成记录：Codex 独立草案完成后，已与既有 Claude 阶段 1 设计交叉比对。本文件按本轮 Owner 明确指令完成合成；当前指令同时是对旧设计冲突的裁决与执行授权。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “本轮在现有未提交改动基础上修这 9 条，不重做。迁移 0175 尚未应用于任何库，故改 DDL 是自由的（不需第二次迁移）。” |
| Scope | 修正 `settlement_intents` 唯一键、外键与金额/计数约束；使用 `billing.ReserveResult.AttemptSeq`；让恢复入队结果、`actual_cost`、客户端完整写证据、真实首字节时刻进入意图生命周期；补齐流式 handler 接线、生产 Store 构造和全量 fail-open 判别测试；清理测试中的开发过程叙述。 |
| Out of scope | 不修改 Reserve/hold 准入、余额扣减、结算金额口径、actual cost 权威来源、billing 核心、灰度默认值；不新增第二个迁移；不实现 sweeper；不执行 `git add`、`git commit`、`git push`。 |
| Success criteria | S1-A 至 S1-H 与 S2-1 均有实现修复和能区分错误实现的测试；真 PostgreSQL 验证复合唯一键、复活 attempt 并存、claim 外键与 CHECK；短写/断连不产生伪交付；取消/慢库不阻塞主交付且 `first_byte_at` 不早于真实写出；双失败落 `failed` 并保存真实 `actual_cost`；enabled/disabled 生产装配可判别；所有指定门禁通过。 |
| Time estimate | 约 6—10 小时墙钟时间；单 agent 约 10—16 小时工程时间，主要取决于 handler 级流式夹具、真 PostgreSQL fixture 与全量门禁反馈。 |
| Blast radius | 触及尚未应用的 money-path 新表 DDL、sqlc 参数/返回结构、relay 流式与非流式交付时序、结算恢复旁路和生产依赖装配。错误可导致串账、意图终态失真、漏存实际金额、请求被旁路数据库拖住或灰度开启仍永久 no-op。 |
| Failure modes | FK 引用列不具备唯一约束：先读取现有账本 DDL，再选择租户复合 FK 或单列 FK；sqlc 参数错位：保持查询参数顺序并运行生成一致性检查；写证据回调放错时点：以完整写返回值和记录时钟夹具验证；取消后终态丢失：只让交付前写尊重请求取消，终态仍用有界 `WithoutCancel`；测试夹具绕过 handler/生产工厂：从真实 handler 入口和 runtime 工厂构造断言；日志泄密：捕获 `slog` 并断言无原始错误或密钥；门禁空转：每门最多三轮，连续三次不通过即停止并如实报告。 |
| Decision points | 当前 Owner 指令已明确授权修改未应用的 `0175` DDL，以及仅在新增意图旁路中传播权威 attempt、实际金额和恢复入队结果。若现有 `billing_ledger_claims` 无法建立合法 FK 而需要修改既有表/迁移，或修复必须改变 Reserve、额度、账本、结算金额权威口径，则立即停止并请求 Owner 确认。 |
| Pre-execution checklist | 见下列清单。 |

## 独立风险判断

- 最高风险是把 HTTP 请求标识误当作 claim attempt 身份。意图行防串账身份必须由租户、claim 和 billing 权威 attempt 组成；请求标识只保留可观测性。
- `first_byte_at` 必须是成功写出业务字节后的证据，不能是准备写、调用数据库或进入 handler 的时间。
- `settling` 与 `failed` 的区别必须取决于恢复工作是否真实入队成功，不能只取决于恢复组件是否配置。
- 意图旁路对主请求始终 fail-open；“不阻断”与“吞掉可观测错误”不是一回事，warning 必须脱敏且覆盖每个操作。
- handler 接线和生产 runtime 构造都要从真实入口验证，不能用测试直接注入同一依赖来证明生产接线存在。

## 双计划交叉讨论结论

### 一致项

- 意图证据必须在 relay 生命周期中覆盖 pending、首个业务交付和结算/Abort 终态。
- 新意图表是旁路证据，写失败不得改变原请求、Reserve、结算或 Abort 的控制流。
- 灰度开关保持默认关闭，真 PostgreSQL 与 handler/runtime 真实入口测试是落地门槛。
- 不改变 Reserve、hold、余额、额度、结算金额和 actual cost 的权威口径。

### 冲突项及裁决

- 旧 Claude/Codex 阶段 1 计划都把 `(request_id, attempt_seq)` 当唯一身份；本轮 Owner 已指出 `request_id` 每个 HTTP 请求都会重建，不能防 claim 复活串账，裁决为 `UNIQUE(tenant_id, claim_id, attempt_seq)`。
- 旧 Codex 计划允许 tracker 本地 attempt；本轮 Owner 已指定 `billing.ReserveResult.AttemptSeq` 是唯一权威值，裁决为删除本地自增。
- Claude 总体设计涉及 Reserve Tx1 原子合并、sweeper、恢复 proof 和运维裁决；本轮明确只修阶段 1 九项，以上保持 out of scope，避免扩大 money-path 变更面。

### 单方发现的缺口

- 本轮对抗结果新增了 claim FK、非负 CHECK、`actual_cost` 终态持久化和恢复入队真值要求，原两份阶段 1 计划均未闭合。
- 原计划没有覆盖 L2 cache-hit 的短写/断连、交付前旁路写尊重取消、`first_byte_at` 真实写出时刻。
- 原测试策略只能证明直接注入的 forwarder callback 和 routes 字段复制，不能证明 handler 接线或生产 Store 构造。
- 原 fail-open 测试只覆盖 Insert 错误，缺少六操作 × error/timeout、nil store、id==0 和脱敏 warning 证据。

## 执行前检查清单

1. 记录工作树基线，逐文件区分本轮修复与既有未提交阶段 1 改动，不覆盖无关改动。
2. 读取 `billing_ledger_claims` 的主键/唯一约束、`ReserveResult.AttemptSeq`、claim 复活路径和现有真 PostgreSQL fixture。
3. 读取 `0175`、sqlc 查询/生成代码、`settlementintent` Store/tracker、结算恢复函数、非流式与 L2 cache-hit 写路径、流式 handler 接线和 runtime 工厂。
4. 先列出九项现状证据及对应测试入口，再开始修改；每项测试必须断言正确结果，而不是只断言“不等于错误值”。
5. 保留灰度默认关闭，确认新增依赖不会改变 disabled/nil store 路径。
6. 任何需要修改既有 schema、billing 核心、额度/扣款或 actual cost 来源的情况立即停止。

## 具体执行顺序

1. **S1-A / S1-B 数据身份与约束**：确认 claim 键后修改 `0175`；更新 sqlc 结构；补真 PostgreSQL 重复 attempt、复活 attempt、缺失 claim 和 CHECK 判别测试，并修正 fixture 先创建真实 claim。
2. **S1-A 权威 attempt**：删除 tracker 本地 attempt 自增；Reserve 成功后直接传 `ReserveResult.AttemptSeq`；用同 claim 两个 attempt 验证意图行身份。
3. **S1-C 恢复与金额**：让结算恢复函数返回实际入队结果；`MarkSettling`/`MarkFailed` 全链路接收并持久化 `actual_cost`；覆盖 settle+enqueue 双失败和单失败恢复成功。
4. **S1-D / S1-E 非流式与 cache-hit 交付证据**：统一完整写判定；成功写后记录真实时刻；交付前旁路写改为请求 ctx 的短预算，终态继续有界脱离取消；覆盖短写、零写、断连、慢库和取消。
5. **S1-F 流式真实入口**：从 `stream=true` handler 驱动 forwarder，覆盖成功生命周期、恢复生命周期与客户端写失败；测试必须依赖 handler 对首业务帧回调的接线。
6. **S1-G 生产工厂**：抽取或使用现有可测试 runtime 构造边界，断言 enabled 构造非 nil Store、disabled 为 nil，并同时验证 routes 复制。
7. **S1-H fail-open 网格**：表驱动覆盖六个操作的 error/timeout、nil store、id==0；每格断言 HTTP 200、原结算/Abort 次数不变、warning 存在且不含原错误/密钥。
8. **S2-1 注释清理**：扫描本轮及阶段 1 新增测试注释，统一改成业务不变量和失败场景，不出现开发过程、任务号、日期或借鉴项目名。
9. **判别证据**：先跑正确实现测试；再对每个要求的关键点做临时工作树变异并运行定向测试，保存真实失败摘要后立即恢复变异；不借助 commit/stash，不覆盖其他未提交改动。
10. **门禁**：检查改动 Go 文件 `gofmt -l` 为空，依次跑 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、官方纯净 PostgreSQL 脚本、codebudget、必要时 OpenAPI 一致性；每门最多三轮。
11. **交付**：检查最终 diff 与未跟踪文件，确认无 `git add/commit/push`，按 S1-A 至 S2-1 报告文件、意图、DDL、真实判别输出、门禁摘要和剩余风险。

## 测试判别红点

| 项目 | 错误实现必须触发的失败 |
| --- | --- |
| S1-A | 唯一键退回请求标识，或 attempt 退回 tracker 本地计数时，同租户同 claim 同 attempt 的重复写不再被拒绝，复活身份断言失败。 |
| S1-B | 移除 FK 或任一 CHECK 时，不存在 claim 或非法计数/金额被数据库接受。 |
| S1-C | 仅检查 DLQ 配置或丢弃入队错误时，双失败错误落成 `settling`；漏传金额时终态 `actual_cost` 不等于权威值。 |
| S1-D | cache-hit 无条件标记时，短写/断连后仍观察到 delivering/settled。 |
| S1-E | 标记移回写前或使用 `WithoutCancel` 时，`first_byte_at` 早于真实写出，或取消后旁路慢库拖住请求。 |
| S1-F | 删除 handler 对首业务帧回调的注入时，handler 级流式生命周期缺少 delivering 或终态。 |
| S1-G | 删除生产 Store 构造或写死开关时，enabled/disabled 工厂断言失败。 |
| S1-H | 任一 Store 错误被改成主链路 return、warning 被吞或泄漏原始错误时，对应表格单元失败。 |
| S2-1 | 注释扫描发现开发过程、任务号、日期或借鉴项目名时，洁净规则检查失败。 |

## 明确不变量

- 功能不缩水：意图行仍覆盖 pending、delivering、settled、settling、failed、aborted，灰度默认关闭不变。
- clean-room：只读取 HUAKAI 内部代码与已给定行为要求，不读取任何参考项目源码，不引入其标识、结构或注释。
- money-path：Reserve、hold、余额、额度、结算金额和 actual cost 权威来源保持不变；新增意图表只记录旁路证据。
- 安全：warning 只记录稳定操作码和脱敏上下文，不回显原始错误、密钥或请求正文。
