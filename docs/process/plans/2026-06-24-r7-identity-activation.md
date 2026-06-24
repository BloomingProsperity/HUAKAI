# R7 身份改写激活闭环 计划（2026-06-24）

## 0. 一句话

PR#115 已把 `mimicryidentity` 子包接进 dispatch 路径,但接线点传给它的 `ExternalAccountID`
**恒为空**(`chat_completions_stream.go:139` 硬编 `""`),致**双重 inert**:即便 operator 开
`HUAKAI_MIMICRY_IDENTITY_REWRITE=true` + 设 `HUAKAI_MIMICRY_IDENTITY_SECRET`,也因 external account
id 为空恒 fail-open 不改写。本切片把 credentialstore 里已存的上游账号 UUID(`account_credentials.
external_account_id`,迁移 0141 列)穿到 `provider.AccountInfo`,再喂进 dispatch 调用点,**让 operator
显式启用后 metadata.user_id 真被改写成含该上游 UUID 的派生身份**。

## 0.1 追加(2026-06-24 同切片):补全 HCSF canonical 路覆盖 = 三路径闭环

**对抗审查抓出的 S2(已亲核)**:上面接线只覆盖了【流式路】(`chat_completions_stream.go:171`)与
【legacy raw 缓冲路】(`chat_completions_handler.go:740`,仅 HCSF 关时走)。但 `hcsfDispatchEnabled()`
**默认开**(`HUAKAI_DISPATCH_HCSF!="0"`),非流式请求默认走 `dispatchCanonicalBuffered`
(`chat_completions_dispatch.go:727`)→ `DispatchHCSF`。该路上游真实 body 由
`MarshalToProviderRequest` 从 canonical 结构【重新 marshal】出来:亲核证实
`RequestToCanonical`(anthropic,`anthropic_messages_request.go:329-330`)把客户端 `metadata`
**整段丢弃**(仅记 `d1_metadata_not_yet_implemented` loss),`marshalAnthropicMessages`
(`hcsf_graph_marshal.go:108-199`)又**根本不产 metadata 字段**,`mergeHCSFRawPassthroughFields`
(`upstream_dispatcher_hcsf.go:373`)只对 `openai_chat` ingress 合并 raw 字段、对 anthropic 不合并。

**结论**:在 dispatch 入口对 `ex.body` 做 identityRewrite【流不过去】(canonical 往返把 metadata 丢了)。
故本切片把改写【施加在 marshal 出的最终上游 body 上】:`HCSFDispatchInput` 新增
`IdentityRewrite func([]byte) []byte` 钩子(实参 = `ex.identityRewrite`,与流式/raw 同一来源,
保改写逻辑单一来源),`buildHCSFProviderRequest` 在 native-raw 子路与 canonical-marshal 子路的
`in.InboundBody = body` 之前各施加一次 `applyIdentityRewrite(body, identityRewrite)`。anthropic 的
marshal 产物无 metadata,`RewriteMetadataUserID` 在 `MetadataInjectRewrite` 模式下走 fallback
**注入**(reason `injected`),补出含池账号身份的 `metadata.user_id`。

**至此三路径全覆盖:流式 + legacy raw + HCSF canonical**(此前文档"两路径"表述不准,已更正)。

安全:钩子默认关时 = `ex.identityRewrite` 的 no-op(返回入参拷贝)→ HCSF 路上游 body **字节等价**
(anthropic 仍无 metadata),与不接线时一致;external id / secret 空 → fail-open 不注入。CCH 缓存键
在 dispatch 前用 `ex.body` 算,本钩子只作用于 dispatcher 内已 marshal 的【dispatch 专用 body】,
**不碰 ex.body / 不污染缓存键**。未翻全局默认,仍 operator opt-in。

本追加动的生产文件:`backend/internal/gateway/upstream_dispatcher_hcsf.go`(新增钩子字段 +
`applyIdentityRewrite` + 两子路施加点 + 导出 `HCSFDispatchInputFromContext` 供接线断言)、
`backend/internal/gatewayhttp/chat_completions_dispatch.go`(`HCSFDispatchInput` 加
`IdentityRewrite: ex.identityRewrite`)。

## 1. 范围(只动这 4 个生产文件 + 测试)

| 文件 | 改动 | 性质 |
|---|---|---|
| `backend/internal/provider/adapter.go` | `AccountInfo` 增 `ExternalAccountID string` 字段 | 加性,非破坏 |
| `backend/internal/credentialstore/postgres_store.go` | `CredentialRecord` 增 `ExternalAccountID *string`;`resolveActiveQuery` 增选 `ac.external_account_id`(贯穿 5 处 CTE + 2 处 final SELECT + no_serving 占位 NULL);`ResolveActive` Scan 增该列 | 加性查询列,**不改 schema**(列已由迁移 0141 存在) |
| `backend/internal/provider/postgres_vault.go` | `resolveFromStore` 把 `rec.ExternalAccountID` 填进 `AccountInfo.ExternalAccountID` | 接线 |
| `backend/internal/gatewayhttp/chat_completions_stream.go` | `identityRewrite` 把实参 `""` 换成 `ex.accInfo.ExternalAccountID` | 点亮闭环(命门) |

**不碰 schema**(0141 列已在)、**不碰 auth/钱/quota**、**不翻全局默认开关**。

## 2. 真实数据路径(file:line,亲核)

上游账号 UUID 的产生与落库 → 解析回 dispatch:

1. **产生/提取**:`credentialacq/accountident_wire.go:37`(写 `candidate.ExternalAccountID`)→
   `credentialacq/finalizer.go:66`(投影进创建输入)→ `credentialacq/types.go:172`(字段定义)。
2. **落库**:`credentialstore/postgres_store.go:306` INSERT 写 `external_account_id` 列(迁移 0141:
   `account_credentials.external_account_id text`,与凭据行 1:1 同生命周期)。
3. **解析回出站**(命门链):
   - `gatewayhttp/chat_completions_dispatch.go:665` `CredentialVault.Resolve(...)` 返回
     `(Credential, AccountInfo, error)`;
   - 生产实现 `provider/postgres_vault.go:136` `resolveFromStore` → `credentialstore.Store.ResolveActive`
     (`postgres_store.go:696`)取出凭据行,但**当前 `CredentialRecord` 无 `ExternalAccountID` 字段**
     (`postgres_store.go:82` 结构体 + `:613` `resolveActiveQuery` 均不含该列)→ AccountInfo 里也没有;
   - `chat_completions_dispatch.go:679` `ex.accInfo = accInfo`;
   - dispatch 时 `chat_completions_stream.go:132` `identityRewrite()` 调
     `mimicryidentity.RewriteForDispatch(dispatchBody, ex.accInfo.AccountID, "", ...)` —— **第 3 个实参
     硬编 `""`(`:139`)= 当前 inert 命门**;两条非流式 dispatch 路径(缓冲路
     `chat_completions_handler.go:740` + 流式路 `chat_completions_stream.go:165`)共用此方法。

**结论**:数据已落库、`AccountInfo` 已是账号选定后的统一摘要,缺的只是把 `external_account_id` 从
`ResolveActive` 取出 → 经 `AccountInfo` → 喂进 dispatch 调用点。穿这一条线即闭环。

`AccountIdentity.ExternalAccountID` 定义:`mimicryidentity/identity.go:58`;dispatch 入口
`RewriteForDispatch`:`mimicryidentity/dispatch_wiring.go:46`;serverSecret 来源 env
`HUAKAI_MIMICRY_IDENTITY_SECRET`:`dispatch_wiring.go:17`(PR#115 已接好,复用)。

## 3. 三镜研究(§16,仅描述机制,标识符不照搬)

- **sub2api(唯一有等价者,默认 tiebreaker)** @e34ad2b1:生产链
  `gateway_service.go:1304-1305` 与 `:1425-1426` 从池账号 Extra 取 `account_uuid`,经
  `metadata_userid.go:74` 的格式化函数投影进 metadata.user_id;`account_uuid==""` 时 account 组件为空
  (= fail-open,不投影到真实账号,`metadata_userid_test.go:34` 坐实空串路径)。**HUAKAI 取其机制**:
  从池账号侧拿稳定上游标识投影进 user_id + 空标识 fail-open。**HUAKAI delta**:HUAKAI fail-open 更严
  (external id 空时**整条改写跳过、零字节变更**,而非投影成空 account 组件);且 device/session 用
  `SHA256(serverSecret::accountID::scope)` 确定性派生免存储(架构升级:无状态派生)。
- **new-api** @1ac0f580:`channel_affinity_setting.go:104` 的 `metadata.user_id` 仅是 gjson 路径,用于
  channel-affinity 路由**配置**(vendor DTO 字段),**不改写转发 body**。→ **no equivalent**。
- **CLIProxyAPI** @2a050dc9:`sdk/cliproxy/auth/selector.go:472-637` 仅**读** incoming body 的
  `metadata.user_id`(解析 session_id/account_uuid)做账号亲和选择,**不注入/改写池账号身份**。→
  **no equivalent**。

仅 sub2api 把池账号上游身份投影进转发 body;与 PR#115 已记录的镜像分析一致。

## 4. 闭环定义(成功判据)

operator 设 `HUAKAI_MIMICRY_IDENTITY_REWRITE=true` + `HUAKAI_MIMICRY_IDENTITY_SECRET=<固定值>`,
且选定账号的 credential 带 `external_account_id`(如 `acc-xyz`)→ 经 dispatch 后 metadata.user_id
**真被改写**成含基于 `acc-xyz` 的派生身份(account 组件 = `acc-xyz`,device/session 确定性派生),
≠ 原 user_id。把穿线改回喂空(重现原 inert)→ 退回 fail-open user_id 不变 → 守卫测试变红。

## 5. 安全(架构决策,严格遵守)

- **全局默认保持关**(operator opt-in):`HUAKAI_MIMICRY_IDENTITY_REWRITE` 默认仍关。metadata 改写
  未对真实上游验证过,强制全量改写有风险;operator 显式设两个 env 才启用 = 安全激活。**本切片不翻
  全局默认**(默认开属 Owner-gated 二阶段)。
- **fail-open 保持**:账号无 `external_account_id` / secret 空 → 不改写、不阻断(镜像 sub2
  `account_uuid==""`)。穿线只是"让非空时真能用上",空时行为不变。
- **只动 metadata 子树**:复用 PR#115 的 `gateway.ApplyMimicryPlan`(仅 step5),保 CCH 缓存签名不变
  (dispatch 专用 body 拷贝,绝不碰参与缓存键的原始 body)。
- **不碰 schema**:0141 列已存在,只新增**读**列;`CredentialRecord` 新字段为 nullable `*string`。
- **DR-001 不受影响**:`external_account_id` 随凭据行同 tenant 解析,沿用 `ResolveActive` 既有
  `tenant_id=$2` 双侧绑死,无跨租户泄露。

## 6. blast radius

- `AccountInfo` 加字段:零值安全(空串),所有现有构造点不填即空 = fail-open,无回归。
- `resolveActiveQuery` 加列:**结构敏感**,需在 5 处 CTE 列清单 + 2 处 final SELECT + no_serving 占位
  NULL 全部对齐,Scan 顺序对应。错配会编译/运行期报错(可被测试/build 捕获),非静默亏损。
- `chat_completions_stream.go:139` 改实参:点亮闭环,默认关时仍零字节变更(开关默认关短路在
  `RewriteInboundBody` 最前)。
- countTokens 路径(`geminihttp/generate_content.go:292`)也走同一 `Resolve`,会顺带拿到
  `ExternalAccountID` 但它不做身份改写 → 无影响。
- codebudget:`credentialstore/postgres_store.go`(grandfather 1377)与
  `chat_completions_stream.go`(824)均已超预算,改动控制在极小行数内(5% 增长允许)。

## 7. 测试(变异证伪)

- **A 闭环点亮**(`mimicryidentity` 包,纯函数):开关开 + secret 设 + `ExternalAccountID="acc-xyz"`
  → 改写后 user_id 含基于 acc-xyz 的派生身份(≠ 原值,且 account 组件 == acc-xyz)。**变异**:接线点
  喂空 external id → 退回 fail-open user_id 不变 → 红。(直击双重 inert 原状)
- **B 默认关零变更**:PR#115 已有 `TestRewriteForDispatch_默认关字节等价`,确认仍绿。
- **C fail-open**:开关开但 external id 空 → 不改写。变异:空也强行改写 → 红。
- **D ExternalAccountID 真穿到 AccountInfo**:用 fake/真 store 验账号解析后
  `AccountInfo.ExternalAccountID == credential 的值`。变异:穿线丢字段 → 红。fake 必须能让"丢字段"变红。
- 读 DB 的测试走 `integration_pg`(本地 huakai 库,0141 已 apply);无则 fake store 验穿线。
  读运行时 env/文件的测试 `-count=1`。

## 7.1 HCSF 三路覆盖追加测试(同切片,变异证伪)

落在 `backend/internal/gateway/upstream_dispatcher_hcsf_identity_test.go`(gateway 包,直驱
`buildHCSFProviderRequest` = marshal + 钩子真实接线点,`stubAdapter.lastInput.InboundBody` 即
发往上游的真实 body)与 `backend/internal/gatewayhttp/`(端到端驱 `dispatchCanonicalBuffered`):

- **HCSF-A 覆盖点亮**:anthropic canonical 路 + 改写钩子 → 上游 body 出现含池账号 UUID 的
  `metadata.user_id`。**变异**:删 canonical-marshal 子路的 `applyIdentityRewrite` 调用(重现 S2 漏覆盖)
  → 上游 body 退回无 metadata 的 marshal 原貌 → 红(已实测 RED)。
- **HCSF-B 默认关零变更**:钩子 nil(= R7 默认关时 `ex.identityRewrite` 的 no-op)→ 上游 body 与
  不接钩子字节等价、绝无 metadata 字段。**变异**:默认关也强行注入 metadata → 红(已实测 RED)。
- **HCSF-C fail-open**:钩子拿空 external id → 不注入 metadata、与默认关等价。**变异**:空也强行
  marshal 注入 → 红(已实测 RED)。
- **HCSF 接线证据(端到端)**:`TestChatCompletions_HCSF路真接R7改写钩子`——R7 开 + 账号带
  `ExternalAccountID` 时,经真实 `dispatchCanonicalBuffered` 后,自定义 dispatcher 从 ctx 取回的
  `HCSFDispatchInput.IdentityRewrite` 非 nil,且对无 metadata 的 anthropic body 施加后 account 组件
  == 上游 id。**变异**:删 `chat_completions_dispatch.go` 的 `IdentityRewrite: ex.identityRewrite`
  接线行 → 钩子 nil → 红(已实测 RED,精确锚住 S2 漏接线)。
- **三路一致(gatewayhttp)**:`TestIdentityRewrite_HCSF三路一致_marshal后body被注入身份`——同一个
  `ex.identityRewrite` 闭环对【无 metadata 的 HCSF marshal 产物】走 inject、对【自带 metadata 的客户端
  body(流式/raw 形态)】走 rewrite,两形态最终 account 组件都落到同一上游 id。

## 8. 工程流程门

worktree `/home/ubuntu/wt-r7-activation`(off origin/feat/frontend-portal)→ 协调锁已认领 →
build/vet/受影响包 `-count=1`/codebudget 门 → 干净基线全绿 → commit(中文正文,标注"默认仍关
operator opt-in、未翻全局默认")→ 不 push、不开 PR,留验证给 Owner → 释放锁。
