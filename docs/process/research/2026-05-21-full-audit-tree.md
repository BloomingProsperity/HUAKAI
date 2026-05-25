# HUAKAI 全面自查 —— 标满状态的功能树 + parity 缺失总表

> 2026-05-21。以 Owner 16-section 功能树为骨架,5 个 codex specifier lane 逐叶核实 HUAKAI 真实代码状态 + 交叉对比 sub2api / CLIProxyAPI / new-api。
> 详细逐叶证据见 5 份 lane 报告:`2026-05-21-audit-A..E.md`。本文件是汇总视图。
> 参照口径:`Wei-Shaw/sub2api@16793d3af0` / `router-for-me/CLIProxyAPI@21fad9db` / `QuantumNous/new-api@20d3e737`。

## 0. 总结论 —— 回答「是这样的状态树吗」

**结构对,状态不对。** 这 16 个 section 作为项目地图基本准确;但作为「状态树」会误导,因为它把所有节点画得像已完成的功能,**真实状态是「地基铺得很宽,闭环极少」**。

逐叶状态分布(约 130 个叶子):
- ✅ 完整(实现+接线+测试,生产可用):**约 11 个**(~8%)
- 🟡 部分(有实现但有缺口:未接线/缺测试/仅 happy path/仅单 vendor):**约 100 个**(~77%)
- 📋 仅 spec(只有文档,impl=0):**约 6 个**
- ❌ 缺失:**约 3 个**
- ⚠️ 名实不符(树这样写但项目实际不是):**约 8 个**

一句话:**HUAKAI 不是「做完了大部分」,是「几乎每个模块都开了头、极少数收了尾」。** 🟡 占 77%。

## 1. 标满状态的 16-section 树

```
HUAKAI
├─ §1 用户与权限 ........................ 🟡 整体(身份链强,组/多租户面弱)
│  ├─ 用户注册/登录 .................... ✅  缺 2FA/验证码/默认权益初始化(MED)
│  ├─ Session 会话 ..................... ✅  防重放+token hash,强于三参照(LOW)
│  ├─ API Key 管理 .................... ✅  缺用户自助+令牌级配额/IP/模型策略(MED)
│  ├─ 用户组/权限组 ................... 🟡  ❗未找到组实体/成员/CRUD(HIGH)
│  ├─ 管理员权限 ...................... ✅  平台/租户 operator + 审计(LOW)
│  └─ 多租户隔离 ...................... 🟡  底层 schema 强,但 pool 管理面硬编码默认租户(MED)
│
├─ §2 模型接入 ......................... 🟡 整体(主路径通,长尾薄)
│  ├─ OpenAI Chat/Responses ........... 🟡  主路可用,高级字段有损耗(MED)
│  ├─ OpenAI Codex ................... 🟡  ❗仅 session passthrough,非完整接入(HIGH)
│  ├─ Anthropic Messages .............. 🟡  洞③ 刚补非流式;跨协议保真待补(MED)
│  ├─ Gemini Messages/SSE ............. 🟡  ❗能读流但请求归一化/原生入口未闭环(HIGH)
│  ├─ Bedrock ......................... 🟡  typed adapter 较强,缺未知事件策略(LOW/MED)
│  ├─ OpenRouter ...................... 🟡  passthrough,缺 provider 语义(MED)
│  └─ Grok/DeepSeek/Mistral/GroqCloud/Together/Perplexity/Fireworks
│      .................................. 🟡  全是薄 OpenAI-compatible passthrough(LOW/MED)
│
├─ §3 账号与凭证 ....................... 🟡 整体(框架强,运营闭环弱)
│  ├─ Provider Account ................ ✅  字段全,缺批量导入/多 key(MED)
│  ├─ 凭证存储(7 类) ................ ✅  Credential V2 AES-GCM+AAD,强于三参照(LOW)
│  ├─ 凭证续期 ........................ 🟡  ❗storm 预算只有 account 级,OAuth adapter 不全(HIGH)
│  ├─ 凭证轮换 ........................ 🟡  单凭证安全轮换有,缺批量(MED)
│  ├─ 凭证健康状态 .................... 🟡  状态机有,缺主动巡检/批量(MED)
│  └─ 凭证获取流程 .................... 🟡  ❗通用表有,OAuth/批量导入运营闭环不足(HIGH)
│
├─ §4 账号池/资源池 .................... 🟡 整体(主链有骨架,绑定不足)
│  ├─ Pool 管理 ....................... 🟡  CRUD 有,管理面未全租户化(MED)
│  ├─ 账号/用户/API Key/模型 绑定 ..... 🟡  ❗绑定关系是权益+隔离+调度的核心缺口(HIGH)
│  ├─ 并发槽位 ........................ 🟡  原子获取测试通过,缺 orphan sweep/重试(MED)
│  ├─ 等待队列 ........................ 🟡  只「告知等待」返 429,非真排队(MED)
│  ├─ Sticky 会话 ..................... ✅  DB sticky+TTL(LOW)
│  └─ Account-to-API 主链 ............. ✅  入站→路由→claim→attempt→结算 全接线(LOW)
│
├─ §5 网关核心 ......................... 🟡 整体(三入口完整,vendor 覆盖未闭环)
│  ├─ /v1/chat,/v1/responses,/v1/messages  ✅  三入口完整(LOW)
│  ├─ 请求标准化 ...................... 🟡  ❗HCSF 方向对,Gemini 请求转换未实现(HIGH)
│  ├─ 响应标准化 ...................... 🟡  核心可用,高保真不足(MED)
│  ├─ 协议转换 ........................ 🟡  框架优于散装,Gemini/Responses 长尾未闭(MED/HIGH)
│  ├─ 模型别名 ........................ 🟡  基础 override 有,缺运维级 mapping(MED)
│  ├─ 路由选择 ........................ 🟡  RoutePlan 有,只提取 stream feature(MED)
│  ├─ 重试 ............................ 🟡  缺同账号短重试+内部 backoff(MED,见 W1 审计)
│  ├─ Failover ........................ 🟡  跨账号/池有,WaitPlan 直接返客户端(MED)
│  ├─ Timeout ......................... 🟡  基础有,缺细粒度配置(LOW/MED)
│  ├─ Streaming ....................... 🟡  框架较强,provider 长尾有坑(MED)
│  └─ Error Normalization ............. 🟡  类型化好,provider-specific 解析不足(MED)
│
├─ §6 Rust 高性能网关 .................. ⚠️ 名实不符 —— 真实代码但 NO-GO,未上线,非生产数据面
│  ├─ core_gateway 等 8 个子项 ........ 🟡  Rust crate 真实存在(listener/proxy/SSE/限流/redaction/mimicry)
│  │                                       但 READINESS.md 明确 NO-GO,生产入口是 Go(MED)
│  └─ Go 控制面 gRPC 交互 ............. ❌  Rust 侧定义了 proto,Go 侧无对应 gRPC server(HIGH/按新方向 MED)
│
├─ §7 路由与调度 ....................... 🟡 整体(底座有,策略未产品化)
│  ├─ Route Plan ...................... 🟡  plan artifact 可审计,动态策略少(MED)
│  ├─ Account Selection ............... 🟡  gate/sticky/rank/slot 链有(MED)
│  ├─ Weighted/Round Robin/Fill First . 🟡  可近似,缺 operator 可见的命名策略(LOW/MED)
│  ├─ Risk Pareto ..................... 🟡  PASR 评分有,不是清晰产品策略(MED)
│  ├─ Cooldown/Ban Signal ............. 🟡  状态机强,长尾 provider 分类有缺口(MED)
│  ├─ Retry Budget .................... 🟡  预算有,等待/冷却消化弱(LOW/MED)
│  ├─ Model Fallback .................. 🟡  基础 override 有,缺产品化 policy(MED)
│  └─ Route Cache/Token Pool .......... 🟡  有 PASR/L2/sticky,缺命名 route cache(LOW/MED)
│
├─ §8 用量与计费 ....................... 🟡 整体 —— ❗钱路径未闭环,本 section 最危险
│  ├─ Usage Record/Token 统计 ......... 🟡  ❗5m/1h cache TTL 进 canonical 却被结算器写 0(HIGH)
│  ├─ 模型价格表/Pricing Snapshot ..... 🟡  版本表有,默认价格空,细分 bucket 不足(MED)
│  ├─ 余额扣费/配额预留/结算/退款 ..... 🟡  ❗有 claim/settle/refund,但无真钱包余额扣减(HIGH)
│  └─ Voucher/充值订单/成本收据 ....... 🟡  ❗Voucher 可兑换;充值订单/支付回调缺失(HIGH)
│
├─ §9 审计与信任链 ..................... 🟡 整体(HUAKAI 差异化强项,但被 §8 拖累)
│  ├─ Audit Ledger/Merkle/Ed25519/PubKey  🟡  后端真实;Merkle 实为 hash-chain,非标准 inclusion proof(MED)
│  ├─ Receipt Verify/Refund/Key Rotation  🟡  ❗链路真存在,但 receipt 缺细分 token 且非强制(HIGH)
│  └─ 反掺水/用户可验证账单/Trust Chain  🟡  方向强于三参照,前端仍 mock(MED)
│
├─ §10 可观测与运维 .................... 🟡 整体(后端较强,运维 UI 弱)
│  ├─ Request Log/Attempt/Ops Trace ... 🟡  后端有,前端排障面弱(MED)
│  ├─ Channel Health/Error Trend ...... 🟡  自动健康处置成形,趋势聚合不足(MED)
│  ├─ DLQ/Async Worker/Alert .......... 🟡  DLQ 核心强,缺可配置外部告警(MED)
│  └─ Admin Retry/故障排查面板 ........ 🟡  后端动作在,缺操作员可用页面(MED)
│
├─ §11 安全与隐私 ...................... 🟡 整体(隐私强,SSRF/限流是硬缺口)
│  ├─ Secret Redaction/Body/Log Guard . ✅  body zeroize+sentinel 测试,强于三参照(LOW)
│  ├─ Rate Limit/Admin Audit/SSRF Guard 🟡  ❗SSRF Guard 生产链路缺失,通用限流未落地(HIGH)
│  └─ 跨租户隔离/敏感脱敏/Clean-room ... 🟡  隔离+规则强,低层 error string 泄露风险(MED)
│
├─ §12 反封禁/网络策略 ................. 🟡/📋 整体 —— 大量仅 spec,不能按已实现承诺
│  ├─ Proxy/出口 IP 池/IP 绑定 ........ 🟡  ❗Proxy 有基础,出口 IP 池/账号绑定仅 spec(HIGH)
│  ├─ TLS 指纹/HTTP2 指纹/Header 顺序 . 🟡  ❗Go uTLS 有真实现,H2/header 控制面不完整(HIGH)
│  └─ 设备指纹/请求节奏/风险探测 ...... 📋  ❗大多停在 spec,生产实现缺失(HIGH)
│
├─ §13 Juice ........................... ⚠️ 树标签是旧「降智检测」,应按「透明版」口径
│  ├─ 用户可见 请求→路由→上游 三段链 .. 🟡  ❗ModelChain/header/ledger 有,完整透明 UX 未成(HIGH)
│  ├─ 管理员改映射用户可见 ............ 🟡  账本能展示,缺变更公告链(MED)
│  ├─ ModelChain ...................... 🟡  类型/header/ledger/测试有,异常/fallback 路径未全覆盖(MED)
│  ├─ EmitModelMismatchLoss ........... ⚠️  ❗函数+测试存在,生产零调用 = 死代码(HIGH)
│  ├─ tokencheck 包 ................... ⚠️  CrossCheck/CacheVerify 存在,生产零调用(MED)
│  └─ Benchmark/能力探针/输出评分/降智趋势  ⚠️  旧方向,Owner 已改透明版,未实现(LOW)
│
├─ §14 社区/商业增长 ................... 🟡/📋 整体 —— 邀请码有头,其余多 spec
│  ├─ 邀请码 .......................... 🟡  生成/校验/幂等/注册 redeem 有,缺 UI/admin(MED)
│  ├─ 推荐码/Referral ................. 🟡  ❗表+spec 有,qualification 未接账单(HIGH)
│  ├─ Reward/返佣 ..................... 📋  ❗仅表+spec,无发放/冻结/退扣/提现(HIGH)
│  ├─ Tier ............................ 📋  仅表+spec,无 recompute/展示(MED)
│  ├─ 反作弊 .......................... 📋  ❗仅 spec,未接注册/账单/reward(HIGH)
│  └─ 活动审计 ........................ 📋  ❗spec 要求审计事件,未接 audit ledger(HIGH)
│
├─ §15 前端管理面板 .................... 🟡 整体(部分真接后端,Dashboard/Mimicry/绑定 仍假)
│  ├─ Dashboard ....................... ⚠️  ❗mock 数据,UI 自称 simulated(MED)
│  ├─ 用户面板 ........................ 🟡  chat/audit 真调用,缺 key/用量/钱包/推广页(MED)
│  ├─ 管理员面板 ...................... 🟡  accounts/pools/audit 真页,缺导航/settings(MED)
│  ├─ 账号管理 ........................ 🟡  真调 API,部分 501 缺口(MED)
│  ├─ 凭证续期 ........................ 🟡  只读续期状态,手动 renew 按钮 disabled(MED)
│  ├─ Pool 绑定 ....................... ⚠️  ❗pool CRUD 真,account 绑定是假 preview(HIGH)
│  ├─ 模型管理 ........................ 🟡  无独立页,分散在 chat/accounts/pools(MED)
│  ├─ 用量账单 ........................ 🟡  部分真读,Sidebar Usage 仍 disabled(MED)
│  ├─ 审计验证 ........................ 🟡  真调 verify/merkle,浏览器侧校验(偏强,LOW)
│  ├─ Mimicry 配置 .................... ⚠️  ❗纯 mock,无后端 endpoint(HIGH)
│  ├─ 可观测面板 ...................... 🟡  真轮询 debug vars/usage/claims,覆盖窄(MED)
│  └─ 系统设置 ........................ ❌  Sidebar 有但 disabled,无 /settings 页(MED)
│
└─ §16 文档/测试/发布 .................. 🟡 整体(文档强,测试弱)
   ├─ OpenAPI .......................... 🟡  存在,但有契约 drift(get/post 错位)(MED)
   ├─ Capability Contract .............. ✅  能力合同治理完整(LOW)
   ├─ Feature Parity Matrix ............ 🟡  矩阵在,多状态需实现证据更新(MED)
   ├─ Acceptance Test Matrix ........... 🟡  矩阵在,多条目仍 planned(MED)
   ├─ Clean-room Policy ................ ✅  治理完整(LOW)
   ├─ Go Tests ......................... 🟡  入口+大量测试文件在(MED)
   ├─ Rust Tests ....................... 🟡  曾通过,readiness 仍 NO-GO(MED)
   ├─ Frontend Type Check/Tests ........ 🟡  只有 type-check,无测试配置(MED)
   ├─ Security Audit ................... 🟡  有 schema review/deny,无 CI 安全链(MED)
   └─ Release Gates .................... ✅文档/🟡执行  多 mock/partial 叶子,gate 须阻止误标 released
```

## 2. 树需要修正的地方

### 2.1 名实不符(⚠️,树该改写)
- **§6 Rust** 不能与 §5 Go 网关并列成「两个生产网关」。Go `gatewayhttp` 是唯一生产数据面;Rust `core_gateway` 是探索性 fork(`READINESS.md` 自标 NO-GO)。树应标「Rust = 可选 outbound sidecar,未上线」。
- **§13 Juice** 子项是旧「降智检测」框架(Benchmark/探针/评分/降智趋势)。Owner 已改透明版。子树应重写为「模型路由透明链」。
- **§15 Dashboard / Mimicry 配置 / Pool 绑定** 在树里像功能页,实际是 mock / 假数据 / 无后端。
- **§13 EmitModelMismatchLoss / tokencheck** 树/代码里存在,但生产零调用,是死代码。

### 2.2 树漏列的 HUAKAI 真实模块(项目有、树没画)
- **HCSF canonical 中间层** —— 协议转换的核心,OpenAI/Anthropic/Responses/Gemini/Bedrock 互转都过它。这是 HUAKAI 相对三参照的架构优势,树却没画。
- **L2 响应缓存**(非流式)+ **幂等重放 / idempotency replay** —— 影响钱路径与重复请求。
- **PASR cache-locality routing**(segment table + cache feedback)。
- **channel health cooldown / ramp 状态机**。
- **billing claims DLQ / outbox / savepoint**、**CompletionBus 直接完成 fallback**、**流式中断 input-only 计费策略**。
- **Credential V2 审计**、**admin key 发行审计**、**body zeroize / sentinel 防泄露测试**、**stream 审计 + bounded drain**。

## 3. HIGH 级缺失/缺陷总表(按 section)

| # | section | 缺口 | sub2api | CLIProxyAPI | new-api | 补救估时 |
|---|---|---|---|---|---|---|
| 1 | §8 | Anthropic 5m/1h cache TTL 进 canonical 却被结算器写 0(碰计费) | 用量日志保留细分 | 仅外部 usage 统计 | tiered 结算含细分 | 1-2 天 |
| 2 | §8 | 图片输出 + 细分成本落账丢失 | 有图片计量 | 无 | 有图片计价 | 2-4 天 |
| 3 | §8 | 无真钱包余额扣减 + 多维 quota reserve(只有 claim) | 事务内扣余额/额度 | 无 | 预扣/补扣/退款完整 | 4-7 天 |
| 4 | §8 | 充值订单 / 支付回调缺失(只有 voucher) | 支付订单+回调+对账 | 无 | 充值订单+回调完整 | 5-8 天 |
| 5 | §9 | Receipt 非强制结算产物 + 缺细分 token | 无签名 receipt | 无 | 无签名 receipt | 2-4 天 |
| 6 | §1/§4 | 用户组/权限组缺实体+成员+CRUD;账号/key/模型→pool 绑定不完整 | 显式组+丰富策略 | 配置型 | 用户组+令牌组 | 5-8 天 |
| 7 | §3 | 凭证续期 provider/global storm 预算未实现 + 部分 OAuth adapter 未闭环 | 统一续期+多 refresher | 自动刷新循环 | Codex 刷新任务 | 3-5 天 |
| 8 | §3 | OAuth / 批量导入凭证获取运营闭环弱 | Codex 批量导入 | 多 provider OAuth | Codex OAuth+自定义 | 3-5 天 |
| 9 | §2 | OpenAI Codex 仅 session passthrough,非完整接入 | Codex 导入+变换 | 专门 Codex executor | Codex 渠道 | 4-7 天 |
| 10 | §2/§5 | Gemini 请求归一化 / 原生入口未闭环 | Gemini 一等平台 | Gemini executor | Gemini native route | 3-5 天 |
| 11 | §11 | SSRF Guard 生产链路缺失 + 通用 API 限流未落地 | 有限流 | 有 SSRF/限流 | 有 SSRF/限流 | 3-5 天 |
| 12 | §12 | 出口 IP 池 / 账号稳定 IP 绑定仅 spec | 部分代理 | 代理工具 | 代理 | 5-10 天 |
| 13 | §12 | TLS/H2/header 指纹控制面不完整(Rust 能力≠上线) | 无 | uTLS 库 | 无 | 依赖 Rust 上线 |
| 14 | §12 | 设备指纹/请求节奏/主动风险探测大多仅 spec | 无 | 无 | 无 | 大工程 |
| 15 | §13 | EmitModelMismatchLoss 死代码未接生产 | 无 | 无 | 无 | 1-2 天 |
| 16 | §14 | Referral/Reward/Tier/反作弊/活动审计 大多 spec/schema | 完整推广闭环 | 无 | 完整邀请/兑换/推广 | 7-12 天 |
| 17 | §15 | Pool 绑定 + Mimicry 配置 前端假数据/无后端 | 完整管理面 | 生态工具 | 成熟商业前端 | 3-6 天 |

外加一个洞③ review 发现、非本审计范围的系统性 bug:**`proto/tool_call_id.go` isHexID 假设错** —— 真实 tool ID 是 base62 非 hex,多轮 tool 调用会断,跨 4 协议、跨流式/非流式。HIGH。

## 4. HUAKAI 反而比三参照强的地方(没退化、是真壁垒)

- **可验证审计信任链** —— Audit Ledger + Ed25519 签名 + 公钥 registry + receipt mismatch 自动退款。三参照都只有内部审计日志,没有用户可独立验证的签名账本。
- **HCSF canonical 中间层** —— 协议转换集中可审计,而非散在各 provider adapter。
- **幂等 claim / replay** —— 钱路径防重扣,结构强于三参照。
- **Session 防重放 + Credential V2 AES-GCM/AAD 加密 + 租户级 schema 隔离**。
- **隐私最小化** —— body zeroize、sentinel 防泄露测试、3 通道日志分离。
- **transport/TLS 错误 taxonomy + StreamEndClass + 交付后禁重试**。
- **PASR cache-locality routing**、**DLQ operator replay**、**clean-room 治理 + capability contract**。

结论:HUAKAI 的「信任/透明/隐私/可审计」这条差异化主线是真的、强于三参照;弱的是「钱闭环、运营面、前端、增长、反封禁实现」这些「收尾」工作。

## 5. 给 Owner 的建议(优先级)

1. **钱路径必须先闭环**(§8 #1-5)—— 现在 claim/settle/refund 是骨架,没真钱包扣费、没充值、cache TTL 落账归零。商业上线前这是头号阻塞。其中 #1(cache TTL)碰计费 ledger,需 Owner 确认。
2. **用户组 + 绑定关系**(#6)—— 是权益/隔离/调度/计费四件事的共同地基,缺它很多 section 无法真正闭环。
3. **§13 EmitModelMismatchLoss 接线**(#15)—— 小活、1-2 天,直接点亮 HUAKAI 差异化主线(juice 透明版)。
4. **tool-id systemic bug** —— 小活,但接真上游做多轮工具调用会断,该早修。
5. §2 Codex/Gemini、§11 SSRF/限流、§12 反封禁、§14 增长 —— 按商业路线排期。
6. **前端**(§15)—— Owner 此前已决定搁置到 Rust 之后;Dashboard/Mimicry/绑定 的假数据违反「不做假」原则,解冻时优先清。

Source: 5 lane 报告 `2026-05-21-audit-A..E.md`(各含逐叶 file:line 证据 + 三参照 `@sha` 引用)。
