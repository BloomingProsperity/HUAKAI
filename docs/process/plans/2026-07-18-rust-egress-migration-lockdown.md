# 2026-07-18 Rust 出口迁移锁定（唯一执行计划）

> 本文由 Codex 依据 Owner 最新指令、当前源码和测试独立维护。
> **事实来源顺序：Owner 最新指令 -> 当前源码与测试 -> 本文。**
> 本目标关闭并行双计划；架构、实现、测试和收尾均由 Codex 统一负责。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “需要让你做，这个是当前目标”；“边界不要定太死，一切以能上线、能跑为核心”；“完整报告看下，核实之后再动手修复，并入刚刚给你的需求里面”；“有错误就直接修复”；“并行双计划关闭，全靠你一个人”；“发现一个问题时必须沿完整业务链、关联模块、失败恢复和运维面展开；成熟项目已经具备而我们缺失的有效能力，必须进入实现或明确的强制路线”；过期规则、文档和代码注释可以清理。 |
| 最终目标 | mimicry TLS 唯一由 Rust `tls-sidecar` 执行并可单镜像上线；同时关闭全局 Renew 报告经源码坐实的钱路、鉴权、调度、健康、多实例与恢复缺陷，删除被证伪结论和旧链路。 |
| 不变边界 | standard API key 继续走 Go 标准 transport；应用层 body mimicry 保持 Go；不复制外部项目实现；未经判别测试证明的报告结论不得驱动架构。 |
| 工作树 | `/home/ubuntu/HUAKAI-wt-baseline`，沿用 `fix/backend-closure-mvp`，不新建分支。 |
| 交付 | 小提交、逐提交 review、一个 PR，未经 Owner 同意不合主线。 |
| 估时 | 10-16 个工程日；真实账号/代理、PostgreSQL、Redis 与容器验证取决于本机可用条件。 |

## 并入切片：全局规则与 Skill 顺序治理

| 项目 | 内容 |
| --- | --- |
| Owner directive | “读一下我的规则和 skill，按照逻辑排序”；“涉及钱可以查看发卡网；别的模块可以全网搜索顶尖项目”；“刚刚的也要放进规则，对全局生效”；“必须读源码”。 |
| 范围 | 整理 `AGENTS.md`、模型适配入口、`docs/RULES.md`、release gate 与 `.agents/skills/*/SKILL.md`；`.claude/skills/` 只同步 canonical mirror。清理仍会被工具读取的过期固定角色、双计划和自动派发入口，不新建规则文档或平行计划。 |
| 成功标准 | 全局规则按真实执行生命周期排序；删除或明确退役与最新 Owner 指令冲突的并行双计划、固定角色和万能三镜条款；Skill 有唯一 canonical 来源和明确调用顺序；所有领域借鉴都强制读源码、执行 clean-room 前置门。 |
| 估时 | 2-4 小时整理与校验；不计后续逐领域源码调研时间。 |
| 爆炸半径 | 规则排序错误会让后续 agent 漏门、重复文档、误读外部源码或在错误领域只看中转站项目。 |
| 失败模式 | 误删仍有效规则：先做规则映射再重排；mirror 漂移：逐文件 `cmp`；旧引用断裂：检查 Markdown 链接和规则 ID；clean-room 缩水：保留完整 lane guard 与 source-must-read 门。 |
| 决策点 | 无新增 Owner 决策；按最新指令采用“中转站三镜基线 + 领域头部项目补强”，专业领域候选必须先验许可证、维护活跃度并读源码。 |
| 执行顺序 | 规则/Skill 全量读取 → 重复与冲突映射 → 整理 canonical 全局执行顺序 → 整理 Skill 调用顺序与职责 → 同步 Claude mirror → diff/check/review → 单独规则提交。 |

治理结果：

- [x] `AGENTS.md` 已按权威/启动门/领域证据/clean-room/行为合同/全链路/计划/实现/测试/review/Skill/PR/完成定义排序。
- [x] `CLAUDE.md`、`GEMINI.md` 已改为薄适配层，不再赋予固定 PM、前端或“小补丁”角色。
- [x] 12 个 canonical Skill 已统一为“触发 -> 输入 -> 步骤 -> 输出 -> 阻断项”，Claude mirror 机械同步。
- [x] 删除纯重复的 `docs/RULES-DIGEST.md`、旧中文 AI 协作/项目经理摘要，以及已经被当前规则完整覆盖的 PM/workflow/round-table/总纲文件；Git 历史承担追溯，不保留回滚占位页。
- [x] 删除 `.coordination` 自动 dispatcher/worker 文档、loop、旧任务和救援入口；只保留未来明确恢复多执行者时使用的最小文件冲突锁。
- [x] 删除同主题旧 Rust 出口计划和租户默认出口双计划草稿；已综合完成的独立历史切片只保留最终版本。
- [x] 发布门已去掉模型绑定并补齐 truth、source、clean-room、whole-chain、PostgreSQL、billing、ops、codebudget 和独立 review 门。
- [x] Skill validator、mirror `cmp`、Markdown 本地链接、保留脚本语法与 `git diff --check` 已通过。
- [x] 独立 Codex review 已通过；两项 P1 均来自本提交范围外的未跟踪文档，本批未纳入、未修改。

规则保全映射：

| 原有效规则族 | 当前落点 | 处理 |
| --- | --- | --- |
| Truth-first / Observed-Inferred-Open Question / Source Coverage | `AGENTS.md` §0、§1、§5.2 | 保留并前移到证据阶段 |
| Source-must-read / stale citation / repo@sha:file:line | `AGENTS.md` §5.2、`reference-project-miner` | 保留；三镜从万能规则修正为中转站基线，专业领域补头部项目 |
| Clean-room lane guard / provenance tail / no-op escalation | `AGENTS.md` §5.3、`clean-room-license-guard` | 保留完整门并新增 specifier 禁读 HUAKAI 实现 |
| Feature Preservation / disposition-status / merged equivalent | `AGENTS.md` §1、§6、parity/merger Skill | 保留 |
| Plan-before-execute | `AGENTS.md` §4、§8 | 保留；平行双计划被最新 Owner 指令替换为唯一计划 |
| 决策项目对照 | `AGENTS.md` §5、§8.2 | 保留；按领域选择证据，不拿无关项目凑数 |
| Module interplay / concurrency / recovery / Day-2 ops | `AGENTS.md` §6、§7、§10 | 保留并扩为全业务链与细粒度规则 |
| Codebudget / package-file responsibility | `AGENTS.md` §9、release gate | 保留；旧硬冻结继续退役 |
| Discriminating tests / mutation / smell library | `AGENTS.md` §10、§11.2 | 保留完整 fixture、SQL `WHERE`、AllowAll、skip、并发等 smell |
| Per-commit review / S0-S3 / two-round anti-spiral | `AGENTS.md` §11 | 保留；S2/S3 进入唯一计划或 commit body，不再堆 review 文档 |
| 固定 Claude/Codex/Gemini 角色、平行双稿、自动 dispatcher | 模型适配层与 `.coordination` 最小冲突锁 | 被最新 Owner 指令明确覆盖；旧说明、脚本和草稿直接删除 |

## 已锁决策

1. **Rust-only**：Go uTLS 不再新增功能；Rust 全门通过后删除。
2. **动态 profile 采用 inline**：Go 只做账号绑定/轮换选择和规范化，Rust 做严格校验与 BoringSSL 执行。数据库结构不变。
3. **单镜像双进程**：容器入口先起 sidecar并等 ready，再起 gateway；任一进程退出，容器整体退出，由容器平台重启。
4. **无长期双栈**：当前未上线，不维持兼容债；Git 历史是恢复手段，运行时不背两套 TLS 实现。
5. **H1 保持**：按 profile 的真实 ALPN/H1 行为；既有 H2 bridge 保留但不作为本轮启用条件。
6. **清理旧真相**：迁移完成后，过期规则、SSOT 旧现状、误导注释、旧开关和死代码一起清理，只留最新合同。

## 源码运行链

```text
账号认证模式/协议族
  -> ResolveDispatchTransport(provider, mode)
  -> builtin profile 或 tlsfpresolve 动态选择
  -> ProxyResolver
  -> RustSidecarRoundTripper
  -> IPC v2(capability + profile + proxy + target)
  -> Rust profile 校验
  -> proxy tunnel/直连二选一
  -> BoringSSL(SNI/ALPN/ClientHello)
  -> H1 raw tunnel / profile 驱动的既有 H2 bridge
  -> Go HTTP response
  -> retry/failover/channel health/audit
  -> buffered 或 streaming client response
```

Rust 出口迁移开始时的四个硬缺口：

- [待收尾] factory 在 socket 为空时仍回 Go uTLS，并保留 native fallback。
- [已关闭，`29cfc50a`] Rust 已接线 Anthropic、Codex、Gemini、Kiro 四个内置 profile。
- [已关闭，`29cfc50a`] DB 动态 profile 已改为 inline IPC，由 Rust 严格校验和执行。
- [部分关闭，`29cfc50a`] IPC v2、capabilities、ready、结构化错误、取消与超时已完成；联合容器生命周期仍待 S5。

全局 Renew 报告的核实结论：

- **坐实并实施**：DLQ poison 冻结、Replicate 重复付费重试、非密码登录绕过 2FA、退款未校验 captured、weighted 失效、ramp 低流量/热路径写、degraded 不降权、class 转移丢 exclusion、ramp key 漂移、DLQ 兜底丢弃、TTS 部分交付全退、配额基础设施 fail-open、结算意图默认关闭、诊断凭据轴失真、legacy 内联账号静默掉池、多实例限流/worker/readiness。
- **证伪并删除**：`channel_health_state` 完全没接生产选号、没有统一恢复原语、chat 裸 401 永远只进软冷却。源码已存在 `ServicePoolGate`、`RecoverAccountState` 和 auth challenge 分类；真实缺陷是状态轴重复、热路径写与诊断口径发散。
- **另行实现能力**：模型维度买家配额、可配置错误策略、缺失模型发现；不得把“参考项目有”当实现证据，必须从 HUAKAI 现有合同独立设计。

## Shape inventory

必须覆盖：builtin profile、账号绑定 profile、租户轮换池、direct、HTTP/HTTPS/SOCKS5 proxy、OAuth 热刷新后模型重试、buffered、streaming、取消、sidecar 不可达、profile/协议不匹配、启动/退出、容器 health、并发隔离、retry/failover/health/audit。

已有 mode 但没有 HUAKAI 内部实测 profile 的能力，不编造 profile、不静默 standard fallback；在账号可选和启动 preflight 处显式阻断，直到有内部批准数据。这样不伪装“已完成”，也不删除能力。

## 执行切片

### S0：基线合同与切片护栏

- 固定当前 mode/account/profile/proxy 矩阵和已有测试基线。
- 为 dynamic profile、sidecar-only、proxy fail-closed、流式取消写会抓真缺陷的判别测试。
- 记录现有 dirty 文件归属；所有临时产物放 `/home/ubuntu/.cache/huakai-codex/rust-egress/`。
- 门：Go targeted tests、Rust sidecar tests、codebudget 基线绿。

### S1：IPC v2 与真实 preflight

- IPC 增加协议版本、operation、capabilities、结构化错误和 inline profile 容器。
- 新增不连接上游的 `ready/capabilities/profile_exists` 操作，删除假目标+错误文本 probe。
- IPC 帧保留大小上限；inline profile 做字段数、长度、枚举、host 和 TLS 参数校验。
- 判别：未知版本/operation/capability/profile 明确失败，不挂起、不误报 available。

### S2：动态 profile 全链路

- `tlsfpresolve` 从“返回 Go RT”改为“返回规范化 profile 选择结果”。
- 保留账号绑定、租户 active 池、同账号稳定散列、坏 profile 安全语义。
- dispatcher 把选择结果注入 sidecar RT；proxy 仍在 profile 之后组合。
- Rust 用 inline profile 构建 BoringSSL，不写全局可变注册表。
- 判别：builtin 与动态 wire 必须不同；忽略 inline 的 mutation 必须红；同账号稳定、并发不串 profile。

### S3：builtin profile 与 mode 覆盖

- 把 HUAKAI 内部已批准的 Anthropic/Codex/Gemini/Kiro profile 迁入 Rust builtin。
- Go mode 映射补齐这些 profile；每个 mode 启动 preflight。
- JA3/JA4 以 BoringSSL 实际 ClientHello重新计算/校验，不能照搬 Go uTLS 期望值。
- Kiro 等随机字段按稳定维度判别，不用固定错误值强判。
- Gemini UA 只在内部已批准实测合同支持时同步修正。

### S4：错误、retry、health 与 audit

- Rust ACK 返回稳定错误类：协议/profile、proxy、DNS/connect、TLS、timeout、upstream reset。
- Go 映射现有 transport taxonomy；本地 sidecar故障不得误伤账号凭据/health。
- 关联日志不记录 token、proxy密码、请求体和 inline profile 原值。
- 覆盖模型请求 401 后 OAuth 热刷新/重试；refresh 端点继续现有 SSRF-protected standard client，除非真码合同另有要求。

### S4A：钱路、鉴权与恢复闭环

- Replicate 已创建付费 prediction 且取消未确认时，任何 class/fallback 分支都不得再发第二次付费请求。
- post-delivery poison 达阈值转 `operator_review`，停止自动认领但继续保护 hold；提供带审计、带账务证据检查的人工解决原语，不能自动免费服务。
- 所有可签发完整 session 的登录方式统一经过登录资格与 2FA 门；门配置读取失败 fail-closed。Passkey 是否作为第二因子必须由显式策略表达，不能靠路径旁路。
- refund 只能退已 captured 金额并按已退累计封顶；无 hold/未 capture 的 opt-in claim 不得凭空增余额。
- mismatch 退款从可信签名收据校验、服务端重新派生、差额计算、DLQ 入队、worker 原子执行一直验证到余额、账单、审计收据和 pending 状态；退款额必须等于旧签名收费事实与当前可信收费事实之差，伪签名、跨租户、under-charge 和未实际扣款均不得入队或增余额。
- 支付订单退款补齐真实原路退款：绑定创建订单的原支付实例与商户快照，使用精确金额，支持部分/累计退款、充值与订阅追回、渠道 `pending/succeeded/failed`、退款单号、查询确认、幂等重放、失败补偿、人工核验和对账。系统内部余额回收不得再冒充渠道已退真钱；不支持自动退款的渠道必须进入明确人工状态。
- TTS 已发送响应头或字节后失败按部分交付结算；结算意图默认开启并在写入失败时阻止不可恢复交付。
- 强配额在存储故障时默认 fail-closed；observe/未配置策略保持 no-op，不误伤空部署。

#### S4A-1：退款幂等事实与冲突闭环（已完成）

- `AuditRequestID` 只承担审计追踪，新增独立 `IdempotencyKey`；调用方必须提供稳定键，禁止再把审计号隐式当成幂等合同。
- 新增 append-only 退款事实表，原子记录租户、claim、幂等键、规范化请求摘要、请求金额、原因、精确模式、实际回补、累计覆盖、结果和账单事件引用；零金额跳过与既有调整已满足也必须留事实，保证之后重放结果不漂移。
- 同租户同幂等键先取事务级锁；摘要一致返回已存结果，金额、原因、模式或 claim 任一变化均返回显式冲突，余额和账单事件保持不变。历史退款若没有可证明的请求事实，不猜测等价，转人工消歧。
- 异步 mismatch 退款把稳定 DLQ 键传给账务层；幂等冲突属于结构性失败，pending 标失败且 DLQ 首次进入 `quarantined`，保留完整错误供运营处理，不无意义重试。
- 横向核对订单退款和其他资金入口；发现同类“同键只比部分字段”时一并收紧为完整业务效果比较。
- 判别测试：同键同请求稳定重放；同键改金额/原因/精确模式/claim 全部冲突；并发同键只产生一笔余额效果；累计退款不超 captured；冲突后事实、余额、事件数量不变；异步冲突进入隔离；订单退款同键改业务字段冲突。

#### S4A-2：退款与配额同事务闭环（已完成）

- mismatch 与成本争议两条退款入口都必须在钱包回补、负向账单、退款事实、配额成本冲减及审计事实的同一 PostgreSQL 事务内完成；任一强一致写失败就整笔回滚，由既有任务重试或人工恢复，禁止提交后只打日志。
- 配额冲减只对本事务新落地的实际退款金额执行；退款事实幂等重放不得再次冲减。没有可冲减预留或窗口时允许记录为跳过，存储错误与事务冲突不得伪装成成功。
- 成本争议必须同时绑定 tenant、user 与 request；唯一 committed claim 才能自动退款。同一用户仍命中多条时显式返回歧义错误并保持争议未终结，禁止按最新、最旧或任意第一条自动写钱。
- `AuditRequestID` 只承担追踪，不能兼任幂等键；不同退款操作允许共享追踪号。只有不存在退款事实关联的历史负向事件才进入人工消歧。
- 判别测试覆盖：配额写失败时钱包、账单、事实、回执和争议状态全部不变；成功时钱包回补额与配额冲减额相等；重放不双冲；同租户跨用户同 request 不串账；同用户多 claim 明确冲突。

S4A-1/S4A-2 执行结果（2026-07-18）：

- [x] migration 0193 新增 append-only `billing_refund_operations`，以租户级幂等键和规范请求摘要固化 claim、金额、原因、精确模式、实际回补、累计覆盖、结果和账单事件引用；从零迁移、单步 down/up 和约束核验通过。
- [x] 退款只允许 committed claim + captured hold + 现存余额行；按 claim 行锁和累计负向调整封顶。`RequireExact` 表示累计补偿目标，既有调整可满足或抵扣本次金额，超出 captured 明确失败。
- [x] `AuditRequestID` 只保留追踪语义；不同操作可共享追踪号。历史无请求事实的负向事件只在同一 claim 内触发人工消歧，不会串到同租户其他 claim。
- [x] mismatch 入队强制 `mismatch_pending + over_charge + 正差额`，并在入队前核验真实 captured 扣款；under-charge、伪签名、跨租户、无扣款和超 captured 均不能进入资金执行。
- [x] mismatch worker 校验 DLQ tenant/claim/source/幂等身份；退款、余额、账单、退款事实、审计账本、签名收据和配额冲减在 PostgreSQL 同一事务内提交。假 `completed` 会按真实证据重建，冲突或损坏证据进入失败/隔离，不盲信状态字段。
- [x] 重复审计账本必须同时匹配租户、请求号和退款语义；重复退款收据必须匹配 tenant/request/claim、签名证据、退款幂等引用和有效账单事件引用，不能任选第一条或仅看“存在”。
- [x] 成本争议只允许 tenant + user + logical request 唯一命中一条 committed claim 后自动退款；0 条明确无扣款，2 条及以上返回歧义冲突，争议状态与全部资金效果一起回滚。
- [x] 争议创建/裁决采用严格 JSON 合同，拒绝未知字段和尾随 JSON；生产 wiring 已核实注入事务退款器、配额冲减器、资格校验器和同一 PG 收据存储。
- [x] 订单内部余额退款幂等比较覆盖订单、金额、原因、actor kind/id/ref；重放还要求订单、用户、币种、终态以及关联 `payment_refunded` 账单事件恰好一条且金额精确相反。缺失或多义证据返回 `refund_fact_invalid`，不伪装成可重试后端故障。
- [x] 内部订单冲正允许一单多笔并按原入账累计封顶；部分冲正保持 `completed`，累计达到原入账额才进入 `refunded`。用户退款申请采用累计目标补差，即使此前已有人工部分冲正也只执行剩余金额；响应和资金日志同时给出本次、累计和剩余金额。
- [x] migration 0194 取消“一单一退款”旧约束，以订单行锁和数据库触发器双重守住累计上限、用户与币种一致性；退款事实只增不改，直接 SQL 也不能超退、串身份或改写历史。运营导出补充请求目标、精确模式和账单事件引用。
- [x] mismatch 恢复不再凭一张字段看似完整的收据直接宣布完成；必须先经账务幂等事实取得真实账单事件，再精确核对收据中的账单和日志账本引用，假 ID 或跨事实引用会失败并进入隔离。
- [x] 用户退款申请的资金冲正与 `approved` 决策已收拢到同一 PostgreSQL 事务；决策写失败时余额、退款事实和账单事件一起回滚。历史拆分事务遗留的 pending 申请可由另一管理员接管，但会按原请求语义做严格幂等复核，原资金操作者不被改写，且只有累计已全退才能收敛为 approved。
- [x] migration 0193/0194 的 down 已改为事实保护门：存在任何持久退款事实时明确拒绝降级，不再删事实或伪造“已全退”状态；空库仍可正常 down/up。`billing_refund_operations` 同时增加数据库级 append-only 保护。
- [x] 代码结构按职责拆出 `refund.go`、`refund_queue.go`、`refund_pending.go`、`refund_tx_retry.go`；不抬高 codebudget 基线。

验证证据：

- [x] `make test` 全后端包带 `-race -count=1` 通过，`go vet ./...` 与 `git diff --check` 通过；临时目录固定到 `/home/ubuntu/.cache/huakai-codex/go-tmp`。
- [x] gateway/audit/billing/controlhttp/gatewayhttp/payment/paymenthttp/quota/pool/codebudget 受影响包 `-race` 全部通过。
- [x] `scripts/integration-pg.sh` 初跑 75 个 PostgreSQL 包时 74 个通过，唯一失败是累计场景仍断言单笔旧结果；修正断言后 `paymenthttp` 在全新克隆库带 `-race` 通过，新增的不同幂等键并发累计上限测试也在 `payment` 全新克隆库通过。
- [x] migration 193 从零 up、down 1、再次 up 均通过；migration 194 从零完整迁移到 `194:false`，并通过迁移往返门、数据库累计上限、身份一致性及 append-only 判别测试。
- [x] 新增真 PostgreSQL 判别测试已通过：强制申请决策 UPDATE 失败后资金效果为零；另一管理员可恢复旧崩溃窗口且资金操作者不变；有事实的 0193/0194 down 失败并保留原状态，空库 194 -> 192 -> 194 往返通过。
- [x] 独立只读 Codex 复审结论为 `ACCEPT`：上一轮三个 S1 均已关闭，无新 S0/S1；唯一过期原子性注释已当场订正，未新建 review 文档。

Owner 已明确当前支付边界：HUAKAI 暂不接真实直付渠道，现有资金来源是管理员手动入账、兑换码等内部能力。因此当前退款只建设可验证的内部余额冲正、兑换事实补偿和人工留证，不建设不存在的自动原路退款，也不得在 API、运维面或文案中宣称渠道已退款。

后续边界：

1. **真实直付启用门**：只有未来明确接入真实 PSP、自动收款或程序化商户渠道时，才能同步启用外部原路退款切片。启用前必须保存建单时 provider/商户快照，新增渠道退款单、外部与内部双状态轴、部分/累计上限、幂等副作用、Webhook/查询确认、余额或订阅权益追回、对账和人工恢复。该能力是未来直付的强制前置门，不是当前未完成的上线阻塞项。
2. **三身份资金与争议授权**：Owner 已确认逐级管理边界。部署者只能增减下级租户这一层的额度，不能查看或修改下级租户名下用户的余额；下级租户管理员只能管理自己租户的用户；用户只能操作自己的资源。部署者可授予或撤销租户管理能力，但不能代租户裁决争议。当前源码没有独立租户钱包，`/admin/v1/balances/adjustments` 反而允许 platform admin 指定任意 tenant/user，且只支持正向用户充值；不能把某个 `users.role=admin` 用户余额冒充租户资金池。该项需要先确定租户钱包与逐级资金守恒模型，再修改 schema、API 和 auth core，当前不擅自实施。
3. **日志系统与保留**：项目产品层统一称“日志系统”，内部按操作、资金、安全、错误、访问和恢复分类，不另设并列的“审计系统”；既有 `audit` 技术标识不做破坏性改名。普通日志必须结构化记录时间、来源、操作者身份与范围、目标租户和对象、动作、结果、错误分类与错误码、严重级别、请求/链路/幂等关联和人工恢复状态；错误必须明确区分输入、认证、授权、冲突、余额不足、依赖故障、数据完整性及人工恢复。普通日志滚动保留 30 天并自动分批删除。`billing_events`、充值、退款、争议资金效果和幂等事实属于余额真相，不是可清理日志，必须永久保留。日志清理和查询都不能扩大部署者对下级租户用户资金明细的权限。

### S4B：调度、健康与诊断闭环

- `priority_weighted` 在同优先级候选带内按权重选，不要求 load/time 微秒完全相同。
- cooling 到期转 ramp 由后台 worker 持租约推进；请求热路径只读。低流量账号有可达的时间/样本推进条件。
- 默认 selector 健康优先于 degraded；目标 class 重试继承所有失败账号 exclusion；ramp key 对同请求/会话稳定。
- 健康诊断读取真实活动 credential，不再依赖冻死镜像列；legacy 内联凭据停止新建并提供明确迁移/启动诊断，不用扩大明文调度面。
- DLQ 未知兜底和 reconciliation 进入 quarantine，不再落入 metrics discard；恢复入口收敛到现有统一 service。

### S4C：多实例上线闭环

- 公网入站与登录节流使用共享存储；本地内存只允许显式单实例开发模式。
- 探测、聚合、签到等副作用 worker 统一竞争 leader lease；不得每副本重复打上游。
- 新增无鉴权 `/readyz`：DB、Rust sidecar、drain 状态可判；`/healthz` 仍仅表示进程存活。
- 主动探测遵守 Owner 已定成本预算 B、自动恢复范围 A；默认关闭真实付费探测，并具备租约去重。

### S4D：缺失运营能力

- 配额合同增加模型维度并贯穿 reserve/settle/audit/admin，不能只在 handler 做字符串限流。
- 上游错误策略支持受控匹配、客户端映射和“是否影响健康”，默认仍走安全固定目录。
- 模型同步产出“上游发现但目录未登记”的待处理清单，不能自动上架或静默忽略。

### S5：单镜像可运行交付

- Docker 增加固定 Rust/BoringSSL builder，复制 sidecar 与 gateway 两个二进制。
- 新入口负责 UDS 目录、权限、sidecar ready、gateway 启动、SIGTERM、双进程退出。
- UDS 使用容器内运行目录，不再默认 `/tmp`。
- dev/direct/prod Compose 使用同一镜像和相同合同；gateway readiness 同时验证 sidecar能力/profile。
- 不引入新的运行时第三方监督依赖，入口只用镜像已有 shell/进程原语。

### S6：重测试与容器 smoke

- Rust wire mutation：cipher、extension、sigalg、ALPN、force-H1、inline profile。
- proxy：HTTP/HTTPS/SOCKS5，认证/无认证；注入“代理失败后直连”必须红。
- 并发：数百混合账号/profile/proxy请求，流式长连接和短请求混跑，race/FD/task回收。
- 故障：坏帧、半帧、版本错、socket权限、慢握手、sidecar退出、gateway退出、旧 socket。
- 容器：冷构建、ready、standard 不经 sidecar、mimicry 经 Rust、kill 任一进程使容器退出并可重启。
- 可用受控真实账号时，选低成本模型/小 token 做四家 smoke；无账号时不得虚报真实验证通过。

### S7：Rust-only 翻转与旧实现删除

- factory 的 mimicry 分支必须要求 sidecar；socket 缺失/不可用 fail-closed。
- 删除 native uTLS、Go模板到 uTLS 转换、DB profile RT、native fallback、旧环境变量与相关测试。
- 移除 Go uTLS 依赖；保留 sidecar client、profile合同、proxy 和 standard transport。
- 清理已被七月决策覆盖的 `CB-001`、SSOT 旧现状、过期计划引用和误导代码注释。
- 删除清单逐项证明 Rust 替代已过测试；不保留“也许以后回滚”的死代码。

### S8：发布门与 PR

- Go：targeted、全量、vet、staticcheck、codebudget，必要的 PostgreSQL 集成。
- Rust：fmt、clippy、`cargo test -p tls-sidecar`、冷构建。
- Docker/Compose：三套 config、build、up、health、kill/restart smoke。
- 每个提交先 stage，再跑 Codex review；S0/S1 清零。
- 最终完整 cross-review；推送并开 PR，不合并主线。

## 判别测试原则

每个测试必须写清它抓的具体缺陷，并能说明 mutation：

- 删版本检查 -> 版本错测试红。
- 忽略 inline profile -> 两账号 wire 差异测试红。
- proxy 失败改直连 -> 出口泄漏测试红。
- sidecar 健康只看 socket 文件 -> kill/restart readiness 测试红。
- 取消传播删除 -> task/FD 回收测试红。
- mimicry 恢复 native fallback -> Rust-only factory 测试红。

## 成功标准

1. 单镜像冷构建成功，容器一次启动即 ready，可由普通部署者直接跑。
2. standard API key 保持 standard；启用的 OAuth/session mimicry 全走 Rust。
3. builtin、dynamic、轮换、proxy、stream、retry/failover/health/audit无缩水。
4. 用量错账退款有签名校验到资金与新收据的全链验收；支付退款区分内部余额回收与渠道原路退款，并具备异步确认和人工恢复。
5. sidecar/profile/协议故障 fail-closed且可诊断；无字符串控制流和假健康。
6. 并发、故障、wire、容器 smoke及全量门通过。
7. Go uTLS、双栈 fallback、过期规则/注释清理完成；最终树只保留最新合同。
8. 没有 clean-room 复制风险；参考项目只用于行为证据，不复制实现。
9. PR 已创建，主线未合并，等待 Owner批准。

## 风险与停点

| 风险 | 处理 |
| --- | --- |
| 内部没有某 mode 的实测 profile | 不伪造；该 mode显式阻断并如实报告。若 Owner要求本轮补抓，另开受控采集。 |
| inline profile 现有 DB 字段表达不足 | 先证明并按当前唯一计划设计；仅同时命中实质分歧、无成熟依据、选错高危时停下请 Owner 决策。 |
| Docker 构建引入新 runtime dependency | 优先复用现有依赖；确需新增时先做许可证、维护、供应链和镜像影响审计，命中决策停门才询问 Owner。 |
| 真实账号/代理不可用 | 本地 wire/fixture全部完成，但真实 vendor 状态标“未验证”，不虚报上线门通过。 |
| auth/billing/quota 修复造成降级或绕过 | 先写反例与 PostgreSQL 集成测试；钱路、鉴权、强配额默认 fail-closed，不能用日志代替约束。 |
| 报告结论与源码冲突 | 以源码和判别测试为准，直接修报告；不为维护报告面子而新建状态表。 |

## 文件预算

- `factory.go` 已接近 600 行，不继续堆协议细节；IPC/profile/preflight 按内聚职责拆文件。
- Rust 现有大文件在触及时抽出协议/动态 profile/生命周期模块，使职责更清楚，不抬高 baseline。
- 每切片运行 codebudget；删 Go uTLS 应让 `mimicry` 包体量明显下降。

## Pre-execution checklist

- [x] 协调锁无冲突，dirty 文件逐项归属明确。
- [x] S0 基线和 mutation目标已列清。
- [x] mode/profile/account矩阵来自真码和内部实测资料。
- [x] Rust/Go IPC 字段能从现有 DB数据无损构造。
- [ ] Docker 构建上下文能访问 Rust workspace/vendor。
- [x] 本地 TLS capture、proxy、stream fixture 和缓存目录准备好。
- [ ] 每切片提交范围、测试门、review门明确。
- [ ] 删除旧文件前替代路径和测试证据齐全。

`REFERENCE PROJECTS IN SCOPE`：CLIProxyAPI、sub2api、new-api。implementer 只消费 HUAKAI 内部规格，不重新读取外部源码。
