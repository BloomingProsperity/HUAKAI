# 2026-07-20 账号转 API 全链路闭环计划

| 项目 | 内容 |
| --- | --- |
| Owner 指令 | “我要的是全链路测试”“对全局和所有账号生效”“找到之后接线，修复他们”“有些重复的功能代码和逻辑互相悖论的可以取其一，删其二”“但要功能都要有，集成为唯一”“不只这一条链路，是我整个项目所有的链路模块” |
| 范围 | 第一主线是所有已部署账号类型的导入、身份与订阅识别、凭据存储与刷新、额度健康、模型发现/同步、选号与 gate、凭据物化、协议适配、统一 Dispatcher 出口、上游错误回流、claim/结算、日志和人工恢复。全局横向审计同时覆盖 Go 后端全部业务与运维模块、官方 Key 的 Go standard 出口、会话账号的 Rust mimicry 唯一出口、数据库迁移与查询、启动和 DI、配置、定时任务、队列/DLQ、幂等与恢复、部署和运行脚本；逐模块核实并处理已实现未接线、重复冲突、只写不读、只测不注册、被替代仍可达和缺少运维闭环的代码。 |
| 不包含 | 前端页面；生产部署；读取真实秘密；未经 Owner 批准合并主线。 |
| 成功标准 | 每种账号都能从导入结果追到生产出站资格；供应商与认证模式精确分派；静态凭据不被误刷；订阅/额度事实不伪造；失败分类、退避、健康和日志一致；模型同步状态可运维；所有外部请求只从统一 Dispatcher 进入，官方 Key 必须走 Go standard transport，需要客户端指纹的会话账号必须走 Rust mimicry 且不得降级；全项目每个对外入口和后台任务都能追到生产注册、状态写读、错误处理、幂等恢复与运维观测；无功能缩水的重复实现已清理；聚焦、全量、竞态、真实 PostgreSQL/Rust 和真实账号端到端测试通过。 |
| 爆炸半径 | 账号可用性、OAuth 轮换、调度公平性、上游限流、模型目录、Rust 出站、鉴权与租户隔离、配额和结算、任务队列、日志与恢复、部署启动。错误可能导致错刷账号、刷新风暴、越级用池、漏计或重复结算、后台任务僵死、部署形态双轨或让运营看到假状态。 |
| 失败模式与缓解 | 只按 vendor 分派会覆盖 auth mode；用文档/搜索命中冒充生产接线；删重复实现时丢独有恢复能力；把 429、订阅耗尽和 5xx 混为一类；全链测试绕过真实入口。所有结论必须回到入口、DI、运行时调用和状态回流核实，先做能力并集和判别测试，再删除旧实现。 |
| Owner 决策点 | 当前目标内的代码、schema、鉴权、配额和结算修复已获所有权，可按证据直接推进；生产秘密、部署、不可逆数据操作、`LICENSE` 和合并主线仍需 Owner 明确批准。分组路由读取失败已由 Owner 定为“优先保证绝不越级用池”：策略真相不可用时直接返回明确 503，禁止缓存旧授权继续放行。 |

## Owner 最新执行顺序

- 先闭环 Owner 已提供真实账号的 Claude、Gemini、Antigravity、Kimi、Grok、Codex 等账号链，完成导入、身份与订阅识别、刷新、额度、模型、调度、出站、回流和恢复。
- Copilot、Windsurf、Cursor 统一封存并移出本次上线范围。三者只允许在 HUAKAI 首次正式上线后另立目标、经 Owner 明确解封再继续，不得抢占当前主链时间。
- 三个封存项现有且已通过测试的安全基础修复保留；本目标不再扩展、不默认启用，也不得把“有入口”“有 adapter”或“可导入”表述成“可服务”。本次发布矩阵必须明确标记为“封存/不可发布”。
- 当前账号转 API 主线不中断；全局扫描并行提供线索，按“入口 → 鉴权/租户 → 能力和策略 → 状态与数据库 → 外部调用 → 计费/配额 → 日志 → 队列/DLQ → 人工恢复 → 运维展示”逐模块核实。任何扫描结论必须由当前执行者重新打开源码、确认生产可达性并用判别测试证明后才能修复或写入完成事实。
- 全局扫描不把搜索命中数当结论，不把测试 helper 当生产接线，不把数据库表存在当功能可用，也不因某模块暂时没有 UI 就忽略后端运行合同。

## 行为合同

1. 账号必须同时保存供应商和认证模式；刷新资格至少由 `vendor + auth_mode + credential shape` 判定。sub2api 将平台与认证类型分别持久化，并在候选扫描中限制认证类型和刷新凭据：`Wei-Shaw/sub2api@d4b9797ff72024960a035cf22fdd8f213e149169:backend/internal/domain/constants.go:19-35`、`Wei-Shaw/sub2api@d4b9797ff72024960a035cf22fdd8f213e149169:backend/internal/service/token_refresh_service.go:502-536`。
2. 缺少 refresh token 只表示不可自动刷新，不能推导为不可出站；静态 key 和仍有效的短期 token 必须保持各自语义。CLIProxyAPI 的多个执行器在缺少刷新材料时保持原状态：`router-for-me/CLIProxyAPI@7c61e982e490f028d295d69e22e372b29cd2db8c:internal/runtime/executor/codex_executor.go:1746-1785`、`router-for-me/CLIProxyAPI@7c61e982e490f028d295d69e22e372b29cd2db8c:internal/runtime/executor/claude_executor.go:884-918`。
3. 导入必须区分解析、存储、可刷新、真实出站验证和可调度；部分失败逐条返回。sub2api 的 Codex 导入校验令牌时效并对过期输入拒绝或暂停：`Wei-Shaw/sub2api@d4b9797ff72024960a035cf22fdd8f213e149169:backend/internal/handler/admin/account_codex_import.go:605-653`、`Wei-Shaw/sub2api@d4b9797ff72024960a035cf22fdd8f213e149169:backend/internal/handler/admin/account_codex_import.go:773-806`。
4. 额度快照必须带来源、采样时间、窗口和 reset；普通 429、订阅耗尽、并发上限和付费额度不足分别分类。API Key 不生成个人订阅标签。
5. 多副本刷新、模型同步和主动探测必须使用数据库租约/锁；进程内锁不能冒充跨副本唯一执行权。
6. 全局采用“能力并集、实现唯一”：先把有效独有能力并入唯一入口并覆盖正常、失败、恢复和运维路径，测试通过后删除旧实现、旧配置、旧测试和误导注释。
7. 套餐系统标签只归一上游明确证据与官方现行名称，未知值保留原文且不得猜成免费档；令牌到期不等于订阅到期。当前名称依据 [GitHub Copilot 官方套餐](https://docs.github.com/en/copilot/get-started/plans)、[Kimi 官方会员说明](https://www.kimi.com/help/membership/membership-overview)、[Windsurf 官方套餐说明](https://docs.windsurf.com/windsurf/accounts/usage)、[Claude 官方套餐说明](https://support.anthropic.com/en/articles/11049762-choosing-a-claude-ai-plan)、[Gemini 官方套餐说明](https://support.google.com/gemini/answer/16275805) 与 [Grok 官方套餐说明](https://x.ai/pricing)。
8. 所有借鉴项目调研必须同步核实近期提交、Release/Changelog 和公开 Issue；源码证明当前行为，更新记录只定位变化，Issue 只提供故障场景，未经源码或复现交叉验证不得写成已实现或已修复。发现新增有效能力或公开缺陷时，逐项追 HUAKAI 的导入、凭据、调度、出站、回流和运维链，存在同类差距就进入本目标实现或明确强制路线。
9. 账号转 API 借鉴项目同时核实模型原生 function/tool calling 与 MCP client/server 两条能力轴，包括请求转换、流式工具事件、终止原因、用量、权限与租户隔离、日志和失败恢复；外部源码未证明时保留 `Open Question`，HUAKAI 侧仍须独立核对现有协议与工具链，不能静默缩水。
10. “统一出口”只表示所有外部调用必须经过同一 Dispatcher、代理和错误治理链，不表示所有 TLS 都由 Rust 执行。官方 API Key 使用 Go standard transport；只有订阅号、OAuth 或会话号中明确需要客户端指纹的认证模式才进入 Rust sidecar。Rust 是 mimicry 的唯一实现，mimicry 不得回退 standard；官方 Key 也不得被误送入 mimicry。该矩阵对业务转发、模型发现、额度探测和账号测试共同生效。

## 三身份与余额分发合同

Owner 定义的身份只有三类：部署者、部署者的普通用户、下级租户管理员及其本租户用户。代码中的 `platform_admin` 对应部署者管理身份，`tenant_operator` 对应下级租户管理身份，`users.role='user'` 对应最终用户；不得再把任意租户内的 `users.role='admin'` 会话统一提升成跨租户部署者。

当前真码差距：`adminsessionauth.Resolver` 把所有租户的管理员会话都映射为 `platform_admin`；`POST /admin/v1/balances/adjustments` 又只允许该角色，并接受任意 `tenant_id + user_id`，因此部署者可以越级给下级租户用户充值。现有正向调额还复用支付订单，并用 `payment_credits - payment_refunds` 返回所谓净余额，遗漏模型消费、兑换码、奖励和其他已写入 `user_balances` 的变动，返回值不是余额唯一事实。

专业领域源码核实后的适配合同：

1. 下级租户额度落入租户经济钱包，管理员只是已认证操作者；界面可称“租户余额”，不得把共享经营额度绑到某个管理员个人钱包。
2. 部署者可以增加或收回下级租户钱包额度；不得在该路径指定下级租户用户。部署者给自己直属用户调额是另一种明确交易形状，仅允许目标属于部署者工作租户。
3. 下级租户管理员只能在自身作用域内把租户钱包额度分给 `role='user'` 的客户，或从该客户可用余额收回到租户钱包；来源与目标、租户归属和角色均由服务端认证身份与数据库事实决定。
4. 所有调额都是同币种双边交易，来源扣减、目标增加、前后余额、不可变交易与分录、幂等结果在同一个 `SERIALIZABLE` 事务提交；默认禁止透支，扣减必须满足 `balance - held >= amount`。
5. 幂等唯一范围至少包含租户、操作形状、操作者与幂等键；同键同指纹返回首次结果，同键不同来源、目标、金额、币种或原因明确冲突。并发按稳定顺序锁账户，序列化/死锁冲突有限重试。
6. 原资金事实不可更新或删除；纠错通过反向交易。普通部署者没有越级改下级租户用户余额的后门，break-glass 纠错不在本轮静默开放。
7. 永久资金事实记录操作者、租户、来源/目标账户、金额、币种、原因、请求与幂等键、前后余额和结果；普通操作/拒绝/错误日志仍按全局合同分类并保留 30 天。

外部行为证据：三个中转站镜像均未提供完整三级分账，故只作为反例和入口颗粒度输入；客户余额隔离与正负流水见 `medusajs/medusa@7d7edad6fdf47ae36c06cd5f5b71232c9d51c70b:packages/plugins/loyalty/src/modules/store-credit/service.ts:164-323`，事务内双边记账、幂等冲突与反向纠错见 `formancehq/ledger@0695abcaf9eb74727f1607a4e92f96ccd12df32b:internal/controller/ledger/log_process.go:34-246`，双账户锁、余额不足拒绝和同事务持久化见 `blnkfinance/blnk@05fb1b9272d56f8ac5f84dfeb099de9498739000:transaction_execution.go:606-700`、`blnkfinance/blnk@05fb1b9272d56f8ac5f84dfeb099de9498739000:database/transaction.go:261-327`。上述项目分别为 MIT、MIT 和 Apache-2.0；本地仍独立设计，不复制 schema、标识符或实现顺序。

## 已完成事实

- Gemini 未知厂商能力在边界归一，不再拖垮原子同步；严格内部能力合同保持不变。
- Codex 导入元数据进入真实出站，5h/7d 主动/被动额度统一投影，过期和未知值不伪造成可用。
- Antigravity 使用固定公开 OAuth profile，刷新后同步 token、project 和 tier；通用 Gemini 配置不能覆盖其认证模式。
- Rust H2 bridge 清理普通 `Host` 头；Go 侧不再新增 TLS 出口。
- 凭据刷新调度已收敛为唯一 `(vendor, auth_mode)` 模式入口；Claude 专用客户端、Codex 配置、Gemini/Antigravity、Copilot 和 Windsurf 独有能力保留。
- 旧 `provider_accounts.credentials` 刷新栈、粗粒度 vendor 覆盖分派和 mock-only 假 fallback 已删除；缺统一刷新器会启动失败。
- 刷新失败分类已贯通状态与日志；429 保留 `Retry-After` 且不立即重打，5xx 才进入远端重试，临时错误不再误记为永久禁用。
- 模型同步定时状态已接入部署管理员 `GET /admin/v1/model-sync` 运维入口；旧 placeholder 总开关已从生产代码移除。
- Gemini `countTokens` 已补齐绑定 RPM/TPM、输入 token 估算、模型上下文窗口和绑定并发槽位；Default/PASR 两套选号器对无 claim 的受限辅助请求采用同一占槽与释放合同。
- 分组路由策略库未注入或读取失败时已从 fail-open 收紧为终态 `503 group_policy_unavailable`；Default/PASR、canary dispatcher、chat 和共享协议失败分类均禁止换号、换池或绕到另一选号器，旧 fail-open 指标与误导测试已删除；成功读取到 `Configured=false` 才保留未配置租户兼容放行。
- Copilot 固定公开设备码、长期授权材料隔离和运行令牌刷新能力继续保留并由底层单元测试覆盖，但按 Owner 最新顺序不作为本次上线能力。Copilot 与 Windsurf 的模式计划已明确标为“首次上线后封存”，默认账号目录不展示；标准流程、兼容 helper、批量导入预检、OAuth 回调、设备轮询和最终落库均由服务端发布闸拒绝，历史流程只允许查询与取消。每次拒绝记录操作者、租户、账号、模式、入口、结果、稳定失败原因和严重级别，不记录秘密，也不把封存拒绝伪造成上游失败。Cursor 未进入正式模式表，同样不能借历史回调绕过封存。
- 套餐字典补齐 Claude Max 5x/20x、Gemini Plus、Grok Lite/Business/Enterprise、Copilot Free/Student/Pro/Pro+/Max/Business/Enterprise、Kimi 五档会员和 Windsurf Free/Pro/Max/Teams/Enterprise；个人与工作区作用域分开，未知值继续保留原文。旧 `huakai_codex_*` 测试库已在确认零连接后删除 22 个，只保留本目标的最新可用测试库，避免旧迁移制造假红。
- Antigravity 导入、刷新和出站统一要求真实 project 身份；缺失或冲突时明确拒绝，403 project 拒绝只允许一次同步刷新并重试。Google 429 的结构化退避信息已进入统一错误分类，避免无节制重打。
- 账号额度采集启动即运行，并以 PostgreSQL 会话租约保证多副本只有一个采集者；列表、单账号健康和诊断入口复用同一 5h/7d 投影，过期、未知和失败快照不伪造成满额。
- 原生 function/tool calling 的请求、流式工具事件和终止原因已有生产形态测试；MCP 细粒度授权在本轮外部源码中未观察到可靠现成合同，保留为强制路线，不用“模型支持工具”冒充“MCP 已上线”。
- 生产刷新链已收敛到 `credentialworker` 唯一注册表，Anthropic、Codex、Gemini、Antigravity、Grok、Kimi 继续保留实际所需能力；删除了无生产入口的旧 provider 刷新包装、凭据存储适配层和重复测试支架，静态检查基线从 92/787 降至 77/697，没有放宽预算。
- 五个超预算文件已按职责拆分，外部 API、schema 和运行语义不变；全仓代码预算门恢复通过。
- Claude API Key、Claude AI OAuth、Claude Code、Gemini AI Studio、Gemini Code Assist、Antigravity、Kimi API Key、Kimi OAuth、豆包 API Key、混元官方 API Key、Codex、Grok、ChatGPT session 和 OpenAI 图片账号的活体脚手架，均先经过限定租户管理员令牌、显式能力授权和 `plan/execute` 正式导入 API 原子建号，再进入选号、出站、计费和恢复断言；账号专用活体测试已无直接写 `provider_accounts` 或 `account_credentials` 的旁路。多账号并发、轮询和故障切换由专门的调度夹具验证，不再用数据库直写冒充账号导入成功。
- 活体脚手架已纳入当前源码构建的 Rust `tls-sidecar` 生命周期和 readiness，Go 网关显式使用本次测试唯一 Unix socket；此前只启动 Go 进程、在 mimicry 唯一出口启动门前假失败的问题已修复。官方 Key 仍由同一 Dispatcher 分派到 Go standard transport。测试连接池改为最后关闭，正式导入产生的健康、日志、凭据和授权数据会先清理，重复运行不再污染共享测试库。
- 专用活体清理已覆盖 `scheduler_outbox`、`usage_record_dlq`、append-only `usage_records`/`billing_events`、余额占用、账号槽和 claim；删除触发器只在测试事务内临时关闭，失败自动回滚，成功后恢复。四类专用活体测试重复运行后测试租户残留为 0，相关 append-only 触发器均保持启用。
- 异步媒体任务新增迁移 `0210`，把原始 `binding_id` 以及绑定 RPM、TPM、最大并发快照持久化到任务。提交前选号只消费用户/绑定逻辑预算，worker 真正占槽时只消费账号预算并恢复原绑定约束，避免双计数，也禁止任务脱离原池组合同重新选号。固定账号轮询和受保护产物下载共用原账号 RPM 原子准入；多副本并发不会用分离的检查/记录窗口突破限制。
- Grok/Gemini 视频提交、轮询、下载、重试和结算统一保留原账号、原凭据与原绑定；429/`Retry-After` 转成延迟重试，不再由 worker 紧循环轰击上游。内容代理在上游返回空 body 时明确 502，不会先写出伪成功 200。
- 视频操作能力和耐久账号绑定在创建任务、冻结余额前双层校验：Gemini 编辑/延长以及通用媒体入口缺少账号、池、协议、模型或绑定快照的请求同步拒绝，不再先返回 202 后进入后台必然失败。

## 验证结果

- `go test -race -count=1 -timeout 12m ./...`：通过。
- `scripts/quality-gate.sh`：通过，静态检查与死代码债务均下降。
- 两轮独立审查发现并修复四项阻断问题：账号模型同步的租户管理员会话写权限、媒体上游成本与客户实扣口径说明、视频厂商不支持操作的预扣前拒绝、通用媒体入口缺少耐久绑定的预扣前拒绝；修复后目标包与全仓竞态测试通过。
- PostgreSQL 集成：80 个隔离包全部通过。
- 数据库迁移与真实 SQL：全新数据库从 1 升至 `0210`，媒体任务绑定快照、同绑定并发互斥和账号 RPM 原子准入均在 `integration_pg -race` 下通过；全仓 80 个隔离 PostgreSQL 包通过。
- 性能门：6400 请求、32 并发的混合负载通过，p95 约 6.0ms、p99 约 8.1ms。
- Rust `tls-sidecar`：`fmt`、`clippy -D warnings`、普通测试和忽略测试全部通过。
- `GOTOOLCHAIN=go1.25.12 govulncheck ./...`：生产可达漏洞为 0；本地默认 Go 1.25.0 的标准库告警不作为源码缺陷冒充修复结果。
- 真实账号均从正式导入入口进入一次性 PostgreSQL、网关和相应出站。OpenAI、Grok、Kimi Coding、Gemini AI Studio 官方 Key 完整链通过；Codex Responses 矩阵覆盖流式文本、工具调用、图片生成、reasoning、图片输入、请求变换和非流式聚合；Antigravity 导入、project/tier 与真实调用通过。Claude AI OAuth 本轮抵达上游后被限流，本地正确冷却并在无第二账号时停止盲重试；Gemini Code Assist 上游明确要求部署者提供 Google Cloud `project_id`，本机材料没有该字段；Grok/Kimi OAuth 缺完整材料，两个 Moonshot Key 均被上游 401 拒绝。这些外部前提不冒充本地代码失败，也不冒充活体通过。
- Grok 视频完成真实“正式导入 -> 混合池选号 -> 异步提交 -> 原账号轮询 -> 成功终态 -> 费用凭证/用量/余额/配额结算 -> 槽释放 -> 六跳日志”闭环，约 21 秒完成。Gemini Veo 账号仍无视频生成配额，因此只保留协议、绑定、退避、下载与钱账的判别测试，不宣称真实产物通过。
- Codex、Grok、OpenAI 图片三条合成凭据判别测试及通用账号正式导入测试均在 `-race` 下通过；ChatGPT session 使用本地假上游完成“正式导入 → 选号 → Rust sidecar → 出站 → 响应 → claim/用量/余额”全链闭环。测试库残留为 0，`usage_records`、`billing_events` 和订阅观测三类 append-only 触发器均为启用状态。
- ChatGPT session 全链测试已改用生产形态 PostgreSQL 日志账本，并强制断言 claim 对应的签名费用凭证已落库，覆盖配额预占与结算、余额占用捕获、计费 claim、费用凭证和账号槽释放；费用凭证缺失不再只留警告后放过测试。
- `smoke` 与 `e2e_concurrency` 已接入当前源码构建的 Rust sidecar，修复 mimicry 出口退役 Go uTLS 后测试仍假设 Go 可独立模拟指纹的问题；Go standard transport 继续承担官方 Key 出站。账号容量、等待队列、快速溢出、超时中止、绑定并发、断连恢复、钱账和槽位最终收敛均通过。
- Rust sidecar 冷构建和网关启动使用独立的五分钟准备时间窗，正式 HTTP/数据库断言才启动原有业务超时；冒烟、前端接线后端测试、账号槽并发和绑定并发不再因干净 CI 的首次 Rust 编译提前耗尽请求预算。
- 活体清理顺序已修正为“先清业务数据、后关闭连接池”，并覆盖配额事实、调度恢复、DLQ、签名费用凭证和 PostgreSQL 日志账本。此前清理 SQL 因连接池先关闭而静默失败、继而污染后续恢复测试的问题已修复；清理错误现在直接使测试失败，不再忽略。
- 在迁移 `0210` 的全新 PostgreSQL 16 数据库上，`smoke`、`e2e_concurrency`、`dlq`、`settlementrecovery` 和 80 个隔离集成包均通过。测试后 `huakai_e2e_*`、`huakai_codex_*`、`huakai_it_*` 数据库残留为 0，活体网关与 sidecar 进程残留为 0。
- 提交前独立审查发现的运行时路径硬编码、`CARGO_TARGET_DIR` 不一致、身份断言过弱、冷构建占用业务超时和凭据日志脱敏不全均已修复；嵌套 camelCase、`setup_token`、通用 `token`、服务账号私钥与 AWS 密钥均有可判别泄漏断言，聚焦竞态测试通过。
- 前端目录零改动；Copilot、Windsurf、Cursor 未被公共链改动解封。
- 封存闸相关 `credentialacq`、账号导入、模式目录和 `gatewayhttp` 全包测试及竞态测试通过；最终全仓 `go test -race -count=1 -timeout 8m ./...` 与质量门通过。两轮独立只读审查首轮发现拒绝日志缺口并已修复，第二轮未发现明确缺陷。判别测试确认即使底层 exchanger/adapter 仍注册，也不能从生产入口新建或推进封存流程。
- PR #286 已合并为当前主线基线；本轮新增的大批未提交改动不在 #286。收口后只创建一个新 PR，仍由 Owner 决定是否合并。

## 剩余顺序

1. 复核最终 diff、未跟踪文件和秘密泄漏，只 stage 本目标内容。
2. 执行独立只读审查；修复 S0/S1 后提交，在唯一分支上吸收最新主线并重跑受影响冒烟。
3. 推送一个新 PR，等待远端 Go、PostgreSQL、Rust 和镜像生命周期检查；不得自行合并。
4. 新 PR 完整覆盖后关闭旧开放 PR；拒绝不安全的 `#278`，不把它写成已吸收。
5. Gemini Code Assist `project_id`、Grok/Kimi OAuth 材料、Gemini Veo 配额、Claude 限流和 Moonshot 有效 Key 在外部条件恢复后复验。Copilot、Windsurf、Cursor 继续封存至首次正式上线后。

## 后续需求队列

当前账号转 API 闭环完成后，为用户端和租户端建设动态池链路模块图。它不是普通单次请求详情页，而是基于后端唯一事实源实时展示可用账号池、池健康与容量，以及请求实际流经的账号池，并继续展示上游结果、实际用量、预扣、最终扣款、释放或待对账状态。用户只能查看自己的调用与金额，租户管理员只能查看本租户汇总和本租户用户调用；前端页面不进入本轮 PR，本轮必须保证池选择、切换、用量和钱账状态来自同一条真实链并可统一投影。

## 上线门

- 不得以 refresh dry-run、handler 单测、全局模型目录或前端按钮冒充账号转 API 成功。
- 每个宣称支持的账号类型必须同时有导入/校验、凭据物化、真实 adapter、出站、失败分类、状态回流和恢复入口；未验证能力明确标红或 feature flag，不伪装上线。
- 凭据、Cookie、access token、API key 和完整上游错误正文不得进入响应、日志、计划或提交。
- 真实账号测试未完成前，不宣称相应厂商活体已通；外部账号失效必须与本地实现失败分开记录。
