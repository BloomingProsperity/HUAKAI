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

## 状态维护规则

- 本文只保留一个当前目标、最新状态和仍需执行的工作；完成项不再保留旧执行清单、过程日志或平行目标。
- 完成证据以当前源码、判别测试、数据库迁移和 Git 历史为准，计划只保留简洁结论。
- 新需求排在当前未完成切片之后；只有完成对应测试与复审，才从“进行中”改为“已完成”。
- 子目标达到验收门后立即更新本文并删除其旧状态；不得积攒到总目标结束才一次性关单。
- 已完成：全局规则与 Skill 顺序治理。旧固定角色、平行双计划、自动派发入口和重复规则文档已经清理，当前规则入口以仓库根 `AGENTS.md` 与 `docs/RULES.md` 为准。

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

- [已关闭] factory 的 mimicry 路径只允许 Rust sidecar；socket 为空或不可用时 fail-closed，Go uTLS/native fallback 生产代码与依赖已经删除，并已通过全量、压力、故障和容器验收。
- [已关闭，`29cfc50a` + 本批] Rust 已覆盖八种 mimicry mode：Anthropic、Codex、Gemini、Kiro 使用内部实测 profile；Antigravity、Cursor、Copilot、Windsurf 使用独立命名、可执行且通过 wire 校验的 Rust Safe Equivalent，不冒充官方客户端精确指纹。
- [已关闭，`29cfc50a`] DB 动态 profile 已改为 inline IPC，由 Rust 严格校验和执行。
- [已关闭] IPC v2、capabilities、ready、结构化错误、取消与超时已完成；单镜像冷构建、`direct` Compose 启动、双进程可见性、kill/restart、数据库中断与恢复 smoke 已通过。

全局 Renew 报告的核实结论：

- **坐实并实施**：DLQ poison 冻结、Replicate 重复付费重试、非密码登录绕过 2FA、退款未校验 captured、weighted 失效、ramp 低流量/热路径写、degraded 不降权、class 转移丢 exclusion、ramp key 漂移、DLQ 兜底丢弃、TTS 部分交付全退、配额基础设施 fail-open、诊断凭据轴失真、legacy 内联账号静默掉池、多实例限流/worker/readiness。
- **源码冲突待闭环**：`SettlementIntentEnabled` 当前真码、测试和部署示例仍明确默认关闭；旧报告把它写成“已修复”不成立。它属于钱路恢复默认策略，保留到最终审批项，未拍板前不得假报 S4A 全部完成。
- **证伪并删除**：`channel_health_state` 完全没接生产选号、没有统一恢复原语、chat 裸 401 永远只进软冷却。源码已存在 `ServicePoolGate`、`RecoverAccountState` 和 auth challenge 分类；真实缺陷是状态轴重复、热路径写与诊断口径发散。
- **另行实现能力**：模型维度买家配额、可配置错误策略、缺失模型发现；不得把“参考项目有”当实现证据，必须从 HUAKAI 现有合同独立设计。

## Shape inventory

必须覆盖：builtin profile、账号绑定 profile、租户轮换池、direct、HTTP/HTTPS/SOCKS5 proxy、OAuth 热刷新后模型重试、buffered、streaming、取消、sidecar 不可达、profile/协议不匹配、启动/退出、容器 health、并发隔离、retry/failover/health/audit。

已有 mode 优先使用 HUAKAI 内部实测 profile。尚无精确采集数据、但存在经过真实协议与 wire 校验的 Safe Equivalent 时，必须使用独立的 `*-rust-safe-v1` 标识、完整启动 preflight 和如实状态，不得冒充官方客户端精确指纹；连 Safe Equivalent 也未验证时才在账号可选和启动 preflight 处显式阻断。任何情况都禁止静默退回 standard 或编造“精确模拟已完成”。

## 执行切片

| 工作 | 当前状态 | 剩余验收 |
| --- | --- | --- |
| 全局规则与 Skill 治理 | 已完成 | 无；旧记录已清理 |
| S0-S3 Rust IPC、动态与内置 profile | 已完成 | 自动化回归已通过 |
| S4A 钱路、鉴权、退款、日志 | 已完成既定能力 | 结算意图仍按既有默认关闭，不在本批擅自翻转 |
| S4B 调度、健康与诊断 | 已完成 | 全协议、恢复与诊断口径回归已通过 |
| S4C 多实例上线 | 已完成 | 共享限流、租约、`/readyz` 与 Redis 故障验收已通过 |
| S4D 模型配额、错误策略、模型发现 | 已完成 | 随 S8 做最终发布复核 |
| S4E 套餐识别与系统标签 | 已完成 | 真实厂商账号最终复验 |
| 账号凭据设备码安全闭环 | 实现、自动化验收与独立复审完成 | 受控真实厂商账号复验 |
| S4F 内置服务器监测 | 实现、自动化验收与独立复审完成 | 无 |
| S4G 账号运营能力补齐 | 实现、自动化验收与独立复审完成 | 受控真实账号复验 |
| S4H 厂商额度与自适应调度 | 实现与自动化验收完成 | 受控真实厂商账号最终复验 |
| S5/S7 单镜像与 Rust-only 翻转 | 已完成 | 无；可重复容器生命周期验收已进入 CI |
| S6/S8 重测试与发布 | 分支自动化门与最终复审已完成 | 唯一 PR、远端 CI 与合并后主线复验；真实厂商账号未验证且不虚报 |

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
- 为 Antigravity、Cursor、Copilot、Windsurf 建立四个独立命名的 Rust Safe Equivalent；它们复用已通过真实 wire 校验的协议形状，但不宣称是对应官方客户端的精确采集结果。
- Go mode 映射覆盖全部八种 profile；每个 mode 都进入启动 preflight，任一 profile 缺失都不能 ready。
- JA3/JA4 以 BoringSSL 实际 ClientHello重新计算/校验，不能照搬 Go uTLS 期望值。
- Kiro 等随机字段按稳定维度判别，不用固定错误值强判。
- Gemini UA 只在内部已批准实测合同支持时同步修正。
- 四个 Safe Equivalent 后续取得受控实测数据时可原位替换 Rust profile 内容，不改变 Go mode、IPC 或运维合同。

### S4：错误、retry、health 与 audit

- Rust ACK 返回稳定错误类：协议/profile、proxy、DNS/connect、TLS、timeout、upstream reset。
- Go 映射现有 transport taxonomy；本地 sidecar故障不得误伤账号凭据/health。
- 关联日志不记录 token、proxy密码、请求体和 inline profile 原值。
- 覆盖模型请求 401 后 OAuth 热刷新/重试；refresh 端点继续现有 SSRF-protected standard client，除非真码合同另有要求。

### S4A：钱路、鉴权与恢复闭环

S4A 主体已完成：退款事实、累计封顶、幂等冲突、配额同事务冲减、争议唯一归属、DLQ 隔离和人工恢复均已接入；迁移 `0193`、`0194`、真实 PostgreSQL、全量竞态测试及独立复审通过。结算意图默认策略仍是本切片唯一待审批项，不算作已完成；其余旧执行记录已删除。

当前边界长期有效：HUAKAI 暂无真实直付，退款只表示内部余额冲正、兑换事实补偿和人工留证，绝不冒充渠道原路退款。部署者只能调整下级租户这一层的额度，不能查看或修改下级租户用户余额；租户管理员只管理本租户用户，用户只管理自身资源。普通日志按六类保留 30 天，资金、幂等和恢复事实永久保留；本目标不扩张这三条边界。

#### S4A-3：全局日志分类与固定 30 天保留（已完成）

统一日志信封、操作/资金/安全/错误/访问/恢复分类、异常优先采集、固定 30 天有界清理、数据库租约、管理接口和健康面均已闭环；资金、幂等与恢复事实永久排除在日志清理之外。迁移 `0195`、真实 PostgreSQL、全量竞态测试和两轮独立复审通过，旧执行过程已删除。

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

#### S4D-1：模型维度买家配额

已完成：迁移 `0200`、运行时策略解析、reservation 重放身份、管理 CRUD、当前窗口投影与 OpenAPI 已统一接入 `model_selector`。通配与精确模型策略累计约束，旧聚合接口只读取通配策略，避免重复计量；迁移回退在存在精确策略时显式拒绝。重复手写 SQL 死代码已删除，查询与生成文件按策略、窗口、预留、并发、日志和对账职责拆分，单个生成文件最大约 415 行。定向测试、全量 Go、codebudget、真实 PostgreSQL 竞态集成及迁移升降保护均已通过。

#### S4D-2：可配置上游错误策略

已完成：账号高级规则现已具备稳定 `rule_id`、首匹配、严格校验、安全客户端投影与独立 `affect_health` 语义；chat、completions、embeddings、rerank、images、audio 和 Gemini 共用同一错误决策。原始上游 body/header 不透传，动态规则不能覆盖 billing、quota、retry、鉴权刷新或永久禁用结论。专项核实同时修复了 buffered chat 自定义状态命中后未写账号冷却的问题，并删除无人调用的旧错误包装入口。定向判别、全协议、race、全量 Go、vet、quality gate、codebudget 与真实 PostgreSQL 集成测试均已通过。

#### S4D-3：缺失模型发现与人工上架

已完成：迁移 `0201`、周期同步、发现箱、平台管理员分页与 `promote/ignore`、OpenAPI 和账号模型观测已闭环。未知模型只保存公开元数据，不会自动进入全局目录；人工别名与 operator 模型不会被同步覆盖，大面积目录收缩会显式回滚。上架把模型、别名、能力、状态、租户快照和管理日志放在同一可重试串行化事务中，数据库瞬时竞争自动重试，真实身份冲突返回 409 并要求人工消歧；多副本周期任务使用 PostgreSQL 会话租约。专项核实同时修复了 Gemini 发现项协议名与数据库约束不一致的问题，并把发现 API 拆出通用 `adminhttp`、把同步写文件从 775 行降到 585 行。定向判别、10 轮真实 PostgreSQL 并发上架、Gemini/OpenAI 生命周期、迁移升降保护、race、全量 Go、vet、OpenAPI、codebudget 与 quality gate 均已通过。

执行顺序：S4D-1、S4D-2、S4D-3 已关闭；下一项进入全局包名、职责边界与生产接线审计，再进入压测、故障注入、真实厂商账号复验和最终发布门。

### S4E：导入套餐识别与系统标签

已完成：`subscriptionprofile`、迁移 `0197`、导入/换码/刷新接线、批量结果、账号列表/详情/筛选、健康与诊断均读取同一套餐投影。系统标签与人工标签分离，未知值不伪装免费套餐，弱证据不覆盖强证据；真实 PostgreSQL、OpenAPI、全量 Go/Rust 门和独立复审通过。旧执行清单与调研过程已删除。

### 并入切片：账号凭据设备码安全闭环

- [x] 设备码与 SSO 临时认证材料不再明文落库；复用现有凭据加密器和 AAD，终态、消费和取消均清除瞬态材料，迁移 `0199` 强制数据库约束。
- [x] 建立单次上游轮询、稳定错误分类、`Retry-After`、90 秒数据库 CAS 租约、跨实例去重、陈旧租约接管和进程崩溃后的加密候选恢复。
- [x] 增加设备码轮询管理接口；开始接口只返回公开验证码与验证地址，成功仍统一进入身份校验、套餐识别、凭据加密和幂等 Finalizer。
- [x] 修复空 scopes 写成 JSON `null` 的真实数据库约束错误，以及凭据套餐并发首写时旧错误被变量遮蔽的问题。
- [x] 2026-07-19 定向竞态测试通过；专用 PostgreSQL 从 0 迁移到 199、真实加密/CAS/终态清理测试及 199 -> 0 -> 199 往返通过；OpenAPI、codebudget 与 quality gate 通过。
- [x] 全量 Go/Rust、最终 OpenAPI、真实 PostgreSQL、容器冷构建和生命周期门已通过。
- [x] 独立复审已完成；受控真实厂商账号材料当前不可用，Owner 于 2026-07-19 明确授权该项不阻塞本次 PR，状态保持“未验证”。

### S4F：内置服务器实例监测与完整探针路线

| 项目 | 内容 |
| --- | --- |
| Owner directive | “服务器信息监测这一块有吗？可以参考探针，类似哪吒探针那个的。” |
| 当前实现 | `servermonitor` 已内置采集本 gateway 实例，迁移 `0198` 持久化当前投影与一分钟历史；部署管理员可查列表、详情和历史，系统健康已接入实例摘要并修复告警依赖与权限口径。定向竞态、真实 PostgreSQL、OpenAPI 与 codebudget 已通过。 |
| 本轮范围 | 完成全量回归和独立复审；不写当前未开工前端。 |
| 本轮不做 | 不做远端 agent、远程命令、终端、文件管理、端口转发、公网裸 exporter、逐容器明细或节点级自定义告警；这些不会伪装成已完成。 |
| 成功标准 | 启动即采；30 秒刷新，90 秒无活动判离线；CPU、内存、swap、load、根盘、磁盘 I/O、网络、系统 uptime 与进程资源可见；每组指标有独立状态；容器与宿主机不混报；历史缺口不补零；30 天自动清理；多副本不会用同一节点身份互相覆盖；所有接口服务端 platform-admin only。 |
| 爆炸半径 | 节点身份不稳会制造幽灵节点；容器内 `/proc` 可能把宿主指标冒充容器指标；部分失败沿用旧值会制造假健康；重复副本会互相覆盖；错误原文可能泄漏路径/主机信息；历史无清理会持续膨胀。 |
| 决策点 | 无新增停点：采用标准库、追加式迁移和既有管理员鉴权；有成熟源码依据且不命中“三项同时成立”停门。远端 agent 涉及独立凭据/协议/部署面，保留为 Mandatory Roadmap，不在本轮草率扩张。 |

clean-room 行为输入：

- **Observed**：成熟节点链路把稳定身份与接入主体绑定，认证主体不匹配时拒绝；轻量监测支持限时登记、agent 指纹/挑战和凭据变化后断开旧会话。证据：`nezha@a416e3bf297400d8fdb833509a8782716cced30c:service/rpc/auth.go:36`、`nezha@a416e3bf297400d8fdb833509a8782716cced30c:service/rpc/auth.go:159`、`beszel@d3a1d61955b0e45fb6b6c76e3ef970cb6518a7e1:internal/hub/agent_connect.go:35`、`beszel@d3a1d61955b0e45fb6b6c76e3ef970cb6518a7e1:internal/hub/agent_connect.go:58`、`beszel@d3a1d61955b0e45fb6b6c76e3ef970cb6518a7e1:internal/hub/systems/system_manager.go:121`。
- **Observed**：agent 主动出站，hub 以本地接收活动更新当前状态；活动连接有读截止期。证据：`nezha-agent@84939acc8daa41f264de3d8b8e6b60ac98654e21:cmd/agent/main.go:327`、`nezha@a416e3bf297400d8fdb833509a8782716cced30c:service/rpc/nezha.go:159`、`beszel@d3a1d61955b0e45fb6b6c76e3ef970cb6518a7e1:agent/client.go:128`。
- **Observed**：当前快照与历史采用不同语义；历史有明确保留、时间桶、降采样和分层清理，空输入不伪造样本。证据：`nezha@a416e3bf297400d8fdb833509a8782716cced30c:pkg/tsdb/config.go:5`、`nezha@a416e3bf297400d8fdb833509a8782716cced30c:pkg/tsdb/query.go:345`、`beszel@d3a1d61955b0e45fb6b6c76e3ef970cb6518a7e1:internal/records/records.go:90`、`beszel@d3a1d61955b0e45fb6b6c76e3ef970cb6518a7e1:internal/records/records_deletion.go:59`。
- **Observed**：主机采集覆盖静态平台与 CPU、内存、swap、load、磁盘、网络、uptime；采集项应隔离失败并有超时/并发边界。证据：`nezha-agent@84939acc8daa41f264de3d8b8e6b60ac98654e21:pkg/monitor/monitor.go:73`、`nezha-agent@84939acc8daa41f264de3d8b8e6b60ac98654e21:pkg/monitor/monitor.go:129`、`node_exporter@d6c236faac2f1887caf129b10555d5a38f9e41b0:collector/collector.go:145`、`node_exporter@d6c236faac2f1887caf129b10555d5a38f9e41b0:collector/filesystem_linux.go:35`。
- **Observed**：中转站运行指标在多副本下使用领导租约并记录任务最后结果；另一成熟中转站把单进程 CPU、内存、磁盘快照置于最高管理员门后。证据：`sub2api@d4b9797ff72024960a035cf22fdd8f213e149169:backend/internal/service/ops_metrics_collector.go:162`、`sub2api@d4b9797ff72024960a035cf22fdd8f213e149169:backend/internal/service/ops_metrics_collector.go:191`、`new-api@5a6c53d4966b2e34690ab49f3dd19be01c88fdbe:common/system_monitor.go:36`、`new-api@5a6c53d4966b2e34690ab49f3dd19be01c88fdbe:router/api-router.go:214`。
- **范围声明**：CLIProxyAPI 所读管理路由只提供单进程管理/保活线索，本合同没有把它当作节点探针来源。证据：`CLIProxyAPI@93d74a890a44802f656d7f39a573916b2611896e:internal/api/server.go:134`。

HUAKAI 独立设计合同（均为 **Inferred**，不宣称是外部实现）：

1. 本轮只有 gateway 内置实例。显式配置的 opaque 节点 ID 是稳定身份；缺省 ID 使用运行环境标识的不可逆摘要，并在 API 标明可能随编排重建变化。本轮不提供删除入口，过期节点转离线并按保留策略清理。
2. 每次启动生成新会话世代，序号从 1 开始；同一节点 ID 必须持有数据库会话租约。旧租约释放后新世代才能接管，旧序号不能覆盖当前状态。DB 或进程重启后以持久化的最近活动时间计算离线，新进程启动即首采。
3. 30 秒采集、90 秒离线；当前投影与一分钟历史分开；历史保留 30 天。空桶就是缺口，不补零、不补写停机期间数据。
4. 每组指标标记 `fresh/warming/unavailable/error`。失败组在本次快照为 `null`，不沿用旧值冒充 fresh；成功组仍发布。恢复后清除活动错误、保留最近错误时间并写最近恢复时间。
5. 明确 `process/container/host` 视图；无法确认宿主机时只报告容器或进程，不把容器限额冒充宿主机容量。
6. 列表、详情、历史和错误均由服务端 `platform_admin` 门强制；tenant admin 与普通用户不可见。
7. 不采集或持久化真实主机名、IP、硬件序列和进程命令行；错误分类、API 与全局日志只写稳定错误码，不写原值或系统路径。
8. 查询面包含节点列表、当前详情、历史、活动错误和最近恢复时间；现有系统健康接入节点总数/离线/降级摘要，并修复告警 firing 依赖假零。
9. 远端 agent、一次性登记、逐节点凭据轮换、跨 hub 会话租约、逐容器详情和节点级告警进入 Mandatory Roadmap；本轮 API 必须如实标识 `builtin`，不能冒充完整哪吒式探针。

执行顺序与判别门：

1. [x] 独立 specifier 读取三镜、哪吒 dashboard/agent、Beszel 和 node_exporter 生产源码；隔离 reviewer 要求补齐身份、重启、部分失败、恢复、权限与隐私后确认边界完整，未发现实现贴译风险。
2. [x] 迁移 `0198` 建立节点当前投影与一分钟历史；唯一键、会话世代和序号阻止旧写覆盖，历史与离线节点按 30 天分批清理。
3. [x] 建立内聚的 Linux 内置采集器与非 Linux 安全降级；CPU/计数器使用跨样本差值，首次为 warming；容器优先读 cgroup，不能确认 host 时不输出 host 值。
4. [x] worker 启动即采并每 30 秒运行；同节点 ID 持有 PostgreSQL 会话租约，重复 ID 启动 fail-loud；Stop 先停采集再释放租约。
5. [x] 新增 platform-admin only 的节点列表、详情和历史 API；接入现有系统健康组件，修复告警 service 漏接与 OpenAPI 权限漂移。
6. [x] 解析、计数器回绕、warming、部分失败、恢复、容器/host、重复身份、重启世代、旧序号、空桶、清理和越权的定向竞态测试、真实 PostgreSQL、OpenAPI、codebudget 与全量 Go/Rust 门均已通过。
7. [x] 独立复审完成，本切片已关闭。

### S4G：账号运营能力补齐

本切片来自 Owner 对当前源码与成熟运营链差距的追加确认，排在 S4B/S4C 之后、最终发布门之前。当前已有的统一接入计划/执行、套餐标签和凭据加密合同继续复用，没有建立平行入口。

已完成：Claude Cookie 与 Setup Token 一等入口、Codex Agent Identity 专用批量入口、CRS 预览/执行同步、账号整包导入导出、三身份 capability grant 和管理 API 已接入同一接入管线；迁移 `0204` 至 `0207` 覆盖身份模式、租户授权、加密暂存与迁移包日志动作。重复上游身份、计划漂移、跨租户、包篡改和代理映射冲突均显式拒绝或进入逐项结果，不会随便更新第一条账号；凭据密文、套餐标签、健康初始化和日志沿用现有权威服务。OpenAPI、全量 Go、真实 PostgreSQL 集成、codebudget 与 quality gate 已通过，本轮没有采用旧前端。

- Claude Cookie：建立服务端 Cookie 换取凭据、身份识别、创建或更新消歧、凭据加密、套餐观测、健康初始化与管理日志完整链路；Cookie 不落普通日志或明文临时表，失败不得留下半账号。
- Codex Agent Identity：建立专用批量入口和明确材料合同，接入现有预检、身份冲突、套餐识别、加密落库和逐项结果；不得继续以“解析器拒绝，请走不存在的入口”作为终态。
- CRS：提供预览与执行两阶段同步，先展示创建、更新、跳过、冲突和失败，再以计划摘要防止预览后数据漂移；重复上游身份必须人工消歧。
- 账号整包迁移：覆盖账号公开配置、加密凭据和必要代理映射；导出必须二次确认、最小权限、显式版本和完整性校验，导入必须先预检并按租户隔离，不导出日志、余额、用户或运行时健康历史。
- 验收覆盖单条/批量、创建/更新、重复身份、跨租户拒绝、部分失败、重放、并发、密文清除、迁移包篡改、代理映射冲突与人工恢复；本轮只建立后端合同，不采用当前旧前端。

### S4H：厂商额度采集与自适应调度

已完成：迁移 `0202`、`0203` 建立统一额度事实、路由 EWMA 与显式上游成本比；Claude 5h/7d、Antigravity 模型余量和 Grok 账期/积分进入同一投影，Gemini 在缺少可验证真实窗口时明确保持 unknown。账号列表、详情、健康接口、Hermes 与生产选号共用该事实；过期、部分失败和未知值不冒充可用额度。采集 worker 启动即跑并持 PostgreSQL 会话租约，Antigravity 只在可恢复故障切备用端点，鉴权失败不切换，所有采集日志只落稳定错误分类。

自适应评分已在硬 gate、优先级、并发、黏性和 retry exclusion 之后引入错误率、成功率、响应速度、额度余量、重置时间和上游成本；未知信号中性，陈旧信号失效，恶化黏性可逃离且仍保留最后兜底。账号管理 SQL 已拆分并改为可重复生成的 Raw 查询加稳定包装层，避免重新运行 `sqlc` 破坏接口。全协议、竞态、真实 PostgreSQL、迁移升降、代码预算、OpenAPI 与全量 Go 均通过；只剩受控真实厂商账号最终复验。

### S5：单镜像可运行交付

- Docker 增加固定 Rust/BoringSSL builder，复制 sidecar 与 gateway 两个二进制。
- 新入口负责 UDS 目录、权限、sidecar ready、gateway 启动、SIGTERM、双进程退出。
- UDS 使用容器内运行目录，不再默认 `/tmp`。
- direct/prod Compose 使用同一镜像和相同运行合同；dev Compose 只提供 PostgreSQL/Hermes 基础设施，gateway 由开发者本机启动。gateway readiness 同时验证数据库、sidecar 和 drain 状态。
- 不引入新的运行时第三方监督依赖，入口只用镜像已有 shell/进程原语。

### S6：重测试与容器 smoke

本地自动化门已完成：Rust workspace 常规与 ignored 套件、100/500/1000 并发、长连接取消/回收、wire mutation、proxy 与协议故障、Go 全量与真实 PostgreSQL 集成、容器冷构建、`/readyz`、数据库/Redis 故障以及任一子进程退出均已通过。`backend/scripts/container-smoke.sh` 已把双进程可见性、Redis 失效、分别杀死 sidecar/gateway、重启恢复与 SIGTERM 优雅退出固化为可重复门；真实厂商账号仍必须在材料可用时单独执行并如实记录。

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
- Docker/Compose：dev/direct/prod 配置解析、冷构建、direct 启动、health、kill/restart 和数据库中断隔离 smoke 已通过；prod 使用同一镜像合同，未连接真实生产依赖启动。
- CI：普通 Go 门强制连接 PostgreSQL 与 Redis，独立 `integration_pg`、Rust 常规/ignored 压力故障套件和真实容器生命周期均为阻塞 job，环境缺失不能再以 `t.Skip` 假绿。
- 每个提交先 stage，再跑 Codex review；S0/S1 清零。
- 最终完整 cross-review 与两轮提交前复审已执行；其对退款和额度落库调度的缺口判断经全仓源码核实为暂存范围误报，现有真实 PostgreSQL 全链测试已覆盖；动态 profile 并发线缆隔离和可重复容器门的真实缺口已补齐。分支最终门已通过普通 Go、真实 PostgreSQL/Redis 全量集成、`go vet`、quality gate 与生产镜像构建；真实厂商账号未验证，Owner 已授权不阻塞本次 PR。推送唯一 PR 后，CI 全绿即合并，并在干净主线执行全量复验。

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
4. 用量错账退款有签名校验到资金与新收据的全链验收；当前无真实直付，退款只执行内部余额冲正、兑换事实补偿和人工恢复，不建立或冒充渠道原路退款。
5. sidecar/profile/协议故障 fail-closed且可诊断；无字符串控制流和假健康。
6. 并发、故障、wire、容器 smoke及全量门通过。
7. Go uTLS、双栈 fallback、过期规则/注释清理完成；最终树只保留最新合同。
8. 没有 clean-room 复制风险；参考项目只用于行为证据，不复制实现。
9. Claude Cookie、Codex Agent Identity、CRS、账号整包迁移、Gemini/Antigravity/Grok 额度采集和自适应调度均完成后端闭环，不以路线图或前端占位替代实现。
10. 唯一 PR 测试全绿后按 Owner 已给授权合并；合并后的干净主线再次通过全量 Go、真实 PostgreSQL、Rust、静态和容器门。

## 风险与停点

| 风险 | 处理 |
| --- | --- |
| 内部没有某 mode 的精确实测 profile | 已有真实协议与 wire 验证依据时，使用独立命名并如实标注的 Safe Equivalent，同时保留精确采集替换路径；没有任何可验证等价物时显式阻断，绝不伪造或静默 standard fallback。 |
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
- [x] Docker 构建上下文能访问 Rust workspace/vendor，禁用缓存的冷构建已通过。
- [x] 本地 TLS capture、proxy、stream fixture 和缓存目录准备好。
- [x] 每切片提交范围、测试门、review 门已在本文明确。
- [x] Go uTLS 旧文件的 Rust 替代路径、判别测试和最终独立复审证据齐全。

`REFERENCE PROJECTS IN SCOPE`：CLIProxyAPI、sub2api、new-api。implementer 只消费 HUAKAI 内部规格，不重新读取外部源码。
