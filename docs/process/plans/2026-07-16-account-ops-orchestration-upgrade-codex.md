# 2026-07-16 账号运营联动升级（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “修复吧。我需要像sub2，甚至比他更优秀，更智能的的联动。但是要考虑一个边界，就是好运维。直观，参考sub2”；并纳入 Claude Cookie、Setup Token、Codex 批量/Agent Identity、CRS/账号迁移包四项缺口。 |
| Scope | 以 HUAKAI 现有账号、加密凭据、selector、channel health、auth cooldown、逐模型冷却、claim、审计和恢复代码为基础，建设 provider-neutral 的账号运营与接入闭环。第一阶段不新增页面，提供未来“上游与模型”统一容器可直接消费的运营聚合 API、bulk 原子合同和统一 `AccountIntakePlan` dry-run 合同。后续独立 PR 再建设 Setup Token、Codex 账号级批量、Claude Cookie Safe Equivalent、Agent Identity Experimental、CRS 连接器和安全迁移包。 |
| Out of scope | 当前低风险执行阶段不修改数据库 schema、资金账、billing ledger、鉴权/RBAC、强配额语义、真实凭据、生产部署或前端页面；不启用 Cookie/Setup Token/Agent Identity/CRS；不导出秘密；不直接启用 PASR；不引入 Redis 调度快照；不复制 Sub2、CLIProxyAPI 或 New API 源码。 |
| Success criteria | 运维通过一个账号运营合同即可看到真实可调度性、阻断层、状态来源、恢复时间和建议动作；所有账号接入来源先形成统一 dry-run 计划，明确 create/update/skip/conflict/fail、去重证据和字段差异；bulk-by-tag 每个账号的修改和审计同成同败并返回完整逐项结果。高风险能力必须保持默认关闭、秘密不进审计、凭据进入现有加密仓库，且成功导入的账号能继续进入刷新、selector、健康、诊断和恢复链。目标测试、相关链测试、代码预算和独立 review 通过，改动提交到 Draft PR，未经 Owner 同意不合并。 |
| Time estimate | 低风险基础阶段约 5-8 小时；统一恢复约 3-5 小时；四项高风险能力按 4-6 个独立 PR 推进，每项需单独决策和真实协议验证。 |
| Blast radius | 基础阶段影响 provider-account 管理 API、admin store、审计事务和导入预检，不改变 selector、鉴权或计费。Setup Token/Cookie/Agent Identity 会改变凭据获取与认证合同；CRS 会新增受控网络出口；秘密恢复包会扩大凭据读取面，均属于高风险。 |
| Failure modes | 聚合 API 自创一套第六种状态；只展示 provider health 而漏 selector 实际 gate；建议动作与真实路由不一致；bulk 为了“全原子”持有过大事务；审计 payload 泄漏敏感信息；逐项结果仍无法安全重试；测试只断言非空；管理 API 名称诱导前端拆出很多页面。缓解：只读聚合现有权威状态、每项标注来源和影响层、动作能力由实际依赖推导、单账号事务而非整批大事务、结果稳定分类、敏感字段白名单、判别式失败/恢复测试、统一 operations 合同。 |
| Decision points | 基础阶段按“只读聚合 + 单账号逐项事务 + 纯 dry-run 接入计划”执行，无 schema/资金/鉴权变化。Owner 已固定三身份、单层租户边界：部署者负责平台治理以及向租户分配能力、账号和经营额度；部署者不能替租户处理客户业务；租户只能管理自己的客户用户、已分配账号和可分发额度；用户只管理自己的资源；租户不能继续创建租户。租户侧管理账号只是代表该租户操作，不构成第四种身份。**当前代码并不满足该边界**：session admin 会映射为全平台 `platform_admin`。后续必须先决定新的部署治理主体、租户侧管理账号和 grant 持久化合同，不能把现有 `platform_admin/tenant_operator` 名称当成目标已经实现。授权绑定用户身份还是管理令牌仍需 Owner 确认；真实账号测试、账号唯一身份和 selector/schema 迁移也需单独确认。 |
| Parallel-plan status | Owner 已明确要求 Codex 独立工作且不要管 Claude。本计划不读取、不修改 Claude 计划或工作树，作为独立 Codex 车道执行。 |

## 设计原则

1. **一个账号，一个运营视图。** 不要求运维在 provider health、channel health、credential、auth 和 model cooldown 五个入口之间人工拼图。
2. **状态不强行压成一个枚举。** 保留正交状态，但统一计算 `schedulable`、阻断原因、恢复时间和来源，避免信息损失。
3. **动作由事实驱动。** API 返回当前允许的动作、禁用原因和风险提示，不让前端猜“该显示哪个按钮”。
4. **请求真相优先。** 运维视图必须以 selector 实际读取的状态为准；仅供管理展示但不参与调度的字段要明确标为 `observational` 或 `legacy`。
5. **逐项原子，整批可恢复。** bulk 每个账号 update+audit 在同一事务；整批返回完整结果，允许失败项重试，不用长事务锁住全部账号。
6. **直观但不失真。** 顶层给 `可调度 / 暂停 / 冷却 / 凭据异常 / 配置异常 / 过期` 等运维摘要，底层仍保留原始来源和时间。
7. **不增加页面数量。** API 面向现有“上游与模型”统一容器，账号详情、状态、动作和历史可在同一场景组合展示。
8. **所有入口归一。** OAuth、Cookie、Setup Token、Codex 文件、Agent Identity、CRS 和迁移包都先转成统一接入计划，不允许每种来源各自绕过账号身份、加密、审计和恢复。
9. **秘密默认不移动。** 预览和普通迁移默认不包含凭据；必须恢复秘密时使用独立加密包、step-up 和短时下载。
10. **导入完成不是终点。** 新账号只有在凭据可刷新、selector 可识别、健康可解释、恢复动作可用时才算真正接入。
11. **三身份、单层租户。** 部署者负责能力、账号和经营额度的分配与回收，不得替租户处理客户业务；租户只能管理本租户用户和已分配资源，不能创建租户；用户只管理自己的资源。现有 `platform_admin/tenant_operator` 只是待迁移的代码事实，不直接等于目标模型。

## 四项缺口源码核实

| 能力 | HUAKAI 当前真码 | 参考行为 | 准确判定 |
| --- | --- | --- | --- |
| Claude Cookie 自动登录 | Anthropic 只有标准 PKCE exchanger；helper 路由没有 Cookie/sessionKey 入口，见 `credentialacq/types.go:214-225`、`admin_credential_acquisition_handler.go:83-98`、`credentialacq/anthropic_oauth.go:62-124`。 | 服务端使用一次性 Cookie 完成组织发现、授权码获取和 token 交换；不保存原 Cookie。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:175-282` | **整条缺失** |
| Claude Setup Token | `FlowKindSetupToken`、long-lived gate 和 refresh 兼容代码存在；模式目录没有正式 plan，生产 deps 与 refresher 都保持 false，见 `credentialacq/types.go:11-23,214-225`、`cmd/gateway/routes.go:95-103`、`credentialworker/mode_refresh.go:96-104`。 | 独立账号形态、窄 scope、统一刷新。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:365-371` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/token_refresher.go:40-71` | **零件存在但未正式接线** |
| Codex 批量/Agent Identity | 通用 parser 支持多行/JSON/raw token，但只能写入一个既有 account；`accountident` 只是非授权 ID/email 元数据。 | 专用账号级导入逐项创建/更新/跳过；Agent Identity 是带 Ed25519 私钥和任务绑定的认证模式。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:159-379` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:64-313` | **批量账号流缺失；Agent Identity 整条缺失** |
| CRS/账号迁移包 | 账号 acquisition 路由未观察到远程账号源或账号包入口；现有 encrypted store 可作为安全落库基础。 | CRS 是 `claude-relay-service` 专用同步；迁移包能带账号/凭据/代理，但有损且文件未观察到加密。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:222-380` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:27-73` | **账号域整条缺失** |

完整 clean-room 证据见 `docs/process/research/2026-07-16-sub2-account-import-four-gaps-specifier.md`。HUAKAI 结论来自实际打开的生产源码，不依赖现有前端页面或旧文档。

## 第一阶段：运维诊断与 bulk 原子性

### A. 账号运营聚合合同

目标输出结构：

- `summary`：`schedulable`、`status`、`headline`、`next_recovery_at`。
- `blockers[]`：稳定 code、严重度、来源、是否被 selector 消费、开始/恢复时间、运维说明。
- `signals[]`：provider health、channel health、credential、auth、model cooldown、到期和人工启停的原始摘要。
- `actions[]`：动作 code、当前是否允许、禁用原因、是否会发真实上游请求、是否可能影响生产流量。
- `warnings[]`：管理字段与 selector 真相不一致、状态陈旧、缺少 credential/version 等。

执行步骤：

1. 打开 provider-account 详情、channel health、credential、auth cooldown、model cooldown 的真实 store 和 handler。
2. 确认每个状态的权威来源、更新时间、selector 消费点和恢复入口。
3. 在现有 provider-account 管理域内增加内聚的只读 operations handler；不新建页面导向型路由家族。
4. 聚合器保持纯函数或窄接口，单测覆盖状态优先级、多个同时阻断、恢复时间和动作可用性。
5. OpenAPI/管理合同同步记录，但不声称真实上游测试已存在。

### B. bulk-by-tag 逐项原子合同

目标行为：

- 先列出匹配账号并固定本批目标 ID。
- 每个账号独立执行 update+audit 事务。
- 一个账号失败不阻止后续账号处理。
- 响应返回 `succeeded[]`、`failed[]`、`skipped[]`、总数和稳定错误 code。
- 成功项保证修改与审计同时存在；失败项保证两者都不存在。
- 保留兼容的 `affected_ids` 和 `count`，避免已有调用方立刻破坏。

执行步骤：

1. 在 admin DB 边界增加单账号事务方法，复用现有 update 和 audit SQL，不改 schema。
2. handler 改为逐项调用事务方法并收集结果，不再首错即停。
3. 审计 payload 仅包含允许修改的非敏感字段和 batch request ID。
4. 单测制造第一个成功、第二个 update 失败、第二个 audit 失败、第三个继续成功等判别场景。
5. 若已有真实 PostgreSQL 集成测试基建可复用，增加事务回滚验证；否则明确记录剩余集成风险。

### C. 统一账号接入 dry-run 合同

本阶段只建立归一化计划和判别测试，不创建账号、不写凭据、不发真实网络请求。

目标输出：

- `batch_id`、`source_kind`、`source_version` 和输入条数。
- `items[]`：规范 vendor/auth mode、可提取身份、去重证据、凭据生命周期摘要。
- `action`：`create / update / skip / conflict / fail`。
- `field_changes[]`：哪些字段会新增、覆盖、保留或被拒绝。
- `warnings[]`：无 refresh material、身份弱、token 已过期、包含私钥、未知版本。
- `required_confirmations[]`：需要 step-up、允许秘密写入、允许网络同步或选择冲突策略。

执行步骤：

1. 把现有 CLI/JSON/CSV parser 包装成只读输入适配器，不改变原 helper 语义。
2. 定义 provider-neutral `AccountIntakePlan` 和稳定错误码；来源适配器只能产计划，不能直接写 store。
3. 先接 Codex token/session 的 dry-run，明确区分“账号身份元数据”和“Agent Identity 认证材料”。
4. 为未来 Setup Token、Cookie、CRS 和迁移包保留 source kind，但未启用的来源返回 `feature_disabled`，不能伪装已实现。
5. 测试覆盖重复输入、已有账号、身份冲突、过期 token、无 refresh material、畸形 JSON 和秘密不进入响应/日志。

## 第二阶段：统一恢复动作

在第一阶段只读合同稳定后，增加同一个 operations 域内的动作：

1. `refresh-now`：只对支持持久化刷新且凭据状态允许的账号开放。
2. `clear-routing-cooldowns`：清理 selector 实际消费的 channel/model/auth 状态，不只清管理列表字段。
3. `resume`：统一人工启用与 channel health 恢复前置检查。
4. `re-authorize`：返回应进入的 acquisition 类型和原因，不在服务端伪造授权。

执行前必须重新核实每个动作的数据库、缓存、credential version、审计和多副本影响；不允许一个按钮只清半套状态。

## 第三阶段：高风险能力，逐项独立 PR

### 真实账号测试

- 复用生产协议映射、凭据物化、代理和 transport。
- 使用独立 test claim/usage 分类，不进入客户账。
- 默认关闭，单账号显式触发，限制模型、token、超时、频率和成本。
- 明确区分 credential、relay、model、quota 四层结果。

### 账号级批量导入

- 先 dry-run，返回 create/update/skip/fail 计划。
- 唯一身份、冲突覆盖、凭据保留和数据迁移等待 Owner 决策。
- 凭据继续进入 HUAKAI 加密 store，不复制参考项目存储结构。

### Claude Setup Token 一等接入

- 复用现有 acquisition session、加密 store、刷新锁和审计。
- acquisition plan、生产 gate 和 refresher gate 必须由同一配置源控制。
- 明确展示 access token 到期与 refresh material 状态。
- 推荐部署级总开关默认关闭；打开后仍须由部署治理主体授权指定租户管理员，部署治理主体本身不替租户执行。

### Claude Cookie Safe Equivalent

- Cookie 仅用于单次服务端转换，不落库、不进审计正文、不进入普通错误文本。
- 固定批准域名、client profile、scope 和 transport；不接受请求输入覆盖 endpoint。
- 部署治理主体只负责授权与撤权；被其授权的租户管理员可在自身 tenant scope 内使用，且要求管理员 step-up。
- 未授权租户管理员固定返回 `403`，不能仅依赖前端隐藏。
- 批量逐项结果、稳定阶段错误码和失败项重试。
- 完成转换后进入普通 OAuth/Setup Token 凭据链，不新建长期 Cookie 凭据类型。

### Codex Agent Identity Experimental

- 独立于现有 account ID/email 元数据，作为“签名身份凭据”建模。
- 部署治理主体不执行导入、轮换或恢复；被单独授权的租户管理员只能操作自身 tenant 内的私钥和任务绑定。
- Ed25519 私钥进入现有应用级信封加密，审计只记录存在性和指纹。
- 实现导入校验、请求签名、任务绑定注册/恢复、持久化和连接失效。
- 未完成真实协议、撤销和租户绑定验证前保持 Experimental。

### CRS 连接器

- 核心域只认识“兼容账号源”，CRS 作为可插拔 adapter。
- 部署治理主体只管理连接器能力开关和授权；连接配置、预览和执行由被授权租户管理员完成，结果只能同步进自身 tenant。
- 默认地址 allowlist、解析时和 dial 时双重 SSRF 检查、响应大小和超时上限。
- 密码使用一次性输入或 secret ref；同步前字段级预览，逐项冲突策略。
- 导入成功后统一进入账号接入、加密凭据和运营诊断链。

### 安全账号迁移包

- 默认包只含账号结构、调度、分组、代理引用和非秘密元数据。
- 部署治理主体不替租户执行导入导出；被授权租户管理员只允许处理自身 tenant，秘密恢复包额外要求 step-up。
- 可恢复包显式包含秘密，必须加密、签名、版本化、step-up、短时一次性下载。
- 导入前预检；导入后记录对象清单、失败重试和按批次撤销新建对象。
- 不把账号包宣称为完整系统 DR；资金、使用、审计、用户/API key 另走各自灾备合同。

### 状态真相迁移

- 先用第一阶段聚合合同统计真实冲突，再决定让账号级时间字段进入 selector，或退役其调度含义。
- 任何 schema/API/selector 语义改变单独提交决策包和迁移/回滚计划。

## 最终成果

完成后，运维看到的不是六套互不相干的导入按钮，而是一条直观流程：

`选择来源 → 预检 → 身份去重 → 字段差异 → 风险确认 → 逐项创建/更新 → 加密凭据 → 自动刷新 → selector/健康诊断 → 恢复动作`

具体成果：

1. Claude 可以按标准 OAuth、Setup Token 或一次性 Cookie 转换接入，最终都落到同一凭据生命周期。
2. Codex 可以批量导入独立账号，清楚区分可刷新 token 会话和带私钥的 Agent Identity。
3. CRS 与文件迁移共用同一预检、冲突和逐项结果合同，不会各写一套账号创建逻辑。
4. 导入成功后立即进入现有 credential refresh、selector、channel health、诊断和恢复，不再出现“账号建了但后续系统不知道它”的半接线。
5. 运营聚合合同直接告诉管理员：为什么不可调度、预计何时恢复、现在能做什么，不要求人工拼 provider/channel/credential/auth/model 五套状态。
6. 所有批量动作可恢复：已成功、失败、跳过、冲突都能精确定位，允许只重试失败项。

## 与 Sub2 的差异

| 维度 | Sub2 已观察行为 | HUAKAI 目标 |
| --- | --- | --- |
| 入口组织 | 多个 provider 专用入口直接执行创建/更新。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:349-371` | 保留专用体验，但后端统一转成 `AccountIntakePlan` 后再执行 |
| 凭据保护 | 管理响应会遮蔽秘密；账号仓储未观察到应用级信封加密。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/account_credentials_redact.go:3-13` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/repository/account_repo.go:545-617` | 继续使用现有 AES-GCM 信封、AAD 绑定、版本/CAS 和 mutation+audit 同事务 |
| 批量导入 | 逐项创建/更新/跳过/失败，整批允许部分成功。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:159-379` | 同样逐项处理，但增加 dry-run、字段差异、冲突证据、失败项重试和批次撤销 |
| Cookie | 服务端直接转换；额外 step-up 和结构化批量错误仍有加强空间。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:175-238` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:350-371` | 默认关闭、step-up、Cookie 单次使用、请求体全量 secret-mask、稳定阶段错误码 |
| Setup Token | 独立账号类型，可刷新维持长期使用。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:365-371` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/token_refresher.go:40-71` | 同样一等建模，同时强制 acquisition/refresher gate 一致和双生命周期展示 |
| Agent Identity | 已有签名身份和任务绑定恢复。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:64-313` | 独立 Experimental 凭据模式，私钥信封加密，真实协议验证通过后才转 Released |
| CRS | 专用 `claude-relay-service` 同步。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:222-380` | 核心域不绑定 CRS 名称，作为受 SSRF 保护的可插拔兼容连接器 |
| 迁移包 | 能迁账号/凭据/代理，但有损且文件未观察到加密。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:27-73` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:245-484` | 默认无秘密安全包 + 显式加密签名恢复包，保留分组/关系并报告不可恢复字段 |
| 调度联动 | 导入账号直接进入其账号调度体系。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:330-379` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:175-313` | 导入后进入 HUAKAI 共享 selector、claim、billing、audit、health 和 recovery 骨架 |
| 运维视图 | 账号动作集中，并展示账号运行指标。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_handler.go:181-248` | 保留集中体验，并增加 selector 真相、阻断来源、恢复时间和建议动作 |

大白话：不是照抄 Sub2 多做几个按钮，而是把它成熟的账号流转体验吸收进 HUAKAI 更严格的多租户、加密、审计、资金和恢复骨架里。我们要做到“导得进、分得清、接得上、出问题能恢复”，同时避免明文迁移包和专用协议侵入核心。

## Pre-execution checklist

1. 仅使用 `/home/ubuntu/HUAKAI-wt-global-wiring-codex` 和 `audit/backend-global-wiring-20260716-codex`。
2. 确认工作树无其它目标改动，继续使用 Draft PR #256；不合并主线。
3. 当前实现会话只读 HUAKAI 源码和已生成 clean-room 行为报告，不读取参考源码。
4. 完整读取 provider-account、channelhealth、credential、auth cooldown、model cooldown、acquisition、account identity 和生产 wiring。
5. 在编辑前列出第一阶段准确文件范围和接口变化。
6. 基础阶段不修改 schema、鉴权、资金、强配额、真实凭据或部署；不启用 Cookie、Setup Token、Agent Identity、CRS 或秘密导出。
7. 所有 Go 注释和测试注释使用中文，代码注释不出现参考项目名。
8. 使用根磁盘下 `/home/ubuntu/.codex-tmp/...`，不使用 `/tmp`。
9. 运行目标单测、相关包测试、真实 PostgreSQL 集成测试（若环境可用）、代码预算和 `git diff --check`。
10. 暂存预期差异后运行只读 Codex review；S0/S1 修复后最多再跑一轮。
11. 中文 commit，推送 Draft PR，等待 Owner 同意后才合并。

## 第一阶段验收场景

1. enabled 且所有真实 gate 健康时，聚合结果为可调度，不伪报管理时间字段。
2. provider health 健康但 channel health 冷却时，结果为不可调度，明确显示 selector 使用 channel health。
3. credential revoked 时，即使 channel health 正常也不可调度，并建议重新授权/轮换。
4. 多个 blocker 同时存在时，全部返回，顶层摘要按可恢复性和严重度稳定排序。
5. 只有管理展示字段阻断、selector 不消费时，返回 warning，不把它冒充真实路由阻断。
6. bulk 中第二个账号 update 失败，第一和第三个仍成功且都有审计。
7. bulk 中 audit 失败时，该账号 update 回滚，响应给出失败项，不出现在 `affected_ids`。
8. 相同失败项可由运维重试，不重复修改已经成功的目标。
9. 租户边界、角色校验和审计 actor/request ID 保持现有合同。
10. 所有响应不包含 credential payload、token、cookie 或私钥。
11. Codex dry-run 能区分独立账号、重复项、已有账号、弱身份和冲突，不向同一个 account ID 假装导入多个账号。
12. `Agent Identity` 输入被明确标记为签名凭据，而不是现有 `external_account_id/email` 元数据。
13. 未启用的 Cookie、Setup Token、CRS 和秘密恢复包返回稳定 `feature_disabled`，不出现空转或假成功。
14. 所有 intake 响应只返回非秘密摘要、指纹和生命周期，不回显原始输入。
15. 导入执行阶段后续验收必须证明新账号进入 refresh、selector、health、diagnostic 和 recovery，而不只证明数据库插入成功。
