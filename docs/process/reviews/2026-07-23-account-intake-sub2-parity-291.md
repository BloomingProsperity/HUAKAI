# 2026-07-23 账号接入全生命周期主线审计

## 一、结论先说

本报告只认 `origin/main@5b9684ef41d9b17d8cdedcfcd7f00e5b08c0a1b3`。当前审计分支、远端主线和本地 `main` 指向同一提交，报告没有吸收旧 PR、其他工作树或未合并代码。

HUAKAI 的账号接入并非“没做”或“全是空壳”。当前主线已经形成包含身份消歧、显式冲突、账号/凭据/健康/日志同事务、OAuth 临时凭据持久化、套餐标签、5h/7d 配额和恢复状态的标准导入主链。

但当前主线仍有四组 S1：

1. Cookie、CRS、加密迁移包共用的来源语义错误，使部分可信 OAuth/Setup Token 账号“入口存在但不能成功落库”。
2. 部署者、租户管理员的权限合同在接入、账号管理、凭据轮换和渠道恢复之间互相矛盾。
3. 单账号更新、启停、删除先改业务状态，再单独写日志，日志失败会留下半完成结果。
4. “账号+凭据直接创建”仍是较弱的平行会话导入通道，可绕过标准链的稳定身份匹配、项目补全和激活通知。

另有四组 S2 和两组 S3。它们不代表主链完全不可用，但会直接影响批量运营、订阅到期预警、原地重认证和秘密导出安全。

## 二、证据范围与限制

### 2.1 固定版本

| 对象 | 固定版本 | 许可证与用途 |
| --- | --- | --- |
| HUAKAI | `5b9684ef41d9b17d8cdedcfcd7f00e5b08c0a1b3` | 唯一实现事实 |
| sub2api | `6aeea70ee008825604ac3293ca0f216e951795d1` | LGPL-3.0，仅提取账号运营行为合同；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:LICENSE:1-20` |
| CLIProxyAPI | `f71ec0eb6776854457892452cf28c47f0d658251` | MIT，仅交叉核实认证文件、OAuth、刷新和运行态管理；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:LICENSE:1-21` |
| new-api | `1721144221ec5c94dd87891a7ae1bee228e7bb63` | AGPL-3.0，仅交叉核实渠道、凭据、刷新和健康运维行为；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:LICENSE:1`、`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:NOTICE:1-27` |

外部项目由三个独立 clean-room specifier 读取当前源码。本报告只综合行为和证据锚点，没有复制函数、结构、注释、数据库设计、UI 或算法顺序。

证据分类口径：正文“事实”、源码直接描述和带锚点的外部行为均为 `Observed`；跨函数、跨入口或故障结果的推导在正文逐项标为 `Inferred I-01` 至 `I-09`；未取得源码或活体证据的事项只列入 `Open Question`。

### 2.2 维护、发布与公开场景

[sub2api](https://github.com/Wei-Shaw/sub2api)、[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)、[new-api](https://github.com/QuantumNous/new-api) 在取证时均未归档，默认分支均为 `main`。维护状态、Release 和 Issue 由 specifier 同步核实，但只承担两种用途：

- 提交与 Release 用于确认源码时效和定位近期变化。
- Issue 只转成失败与恢复验收场景，不反推当前 HEAD 仍有同名缺陷。

| 项目 | 当前维护与发布 | 近期变化 | 公开场景 |
| --- | --- | --- | --- |
| sub2api | HEAD 提交时间 `2026-07-23T11:20:01+08:00`；近期发布 [v0.1.163](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.1.163)、[v0.1.162](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.1.162)、[v0.1.161](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.1.161) | 近 30 天账号域改动集中于稳定身份匹配、自动刷新、订阅到期、额度探测和异常账号隔离；当前合同已回到固定 HEAD 核实：`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_codex_import.go:490-656`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/token_refresh_service.go:831-953` | [#3647](https://github.com/Wei-Shaw/sub2api/issues/3647) 同工作区误合并、[#3757](https://github.com/Wei-Shaw/sub2api/issues/3757) 刷新后仍 401、[#3825](https://github.com/Wei-Shaw/sub2api/issues/3825) Setup Token 刷新、[#4466](https://github.com/Wei-Shaw/sub2api/issues/4466) 坏账号持续调度，转成身份、刷新和隔离验收场景 |
| CLIProxyAPI | HEAD 提交日期 `2026-07-22`；最新发布 [v7.2.95](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v7.2.95)，与固定 HEAD 一致 | [v7.2.94](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v7.2.94) 合入稳定认证身份筛选；当前认证索引和刷新接线见 `router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:sdk/cliproxy/auth/types.go:289-401`、`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:sdk/cliproxy/auth/conductor.go:5895-5950` | [#4489](https://github.com/router-for-me/CLIProxyAPI/issues/4489) 刷新令牌失效、[#4518](https://github.com/router-for-me/CLIProxyAPI/issues/4518) 模型周限额、[#4508](https://github.com/router-for-me/CLIProxyAPI/issues/4508) 等待策略、[#4520](https://github.com/router-for-me/CLIProxyAPI/issues/4520) 模型注册恢复，转成多副本刷新、额度和模型恢复场景 |
| new-api | HEAD 提交时间 `2026-07-21T12:32:16+08:00`；最新发布 [v1.0.0-rc.21](https://github.com/QuantumNous/new-api/releases/tag/v1.0.0-rc.21)，发布于 `2026-07-11` | 近 30 天相关改动覆盖 OAuth 渠道模型发现、分层渠道权限和数据库系统任务；固定 HEAD 证据：`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:router/channel-router.go:19-79`、`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:service/system_task.go:198-336` | [#6406](https://github.com/QuantumNous/new-api/issues/6406) 优先级快速操作、[#6374](https://github.com/QuantumNous/new-api/issues/6374) 刷新后行定位错乱、[#6361](https://github.com/QuantumNous/new-api/issues/6361) 认证刷新失败，转成并发、稳定行身份和失败保持场景 |

这些场景已经影响本报告验收建议：身份匹配必须抗并发和误合并；刷新成功后必须验证服务态真正换新；失败账号必须及时隔离并可恢复；模型和额度状态不能只更新内存或 UI。

### 2.3 2026-07-22 深读文档

主线、所有本地分支和 Git 历史中均未找到：

- `docs/process/reviews/2026-07-22-remaining-deep-read-index.md`
- `docs/process/reviews/2026-07-22-*-deep-read*.md`

因此不能诚实宣称“重新逐行读取原文”。本轮只对旧草稿和对话中保留下来的账号模块声明进行主线复核。无法恢复的原始表述列入证据限制，不用记忆补齐。

### 2.4 定向测试

以下包以 `-count=1` 运行通过：

```text
./internal/credentialacq/intake
./internal/accountbundle
./internal/gatewayhttp/accountintake
./internal/gatewayhttp/accountintakehttp
./internal/adminpoolhttp
./internal/adminhttp
./internal/provideraccountrecoveryhttp
./internal/channelhealthhttp
./internal/credentialworker
```

绿测不能推翻本报告的来源语义缺陷。现有判别测试明确证明普通 JSON/CSV/CLI 必须拒绝 OAuth-only 模式，但 Cookie、CRS 和迁移包测试没有把可信来源接到该闸上：

- 来源闸测试：`backend/internal/credentialacq/intake/source_provenance_test.go:10-35`
- 迁移包唯一 PostgreSQL 往返材料是 OpenAI API Key：`backend/internal/accountbundle/service_integration_pg_test.go:32-85`
- Setup Token 的迁移测试只验证迁移 0191 的数据库 CHECK 往返，不是账号迁移包往返：`backend/internal/gatewayhttp/accountintake/service_integration_test.go:1051-1081`

## 三、主线真实链路

### 3.1 生产入口

主线挂载了：

- 通用 JSON/CSV/CLI 计划与执行
- Codex 专用批量
- Codex Agent Identity
- Claude Setup Token
- Claude Cookie
- OAuth 浏览器或设备码
- CRS 同步
- 加密账号迁移包导出与导入

证据：`backend/internal/gatewayhttp/accountintakehttp/handler.go:85-108`；生产装配：`backend/cmd/gateway/routes.go:970-1089`。

部署者在账号接入入口被固定到平台自有租户；租户管理员只有在本租户获得 `AdvancedAccountIntake` 能力后可用；最终用户被拒绝。能力查询失败返回 503，不会放行：`backend/internal/gatewayhttp/accountintakehttp/handler.go:172-232`。

### 3.2 标准导入主链

标准链为：

```text
来源解析
-> 模式与来源回校
-> 凭据载荷校验
-> 稳定身份提取
-> 同租户库存匹配
-> create/update/skip/conflict/fail 计划
-> 计划哈希与明确确认
-> 执行前重新规划
-> 项目与套餐补全
-> 账号、凭据、身份、套餐、健康、日志同事务
-> 激活后配额探测
-> 刷新、恢复与运维查询
```

身份多命中、同账号同模式多凭据、强身份与弱邮箱冲突时不会任取第一条，而是明确冲突：`backend/internal/credentialacq/intake/match.go:25-149`、`160-205`。

执行时重新核对计划哈希和当前库存；每项创建或更新在短事务中完成账号、加密凭据、外部身份、套餐投影、健康初态和日志，提交后通知配额探测：`backend/internal/gatewayhttp/accountintake/execute.go:27-111`、`212-338`、`341-462`。

### 3.3 当前已做得更好的部分

1. **身份消歧更严。** sub2api 的通用导入逐项创建，专用 Codex 导入才按身份更新；CLIProxyAPI 主要按文件或 Key 派生运行时索引，不是统一供应商身份唯一键：`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_data.go:228-485`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_codex_import.go:490-656`；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:sdk/cliproxy/auth/types.go:289-401`。HUAKAI 标准链会把多命中变成冲突。
2. **写入原子性更强。** sub2api 的账号主记录、组绑定及部分 Day-2 清理分步执行：`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/admin_account.go:455-592`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/ratelimit_service.go:1733-1823`。HUAKAI 标准导入把账号、凭据、健康和日志放进同一事务。
3. **OAuth 临时状态更可靠。** HUAKAI 使用数据库会话和加密候选，执行成功时才在同一账号事务中认领、完成会话和清理临时秘密：`backend/internal/gatewayhttp/accountintake/oauth_service.go:193-269`。
4. **套餐证据不是一个裸标签。** 当前投影保存规范套餐、上游原值、作用域、来源、信任等级、验证状态和观测时间：`backend/internal/subscriptionprofile/profile.go:59-73`；`backend/internal/db/admin/admin_provider_accounts.go:69-83`。
5. **运维恢复比扁平启停更细。** 已有账号限流清除、完整账号恢复、渠道暂停/恢复/强制激活、模型发现与同步、5h/7d 配额窗口和恢复动作建议。
6. **批量更新是逐项短事务。** 当前按标签批量修改 `enabled`、`priority`、`static_weight`，每项重新锁定并把变更与日志放在同一事务，支持部分成功 207：`backend/internal/adminhttp/provider_account_bulk_handler.go:103-193`、`216-321`。

## 四、真实问题

### S1-01：三个可信入口被错误降成普通 JSON

### 事实

来源枚举已经区分 `oauth`、`claude_setup_token`、`crs_sync`，但来源映射把 `SourceCRSSync` 与普通 `SourceJSON` 一起映射成 `json_import`：`backend/internal/credentialacq/intake/plan.go:26-36`、`203-220`。

模式闸随后严格拒绝来源不在 `AllowedHelpers` 中的候选：`backend/internal/credentialacq/intake/plan.go:287-295`。这条闸本身是正确的安全设计，不能删除。

三个入口的错误如下：

| 入口 | 当前来源 | 产生的模式 | 结果（`Inferred I-01`） |
| --- | --- | --- | --- |
| Claude Cookie | 固定 `SourceJSON` | 普通 Cookie 产生 `claude_ai_oauth`；Setup Cookie 产生 `claude_setup_token` | 两者均被来源闸拒绝 |
| CRS | `SourceCRSSync`，随后映射为 JSON | Claude OAuth、Claude Setup Token、OpenAI OAuth、Gemini Code Assist | OAuth/Setup Token 类被拒；API Key 类可继续 |
| 加密迁移包 | 计划和执行均固定 `SourceJSON` | 包内可包含正式导出的任意凭据模式 | 允许 JSON 的 CLI/session/API Key 可继续；OAuth-only 和 Setup Token 不能往返 |

Cookie 证据：`backend/internal/gatewayhttp/accountintake/cookie_service.go:63-102`。Cookie 转换输出专用模式：`backend/internal/credentialacq/claudecookie/exchange.go:331-369`。

CRS 证据：`backend/internal/gatewayhttp/accountintake/crs_service.go:322-378`；其来源解析会生成上述 OAuth 和 Setup Token 模式：`backend/internal/credentialacq/crssource/client.go:325-357`。

迁移包证据：`backend/internal/accountbundle/import.go:90-140`、`270-291`。

### 为什么绿测没抓到

- 来源闸测试只证明“攻击者手写 JSON 不得伪装 OAuth”。
- Cookie 测试验证上游交换器，不验证 `CookieService.Plan -> Execute`。
- CRS 测试分别验证 HTTP stub 和来源归一，没有把真实 `CRSService` 接到模式闸。
- 迁移包 PostgreSQL 往返只有 API Key。

### 影响（`Inferred I-02`）

- Claude Cookie 一键导入和 Setup Cookie 在 UI 上可见，但无法形成可执行创建/更新。
- CRS 的核心订阅号迁移大面积失败，只剩 API Key 子集可用。
- HUAKAI 自己导出的包不能完整恢复 HUAKAI 自己的 OAuth/Setup Token 账号。
- 运营人员会误以为上游凭据失效，实际是本地来源语义错配。

### 修复方向与验收

保留普通 JSON 的 OAuth 防绕过闸，新增“服务端已验证可信来源”的明确语义；Cookie、CRS、迁移包必须按候选的真实获取方式受控映射，不能全局豁免 JSON。

至少增加：

1. Cookie 普通 OAuth 和 Setup Cookie 的计划、执行判别测试。
2. CRS 对 Claude OAuth、Setup Token、OpenAI OAuth、Gemini OAuth 和 API Key 的矩阵测试。
3. 迁移包对 API Key、CLI session、OAuth-only、Setup Token 的完整 PostgreSQL 往返。
4. 攻击者仍不能用通用 JSON 自声明 OAuth-only 的反向测试。

### S1-02：三身份权限合同在同一账号生命周期内分裂

### 事实

接入和直接创建路径采用一套较窄边界：部署者只能操作平台自有租户，租户管理员经能力授权后操作自己的租户：`backend/internal/gatewayhttp/accountintakehttp/handler.go:186-230`；`backend/internal/adminpoolhttp/handler.go:352-382`。

但普通账号列表、详情和部分管理路径仍允许无租户 scope 的平台管理员在查询参数中指定任意租户：`backend/internal/adminpoolhttp/validation.go:38-57`。项目测试还把该行为固定为正向合同。

同时：

- 凭据创建、轮换、禁用、删除要求 `platform_admin`，并接受请求给出的租户：`backend/internal/gatewayhttp/admin_credentials_handler.go:323-353`。
- 渠道暂停、恢复、强制激活只允许 `platform_admin`：`backend/internal/channelhealthhttp/handler.go:158-224`。
- 恢复动作投影把凭据和渠道动作标成平台管理员专属，而账号级恢复允许平台管理员或租户管理员：`backend/internal/provideraccountrecoveryhttp/handler.go:177-241`。

### 影响（`Inferred I-03`）

1. 租户管理员能导入和创建自己的账号，却不能轮换自己账号的凭据，也不能完成渠道级恢复。
2. 部署者在一部分入口被禁止代管租户账号，在另一部分入口却能查询或操作任意租户。
3. 同一个运维助手或前端会收到互相矛盾的 action 权限，无法形成直观闭环。
4. 这不是匿名越权，也不是 SQL 忘带租户，而是产品角色合同本身不一致。

### 修复方向与验收

建立账号生命周期唯一 capability 矩阵，至少区分：

- 平台自有账号
- 租户自有账号
- 账号配置
- 凭据秘密操作
- 健康与恢复操作
- 只读诊断

当前权威规则只明确三种身份、租户管理员的本租户边界，以及部署者不得越级调整租户最终用户；它没有完整定稿“部署者是否可代管租户级账号资源”。因此本报告只确认现行 resolver 互相矛盾，不替 Owner 选择最终矩阵。

实施前需要 Owner 在唯一 capability 矩阵中确认：部署者对租户账号是完全不可操作、只读诊断，还是经租户授权后可代管。无论选择哪一种，获得能力的租户管理员都必须能闭环维护自己的账号，最终用户仍无账号池管理权限。所有 resolver、动作投影和测试必须共用同一合同。

### S1-03：单账号变更与日志不是同一可靠操作

### 事实

直接创建已经把账号、凭据、健康和日志合入一个事务：`backend/internal/adminpoolhttp/handler.go:280-323`。

但以下单账号动作仍先提交业务变更，再单独写日志：

- 更新账号：`backend/internal/adminpoolhttp/handler.go:476-550`
- 启用或停用：`backend/internal/adminpoolhttp/state_handler.go:10-49`
- 软删除：`backend/internal/adminpoolhttp/state_handler.go:52-83`

若日志插入失败，业务状态已生效，但接口返回 503。调用方重试可能再次修改，日志仍可能缺失。

### 影响（`Inferred I-04`）

- “日志详细、分类明确、错误标清、保留 30 天”的全局合同被破坏。
- 运维看到 503 会以为操作未成功，实际账号可能已停用或删除。
- 自动化重试可能重复动作，故障复盘无法仅靠日志还原真实状态。

### 修复方向与验收

复用创建和批量更新的事务范式，把单账号更新、启停、删除与日志写入同一事务。用真实 PostgreSQL 触发日志写失败，断言账号状态整体回滚。

### S1-04：直接创建仍是较弱的平行会话导入通道

### 事实

直接创建会在同一事务中写账号、凭据、健康和日志，这部分已经修好：`backend/internal/adminpoolhttp/handler.go:204-349`。

它也会拒绝纯 OAuth-only、Setup Token 和封存模式。但 `DirectCredentialInputAllowed` 仍允许声明支持 JSON/CLI 导入的会话模式，例如 Claude Code、Codex CLI 和 Kimi OAuth：`backend/internal/credentialacq/types.go:521-535`。

这条路径只向凭据存储传入 `vendor`、`auth_mode` 和载荷，没有经过标准链的：

- 稳定外部身份库存匹配和多命中冲突
- 执行前项目补全
- 导入凭据过期刷新
- 激活后配额探测通知

对比标准执行：`backend/internal/gatewayhttp/accountintake/execute.go:114-176`、`289-298`；直接创建：`backend/internal/adminpoolhttp/handler.go:297-315`。

凭据存储仍可能从载荷识别套餐，因此不能说这条路径“完全没有套餐”；真实缺口是稳定身份、上游项目和激活联动不一致。

### 影响（`Inferred I-05`、`Inferred I-06`）

- 同一上游订阅号可用不同账号名重复直建。
- 重复账号进入池后会放大并发、配额和封控风险。
- 项目型账号可能在首次真实请求时才暴露缺少 project 的问题。
- 列表套餐和配额探测行为取决于从哪个入口创建，形成双真相。

### 修复方向与验收

直接创建只保留真正静态、无需上游身份的 Key/云配置；所有 session/OAuth 载荷统一进入标准接入计划。若为了兼容保留旧 API，也应在服务内部转调标准计划和执行，而不是维持第二套写入逻辑。

## 五、功能缺口

### S2-01：没有显式“重认证这个账号”

OAuth 启动输入只有租户、模式和账号默认配置，没有目标账号或目标凭据：`backend/internal/gatewayhttp/accountintake/oauth_service.go:18-30`。当前依靠 OAuth 完成后的稳定身份匹配决定更新哪个账号。

`Inferred I-07`：正常身份稳定时可原地更新，但身份缺失、上游主体变化或库存冲突时，运维无法明确表达“为账号 123 重新认证”。sub2api 提供显式 OAuth/Setup Token 重认证，并在成功后替换凭据：`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_handler.go:1335-1431`。

建议增加目标账号重认证合同，同时继续校验返回身份与目标账号一致，不能用“显式目标”绕过身份冲突。

### S2-02：有套餐等级，没有订阅起止和到期日

当前支持 OpenAI、Anthropic、Gemini、Antigravity、Grok、Kimi 等套餐规范标签：`backend/internal/subscriptionprofile/profile.go:242-338`。列表投影含套餐、来源、信任、状态和观测时间，但没有订阅开始、结束或到期字段：`backend/internal/db/admin/admin_provider_accounts.go:198-236`。

`Inferred I-08`：账号自身的 `expires_at` 由账号管理入口配置，而套餐投影未把它声明为供应商订阅证据，因此不能把该字段当作上游订阅到期。

sub2api 对 OpenAI 尝试提取计划和订阅到期，CLIProxyAPI 的 Codex 列表暴露计划及订阅起止：

- `Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/openai_oauth_service.go:257-295`
- `router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/handlers/management/auth_files.go:693-730`

建议把“上游明确给出的订阅起止”作为可空、带证据来源的观测字段；未知时必须显示未知，不能伪造到期日。

### S2-03：Day-2 能力存在，但颗粒度和批量面不足

当前已有：

- 单账号测试
- 上游模型发现与同步
- 清账号限流
- 完整账号恢复
- 渠道暂停、恢复、强制激活
- 凭据刷新 worker
- 5h/7d 配额窗口
- 按标签批量启停、优先级、静态权重

当前未形成正式账号管理入口的能力：

- 列表批量返回今日请求数、失败率、上游 P95
- 批量刷新凭据
- 批量清错误或限流
- 批量测试
- 批量改池或组内优先级
- 只清错误但不重置其他健康状态
- 重置某个上游配额观测
- 复制账号
- 定时测试面板

sub2api 的 Day-2 动作包含细粒度清理、恢复和逐项批量结果：

- `Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/admin_account.go:1172-1191`
- `Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/ratelimit_service.go:1733-1823`
- `Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_handler.go:1505-1958`

修复时不能把 HUAKAI 的 6 态健康、15 类信号和渐进恢复压扁成简单启停。正确方向是在现有状态机上增加明确、窄作用域的运维命令。

### S2-04：账号迁移包秘密导出没有后端二次认证

迁移包路由使用普通安全级 session write，账号接入 resolver 只校验平台管理员或获得能力的租户管理员：`backend/internal/gatewayhttp/accountintakehttp/handler.go:85-108`、`172-232`。

导出执行有计划哈希、口令、确认文本和日志，能防误操作与篡改，但没有 TOTP、Passkey 或近期高强度认证。`Inferred I-09`：当前主线明确把“后端不要求 step-up”写成既定合同，因此本项是已知安全差距，不是路由 bug。

sub2api 的账号导出可挂近期 TOTP 二次认证，但配置允许关闭：`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/server/routes/admin.go:381-383`；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/server/middleware/step_up.go:97-140`。new-api 对凭据明文查看使用最高管理权限和安全证明：`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:router/channel-router.go:19-79`。

建议后续把“导出含秘密”与普通账号操作分级。该项会触及核心鉴权体验，实施前应另立计划。

## 六、低风险结构问题

### S3-01：破坏性 `StagedStore.Claim` 已脱离生产主链

生产 OAuth 已使用 `LoadForExecution` 加事务内 `ClaimTx`。旧的 `StagedStore.Claim` 会先清掉加密内容再返回候选，当前只有数据库迁移测试调用，生产没有调用：`backend/internal/gatewayhttp/accountintake/staged_store.go:166-205`。

它不是当前运行时 bug，但保留两个语义相反的认领 API，后续容易被误用。确认迁移测试改用安全合同后可删除。

### S3-02：依赖和注释存在漂移

- `AdminPoolAccountDeps.ChannelHealth` 被装配但直接创建路径重新构造事务内服务，字段没有生产用途。
- 凭据轮换实际默认 90 天开启，配置加载注释已更新，但装配处和 `rotation.go` 仍写“默认关闭/opt-in”：`backend/cmd/gateway/wiring.go:679-705`、`1720-1723`；`backend/internal/credentialworker/rotation.go:66-72`。

这不会立即破坏运行，但会误导运维和后续维护者。

## 七、与三镜的真实差异

| 维度 | sub2api | CLIProxyAPI | new-api | HUAKAI 主线 |
| --- | --- | --- | --- | --- |
| 导入模型 | 通用导入直接创建，Codex 与 CRS 有专用更新匹配；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_data.go:228-485`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/crs_sync_service.go:359-452` | 文件/JSON 导入，运行时按稳定索引注册；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/handlers/management/auth_files.go:805-883`、`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:sdk/cliproxy/auth/types.go:289-401` | 单建、批建和渠道复制；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:controller/channel.go:608-709`、`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:controller/channel.go:1401-1455` | 统一计划/执行，按上游身份创建或更新 |
| 身份去重 | 专用 Codex/CRS 较强，通用导入较弱；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_codex_import.go:490-656` | 主要是文件路径或 Key 摘要，不是供应商身份；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:sdk/cliproxy/auth/types.go:289-401` | 主要围绕渠道和多 Key；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:model/channel.go:199-282` | 标准链最严，但直接创建仍是旁路 |
| OAuth | Claude Cookie、Setup Token、浏览器 OAuth和多供应商刷新；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/oauth_service.go:64-239`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/token_refresh_service.go:831-953` | 多供应商 OAuth 和设备码，落入文件运行态；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/handlers/management/auth_files.go:1971-2640` | 特定供应商 OAuth 自动刷新；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:service/codex_credential_refresh_task.go:19-146` | 数据库临时会话和事务完成更稳，但缺显式目标重认证 |
| 套餐 | OpenAI 计划/到期、Gemini tier 刷新；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/openai_oauth_service.go:257-295`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_handler.go:2658-2805` | Codex 计划和订阅起止；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/handlers/management/auth_files.go:693-730` | 账号运营以渠道余额和模型探测为主；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:controller/channel-billing.go:359-505`、`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:controller/channel_upstream_update.go:284-520` | 多供应商统一等级、证据和可信度，缺订阅起止 |
| Day-2 | 细粒度动作与批量丰富；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_handler.go:1505-1958` | 启停、状态、模型、配额和文件管理；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/server.go:918-1024` | 测试、余额、模型同步、自动禁用和恢复；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:controller/channel-test.go:907-1029`、`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:web/src/features/channels/components/data-table-row-actions.tsx:80-400` | 健康状态更细，但批量和窄作用域动作不足 |
| 原子性 | 主记录、组绑定和部分恢复动作有分步窗口；`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/admin_account.go:455-592`、`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/service/ratelimit_service.go:1733-1823` | 文件与运行态更新是分离阶段；`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/handlers/management/auth_files.go:805-883` | 渠道字段与路由索引存在分步窗口；`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:model/channel.go:532-578` | 标准导入、创建、批量较强，单账号更新、启停、删除仍分步 |

三镜并不都优于 HUAKAI：

- sub2api 通用导入逐项创建，不提供标准链同等级的稳定身份消歧：`Wei-Shaw/sub2api@6aeea70ee008825604ac3293ca0f216e951795d1:backend/internal/handler/admin/account_data.go:228-485`。
- CLIProxyAPI 的文件落盘与运行态注册是分离阶段，不能照搬成数据库平台事务合同：`router-for-me/CLIProxyAPI@f71ec0eb6776854457892452cf28c47f0d658251:internal/api/handlers/management/auth_files.go:805-883`。
- new-api 的渠道字段和路由索引更新有分步窗口，OAuth 自动刷新依赖主节点和进程内周期任务：`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:model/channel.go:532-578`、`QuantumNous/new-api@1721144221ec5c94dd87891a7ae1bee228e7bb63:service/codex_credential_refresh_task.go:19-146`。

HUAKAI 应补齐它们成熟的运维颗粒度，同时保留自己的身份冲突、事务和健康状态优势。

## 八、旧结论复核

| 旧声明 | 当前主线定性 |
| --- | --- |
| Claude scope 缺 `org:create_api_key` | 已过期；当前浏览器 OAuth 已包含该 scope |
| Claude token 端点仍是旧地址 | 已过期；当前使用 `platform.claude.com` |
| Cookie、CRS、迁移包均已完整闭环 | 部分属实；入口和实现存在，但 S1-01 使部分模式不可执行 |
| 同一上游 ID 会任取第一条 | 标准导入链已修复；多命中显式冲突 |
| 账号创建与凭据、日志不原子 | 直接创建和标准导入已修复；单账号更新、启停、删除仍不原子 |
| 套餐标签缺失 | 已修复；多供应商等级标签已进入统一投影 |
| 订阅到期日已闭环 | 不属实；当前没有统一订阅起止合同 |
| Day-2 运维完全缺失 | 误报；已有测试、模型同步、配额、恢复和部分批量，缺的是颗粒度与集中入口 |
| #293 的 Antigravity 身份、OAuth 临时凭据和注册真相问题仍阻断 | 已过期；#294 已进入当前主线，本报告不重复报旧问题 |

## 九、建议实施顺序

1. **先修 S1-01。** 一个根因影响三个正式入口，且当前测试无法证明可用。
2. **统一三身份 capability。** 先由 Owner 定稿部署者对租户账号的代管边界，再改 resolver、动作投影和测试，避免继续扩散。
3. **关闭单账号日志半事务。** 复用已有创建和批量事务范式。
4. **收口直接会话创建旁路。** 静态 Key 保留直建，session/OAuth 统一走接入主链。
5. **补显式重认证和订阅起止。**
6. **在现有健康状态机上扩 Day-2 颗粒度和批量动作。**
7. **最后做秘密导出 step-up、死 API 和注释清理。**

每批修复都应从最新 `origin/main` 起干净分支，只保留一个 PR；先跑针对性判别测试，再跑相关包和主线全量测试；合并必须等 Owner 明确同意。

## 十、验收清单

- [ ] Cookie 普通 OAuth 和 Setup Cookie 均能导入即创建或按稳定身份更新
- [ ] CRS 的 OAuth、Setup Token、API Key 矩阵均有真实服务级测试
- [ ] 迁移包对所有可导出模式可往返，通用 JSON 仍不能伪造 OAuth-only
- [ ] 部署者对租户账号的只读、代管和授权边界已由 Owner 定稿并统一接线
- [ ] 获得能力的租户管理员能维护自己账号的凭据和健康
- [ ] 最终用户仍不能进入账号池管理
- [ ] 单账号更新、启停、删除在日志失败时整体回滚
- [ ] 同一上游身份从任一入口都不能形成重复账号
- [ ] 重认证能明确目标账号，并校验上游身份一致
- [ ] 套餐等级、证据、观测时间和可得的订阅起止统一展示
- [ ] 批量运维返回逐项成功、失败、跳过和可重试信息
- [ ] 迁移包秘密导出达到 Owner 批准的二次认证等级

## 十一、真实性元数据

- HUAKAI 生产/测试区域实读：42
- 外部 clean-room 行为区域：34
- 合理推断：9（逐项如下）
- 开放问题：5
- 未恢复原始文档：8

开放问题：

1. 真实 Claude Cookie 上游交换成功后的活体结果尚未在本轮使用真实订阅号验证。
2. 各供应商是否稳定返回订阅起止日期，需要逐供应商活体证据，不能仅凭 token 字段推断。
3. 部署者对租户级账号资源是禁止代管、只读诊断，还是经租户授权后代管，需要 Owner 最终定稿。
4. 账号迁移包秘密导出的二次认证等级需在后续鉴权实施计划中定稿。
5. 直接创建旧 API 是否存在外部客户端依赖，需要在收口前从访问日志和 API 使用方确认。

合理推断与观测基础：

1. `Inferred I-01`：Cookie、CRS 和迁移包中的 OAuth-only/Setup Token 候选会被来源闸拒绝。基础是各入口传入的来源、候选模式，以及模式闸的允许来源判断：`backend/internal/gatewayhttp/accountintake/cookie_service.go:63-102`、`backend/internal/gatewayhttp/accountintake/crs_service.go:322-378`、`backend/internal/accountbundle/import.go:90-140`、`270-291`、`backend/internal/credentialacq/intake/plan.go:203-220`、`287-295`。
2. `Inferred I-02`：S1-01 会表现为入口可见、可信凭据已转换，但计划不可执行，而不是上游凭据本身失效。基础是入口已挂载、转换器能生成候选、随后来源闸拒绝：`backend/internal/gatewayhttp/accountintakehttp/handler.go:85-108`、`backend/internal/credentialacq/claudecookie/exchange.go:331-369`、`backend/internal/credentialacq/intake/plan.go:287-295`。
3. `Inferred I-03`：同一租户管理员能接入账号却无法完成凭据和渠道恢复闭环，部署者在不同入口也得到不一致权限。基础是接入、账号查询、凭据和渠道 resolver 的直接对照：`backend/internal/gatewayhttp/accountintakehttp/handler.go:186-230`、`backend/internal/adminpoolhttp/validation.go:38-57`、`backend/internal/gatewayhttp/admin_credentials_handler.go:323-353`、`backend/internal/channelhealthhttp/handler.go:158-224`。
4. `Inferred I-04`：日志写失败时，单账号状态已提交而接口返回失败，调用方重试可能造成重复动作和事实缺口。基础是业务写与日志写的执行顺序：`backend/internal/adminpoolhttp/handler.go:476-550`、`backend/internal/adminpoolhttp/state_handler.go:10-83`。
5. `Inferred I-05`：同一上游会话身份可借不同本地账号名从直接创建路径形成重复账号，并放大池内并发和配额风险。基础是直接创建允许会话模式却不执行标准链身份库存匹配：`backend/internal/credentialacq/types.go:521-535`、`backend/internal/adminpoolhttp/handler.go:297-315`、`backend/internal/gatewayhttp/accountintake/execute.go:114-176`。
6. `Inferred I-06`：项目补全、激活通知和首次配额观测会因创建入口不同而产生双口径。基础是标准链执行这些步骤，直接创建只完成凭据和健康初态：`backend/internal/gatewayhttp/accountintake/execute.go:114-176`、`289-298`、`backend/internal/adminpoolhttp/handler.go:297-323`。
7. `Inferred I-07`：身份缺失、上游主体变化或库存冲突时，现有 OAuth 输入无法表达“只重认证指定账号”。基础是 OAuth 输入没有目标账号字段，而账号归属由完成后的匹配结果决定：`backend/internal/gatewayhttp/accountintake/oauth_service.go:18-30`、`193-269`。
8. `Inferred I-08`：账号 `expires_at` 不能替代上游订阅到期。基础是套餐投影没有订阅起止字段，账号列表把 `expires_at` 作为独立账号字段：`backend/internal/subscriptionprofile/profile.go:59-73`、`backend/internal/db/admin/admin_provider_accounts.go:198-236`。
9. `Inferred I-09`：迁移包秘密导出低于独立二次认证保护等级，属于安全差距而非当前路由实现错误。基础是路由安全级、resolver 和既定导出合同均未要求 step-up：`backend/internal/gatewayhttp/accountintakehttp/handler.go:85-108`、`172-232`。

本报告的“真实问题”来自当前主线生产码和可复现测试链；外部项目部分来自独立 clean-room 源码核实；2026-07-22 原始长文不可恢复，因此只验证了保留下来的问题声明，没有伪造原文覆盖。

### Clean-room 前置 lane 溯源

| 项目 | Lane | Agent 与独立会话 | UTC | 源码读取范围 |
| --- | --- | --- | --- | --- |
| sub2api | `specifier` | OpenAI Codex `gpt-5.6-sol`，`019f8d13-68bd-70d0-8e8e-bee4934bde3f` | `2026-07-23T03:52:00Z` | 见下方完整清单 |
| CLIProxyAPI | `specifier` | OpenAI Codex `gpt-5.6-sol`，`019f8d13-ae00-7a23-8e66-6a779c266f2a` | `2026-07-23T03:53:42Z` | 见下方完整清单 |
| new-api | `specifier` | OpenAI Codex `gpt-5.6-sol`，`019f8d13-f640-7390-b099-114007c2cf3f` | `2026-07-23T03:49:19Z` | 见下方完整清单 |

sub2api source files read:

`LICENSE`; `backend/ent/schema/account.go`; `backend/ent/schema/account_group.go`; `backend/ent/schema/group.go`; `backend/internal/server/routes/admin.go`; `backend/internal/server/middleware/admin_auth.go`; `backend/internal/server/middleware/step_up.go`; `backend/internal/handler/admin/account_handler.go`; `backend/internal/handler/admin/account_data.go`; `backend/internal/handler/admin/account_codex_import.go`; `backend/internal/handler/admin/openai_oauth_handler.go`; `backend/internal/handler/dto/credentials_redact.go`; `backend/internal/service/admin_account.go`; `backend/internal/service/account_credentials_persistence.go`; `backend/internal/service/account_credentials_redact.go`; `backend/internal/service/account_expiry_service.go`; `backend/internal/service/account_test_service.go`; `backend/internal/service/account_usage_service.go`; `backend/internal/service/crs_sync_service.go`; `backend/internal/service/oauth_service.go`; `backend/internal/service/openai_oauth_service.go`; `backend/internal/service/token_refresh_service.go`; `backend/internal/service/ratelimit_service.go`; `backend/internal/service/upstream_models.go`; `backend/internal/service/openai_account_scheduler.go`; `backend/internal/service/gateway_scheduling.go`; `backend/internal/service/gemini_oauth_service.go`; `backend/internal/service/antigravity_oauth_service.go`; `backend/internal/service/grok_oauth_service.go`; `backend/internal/service/wire.go`; `backend/internal/repository/account_repo.go`; `backend/internal/repository/claude_oauth_service.go`; `backend/internal/pkg/oauth/oauth.go`; `backend/migrations/011_remove_duplicate_unique_indexes.sql`

CLIProxyAPI source files read:

`LICENSE`; `internal/api/server.go`; `internal/api/handlers/management/auth_files.go`; `internal/api/handlers/management/oauth_sessions.go`; `internal/api/handlers/management/oauth_callback.go`; `internal/api/handlers/management/vertex_import.go`; `internal/api/handlers/management/quota.go`; `internal/api/handlers/management/api_tools.go`; `internal/api/handlers/management/api_key_usage.go`; `internal/api/handlers/management/usage.go`; `internal/watcher/events.go`; `internal/watcher/clients.go`; `internal/watcher/dispatcher.go`; `internal/registry/model_updater.go`; `internal/runtime/executor/codex_executor.go`; `internal/runtime/executor/claude_executor.go`; `internal/runtime/executor/xai_executor.go`; `internal/runtime/executor/kimi_executor.go`; `internal/runtime/executor/antigravity_executor.go`; `sdk/auth/filestore.go`; `sdk/pluginhost/host.go`; `sdk/cliproxy/auth/types.go`; `sdk/cliproxy/auth/conductor.go`; `sdk/cliproxy/auth/antigravity_credits.go`; `sdk/cliproxy/service.go`; `sdk/cliproxy/builder.go`; `sdk/cliproxy/antigravity_models.go`

new-api source files read:

`LICENSE`; `NOTICE`; `main.go`; `router/channel-router.go`; `router/api-router.go`; `controller/audit.go`; `controller/channel.go`; `controller/channel_authz.go`; `controller/channel-billing.go`; `controller/channel-test.go`; `controller/channel_upstream_update.go`; `controller/model_sync.go`; `controller/relay.go`; `controller/system_task.go`; `controller/system_task_handlers.go`; `model/ability.go`; `model/channel.go`; `model/channel_cache.go`; `model/system_task.go`; `service/channel.go`; `service/channel_select.go`; `service/codex_channel_models.go`; `service/codex_credential_refresh.go`; `service/codex_credential_refresh_task.go`; `service/codex_oauth.go`; `service/system_task.go`; `relay/channel/codex/oauth_key.go`; `dto/channel_settings.go`; `web/src/features/channels/api.ts`; `web/src/features/channels/components/channels-dialogs.tsx`; `web/src/features/channels/components/channels-primary-buttons.tsx`; `web/src/features/channels/components/data-table-bulk-actions.tsx`; `web/src/features/channels/components/data-table-row-actions.tsx`; `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx`; `web/src/features/channels/components/numeric-spinner-input.tsx`; `web/src/features/channels/components/channels-columns.tsx`; `web/src/features/system-info/components/system-tasks-panel.tsx`

三个 specifier 的完整文件清单和中性行为合同保存在各自独立会话记录中；它们均明确未读取 HUAKAI。当前 reviewer 没有重新读取三镜源码，只按带 SHA 的证据锚点审查行为合同与 HUAKAI 真码之间的差距。

预提交只读复审：

- 第一轮：OpenAI Codex `gpt-5.6-sol`，会话 `019f8d26-ac47-7052-84f3-83b48b4e184e`，`sandbox=read-only`。提出两项 S1 文档问题：未定稿权限边界被误写成 Owner 合同、clean-room lane 溯源不足。
- 第二轮：OpenAI Codex `gpt-5.6-sol`，会话 `019f8d29-801e-7a41-b80e-3b4a3477d02b`，`sandbox=read-only`。提出一项 S1：三镜维护、Release 和 Issue 时效证据未进入报告；另提出一项 S2：唯一计划状态未同步。
- 第三轮：OpenAI Codex `gpt-5.6-sol`，会话 `019f8d2b-01ab-7090-8aa3-61ffc46f3b98`，`sandbox=read-only`。提出一项 S1：外部行为结论虽有尾部来源清单，但部分比较句和表格单元缺少紧邻的 `repo@sha:file:line` 锚点。
- 第四轮：OpenAI Codex `gpt-5.6-sol`，会话 `019f8d30-a778-7f02-9213-6eefda8a172e`，`sandbox=read-only`。提出一项 S1：真实性元数据统计了九项合理推断，但正文没有逐项标识及列出观测基础。
- 最终轮：OpenAI Codex `gpt-5.6-sol`，会话 `019f8d32-c060-7913-8a2f-d895e3e00d22`，`sandbox=read-only`。未发现剩余 S0/S1 或会误导后续实施的明确缺陷。

本版已完成上述修正。

Source files read: 三个独立 specifier 行为合同及其会话记录；`backend/cmd/gateway/routes.go`; `backend/cmd/gateway/wiring.go`; `backend/internal/credentialacq/**`; `backend/internal/credentialstore/**`; `backend/internal/gatewayhttp/accountintake/**`; `backend/internal/gatewayhttp/accountintakehttp/**`; `backend/internal/accountbundle/**`; `backend/internal/adminpoolhttp/**`; `backend/internal/adminhttp/provider_account_*`; `backend/internal/channelhealthhttp/**`; `backend/internal/provideraccountrecoveryhttp/**`; `backend/internal/subscriptionprofile/**`; `backend/internal/db/admin/admin_provider_accounts.go`

Lane: reviewer

Agent: GPT-5 Codex / 主会话 `019ef462-51f1-7da2-b94c-141e85ff0eb0`

UTC timestamp: 2026-07-23T04:19:39Z
