# 2026-05-23 W5 Audit 原子化敏感变更 综合计划

本文件综合 Claude lane + Codex lane + Codex C prestudy + Owner cloud review + verification,按 Owner 决策锁定执行口径。`AGENTS.md:307-321` 平行 plan 规则要求独立计划之后写无后缀 authoritative plan,执行只能从本合成稿开始。

输入锚点:Claude lane 定义 W5 覆盖 credentialstore / channelhealth / antigravity / gatewayhttp 五处 audit-after-mutation 问题(`docs/process/plans/2026-05-23-w5-audit-atomicity-claude.md:3-16`);Codex lane 补充文件级证据、真 PG 测试与冻结包约束(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:11-31`,`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:62-145`);Owner verification 确认新增 Bug #1 非流式 DLQ ref 透传也应纳入 W5(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:10-15`,`docs/process/research/2026-05-23-owner-cloud-review-verification.md:182-188`)。

## 1. 目标

W5 修敏感变更(credential 生命周期 / channel health 状态 / antigravity OAuth refresh / gatewayhttp admin pool 增改)5 处 audit insert 失败被静默 `_ =` 忽略 + 1 处非流式 audit DLQ ref 透传缺口,共 6 finding。

核心不变量:敏感 mutation 与审计事实必须同成功、同失败;W4 已确立 production trust ledger startup fail-fast + runtime fail-closed 口径,见 12 波 plan 对 W4/W5 依赖的定义(`docs/process/plans/2026-05-22-audit-remediation-wave.md:73-75`)。

成功标准:

1. C-04/C-05 credentialstore:Create/Rotate/Delete/Refresh/SetState 在 audit insert 失败时不提交业务状态;SetState 不再把 active/revoked/operator_attention 混写成 disabled。
2. C-03 Antigravity:refresh/cache/CAS/failure audit writer 缺失或写失败时 production fail-closed,不得先填 cache 或旋转后吞审计错误。
3. C-10 channelhealth:production 缺 signer 或 ledger append 失败时不提交状态变更为“已签名审计成功”。
4. GW-10 gatewayhttp admin pool/provider account:pool/account mutation 与 admin audit 同事务;审计失败时无 committed pool/account side effect。
5. Owner Bug #1:非流式响应在 `Deferred` audit ledger result 时写出 `X-HUAKAI-Ledger-DLQ-Ref`,与流式 W4c trailer 语义对齐。
6. 所有风险测试给出判别 fixture 与 mutation 自检,符合 `AGENTS.md:579-608`。

合成分歧处理:

- Claude lane 倾向 3 commit + W5 收尾;本稿采纳该节奏,因为 Owner 明确要求 C1/C2/C3 三段执行。
- Codex lane 曾提出 5 commit、migration 0051、admin credential sweep;本稿只保留其文件证据、真 PG 测试思路和冻结包风险,不默认吸收 schema migration 或 admin credential sweep。
- Codex lane 提出 `internal/adminops` 是为了避免冻结包新增文件;本稿把它降为 C3 可选实现容器,只有 handler 内无法保持内聚时使用。
- Owner review 新增 Bug #1 是 W5 scope expansion,不再当 W4 遗留小修单独处理。
- verification 对 Owner 行号做过 refined;本稿行号以 verification 和当前源码锚点为准。
- prestudy 只作为 D1-D4 证据来源,不作为实现模板;参考项目弱模式不得降低 HUAKAI fail-closed 目标。

## 2. 文件级范围

| Commit | 路径 | 新/既有 | 当前锚点 | 责任 |
| --- | --- | --- | --- | --- |
| C1 | `backend/internal/credentialstore/postgres_store.go` | 既有 | Create `_ = InsertAuditEvent` `backend/internal/credentialstore/postgres_store.go:229-235`;Rotate `:308-314`;SetState fixed event `:398-432`;Delete `:462-467`;Refresh success/failure `:617-660`;silent no-op `:956-958` | 把 lifecycle mutation + `InsertAuditEvent` 纳入同事务;审计失败返回 typed error;SetState 读 old state 并在 payload 写 before/after。 |
| C1 | `backend/internal/credentialstore/types.go` | 既有 | 合法状态定义由 verifier 引用 `docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:18` | 如需稳定 state action 常量,只加 domain 常量,不扩大 schema。 |
| C1 | `backend/internal/credentialstore/*_test.go` | 既有或新 | 非冻结包;真 PG 要求见 `docs/process/plans/2026-05-22-audit-remediation-wave.md:116-123` | 新增/扩展 integration_pg 风险测试 T_C1-T_C7;若新文件,目标包非冻结。 |
| C2 | `backend/internal/channelhealth/store_postgres.go` | 既有 | nil signer 直接 return nil `backend/internal/channelhealth/store_postgres.go:266-296`;默认构造无 signer `backend/internal/channelhealth/store_postgres.go:28-35` | production required signer policy;`AppendAudit` signer/ledger append 失败返回 error,由 service tx 回滚。 |
| C2 | `backend/internal/channelhealth/service.go` | 既有 | Codex lane 指出现有 mutation 通过 WithTx 包住状态和 audit `docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:20` | 确认 service 在 `AppendAudit` 返回 error 时 rollback,补缺失路径测试。 |
| C2 | `backend/internal/channelhealth/*_test.go` | 既有或新 | 非冻结包 | T_CH1/T_CH2 真 PG 或 tx-aware 测试;不得用 nil stub 掩盖 signer 风险。 |
| C2 | `backend/internal/auth/antigravity_token_provider.go` | 既有 | cache hit 忽略 audit `backend/internal/auth/antigravity_token_provider.go:141-146`;refresh success `:225-231`;lock/cache `:273-283`;CAS/malformed/failure `:523-552`;nil audit `:557-560` | `_ = writeAudit` 改为 production fail-closed;审计成功前不 `populateCache`;failure/marker 路径不吞 audit writer 错误。 |
| C2 | `backend/internal/auth/audit.go` | 既有 | `NoopAuditWriter` 返回 nil `backend/internal/auth/audit.go:28-31` | production 禁用 nil/noop writer;保留 dev/test 显式 permissive。 |
| C2 | `backend/internal/credentialworker/*` | 既有 | cmd/gateway 注入 refresh audit queries 与 audit ledger `backend/cmd/gateway/wiring.go:245-252` | scheduler/provider 共享 production audit writer gate;不得降级 W4 ledger。 |
| C2 | `backend/cmd/gateway/config.go` | 既有 | production signer fail-fast `backend/cmd/gateway/config.go:188-194`;ledger postgres fail-fast `backend/cmd/gateway/config.go:169-174` | D4 继承 startup fail-fast 口径,补 channelhealth signer/auth audit writer require 校验。 |
| C2 | `backend/cmd/gateway/wiring.go` | 既有 | channelhealth 当前用 `NewPostgresStoreWithAuditSigner` `backend/cmd/gateway/wiring.go:168-173`;credentialworker audit deps `:245-252` | 明确 production wiring 不允许 nil signer / nil audit writer / NoopAuditWriter。 |
| C3 | `backend/internal/gatewayhttp/admin_pools_handler.go` | 既有冻结包 | pool create mutation 后 audit `backend/internal/gatewayhttp/admin_pools_handler.go:162-180`;update `:252-273`;store interfaces `:36-54` | 只改既有文件;handler 调用同事务服务或 tx-capable store;失败返回结构化 503,不留下 pool row。 |
| C3 | `backend/internal/gatewayhttp/admin_pool_accounts_handler.go` | 既有冻结包 | create graph `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:188-247`;update/enabled/clear/delete `:372-486`;audit helper `:759-767` | provider account create/update/enabled/clear/delete 与 admin audit 同事务;create 的 credential/channelhealth init 不留下 orphan。 |
| C3 | `backend/internal/gatewayhttp/chat_completions_handler_headers.go` | 既有冻结包 | `WriteHuakaiHeaders` 非 Persisted 直接 return `backend/internal/gatewayhttp/chat_completions_handler_headers.go:55-60`;Persisted headers `:63-83` | `Deferred` result 写 `X-HUAKAI-Ledger-DLQ-Ref`;Persisted 现有 ledger/verify headers 不变。 |
| C3 | `backend/internal/gatewayhttp/*_test.go` | 既有测试文件 | 冻结包禁止新增文件 `AGENTS.md:546-568` | 只能追加现有 gatewayhttp 测试;覆盖 GW-10 与 T_NS1/T_NS2。 |
| C3 | `backend/internal/adminops/*` | 可选新包 | 非冻结包;Codex lane 提议 adminops tx service `docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:15-16` | 若同事务服务不能保持 handler 文件内聚,新建非冻结 `adminops`;不得新增 gatewayhttp 文件。 |
| C3 | `backend/cmd/gateway/routes.go` | 既有 | admin routes 注入 store/credential audit `backend/cmd/gateway/routes.go:145-180` | 若引入 adminops tx service,在路由组装处注入;不改 auth core。 |
| 收尾 | `docs/process/research/2026-05-24-w5-ref-recompare.md` | 新 docs | W5 收尾对照参照项目由 Claude lane 预留 `docs/process/plans/2026-05-23-w5-audit-atomicity-claude.md:38-41` | 实施完成后写对照与路线图;本 synthesis 任务不创建该文件。 |

文件范围执行规则:

1. C1 不触 billing ledger、quota enforcement、auth core;credentialstore 只改 lifecycle/audit 事务与状态审计语义。
2. C2 可以改 auth refresh provider 和 credentialworker wiring,但不得修改 OAuth endpoint allowlist/SSRF 修复,W1 已负责该风险。
3. C2 的 cmd/gateway 改动只做 production require gate,不重构 runtime options 或 release mode 命名。
4. C3 的 gatewayhttp 改动必须保持 public API 响应语义:业务失败仍是 structured JSON error,不把 raw DB error 扩散为新公开契约。
5. C3 如引入 `internal/adminops`,新增文件必须按 pool-group/account 两个职责拆分,不得创建新的 god-package。
6. 所有 `_test.go` 新增文件若在非冻结包,必须在 commit body 标明为什么不能追加既有测试;gatewayhttp 则必须追加既有测试文件。
7. docs 收尾不能提前到代码 commit 中混写,除非 commit 本身只改 docs。
8. 任意高风险文件(`LICENSE`、real secrets、billing ledger、quota、DB migration)不在默认范围,触及即停。

## 3. 6 finding 闭合证明

| Finding | Owner cite | zone cite | verifier verdict | 落 commit | 闭合证据 |
| --- | --- | --- | --- | --- | --- |
| Owner Bug #1 非流式 DLQ ref 未透传 | `docs/process/research/2026-05-23-owner-cloud-review.md:19-22` | W4c 已修流式但非流式 headers 仍只写 Persisted `backend/internal/gatewayhttp/chat_completions_handler_headers.go:55-60` | confirmed,W5 新增 finding `docs/process/research/2026-05-23-owner-cloud-review-verification.md:10-15` | C3 | `Deferred{DLQRef}` 非流式响应写 `X-HUAKAI-Ledger-DLQ-Ref`;paired fixture 清空 DLQRef 不写。 |
| GW-10 admin pool/provider account mutation 与 audit 非同事务 | `docs/process/research/2026-05-23-owner-cloud-review.md:39-42` | `docs/process/research/2026-05-22-deep-audit-gatewayhttp.md:24-28` | confirmed `docs/process/research/2026-05-23-owner-cloud-review-verification.md:38-43` | C3 | audit trigger fail 时 pool/account/credential/channelhealth graph 无 committed row;handler 返回 structured 503。 |
| C-03 Antigravity refresh audit best-effort | `docs/process/research/2026-05-23-owner-cloud-review.md:34-37` | `docs/process/research/2026-05-22-deep-audit-routing-auth.md:21-25` | confirmed `docs/process/research/2026-05-23-owner-cloud-review-verification.md:31-37` | C2 | fake writer error 时 refresh 不返回 token、不填 cache、不旋转后吞错;nil/noop production 构造失败。 |
| C-04 CredentialStore lifecycle audit ignored | `docs/process/research/2026-05-23-owner-cloud-review.md:24-27` | `docs/process/research/2026-05-22-deep-audit-routing-auth.md:27-31` | confirmed `docs/process/research/2026-05-23-owner-cloud-review-verification.md:17-23` | C1 | audit insert fail 时 Create/Rotate/Delete/Refresh success/failure 均回滚或不提交。 |
| C-05 SetState 固定 credential_disabled | `docs/process/research/2026-05-23-owner-cloud-review.md:29-32` | `docs/process/research/2026-05-22-deep-audit-routing-auth.md:33-37` | confirmed;Owner cite refined to `backend/internal/credentialstore/postgres_store.go:398-432` `docs/process/research/2026-05-23-owner-cloud-review-verification.md:24-30` | C1 | revoked -> active 记录 state action 与 payload old/new,不再表现为 disabled-only。 |
| C-10 channelhealth nil signer 静默成功 | `docs/process/research/2026-05-23-owner-cloud-review.md:44-47` | `docs/process/research/2026-05-22-deep-audit-routing-auth.md:63-67` | confirmed `docs/process/research/2026-05-23-owner-cloud-review-verification.md:45-50` | C2 | production signer nil/append fail 返回 error,service tx rollback;dev/test permissive 必须显式。 |

闭合判定不是“代码路径看起来处理了 error”。每个 finding 必须同时满足三件事:

1. 失败注入:测试能稳定制造 audit writer / audit insert / signer / ledger append / DLQRef 缺失的目标故障。
2. 负向断言:业务 mutation、cache 写入、状态迁移或响应 header 必须与坏代码产生不同结果。
3. 正向保持:同一 fixture 只改 audit 成功或 ref 存在后,原有成功路径仍可工作。
4. 操作证据:structured ERROR 或 typed error 能定位 request_id、tenant_id、provider_account_id/pool_id/action。
5. review 证据:per-commit review 没有 HIGH;若 MED deferred,commit body 必须写清楚 Owner 接受的路线图。
6. 真 PG 证据:所有“同事务回滚”声明必须来自 integration_pg 或等价真实 tx 测试,不能只靠 fake store。

## 4. 冻结包合规

冻结包为 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto`,拆分前禁止新增文件,见 `AGENTS.md:546-568`。W5 只触 `gatewayhttp` 既有文件;`gateway` 与 `proto` 不触。

C3 若需要新同事务服务,只允许进入非冻结 `backend/internal/adminops` 或已有非冻结存储包;不得新增 `gatewayhttp/*_tx.go`。这同时满足“一个包 = 一个内聚职责、一个文件 = 一个内聚职责”的结构规则(`AGENTS.md:536-544`)。

credentialstore / channelhealth / auth / cmd/gateway 不是冻结包;可以新增聚焦测试文件或小 helper,但每个新文件必须在 commit plan 与 review 中说明职责和包体量。

## 5. 风险测试

测试纪律:每条测试必须说明守的缺陷、给判别 fixture、做 mutation 自检;不能用 nil stub 把风险掩盖,见 `AGENTS.md:579-608`。W5 必须用真 PostgreSQL integration 覆盖事务/DLQ 关键点,见 `docs/process/plans/2026-05-22-audit-remediation-wave.md:116-123`。

1. T_C1 Create audit failure rolls back credential:真 PG trigger 让 `credential_created` audit insert fail;断言 Create 返回 error 且 `account_credentials` 无 row。mutation 自检:恢复旧 `_ = InsertAuditEvent` 后 credential row 会留下,测试变红。
2. T_C2 Rotate audit failure preserves previous version:seed version=1,trigger 拒 `credential_rotated`;断言版本、payload fingerprint、refresh fingerprint 未变。mutation 自检:先 update 后吞 audit 会产生 version=2。
3. T_C3 Delete audit failure keeps credential visible:trigger 拒 `credential_deleted`;断言 `deleted_at IS NULL` 且 Resolve/List 行为不变。mutation 自检:旧代码软删后吞错会红。
4. T_C4 RefreshSuccess audit failure preserves token version:trigger 拒 `credential_refresh_succeeded`;断言 last_refresh_outcome/token version/fingerprints 未变。mutation 自检:refresh update 先提交会红。
5. T_C5 RefreshFailure audit failure preserves health fields:trigger 拒 `credential_refresh_failed`;断言 state/failure_count/next_attempt_at 未变。mutation 自检:旧代码会写 revoked/temp_unschedulable 或 failure_count+1。
6. T_C6 SetState revoked -> active discriminates action:paired fixture A `revoked -> active`,B `active -> revoked`;断言 payload old/new 相反且 action/semantic 不同。mutation 自检:固定 `credential_disabled` 或只看 new state 会让 A/B 不可区分。
7. T_C7 SetState audit failure rolls back state:seed active,trigger 拒 state-transition audit;断言 state 仍 active。mutation 自检:先 update 后 audit 会变 operator_attention。
8. T_A1 Antigravity refresh audit failure does not cache token:fake OAuth 返回 valid token,writer 返回 sentinel error,cache spy 断言 Set 未调用。mutation 自检:保留 `_ = p.writeAudit` 时会返回 token 并填 cache。
9. T_A2 production rejects nil/noop audit writer:production policy 下 nil 与 `NoopAuditWriter{}` 构造/validate 都失败。mutation 自检:保留 `p.audit == nil return nil` 时 refresh 路径会错误成功。
10. T_CH1 channelhealth nil signer rolls back manual pause:production required signer nil,调用 manual pause;断言状态保持 active、无 signed ledger evidence。mutation 自检:当前 `s.signer == nil return nil` 会提交状态。
11. T_CH2 channelhealth ledger append failure rolls back state and audit:ledger append trigger/fake 返回 error;断言 health state 与 audit row 同事务回滚。mutation 自检:只插 audit row 后吞 ledger error 会红。
12. T_G1 CreatePool audit failure rolls back pool row:trigger 拒 `create_pool_group`;断言无同名 pool row。mutation 自检:当前 `InsertPool` 后 audit 的模式会留下 pool。
13. T_G2 UpdateProviderAccount audit failure preserves enabled/priority:seed account,trigger 拒 `update_provider_account` 或 enable action;断言字段未变。mutation 自检:先 update 后 audit 会改变字段。
14. T_G3 CreateProviderAccount full graph rollback:请求含 credential payload 与 channel health init,trigger 拒 `create_provider_account`;断言 provider_accounts/account_credentials/channel_health_state/admin_audit_events 四处都无提交。mutation 自检:当前 cleanup soft-delete 无法覆盖 audit failure。
15. T_NS1 non-streaming Deferred DLQRef writes header:paired fixture A `LedgerResultStateDeferred + DLQRef="audit_ledger_dlq:1"`,B 只清空 DLQRef;A 写 `X-HUAKAI-Ledger-DLQ-Ref`,B 不写。mutation 自检:保留 `result.State != Persisted return` 时 A 会红。
16. T_NS2 non-streaming Persisted headers unchanged:paired fixture A Persisted ledger id/fingerprint,B Deferred DLQRef;A 写 ledger/verify/sig headers,B 只写 DLQRef 不伪造 verify URL。mutation 自检:把 Deferred 当 Persisted 会错误写 verify/ledger headers。

## 6. Commit 切片

提交标题固定为 `<英文模块> <中文说明>`;每个 commit stage 后运行 `codex exec review --uncommitted --full-auto`,项目要求见 `AGENTS.md:487-503`。

1. `credentialstore 凭据生命周期审计同事务`
   - 闭合 C-04 + C-05。
   - 先写真 PG failing tests T_C1-T_C7。
   - 实现 tx helper:validation/encryption 准备可在 tx 外完成,DB mutation + audit insert 在同一 tx;审计 error 包装为 typed phase error。
   - SetState 在 tx 内读 old state / update / audit,payload 写 `old_state`、`new_state`、`actor_id`,action 语义按 D3。

2. `channelhealth-auth 强制签名与刷新审计`
   - 闭合 C-10 + C-03,含 `cmd/gateway` 启动期 require 校验。
   - 先写 T_A1/T_A2/T_CH1/T_CH2。
   - Antigravity 所有 `_ = writeAudit` 路径改成 production fail-closed;cache populate 与 winning credential return 必须在 audit 成功后。
   - channelhealth production signer required;`AppendAudit` signer/ledger append error 传出,service tx rollback。

3. `gatewayhttp 管理池审计同事务与非流式DLQ透传`
   - 闭合 GW-10 + Owner Bug #1。
   - `gatewayhttp` 只改既有 files;如需事务服务,落非冻结 `internal/adminops`。
   - 先写 T_G1-T_G3 与 T_NS1/T_NS2。
   - pool/account mutation + admin audit 同事务;非流式 `Deferred` result 写 DLQ ref header。

4. `docs W5收尾对照参照项目`
   - 实施完成后补 W5 收尾研究/风险登记/验收矩阵;不在本 synthesis 写入。
   - 对照参考项目只能引用 prestudy 或新 clean-room lane artifact,不得读源码后直接实现。

C1 具体执行顺序:

1. 先读 credentialstore 现有 tx helper / DBTX interface,确认能否在同一 `pgx.Tx` 上执行 mutation 与 audit insert。
2. 写 T_C1-T_C7,先用当前代码跑红;若测试因环境缺 PG skip,记录 blocked,不得继续声称 closure。
3. 抽取 audit insert strict path:缺 store/db/tenant/event_type 不再在敏感路径 nil no-op。
4. 改 Create/Rotate/Delete/SaveRefreshSuccess/SaveRefreshFailure/SetState,每个 path 返回审计 phase error。
5. 跑 targeted integration + unit + race;stage 后 review。

C2 具体执行顺序:

1. 写 auth nil/noop writer 与 audit writer error tests,覆盖 cache hit、refresh success、CAS winning credential、malformed/failure 至少两个高风险路径。
2. 改 Antigravity `writeAudit` 调用顺序,审计成功前不返回 token、不写 cache。
3. 写 channelhealth signer/ledger append rollback tests,确认 service tx 能传播 `AppendAudit` error。
4. 在 cmd/gateway 加 production require gate,复用 `loadAuditSigner` / `buildAuditLedger` 已有 fail-fast 风格。
5. 跑 auth/channelhealth/cmd gateway targeted + race;stage 后 review。

C3 具体执行顺序:

1. 先写 T_NS1/T_NS2,证明非流式 Deferred header 缺口;这是小改,可先落在 C3。
2. 写 pool create/update audit-failure 回滚测试;再写 provider account graph 回滚测试。
3. 决定是否引入 `internal/adminops`;若引入,先写该包真 PG tests,再 rewiring gatewayhttp handler。
4. gatewayhttp handler 只做参数解析/响应,事务与审计组合逻辑不继续塞大 handler。
5. 跑 gatewayhttp/adminops/cmd routes targeted + full backend tests;stage 后 review。

收尾执行顺序:

1. 记录每条风险测试的 mutation self-check 结果。
2. 更新 `docs/10_RISK_REGISTER.md` 增 `RR-W5-001` 或确认无需路线图项。
3. 如 Owner 要求,更新 `docs/11_ACCEPTANCE_TEST_MATRIX.md` 中 admin operations / security / observability 相关 Planned 行。
4. 写 W5 ref recompare 时只引用 prestudy 或新 lane artifact。
5. 汇总 per-commit review verdict 与剩余 MED/LOW。

## 7. 验证命令

```bash
cd backend && GOCACHE=/tmp/huakai-go-cache go test -tags=integration_pg ./internal/credentialstore ./internal/channelhealth ./internal/adminops ./internal/gatewayhttp -count=1
```

```bash
cd backend && GOCACHE=/tmp/huakai-go-cache go test ./internal/credentialstore ./internal/auth ./internal/credentialworker ./internal/channelhealth ./internal/gatewayhttp ./cmd/gateway -race -count=1
```

```bash
cd backend && GOCACHE=/tmp/huakai-go-cache go build ./...
```

```bash
cd backend && GOCACHE=/tmp/huakai-go-cache go test ./... -count=1
```

```bash
codex exec review --uncommitted --full-auto
```

关键要求:integration test 必须连真 PG;如果 `HUAKAI_DATABASE_URL` 缺失导致 skip,交付报告不能宣称事务风险已闭合,只能标 blocked。

## 8. Owner 决策落地

D1 audit-write 失败时 mutation 处理:锁定 same-tx + `InsertAuditEvent` 失败回滚 mutation。依据:LiteLLM 是非阻塞弱模式,不可作为 HUAKAI 下限(`docs/process/research/2026-05-23-w5-ref-prestudy.md:20`);sub2api 少数 payment claim 把 audit 放事务内(`docs/process/research/2026-05-23-w5-ref-prestudy.md:23`);HUAKAI 已有 admin billing settings same-tx 先例(`backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go:135-193`)。

D2 audit DLQ / replay:锁定 fallback path,不为 W5 ordinary admin/credential audit 新增 DLQ EventKind,不复用 `audit_ledger_entry`;仅 structured ERROR + `RR-W5-001` 路线图。依据:Helicone DLQ 是 observability ingestion,不是 admin audit(`docs/process/research/2026-05-23-w5-ref-prestudy.md:29-32`);HUAKAI `audit_ledger_entry` payload 当前是 PreparedEntry-only producer/worker(`backend/internal/auditledger/dlq_producer.go:15-40`,`backend/internal/auditledger/dlq_worker.go:16-31`);W4c gate 已规定不满足 payload gate 则 fallback(`docs/process/plans/2026-05-23-w4c-settle-bypass-synthesis.md:174-200`)。

D3 状态迁移事件粒度:锁定 action enum 扩 + before/after state 进 payload jsonb。依据:LiteLLM 用单 action + before/after 风格(`docs/process/research/2026-05-23-w5-ref-prestudy.md:35-39`);HUAKAI 当前 `SetState` 固定 `credential_disabled` 仅在 payload 放 state(`backend/internal/credentialstore/postgres_store.go:398-432`)。执行时优先不做 migration;若当前 DB CHECK 拒新 action/event string,触发第 12 节 schema gate。

D4 production signer/audit-writer 强制:锁定启动期 fail-fast + 运行期 fail-closed。依据:HUAKAI 已有 production 私钥与 Postgres ledger fail-fast(`backend/cmd/gateway/config.go:169-194`)和 runtime ledger fail-closed(`backend/internal/gatewayhttp/chat_completions_billing.go:360-374`);参考项目多为 weaker optional/non-blocking,prestudy 明确不应降级(`docs/process/research/2026-05-23-w5-ref-prestudy.md:41-45`)。

D 决策实现不变量:

1. D1 不允许“先 mutation 后返回 503”作为等价闭合;必须 rollback 或没有提交。
2. D2 不允许把 ordinary admin/credential audit failure 塞进 `audit_ledger_entry`;这会与 W4 PreparedEntry replay 语义冲突。
3. D3 不允许仅把 `"state": "active"` 放 payload 但外层仍表现为 disabled-only;operator 查询必须能一眼区分 enable/revoke/attention。
4. D4 不允许只靠 startup gate;运行期构造错配、test-only Noop 泄入 production 时也要 fail-closed。
5. D1-D4 的任何降级都必须成为 Owner decision,不能由执行者在实现中临时“为了绿测试”调整。

## 9. 范围外

- Owner Bug #7/#8 Invitation schema 与 FK:跨波单独立项,涉及 DB schema,不吸收进 W5(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:52-67`)。
- Owner Bug #9 OpenAPI strict vs handler:跨波 P2 协议契约,不在 W5 改 decoder(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:68-74`)。
- Owner Bug #10/#11 Rust:W11/W12,除非 Owner 提前 Rust canary(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:76-90`)。
- Owner Bug #12 rate.go 429 reset:W7 routing/cooldown(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:92-98`)。
- Owner Feature #1-#5:产品能力/API surface/provider readiness/large payload policy,不在 W5 修复范围(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:100-139`)。
- Owner Test #1-#5:CI、acceptance matrix、OpenAPI header/trailer 细节等工程缺口,W5 收尾记录路线图,不扩大本波实现(`docs/process/research/2026-05-23-owner-cloud-review-verification.md:141-180`)。
- admin credential handler sweep:Codex lane提出额外 sweep(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:21`),本合成稿不默认吸收;若 Owner 后续指定,走 RR-W5-ADMIN-CRED-001 或新小切片。

范围外但需记录的 W5 相关债务:

- OpenAPI header/trailer 契约:Bug #1 修代码后,OpenAPI 是否声明 `X-HUAKAI-Ledger-DLQ-Ref` 属 P2 协议契约,不阻塞 W5 代码修复。
- CI gate:Owner Test #4 确认仓库无 `.github` workflow,但 W5 不新建 CI;release readiness 后续单列。
- Acceptance matrix:W5 收尾可标注相关 tests,但不把所有 Planned 行一次性补完。
- Product capability gaps:模型/多模态/大请求边界不能在 W5 宣传修复。
- Rust defects:不因 W5 audit 计划触碰 exploratory Rust。

## 10. 风险 + 缓解

| 风险 | 具体失败方式 | 缓解 |
| --- | --- | --- |
| R1 schema CHECK 与 D3 冲突 | migration 0019 对 `credential_audit_events.event_type` 有 CHECK,当前白名单含 `credential_disabled` 等 `backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql:106-111`;若新增 event string 会被拒。 | 默认无 migration;实现前用真 PG test 试插 chosen action/event;失败即停在 schema gate,不偷偷加 migration。 |
| R2 gatewayhttp 冻结包违规 | 为 tx helper 新增 gatewayhttp 文件会违反 `AGENTS.md:546-568`。 | gatewayhttp 只改既有 handler/test;新事务服务只进非冻结 `internal/adminops`。 |
| R3 长事务扩大锁范围 | provider account create 同时写 account、credential、channelhealth、audit,持锁过久。 | JSON decode/validation 在 BeginTx 前;无网络 IO 入 tx;真 PG test 加 context timeout。 |
| R4 nested tx | credentialstore public Create 自己 BeginTx,adminops create provider account 再调会嵌套失败。 | 给 credentialstore 增 tx-bound internal helper或接口,public method 包事务,adminops 用 tx-bound helper。 |
| R5 auth 可用性下降 | audit DB 抖动导致 Antigravity refresh fail-closed,短时无法刷新 token。 | 这是安全取舍;日志/metric 明确 audit_write_failed,RR-W5-001 记录 operator recovery;不降级到无审计 refresh。 |
| R6 channelhealth dev/test 破裂 | 现有测试用 `NewPostgresStore` 无 signer。 | dev/test permissive 必须显式 policy;production wiring 与 tests 区分。 |
| R7 DLQ 误复用 | ordinary audit failure 被塞进 `audit_ledger_entry`,worker 按 PreparedEntry replay 后污染 trust ledger。 | D2 锁定 no-DLQ fallback;per-commit review 查无新 EventKind/无 `audit_ledger_entry` ordinary audit enqueue。 |
| R8 弱测试假绿 | fixture 只断 status 或用 nil stub,删掉 guard 仍绿。 | 第 5 节所有测试都有 paired fixture/mutation 自检;review 按 `AGENTS.md:601-608` 拦截。 |

## 11. 时间估

实施 5-7 小时墙钟 + per-commit review: C1 credentialstore 120-150 分钟;C2 channelhealth/auth/cmd-gateway 120-150 分钟;C3 gatewayhttp/adminops/header 120-150 分钟;targeted/race/full tests 60-90 分钟;每 commit review 与修复 45-90 分钟。

若 schema gate 触发 migration,不得在 W5 默认执行中直接加 migration;需 Owner 再确认高风险 DB schema change,时间另估。

## 12. Schema gate

W5 默认结论:无 schema migration 预期。

- C-05 SetState 事件分类:优先用现有 text 字段与 payload jsonb 表达 action + before/after;如果实际 applied schema 的 CHECK 拒绝新 event/action string,触发 gate 重审。
- D2 fallback 已锁定 no-DLQ:不新增 EventKind,不改 `backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql`。
- gatewayhttp 非流式 DLQRef header:纯 handler/test 改动,无 schema。
- channelhealth/auth production gate:纯 wiring/runtime policy 改动,无 schema。
- 如 implementation 发现必须改 DB CHECK、加列、加表或 migration,立即停止本波代码执行,把该部分转为 `RR-W5-001` 或 Owner-confirmed schema slice。

Schema gate 操作步骤:

1. C1 开工前在测试 PG 查询 `credential_audit_events_event_type_check` 当前定义,确认 chosen event/action 是否被允许。
2. 如果允许,继续无 migration 路线。
3. 如果不允许,不得直接新增 0051 migration;先把 C-05 实现改为现有 schema 可表达的 safe equivalent 或停止请求 Owner。
4. 如果 safe equivalent 仍会让 operator 看到 disabled-only,则不能接受,必须升级为 schema decision。
5. D2 任何 DLQ schema 想法直接落 RR-W5-001,不在 W5 默认 commit 中实现。

## 13. Clean-room

本计划只读 HUAKAI 内部代码、本仓 docs、Owner verified review 与 Codex C prestudy。8 个参考项目源码证据已经由 prestudy 的 specifier lane 单独产出;本 synthesis 只引用其行为摘要和 cite,不重新读取外部源码,不复制非 MIT 项目的函数名、结构、schema、注释、UI 或算法。

Clean-room 风险结论:低。D1-D4 采纳的是 HUAKAI 自有 same-tx / fail-fast / fail-closed 模式,参考项目只作为“生态弱模式不应降级”的证据;sub2api LGPL 证据仅用于行为对照,不迁移实现。

Source provenance:

- Claude lane:HUAKAI internal plan,无外部源码内容复用。
- Codex lane:HUAKAI internal plan,其中参考源码对照被本稿用 prestudy 替代。
- Codex C prestudy:specifier lane,已列 Source files read 与 observed/inferred/open questions `docs/process/research/2026-05-23-w5-ref-prestudy.md:65-82`。
- Owner cloud review:owner-evidence,本稿只采纳经 verification confirmed/refined 的条目。
- verification:HUAKAI internal verifier lane,明确无外部参考源码读取 `docs/process/research/2026-05-23-owner-cloud-review-verification.md:190-197`。
- 本 synthesis:只新增计划文本,不写实现代码,不引入依赖,不修改 schema。

执行前检查:

1. 确认本 synthesis 是 authoritative plan,执行不再读取或改写 Claude/Codex lane。
2. 确认冻结包不新增文件。
3. 确认真 PG integration 可运行,否则不得宣称事务闭合。
4. 确认每个 commit staged 后跑 `codex exec review --uncommitted --full-auto`。
5. 确认所有引用外部参考项目的后续文档使用 clean-room lane guard。
