# 2026-05-22 全仓深度审计 — 总清单 + 补救波计划

> 状态:**待 Owner 拍板**。本文是 4 区深度审计的合成总清单与补救波计划。
> 执行前还需 codex 交叉评审(`feedback_plans_codex_cross_review`)。
> Owner 决策:"先深挖全仓再一次补"(2026-05-22)。

## §0 来源与口径

| 区 | 范围 | 审计方 | 文档 | findings |
|---|---|---|---|---|
| A | `backend/internal/gatewayhttp/`(HTTP 热路径) | Claude 亲审 | `research/2026-05-22-deep-audit-gatewayhttp.md` | GW-01..GW-10(10) |
| B | `billing/` `proto/` `eventbus/` `auditledger/` | codex specifier | `research/2026-05-22-deep-audit-billing-proto.md` | B-01..B-15(15) |
| C | `router/` `pool/` `gateway/` `auth/` `credentialstore/` `channelhealth/` `registry/` | codex specifier | `research/2026-05-22-deep-audit-routing-auth.md` | C-01..C-18(18) |
| D | `exploratory/rust-core-gateway/merged/` | codex specifier | `research/2026-05-22-deep-audit-rust.md` | D-01..D-10(10) |
| O | 首轮 codex 审计未被 A 区复核的 3 条 | — | (Owner 首轮清单) | O-1..O-3(3) |

**总计 56 条 distinct finding** —— HIGH 30 / MED 24 / LOW 2。

clean-room:全部 HUAKAI 内部代码,无参考项目源码读取。

### 关键分层判断 — Go 生产链路 vs Rust 探索路径

`project_two_data_planes`(2026-05-21):**Go `gatewayhttp` 是唯一生产链路**(~13/15 步);Rust `core_gateway` 控制面未接通生产(exploratory)。

→ **A/B/C 区(45 条)= Go 生产链路,正在线上跑,补救优先。**
→ **D 区(10 条)+ O-2(11 条)= Rust 探索路径,不在生产链路,补救延后到 Rust 上生产前批量做。**

---

## §1 主题分组(56 条 → 8 大主题)

每条只给一行摘要,完整证据/失败场景/修复方向见对应 zone 文档。

### T1 信任链脱链 / 审计静默跳过 —— 触碰 HUAKAI 核心卖点"商家不能做假"

| ID | 严重度 | 一行摘要 |
|---|---|---|
| GW-03 | HIGH | 真实流量硬编码 `EvidenceMock`,信任链语义失真(每请求都错) |
| GW-07 | MED | `submitAuditLedgerEntry` 在 signer/ledger 为 nil 时静默跳过,无账本却照样计费 |
| GW-08 | MED | 用量可信度硬编码 `confidence:=1.0`,估算/mock 用量也标满分 |
| B-02 | HIGH | 金钱事件允许 NULL/伪造 audit request id,refund 缺失时合成 `audit-refund-<id>`,billing 与 ledger 可脱链 |
| B-12 | HIGH | `RequestCompletionEvent` 校验只要 ID+TenantID;audit logger 默认不强制 ledger ref → trust-chain bypass |
| B-13 | MED | audit ledger sanitizer error 被忽略,redaction 失败仍签名存储未净化 payload(append-only 无法删) |
| C-03 | HIGH | Antigravity refresh 成功后 `_ = writeAudit(...)`,audit writer 未注入时 token 已轮换却无审计 |
| C-04 | HIGH | 凭据 create/rotate/disable/delete 审计 `_ = InsertAuditEvent(...)`,DB 失败仍返回成功 |
| C-10 | MED | channel health audit 在 signer==nil 时直接返回 nil,通道禁用等变更无签名 trust ledger |
| C-13 | HIGH | streaming trust ledger 在首字节时签名,把首 token 时间当 response 完成时间,审计窗口失真 |
| C-14 | HIGH | streaming ledger 缺失/append 失败只 warning 不阻断,生产 DI 漏配则无 signed entry |

### T2 计费正确性 / 丢钱错账

| ID | 严重度 | 一行摘要 |
|---|---|---|
| B-01 | HIGH | `UserID` 不进 billing 幂等指纹,同一 API key 下用户 B 复用用户 A 的 logical_request_id → claim 折叠/跨用户重放 |
| B-03 | HIGH | slot release miss 触发 deferred rollback,回滚已写入的 billing event + usage record → 上游已消耗却丢账 |
| B-04 | HIGH | token 计数直接 `int32(...)`,超 MaxInt32 静默 wrap、负数照写 → 报表/quota/对账损坏 |
| B-05 | MED | refund 幂等 replay 返回请求金额而非 DB 实际封顶后金额 → 对账系统看到错误退款额 |
| C-07 | HIGH | PASR actual 在 slot manager nil/unavailable 时走 token-only,写 claim 返回成功但无 slot 行 → 绕并发 cap |
| C-11 | HIGH | graceful stream 有输出但无 usage frame 时以 zero-token `reported` 完成 → 按 token 计费漏收 |

### T3 信息泄露(对外响应 / header / 日志)

| ID | 严重度 | 一行摘要 |
|---|---|---|
| GW-02 | HIGH | 上游错误 body(256 字节)经 `err.Error()` 透传客户端 `ClientMessage` |
| GW-04 | MED | `err.Error()` 直写客户端 JSON 响应(registry/router/pricing/DB 错误串,范围广) |
| GW-05 | MED | `err.Error()` 直写 HTTP header(`X-Huakai-Abort-Failed` 等) |
| GW-06 | MED | SSE 错误 header 在流开始后才 Set,客户端收不到 |
| C-02 | HIGH | OAuth 错误脱敏 `labeledSecret` 漏掉 `client_secret`,上游回显则写进 audit/log |
| C-18 | MED | streaming `event:error` payload(如 Bedrock exception)原样透传给 client |
| B-11 | MED | eventbus 把 raw handler error(含 SQL/表/列名)写入 state+DLQ,且忽略 DLQ enqueue 失败 |

### T4 协议正确性(跨协议污染 / 流式 tool-use)

| ID | 严重度 | 一行摘要 |
|---|---|---|
| GW-01 | HIGH | L2 缓存 key 缺 endpoint family / client protocol,三端点同 key → OpenAI 响应可能回给 Anthropic 客户端 |
| B-06 | HIGH | canonical streaming tool delta 类型不一致(`tool_input_delta` vs `input_json_delta`)→ 跨协议流式 tool args 丢弃 |
| B-07 | HIGH | OpenAI Chat renderer 用 block index 当 tool slot + 空参数写 `{}` → 参数错绑/污染 |
| B-08 | HIGH | tool call id 翻译只接受 hex 后缀,合法 opaque id 丢失(= 既知 `project_tool_call_id_hex_bug`) |
| B-09 | HIGH | OpenAI Responses 带 `previous_response_id` 时合法 tool output-only 请求被拒 |
| B-10 | HIGH | OpenAI Responses streaming tool-use 仍是 pending stub,生产流式工具调用静默降级 loss |
| C-12 | MED | SSE scanner 把所有底层读错误都归类成 event overflow → 错误分类/重试决策错 |
| C-16 | MED | cursor/antigravity/kiro/windsurf 用 OpenAI adapter 占位注册为生产可用 |
| C-17 | MED | `VendorFromProtocolFamily` 覆盖不全,8+ 已注册 provider 得到空 vendor → 丢健康/指标维度 |

### T5 跨租户 / 安全边界

| ID | 严重度 | 一行摘要 |
|---|---|---|
| C-01 | HIGH | Antigravity OAuth refresh endpoint 由凭据 JSON `oauth_endpoint` 控制 → SSRF(可打 169.254.169.254) |
| C-09 | HIGH | channel health gate 对 nil store/account 与 unknown state fail-open → 应冷却的通道继续被选 |
| B-14 | HIGH | audit verify 按 `request_id` 查询缺 tenant scope,`tenant_scope_ref` 为空时可读他租户 ledger |

### T6 生产环境的假实现 / panic 地雷

| ID | 严重度 | 一行摘要 |
|---|---|---|
| O-1 | HIGH | storm-controller `AcquireProviderEndpoint`/`AcquireGlobal` 无条件 `panic("TODO...")` |
| C-06 | MED | Azure 凭据允许 `mock_token_endpoint` 作生产 payload,验证期不查真实 secret |

### T7 原子性 / 一致性

| ID | 严重度 | 一行摘要 |
|---|---|---|
| GW-10 | HIGH | 审计写入与 pool 增改非原子(`admin_pools` + `admin_pool_accounts` 同模式)→ audit 失败则变更已生效却返 503 |
| C-05 | MED | 任意 `SetState` 都被审计成 `credential_disabled`,enable 动作被误记为 disable |
| C-08 | MED | Serializable slot acquire 无 retry,并发 40001 当请求失败 → 账号有容量也随机 5xx |
| C-15 | MED | HCSF projection/control injection 失败后回退 raw inbound body,应 fail-loud 变成静默 raw dispatch |
| B-15 | MED | Postgres ledger 读取吞掉 JSON/Merkle 结构错误,损坏行被当正常 entry 返回 |
| O-3 | MED | RoutePlan 缓存被禁用 —— 性能瓶颈(首轮 codex 审计标注) |

### T8 Rust 控制面专项(D 区 + O-2)—— 延后到 Rust 上生产前

| ID | 严重度 | 一行摘要 |
|---|---|---|
| D-01 | HIGH | 路由规划按可伪造 header 取 model/stream/tenant,不解析请求体 |
| D-02 | HIGH | `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 生产残留可绕控制面/凭据/attempt ledger |
| D-03 | HIGH | planned vendor endpoint 允许明文 HTTP,带上游 Bearer 凭据明文发出 |
| D-04 | HIGH | terminal attempt report `try_send` + `let _=`,队列满成功请求丢账 |
| D-05 | HIGH | 非流式成功响应不解析 JSON usage,普通请求 token 账务缺失 |
| D-06 | MED | 客户端 OpenAI org/project header 在 gateway Bearer 下继续转发 |
| D-07 | MED | heartbeat 硬编码健康数据(in-flight/queue/error/latency 全 0),过载节点报空闲 |
| D-08 | MED | 429/408 被归为不可重试 `Upstream4xx`,控制面无法区分租户错误 vs 供应商限流 |
| D-09 | MED | 请求入站字节只信 `Content-Length`,chunked/H2 body 记 0 |
| D-10 | MED | `mimicry-boring` feature 打开后绕过 profile backend intent 的 fail-closed 阻断 |
| O-2 | LOW | Rust `ACTIVE_CONNECTIONS` gauge 定义注册但生产从不 inc/dec |

> GW-09(LOW,超限截断 body 需确保不经 GW-02 外泄)并入 W3 一并处理。

---

## §2 补救波计划

### Go 生产链路 —— 7 子波(建议串行,与 §1"稳点"一致)

| 波 | 主题 | findings | HIGH | 估时(codex) | 关键风险路径 |
|---|---|---|---|---|---|
| **W1** | 信任链 fail-closed 化 | GW-03/07/08 · B-02/12/13 · C-03/04/10/13/14(11) | 8 | 4-6 天 | billing/auth/credentialstore/channelhealth/gateway |
| **W2** | 计费丢钱/错账 | B-01/03/04/05 · C-07/11(6) | 5 | 3-4 天 | billing settler · PASR · forwarder |
| **W3** | 信息泄露·三层错误模型 | GW-02/04/05/06/09 · C-02/18 · B-11(8) | 2 | 3-4 天 | 横切多 handler |
| **W4** | 协议正确性 | GW-01 · B-06/07/08/09/10 · C-12/16/17(9) | 6 | 4-6 天 | proto SSE · cache key · registry |
| **W5** | 跨租户/安全边界 | C-01/09 · B-14(3) | 3 | 2-3 天 | auth core · channelhealth · auditledger |
| **W6** | 生产假实现 + panic | O-1 · C-06(2) | 1 | 1-2 天 | storm_controller · credentialstore |
| **W7** | 原子性/一致性 | GW-10 · C-05/08/15 · B-15 · O-3(6) | 1 | 2-3 天 | admin handlers · slot manager · ledger scan |

**Go 路径合计:45 条,~19-28 天 codex 串行。**

### Rust 探索路径 —— 1 子波(延后)

| 波 | 主题 | findings | HIGH | 估时 | 触发条件 |
|---|---|---|---|---|---|
| **W8** | Rust 控制面专项 | D-01..D-10 · O-2(11) | 5 | 3-5 天 | Rust 上生产前批量做,现不阻塞 |

### 建议执行顺序

`W1 → W2 → W5 → W4 → W3 → W7 → W6`

理由:
- **W1 最先** —— GW-03 每请求标错 evidence,直接腐蚀 HUAKAI 核心卖点;且修复面最大,早做早稳。
- **W2 次之** —— B-03/B-04/C-07/C-11 是真实丢钱路径。
- **W5 第三** —— C-01 SSRF 是可被外部利用的安全洞,不能久拖。
- W4/W3 协议与泄露面广但偏用户体验/合规,排中段。
- W7/W6 偏一致性与清理,且 W7 含 GW-10(§1 P2 的收尾),放最后随 P2 一起落地。

---

## §3 测试纪律(`feedback_risk_based_testing`)

每条 finding 的补救必须带一个对应**具体风险**的测试,不是"能跑通/全绿":

- **W1/W2/W5 必须有真 PG integration test**(`integration_pg` tag)—— 风险活在真实约束、真实账本、真实并发里,stub `return nil` 等于没测。
- **对抗性负向测试** —— B-01 跨用户 claim 折叠、B-14 跨租户读 ledger、C-01 SSRF 内网地址,都要写"我能不能搞坏它"的负向用例。
- stub 必须复刻真实不变量(例:audit stub 自己拒绝非白名单 event_type,像真 CHECK 一样)。
- 每个测试在注释/命名里引用它杀死的风险或 `AT-*` 行。

## §4 执行纪律

1. **本计划执行前必须 codex 交叉评审**(`feedback_plans_codex_cross_review`)—— 评审通过才开第一波。
2. 每子波 = 1 个聚焦补救主题,按 `feedback_commit_naming_v2` 命名(`<模块> <中文说明>`,无阶段号/无 PASS 字样)。
3. 每子波 commit 前跑 `codex exec review --uncommitted --full-auto --sandbox read-only`,处理 HIGH。
4. 每子波收尾对照参照项目同模块(`feedback_per_slice_ref_recompare`):查缺补漏 + 升级点(架构/算法/生态三维)。
5. 收尾验证跑全量 `go test ./...`,不能 scoped 绿当 repo 绿(`feedback_full_suite_verification`)。
6. GW-10 含 §1 P2 的收尾原子性修复 —— P2 不单独提交,随 W7 落地。

## §5 留给 Owner 的决策点

1. **补救范围与节奏** —— 7 个 Go 子波全做(~3-4 周),还是先只做 HIGH(W1/W2/W5/W4 的 HIGH 项)、MED 进路线图?
2. **串行 vs 有限并行** —— W3(泄露)/W6(panic)与 W1/W2 改动文件重叠少,可并行;Owner 此前 §1 选"稳点"串行,补救波是否沿用?
3. **补救波 vs §1 状态树** —— "先深挖全仓再一次补"意味着补救波先于 §1 P3-P10;确认这个顺序?
4. **W8 Rust** —— 确认延后到 Rust 上生产前,现在不动?

---
Source zone docs:`docs/process/research/2026-05-22-deep-audit-{gatewayhttp,billing-proto,routing-auth,rust}.md`
Lane:Claude 合成(HUAKAI 内部代码,无 clean-room 约束)。
UTC:2026-05-22
