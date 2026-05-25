# W5 audit 原子化敏感变更审计 Codex 独立计划

## 1. 目标与 W4 依赖

目标:把 W5 范围内的敏感变更与审计事实绑定成"同成功、同失败"的原子提交,避免出现"管理/凭据/健康状态已经生效,但审计缺条"的状态。W5 覆盖 GW-10/C-03/C-04/C-05/C-10,原补救总计划把 W5 定为`audit 原子化敏感变更审计`,估时 2.5d 且可能含 schema 风险 [docs/process/plans/2026-05-22-audit-remediation-wave.md:55](docs/process/plans/2026-05-22-audit-remediation-wave.md:55)。

W5 继承 W4 fail-closed 契约。W4 已把 production 下 audit ledger/signer 缺失定为启动失败,运行时 ledger append 失败定为 durable DLQ 或双失败 fail-closed [docs/process/plans/2026-05-22-w4-trust-ledger.md:102](docs/process/plans/2026-05-22-w4-trust-ledger.md:102)。W5 不照搬"请求放行+DLQ"到管理变更,而默认采用更严格的同事务策略:审计 insert/签名审计失败时,对应敏感 mutation 回滚并返回错误。理由是 W5 主要是 operator/admin/credential/channel-health 状态变更,可用性压力低于在线 completion 请求;同事务回滚比"先变更后补审计"更容易证明,也满足 W5 真 PG 测试要求:审计失败时无 committed 变更,除非存在 durable recovery 行 [docs/process/plans/2026-05-22-audit-remediation-wave.md:122](docs/process/plans/2026-05-22-audit-remediation-wave.md:122)。

P2 边界:已读`git show 336fc87`。该提交已完成管理池租户隔离和 0049 admin audit action/target 白名单,并明确 GW-10 不在该提交修。W5 不重写 P2 租户解析;当前 create/update pool 进入 mutation 前已解析 tenant [backend/internal/gatewayhttp/admin_pools_handler.go:149](backend/internal/gatewayhttp/admin_pools_handler.go:149),[backend/internal/gatewayhttp/admin_pools_handler.go:222](backend/internal/gatewayhttp/admin_pools_handler.go:222),W5 只补同事务化和全仓 audit-after-mutation。

## 2. 文件级范围

| finding | 现状证据 | 计划修改文件 |
|---|---|---|
| GW-10 admin pools | create 先`InsertPool`再`writeAdminPoolAudit` [backend/internal/gatewayhttp/admin_pools_handler.go:162](backend/internal/gatewayhttp/admin_pools_handler.go:162), update 先`UpdatePool`再写 audit [backend/internal/gatewayhttp/admin_pools_handler.go:252](backend/internal/gatewayhttp/admin_pools_handler.go:252);GW-10 研究要求 mutation+audit 同一事务 [docs/process/research/2026-05-22-deep-audit-gatewayhttp.md:24](docs/process/research/2026-05-22-deep-audit-gatewayhttp.md:24)。 | 修改既有 `backend/internal/gatewayhttp/admin_pools_handler.go:36-84` 接口/适配点和 `:139-181`,`:208-273` handler 调用;修改既有 `backend/cmd/gateway/routes.go:183-187` 注入事务服务;新建非冻结包 `backend/internal/adminops/runner.go`, `pool_groups.go`, `pool_groups_integration_test.go`。 |
| GW-10 admin pool accounts | create 先`InsertProviderAccount`,再 credential/channel health,最后 admin audit [backend/internal/gatewayhttp/admin_pool_accounts_handler.go:188](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:188),[backend/internal/gatewayhttp/admin_pool_accounts_handler.go:201](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:201),[backend/internal/gatewayhttp/admin_pool_accounts_handler.go:220](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:220),[backend/internal/gatewayhttp/admin_pool_accounts_handler.go:243](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:243);update/enabled/clear/delete 同样 mutation 后 audit [backend/internal/gatewayhttp/admin_pool_accounts_handler.go:372](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:372),[backend/internal/gatewayhttp/admin_pool_accounts_handler.go:409](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:409),[backend/internal/gatewayhttp/admin_pool_accounts_handler.go:440](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:440),[backend/internal/gatewayhttp/admin_pool_accounts_handler.go:474](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:474)。 | 修改既有 `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:36-60`,`:155-255`,`:328-486`,`:759-768`;修改既有 `backend/cmd/gateway/routes.go:135-145`;新建非冻结包 `backend/internal/adminops/provider_accounts.go`, `provider_accounts_integration_test.go`。 |
| C-04 credential lifecycle audit ignored | credential create/rotate/delete/refresh success/failure 都用`_ = s.InsertAuditEvent(...)` [backend/internal/credentialstore/postgres_store.go:229](backend/internal/credentialstore/postgres_store.go:229),[backend/internal/credentialstore/postgres_store.go:308](backend/internal/credentialstore/postgres_store.go:308),[backend/internal/credentialstore/postgres_store.go:462](backend/internal/credentialstore/postgres_store.go:462),[backend/internal/credentialstore/postgres_store.go:617](backend/internal/credentialstore/postgres_store.go:617),[backend/internal/credentialstore/postgres_store.go:655](backend/internal/credentialstore/postgres_store.go:655);`InsertAuditEvent` 遇 store/db 或关键字段缺失直接 nil [backend/internal/credentialstore/postgres_store.go:956](backend/internal/credentialstore/postgres_store.go:956)。 | 修改既有 `backend/internal/credentialstore/postgres_store.go:118-165`,`:190-235`,`:280-314`,`:450-467`,`:600-660`,`:956-978`;新建/修改 `backend/internal/credentialstore/postgres_store_integration_test.go`。 |
| C-05 SetState 审计语义错误 | `SetState` 接受多个状态 [backend/internal/credentialstore/postgres_store.go:398](backend/internal/credentialstore/postgres_store.go:398),合法状态含 active/revoked/operator_attention 等 [backend/internal/credentialstore/types.go:33](backend/internal/credentialstore/types.go:33),但固定写`credential_disabled` [backend/internal/credentialstore/postgres_store.go:427](backend/internal/credentialstore/postgres_store.go:427)。现有 CHECK 不含通用 state transition event [backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql:105](backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql:105)。 | 新建 schema migration `backend/sql/migrations/0051_credential_state_transition_audit.up.sql` 和 `.down.sql`;修改 `backend/internal/credentialstore/postgres_store.go:398-433`;必要时修改 `backend/sql/query` 源和生成文件,但优先不改 sqlc 查询。 |
| C-03 Antigravity refresh audit best-effort | provider 成功旋转后忽略 audit 错误 [backend/internal/auth/antigravity_token_provider.go:225](backend/internal/auth/antigravity_token_provider.go:225);失败/畸形/DB conflict 也忽略 [backend/internal/auth/antigravity_token_provider.go:523](backend/internal/auth/antigravity_token_provider.go:523),[backend/internal/auth/antigravity_token_provider.go:543](backend/internal/auth/antigravity_token_provider.go:543),[backend/internal/auth/antigravity_token_provider.go:552](backend/internal/auth/antigravity_token_provider.go:552);nil writer 静默成功 [backend/internal/auth/antigravity_token_provider.go:557](backend/internal/auth/antigravity_token_provider.go:557);公开 Noop writer 返回 nil [backend/internal/auth/audit.go:28](backend/internal/auth/audit.go:28)。 | 修改既有 `backend/internal/auth/audit.go:8-31`, `backend/internal/auth/antigravity_token_provider.go:112-139`,`:214-232`,`:515-568`;修改既有 `backend/internal/credentialworker/scheduler.go:52-86`,`:162-172` 和 `backend/internal/credentialworker/audit.go:15-43`;修改既有 `backend/cmd/gateway/wiring.go:245-253` 加 production audit writer gate。 |
| C-10 channel health audit unsigned | `NewPostgresStore` 默认 signer nil [backend/internal/channelhealth/store_postgres.go:28](backend/internal/channelhealth/store_postgres.go:28);`AppendAudit` 插入 audit row 后 signer nil 直接成功 [backend/internal/channelhealth/store_postgres.go:266](backend/internal/channelhealth/store_postgres.go:266),[backend/internal/channelhealth/store_postgres.go:295](backend/internal/channelhealth/store_postgres.go:295);service mutation 通过 WithTx 包住状态和 audit [backend/internal/channelhealth/service.go:347](backend/internal/channelhealth/service.go:347),但 nil signer 会让 tx commit。 | 修改既有 `backend/internal/channelhealth/store_postgres.go:22-41`,`:266-309`;修改既有 `backend/internal/channelhealth/types.go:325-329` 如需暴露 policy;新增/修改 `backend/internal/channelhealth/store_postgres_integration_test.go`;修改既有 `backend/cmd/gateway/wiring.go:168-173` 加启动期显式校验。 |
| admin credential handler sweep | create/rotate 调用 credentialstore 后 best-effort admin audit [backend/internal/gatewayhttp/admin_credentials_handler.go:154](backend/internal/gatewayhttp/admin_credentials_handler.go:154),[backend/internal/gatewayhttp/admin_credentials_handler.go:186](backend/internal/gatewayhttp/admin_credentials_handler.go:186),helper 忽略错误 [backend/internal/gatewayhttp/admin_credentials_handler.go:385](backend/internal/gatewayhttp/admin_credentials_handler.go:385);state/delete 同样忽略 [backend/internal/gatewayhttp/admin_credentials_handler.go:205](backend/internal/gatewayhttp/admin_credentials_handler.go:205),[backend/internal/gatewayhttp/admin_credentials_handler.go:232](backend/internal/gatewayhttp/admin_credentials_handler.go:232)。 | 推荐作为 W5 内的"同事务服务覆盖",但若 Owner 只要求 domain audit,则此项降为 Mandatory Roadmap RR-W5-ADMIN-CRED-001。修改既有 `backend/internal/gatewayhttp/admin_credentials_handler.go:144-214`,`:218-240`,`:385-397`;不新增 gatewayhttp 文件。 |

## 3. 冻结包合规检查

硬约束:冻结包 `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto` 禁止新增文件 [AGENTS.md:546](AGENTS.md:546),计划/spec 新建文件必须逐个说明目标包且确认非冻结 [AGENTS.md:558](AGENTS.md:558)。W4 也记录过相同约束:新文件只能进非冻结包,`gatewayhttp/gateway/proto` 只改既有文件 [docs/process/plans/2026-05-22-w4-trust-ledger.md:552](docs/process/plans/2026-05-22-w4-trust-ledger.md:552)。

- `gatewayhttp`:只修改既有 `admin_pools_handler.go`, `admin_pool_accounts_handler.go`, `admin_credentials_handler.go` 和既有 test 文件;不新增 `_test.go`。
- `gateway`:W5 不改。
- `proto`:W5 不改。
- 新增代码只放非冻结包: `backend/internal/adminops`、`backend/internal/credentialstore` 测试、`backend/internal/channelhealth` 测试、`backend/sql/migrations`。
- 不把 adminops 事务服务塞进既有 `gatewayhttp/admin_billing_settings_audit_tx.go`;该文件只负责 billing settings 事务 [backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go:62](backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go:62),混入 pool/account 会违反"一个文件=一个内聚职责" [AGENTS.md:536](AGENTS.md:536)。

## 4. 切片计划与 commit 拆分

我建议 5 个 commit。W4 用 3 个 commit 是因为 ledger/DLQ/completion-ref 三段高度内聚;W5 横跨 admin HTTP、credentialstore、auth refresh、channelhealth 和 schema enum,强行压成 1-3 个 commit 会让 per-commit review 无法定位事务/策略回归。

1. `schema 扩展凭据状态审计枚举`
   - 前置:Owner 批准 §11 schema gate。
   - 变更:`0051_credential_state_transition_audit` 扩展 `credential_audit_events.event_type` 和 `admin_audit_events.action`。
   - 测试:真 PG 迁移 up/down guard;证明 active/revoked/operator_attention 都可用同一个 `credential_state_changed` 事件。

2. `credentialstore 原子化凭据生命周期审计`
   - 把 create/rotate/delete/refresh success/failure 的 mutation 与 `credential_audit_events` insert 放进同一事务。
   - 将 `InsertAuditEvent` 拆成严格内部写入:公开只读/兼容路径仍可保留,但敏感 mutation 不再使用 silent no-op。
   - `SetState` 改为 `credential_state_changed` + payload `{old_state,new_state,actor_id}`;必须先读 old state 并在同事务内锁定/更新/审计。

3. `adminops 原子化管理池与账号审计`
   - 新建 `internal/adminops` 事务服务,模式参考现有 billing settings tx runner:BeginTx 后用 `dbbilling.New(tx)` 和 `admindb.New(tx)` 同事务执行 [backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go:204](backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go:204)。
   - pool group create/update 迁到 adminops。
   - provider account create/update/enabled/clear/delete 迁到 adminops;create 的 provider account + credentialstore + channelhealth default init + admin audit 必须同一 tx,避免当前 cleanup soft-delete 兜底留下窗口 [backend/internal/gatewayhttp/admin_pool_accounts_handler.go:208](backend/internal/gatewayhttp/admin_pool_accounts_handler.go:208)。

4. `auth 强制 refresh audit writer`
   - 不复用 W4 `AuditRefPolicy`;新增 auth/credentialworker 本地 `AuditWritePolicy`,因为 W4 policy 管的是 request completion audit-ref,而 C-03 管的是 refresh audit writer 可用性。
   - production:启动期要求 db audit writer;运行期 audit 写失败返回错误,不得填 cache。dev/test:可显式用 Noop,但测试必须能断言 Noop 不会被 production policy 接受。
   - 对 Antigravity provider 成功旋转路径,审计成功前不 `populateCache`。

5. `channelhealth 强制签名审计`
   - `NewPostgresStoreWithAuditSigner` 配置 signed-audit required;signer nil 或 append 失败返回 error。
   - 保留 `NewPostgresStore` 作为 dev/test 可选无签名构造,但 production wiring 必须使用 signed 构造并显式 fail-fast。
   - 真 PG 测试证明 state/audit/ledger 同事务回滚。

## 5. 风险测试清单

测试纪律来自 AGENTS:每个测试必须能说明守的缺陷、用判别性 fixture、做 mutation 自检 [AGENTS.md:579](AGENTS.md:579)。以下测试都必须在测试名或注释写明风险编号。

1. `credentialstore Create audit failure rolls back credential`
   - fixture:真 PG,在 `credential_audit_events` 上建测试 trigger,当 `NEW.actor_id='w5-audit-fail'` 时 raise exception;调用 `Create` actor_id=`w5-audit-fail`。
   - 断言:返回 error,`account_credentials` 无新 row,`credential_audit_events` 无 row。
   - mutation 自检:如果恢复旧代码 `_ = InsertAuditEvent`,测试会看到 credential row 已提交而变红。

2. `credentialstore Rotate audit failure preserves previous version`
   - fixture:seed version=1 active credential,trigger 按 actor_id 拒审计;调用 `Rotate`。
   - 断言:返回 error,版本仍为 1,密文/refresh fingerprint 未变化。
   - mutation 自检:如果 update 与 audit 不同事务或吞 audit error,版本会变 2。

3. `credentialstore SetState active is state_changed not disabled`
   - fixture:seed `revoked`,调用 `SetState(...,"active")`。
   - 断言:审计 `event_type='credential_state_changed'`,payload old=`revoked`,new=`active`,不存在 `credential_disabled`。
   - mutation 自检:把 event type 改回固定 `credential_disabled`,测试直接变红。

4. `credentialstore SetState audit failure rolls back state`
   - fixture:seed `active`,trigger 拒 `credential_state_changed`;调用 SetState to `operator_attention`。
   - 断言:返回 error,state 仍 active,无 audit row。
   - mutation 自检:如果先 update 后 audit 或忽略 audit,状态会变 operator_attention。

5. `credentialstore Delete audit failure keeps credential visible`
   - fixture:seed credential,trigger 拒 `credential_deleted`。
   - 断言:Delete 返回 error,`deleted_at IS NULL`,List/Resolve 行为不变。
   - mutation 自检:旧代码会软删成功且吞 audit error。

6. `credentialstore SaveRefreshSuccess audit failure preserves token version`
   - fixture:seed refreshable credential,trigger 拒 `credential_refresh_succeeded`;调用 `SaveRefreshSuccess`。
   - 断言:返回 error,token version/last_refresh_outcome/refresh fingerprint 都未变化。
   - mutation 自检:如果 refresh update 先提交,`last_refresh_outcome='refresh_succeeded'` 会泄露。

7. `credentialstore SaveRefreshFailure audit failure preserves health fields`
   - fixture:seed active,trigger 拒 `credential_refresh_failed`;调用 `SaveRefreshFailure(...,"invalid_grant")`。
   - 断言:返回 error,state/failure_count/next_attempt_at 未变化。
   - mutation 自检:旧代码会把 state 改 revoked 或 failure_count+1。

8. `auth Antigravity refresh audit failure does not cache token`
   - fixture:fake OAuth 返回 valid access token + rotated refresh token;fake audit writer 返回 sentinel error;cache spy 记录 Set。
   - 断言:GetAccessToken 返回 audit error,cache Set 未调用,store 未报告 committed success 或通过 combined tx 回滚。
   - mutation 自检:保留 `_ = p.writeAudit` 时函数会返回 access token 且 cache 被填。

9. `auth production policy rejects nil or Noop audit writer`
   - fixture:构造 production policy provider/scheduler,分别传 nil 和 `auth.NoopAuditWriter{}`。
   - 断言:启动/validate 返回 `ErrAuditWriterRequired` 类错误。
   - mutation 自检:若 `writeAudit` nil 分支继续返回 nil,测试会通过 refresh 路径而变红。

10. `adminops CreatePool audit failure rolls back pool row`
    - fixture:真 PG,trigger 拒 `admin_audit_events.action='create_pool_group'`;调用 adminops create pool。
    - 断言:返回 error,`pool_groups` 无同 name row。
    - mutation 自检:当前 handler 模式会先插 pool,该断言失败。

11. `adminops UpdatePool audit failure preserves old pool fields`
    - fixture:seed pool name/top_k,trigger 拒 `update_pool_group`;调用 update。
    - 断言:返回 error,旧 name/top_k/allow_last_resort 未变。
    - mutation 自检:如果 update 与 audit 分离,字段会更新。

12. `adminops CreateProviderAccount full graph rollback`
    - fixture:真 PG,请求包含 credentialstore payload 和 channel health init,trigger 拒 `create_provider_account`。
    - 断言:provider_accounts/account_credentials/channel_health_state/admin_audit_events 四处都没有提交。
    - mutation 自检:当前代码至少会留下 provider account,credential failure cleanup 也无法覆盖 audit failure。

13. `adminops EnableProviderAccount audit failure preserves enabled`
    - fixture:seed disabled account,trigger 拒 `enable_provider_account`。
    - 断言:返回 error,enabled 仍 false。
    - mutation 自检:旧代码会 enabled=true 后返回 503。

14. `adminops DeleteProviderAccount audit failure preserves deleted_at`
    - fixture:seed account,trigger 拒 `delete_provider_account`。
    - 断言:返回 error,`deleted_at IS NULL`。
    - mutation 自检:旧代码会 soft delete 后 audit fail。

15. `channelhealth nil signer required policy rolls back manual pause`
    - fixture:真 PG `PostgresStore` required signed policy but signer nil,seed active health row,调用 `ManualPause`。
    - 断言:返回 signer-required error,state 仍 active,无 channel_health_audit_events row。
    - mutation 自检:当前 nil signer 分支会返回 nil 并提交 manual_paused。

16. `channelhealth ledger append failure rolls back state and audit`
    - fixture:真 PG,用 trigger 拒 `audit_ledger_entries` insert 或 fake tx ledger append error,调用会触发 disabled/ramp rollback 的 signal。
    - 断言:返回 error,channel_health_state 保持旧状态,channel_health_audit_events 无新增,ledger 无新增。
    - mutation 自检:如果只插 audit row 后吞 ledger error,状态/audit 会提交。

## 6. 验证命令

基础构建与单测:

```bash
cd backend && go test ./internal/credentialstore ./internal/auth ./internal/credentialworker ./internal/channelhealth ./internal/adminops ./internal/gatewayhttp ./cmd/gateway
```

race 覆盖变更包:

```bash
cd backend && go test -race ./internal/credentialstore ./internal/auth ./internal/credentialworker ./internal/channelhealth ./internal/adminops
```

真 PostgreSQL integration,必须设置 `HUAKAI_DATABASE_URL`:

```bash
cd backend && HUAKAI_DATABASE_URL="$HUAKAI_DATABASE_URL" go test -tags=integration_pg ./internal/credentialstore ./internal/channelhealth ./internal/adminops ./internal/db/admin ./internal/db/...
```

迁移 gate:

```bash
cd backend && HUAKAI_DATABASE_URL="$HUAKAI_DATABASE_URL" go test -tags=integration_pg ./internal/db/admin ./internal/credentialstore -run 'Test.*0051|Test.*CredentialState'
```

全量收尾,不能只用 scoped green 代替 repo green;补救总计划明确要求全量 `go test ./...` [docs/process/plans/2026-05-22-audit-remediation-wave.md:147](docs/process/plans/2026-05-22-audit-remediation-wave.md:147):

```bash
cd backend && go test ./...
```

commit 前 review:

```bash
codex exec review --uncommitted --full-auto
```

## 7. Owner 决策点

1. 审计写失败策略选哪一个?
   - A 同事务 rollback(推荐):mutation 和 audit 一起提交;失败返回 error。
   - B 反向顺序:先 audit intent 后 mutation;mutation 失败时 audit 标 canceled。复杂且会产生"未发生变更的审计"。
   - C W4 式 DLQ pending:mutation 放行,审计进 durable recovery。可用性高,但需要新 recovery 语义和 UI/operator playbook。

2. C-05 schema 采用哪种表达?
   - A 新 `credential_state_changed` event_type + payload old/new(推荐)。
   - B 每个状态一个 event_type,如 `credential_enabled`/`credential_revoked`;schema enum 更大。
   - C 不改 event_type,只把 action/payload 改成 state_transition。会继续让 domain audit event_type 语义弱。

3. admin credential handler 的 admin_audit_events 是否列入 W5 必修?
   - A 列入:admin credential create/rotate/state/delete 也由 adminops 同事务覆盖。
   - B 不列入:credentialstore domain audit 是 W5 canonical audit,admin_audit_events best-effort 暂列 RR-W5-ADMIN-CRED-001。

4. Antigravity/credentialworker audit writer policy 放在哪里?
   - A auth/credentialworker 本地 `AuditWritePolicy`(推荐),不污染 eventbus。
   - B 扩展 W4 `AuditRefPolicy`。名称和职责不匹配,但统一 release mode policy。
   - C 只在 cmd wiring fail-fast,运行期继续 best-effort。不能覆盖测试/错配构造。

5. channelhealth signer nil 行为怎么处理?
   - A production required + runtime fail-closed;dev/test 构造显式 permissive(推荐)。
   - B 所有 PostgresStore 都要求 signer。测试和非生产工具需要集中补 signer。
   - C 保持 nil signer 放行,只靠 wiring 约束。C-10 风险仍存在。

6. 是否允许 W5 引入 DLQ EventKind?
   - A 不引入(推荐):W5 mutation 同事务 rollback,无需新 kind。
   - B 仅复用 W4 `audit_ledger_entry` 处理 channelhealth trust ledger append intent;当前 repo 已有 kind 和 0050 migration [backend/internal/dlq/types.go:18](backend/internal/dlq/types.go:18),[backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql:1](backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql:1)。
   - C 新 `sensitive_audit_mutation` kind。必须先过 §11 schema gate,并定义 replay 幂等和 operator UI。

## 8. 明确不在 W5 范围

- C-06/C-15/C-16 protocol production 注册与投影收口,留 W10 [docs/process/plans/2026-05-22-audit-remediation-wave.md:60](docs/process/plans/2026-05-22-audit-remediation-wave.md:60)。
- C-07/C-08/C-09/C-17/O-3 routing 容量/健康门控/性能,留 W7 [docs/process/plans/2026-05-22-audit-remediation-wave.md:57](docs/process/plans/2026-05-22-audit-remediation-wave.md:57)。
- W6 billing money-path B-01..B-05,不在 W5 改 billing ledger [docs/process/plans/2026-05-22-audit-remediation-wave.md:56](docs/process/plans/2026-05-22-audit-remediation-wave.md:56)。
- W8 usage evidence/provenance GW-03/GW-08/C-11,不在 W5 重写 usage evidence。
- W3 public error model,不在 W5 新建公开错误模型;W5 只避免新增 err leakage。
- W4 audit ledger DLQ worker、`AuditLedgerResult` 三态、completion audit-ref 校验,除 C-10 需要调用既有 AppendInTransaction 外不重构。
- 参考项目源码复查不在本计划执行中;W5 plan 只使用 HUAKAI 内部 docs/code。
- 不改 `LICENSE`、生产 secrets、真实凭据、部署脚本。

## 9. 风险与缓解

| 风险 | 具体失败方式 | 缓解 |
|---|---|---|
| 长事务扩大锁范围 | provider account create 同时做 credential encryption、channel health init、admin audit,可能持锁过久。 | 所有 JSON decode/validation 在 BeginTx 前;无网络 IO 入 tx;credential 加密因 provider_account_id AAD 需要 tx 内执行,但只做 CPU/本地 key 操作;集成测试加 context timeout。 |
| 嵌套事务或 tx 内再 BeginTx | `credentialstore.WithTransaction` 当前要求 db 支持 BeginTx [backend/internal/credentialstore/postgres_store.go:145](backend/internal/credentialstore/postgres_store.go:145);adminops 若用 txStore 调 public Create 可能失败。 | 在 credentialstore 提供 tx-aware 内部 helper或显式 exported `CreateWithStoreDB` 入口;public methods 包事务,adminops 使用 tx-bound helper;用真实 PG 测 CreateProviderAccount full graph。 |
| schema down 静默改写审计语义 | down migration 如果把 `credential_state_changed` 改回 `credential_disabled`,会伪造历史。 | down 迁移仿 0050:存在新 event/action 行则 raise exception 拒回滚 [backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.down.sql:1](backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.down.sql:1)。 |
| gatewayhttp 冻结包继续变大 | 为事务服务在 gatewayhttp 新增文件会违反硬规则。 | 新代码进 `internal/adminops`;gatewayhttp 只改既有 handler 的接口调用,新增测试放非冻结包或追加既有 test 文件。 |
| auth audit fail-closed 影响可用性 | audit DB 抖动导致 refresh token 不更新,上游账号短时不可用。 | 这是敏感凭据旋转的安全取舍;Owner 可选 DLQ/Manual First。默认 fail-closed,并暴露清晰 error/log 指标。 |
| cache 污染 | refresh 成功但 audit 失败后仍 cache 新 access token,造成无审计使用。 | 测试要求 audit 成功前禁止 `populateCache`;mutation 自检删除该顺序会红。 |
| channelhealth nil signer 测试/工具破裂 | 现有 dev/test 直接 `NewPostgresStore` 可能没有 signer。 | 引入显式 policy:dev/test permissive 需要构造时表达;production wiring required。单测改用 memory store或测试 signer。 |
| DLQ 误复用 | 把 ordinary admin/credential audit row 当 `audit_ledger_entry` 重放会污染 W4 ledger intent 语义。 | 默认 no-DLQ;若 Owner 选 DLQ,必须新 EventKind + replay contract,不能偷用 ledger kind。 |

## 10. 时间估计

- 计划/Owner 决策收敛:0.25d。
- Commit 1 schema gate + migration tests:0.5d。
- Commit 2 credentialstore transaction + state transition:0.75d。
- Commit 3 adminops transaction service + handler rewire + PG tests:1.0d。
- Commit 4 auth/credentialworker audit writer policy:0.5d。
- Commit 5 channelhealth signed audit policy + PG tests:0.5d。
- Full suite/race/review/修复:0.5d。

合计:约 3.5 codex-day。若 Owner 选择 no-schema/no-admin-credential-sweep,可压回约 2.5d;若选择 DLQ EventKind,至少增加 0.5-1.0d 和一次 schema/replay review。

## 11. Schema gate

W5 默认需要一条 schema migration,只为 C-05 修审计语义:

- 新 `credential_audit_events.event_type='credential_state_changed'`。
- 新 `admin_audit_events.action='update_account_credential_state'` 或 Owner 选择的同义稳定 action。
- 不新增表、不新增列、不新增 DLQ kind。

硬前置:

1. Owner 先确认 §7.2 的 C-05 表达和 action 名称。
2. 执行前确认当前最高 migration 是 0050,且 0051 未被占用;当前 repo 已有 0050 audit ledger DLQ kind [backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql:1](backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql:1)。
3. up migration 只 drop/re-add CHECK,把新 enum 加入现有白名单;现有白名单位置是 `credential_audit_events_event_type_check` [backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql:105](backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql:105) 和 0049 admin action check [backend/sql/migrations/0049_admin_audit_pool_group_action.up.sql:8](backend/sql/migrations/0049_admin_audit_pool_group_action.up.sql:8)。
4. down migration 若存在新 event/action 行,必须 raise exception,不得自动改写为 `credential_disabled` 或 `disable_account_credential`。
5. C2/C3 实现前必须先跑 migration integration test;否则任何 `credential_state_changed` insert 会被 CHECK 拒绝。

若 Owner 选择 DLQ EventKind,另开 D2 gate:

- 禁止复用 `audit_ledger_entry` 存 ordinary admin/credential audit row;该 kind 当前语义是 trust ledger append intent。
- 新 kind 必须定义 lane、replica status、idempotency key、payload schema、down guard、worker retry/MarkFailed 契约,并先写真 PG replay 测试。

## 12. 执行前清单

- 确认不读取 `docs/process/plans/2026-05-23-w5-audit-atomicity-claude.md`。
- 确认 Owner 对 schema gate、DLQ/no-DLQ、admin credential sweep 给出选择。
- 确认 `HUAKAI_DATABASE_URL` 指向可迁移的临时/测试 PG,不是生产库。
- 确认 `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto` 不新增文件。
- 每个测试先按 mutation self-check 设计 fixture,不写"只证明能跑"的测试。
- 每个 commit 前 run scoped tests + full affected package;W5 收尾必须 run full suite。

## 13. Clean-room 声明

本计划只读取 HUAKAI 内部 docs、当前工作树源码和 `git show 336fc87` 的本仓提交信息。未读取、引用或复制任何非 MIT 参考项目源码;没有实现代码、schema、UI 或算法来自外部参考项目。W5 后续若需要对照参考项目,必须按 clean-room lane guard 另开 specifier/reviewer lane。
