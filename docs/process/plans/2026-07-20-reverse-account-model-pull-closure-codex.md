# 2026-07-20 账号转 API 全链路闭环计划

| 项目 | 内容 |
| --- | --- |
| Owner 指令 | “我要的是全链路测试”“对全局和所有账号生效”“找到之后接线，修复他们”“有些重复的功能代码和逻辑互相悖论的可以取其一，删其二”“但要功能都要有，集成为唯一” |
| 范围 | 所有已部署账号类型的导入、身份与订阅识别、凭据存储与刷新、额度健康、模型发现/同步、选号与 gate、凭据物化、协议适配、Rust 唯一出口、上游错误回流、claim/结算、日志和人工恢复；同时核实并处理已实现未接线、重复、冲突、只写不读和已被替代的代码。 |
| 不包含 | 前端页面；生产部署；读取真实秘密；未经 Owner 批准合并主线。 |
| 成功标准 | 每种账号都能从导入结果追到生产出站资格；供应商与认证模式精确分派；静态凭据不被误刷；订阅/额度事实不伪造；失败分类、退避、健康和日志一致；模型同步状态可运维；Rust 为唯一真实出口；无功能缩水的重复实现已清理；聚焦、全量、竞态、真实 PostgreSQL/Rust 和真实账号端到端测试通过。 |
| 爆炸半径 | 账号可用性、OAuth 轮换、调度公平性、上游限流、模型目录、Rust 出站、结算与日志。错误可能导致错刷账号、刷新风暴、把临时故障永久下线、把不可用账号放入调度池或让运营看到假状态。 |
| 失败模式与缓解 | 只按 vendor 分派会覆盖 auth mode；用文档/搜索命中冒充生产接线；删重复实现时丢独有恢复能力；把 429、订阅耗尽和 5xx 混为一类；全链测试绕过真实入口。所有结论必须回到入口、DI、运行时调用和状态回流核实，先做能力并集和判别测试，再删除旧实现。 |
| Owner 决策点 | 当前目标内的代码、schema、鉴权、配额和结算修复已获所有权，可按证据直接推进；生产秘密、部署、不可逆数据操作、`LICENSE` 和合并主线仍需 Owner 明确批准。分组路由读取失败已由 Owner 定为“优先保证绝不越级用池”：策略真相不可用时直接返回明确 503，禁止缓存旧授权继续放行。 |

## Owner 最新执行顺序

- 先闭环 Owner 已提供真实账号的 Claude、Gemini、Antigravity、Kimi、Grok、Codex 等账号链，完成导入、身份与订阅识别、刷新、额度、模型、调度、出站、回流和恢复。
- Copilot、Windsurf、Cursor 统一封存并移出本次上线范围。三者只允许在 HUAKAI 首次正式上线后另立目标、经 Owner 明确解封再继续，不得抢占当前主链时间。
- 三个封存项现有且已通过测试的安全基础修复保留；本目标不再扩展、不默认启用，也不得把“有入口”“有 adapter”或“可导入”表述成“可服务”。本次发布矩阵必须明确标记为“封存/不可发布”。

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
- Copilot 正式获取入口已接到固定公开设备码流程；请求方不能替换客户端身份、端点或 scope。GitHub 长期授权材料单独存为 `github_access_token`，刷新器换得 Copilot `session_token` 与动态 endpoint 前运行时保持拒绝；仅有引导材料的凭据即使带未来到期时间也会立即进入刷新队列。孤立且无生产调用的旧 Copilot 引导实现已删除。
- 套餐字典补齐 Claude Max 5x/20x、Gemini Plus、Grok Lite/Business/Enterprise、Copilot Free/Student/Pro/Pro+/Max/Business/Enterprise、Kimi 五档会员和 Windsurf Free/Pro/Max/Teams/Enterprise；个人与工作区作用域分开，未知值继续保留原文。旧 `huakai_codex_*` 测试库已在确认零连接后删除 22 个，只保留本目标的最新可用测试库，避免旧迁移制造假红。
- Antigravity 导入、刷新和出站统一要求真实 project 身份；缺失或冲突时明确拒绝，403 project 拒绝只允许一次同步刷新并重试。Google 429 的结构化退避信息已进入统一错误分类，避免无节制重打。
- 账号额度采集启动即运行，并以 PostgreSQL 会话租约保证多副本只有一个采集者；列表、单账号健康和诊断入口复用同一 5h/7d 投影，过期、未知和失败快照不伪造成满额。
- 原生 function/tool calling 的请求、流式工具事件和终止原因已有生产形态测试；MCP 细粒度授权在本轮外部源码中未观察到可靠现成合同，保留为强制路线，不用“模型支持工具”冒充“MCP 已上线”。
- 生产刷新链已收敛到 `credentialworker` 唯一注册表，Anthropic、Codex、Gemini、Antigravity、Grok、Kimi 继续保留实际所需能力；删除了无生产入口的旧 provider 刷新包装、凭据存储适配层和重复测试支架，静态检查基线从 92/787 降至 77/697，没有放宽预算。
- 五个超预算文件已按职责拆分，外部 API、schema 和运行语义不变；全仓代码预算门恢复通过。
- Claude API Key、Claude AI OAuth、Claude Code、Gemini AI Studio、Gemini Code Assist、Antigravity、Kimi API Key、Kimi OAuth、豆包 API Key、混元官方 API Key、Codex、Grok、ChatGPT session 和 OpenAI 图片账号的活体脚手架，均先经过限定租户管理员令牌、显式能力授权和 `plan/execute` 正式导入 API 原子建号，再进入选号、出站、计费和恢复断言；账号专用活体测试已无直接写 `provider_accounts` 或 `account_credentials` 的旁路。多账号并发、轮询和故障切换由专门的调度夹具验证，不再用数据库直写冒充账号导入成功。
- 活体脚手架已纳入当前源码构建的 Rust `tls-sidecar` 生命周期和 readiness，Go 网关显式使用本次测试唯一 Unix socket；此前只启动 Go 进程、在 Rust 唯一出口启动门前假失败的问题已修复。测试连接池改为最后关闭，正式导入产生的健康、日志、凭据和授权数据会先清理，重复运行不再污染共享测试库。
- 专用活体清理已覆盖 `scheduler_outbox`、`usage_record_dlq`、append-only `usage_records`/`billing_events`、余额占用、账号槽和 claim；删除触发器只在测试事务内临时关闭，失败自动回滚，成功后恢复。四类专用活体测试重复运行后测试租户残留为 0，相关 append-only 触发器均保持启用。

## 验证结果

- `go test -race -count=1 -timeout 8m ./...`：通过。
- `scripts/quality-gate.sh`：通过，静态检查与死代码债务均下降。
- PostgreSQL 集成：78 个隔离包全部通过；其中 `mediatask` 因普通测试角色无权终止瞬时连接，改用独立数据库复测通过。
- 数据库迁移：在独立数据库完成 `up -> down -all -> up`，通过至迁移 207。
- 性能门：6400 请求、32 并发的混合负载通过，p95 约 6.0ms、p99 约 8.1ms。
- Rust `tls-sidecar`：74 项测试全部通过。
- `GOTOOLCHAIN=go1.25.12 govulncheck ./...`：生产可达漏洞为 0；本地默认 Go 1.25.0 的标准库告警不作为源码缺陷冒充修复结果。
- 真实账号测试入口可编译，凭据处理与脱敏矩阵通过；在真实 PostgreSQL、真实 Go 网关和真实 Rust sidecar 上，正式管理员鉴权、租户能力授权、导入预检、原子建号与加密凭据、渠道健康初始化和选号查询已通过，且测试后账号、令牌和租户残留均为 0。Claude、Gemini、Antigravity、Kimi、Codex、Grok 的真实厂商调用仍因未获准读取本机会话凭据而未执行，尚不能宣称真实上游已验收。
- Codex、Grok、OpenAI 图片三条合成凭据判别测试及通用账号正式导入测试均在 `-race` 下通过；ChatGPT session 使用本地假上游完成“正式导入 → 选号 → Rust sidecar → 出站 → 响应 → claim/用量/余额”全链闭环。测试库残留为 0，`usage_records`、`billing_events` 和订阅观测三类 append-only 触发器均为启用状态。
- 提交前独立审查发现的运行时路径硬编码、`CARGO_TARGET_DIR` 不一致、身份断言过弱、冷构建占用业务超时和凭据日志脱敏不全均已修复；嵌套 camelCase、`setup_token`、通用 `token`、服务账号私钥与 AWS 密钥均有可判别泄漏断言，聚焦竞态测试通过。
- 前端目录零改动；Copilot、Windsurf、Cursor 未被公共链改动解封。

## 剩余顺序

1. 提交前对抗审查的 S0/S1 已清零；提交中文 commit、更新唯一 PR #286 并等待 CI 全绿，不合并主线。
2. Owner 明确授权读取本机受保护的 Claude、Gemini、Antigravity、Codex 会话凭据，并提供 Kimi、Grok 安全加载方式后，从正式 API 鉴权入口补跑活体端到端验收；仅一个真实账号时，用可控账号替身验证多账号轮询、并发、故障切换与隔离，真实账号只承担单账号活体证明。
3. Copilot、Windsurf、Cursor 保持封存。首次正式上线后另立目标并经 Owner 明确解封，届时分别闭环导入、运行凭据、协议适配、调度、出站、错误回流和恢复合同。

## 后续需求队列

当前账号转 API 闭环完成后，为用户端和租户端建设动态池链路模块图。它不是普通单次请求详情页，而是基于后端唯一事实源实时展示可用账号池、池健康与容量，以及请求实际流经的账号池，并继续展示上游结果、实际用量、预扣、最终扣款、释放或待对账状态。用户只能查看自己的调用与金额，租户管理员只能查看本租户汇总和本租户用户调用；前端页面不进入本轮 PR，本轮必须保证池选择、切换、用量和钱账状态来自同一条真实链并可统一投影。

## 上线门

- 不得以 refresh dry-run、handler 单测、全局模型目录或前端按钮冒充账号转 API 成功。
- 每个宣称支持的账号类型必须同时有导入/校验、凭据物化、真实 adapter、出站、失败分类、状态回流和恢复入口；未验证能力明确标红或 feature flag，不伪装上线。
- 凭据、Cookie、access token、API key 和完整上游错误正文不得进入响应、日志、计划或提交。
- 真实账号测试未完成前，不宣称相应厂商活体已通；外部账号失效必须与本地实现失败分开记录。
