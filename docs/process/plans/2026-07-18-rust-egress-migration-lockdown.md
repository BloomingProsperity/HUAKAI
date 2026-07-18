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
