# W5 计划（Claude lane）—— audit 原子化敏感变更审计

## 1. 目标

W5 修敏感变更与 audit 写入的非原子性:credentialstore 凭据生命周期 / channelhealth 状态变化 / antigravity OAuth refresh / gatewayhttp admin pool 增改,五处 audit insert 失败被静默 `_ =` 忽略 → mutation 已提交但无可靠审计 → 违反「商家不能做假」信任链原则(project_core_trust_chain_differentiator)。

W5 依赖 W4 fail-closed 契约(per `docs/process/plans/2026-05-22-audit-remediation-wave.md` line 73-74),沿用 W4c 设计语义但目标不同:W4 是 settle 时缺账本引用拒,W5 是 audit 写失败时变更拒/回滚。

## 2. Findings 闭合映射

- GW-10(HIGH,gatewayhttp):`admin_pools_handler.go:122-140`(create) + `:217-235`(update);`admin_pool_accounts_handler.go:188`,`:243`;`InsertPool`/`UpdatePool` 与 `writeAdminPoolAudit` 两步非同事务,audit 失败时 handler 503 但 pool 已落,客户端重试 → 重复 pool。P2 commit `336fc87` 已含租户隔离与审计落表(`admin_audit_events` + 迁移 `0049`),GW-10 同事务化随 W5 落。
- C-03(HIGH,auth):`antigravity_token_provider.go:203` + 多处 `_ = p.writeAudit(...)`;refresh 成功旋转后 audit nil-permissive(`NoopAuditWriter` 至 `audit.go:28`),生产 audit DB 短暂失败时 token 已旋转无追溯。
- C-04(HIGH,credentialstore):`postgres_store.go:229`(create),`:308`(rotate),`:462`(delete),`:617`(refresh success),`:655`(refresh failure) 全 `_ = s.InsertAuditEvent(...)`;line 958 `InsertAuditEvent` 在 store/db 缺失时返回 nil 静默 no-op。
- C-05(MED,credentialstore):`postgres_store.go:402` `SetState` 接受任意状态,`:429` audit 事件类型固定 `credential_disabled`;revoked → active 也写 disabled,审计语义错误。
- C-10(MED,channelhealth):`store_postgres.go:295` `AppendAudit` signer == nil 直接 nil;`:28` `NewPostgresStore` 默认无 signer;生产漏 signer 时通道禁用/ramp/manual pause audit 不签名。

## 3. 切片（3 commit,同 W4 节奏 + 收尾）

### C1: credentialstore 凭据生命周期审计同事务

- `backend/internal/credentialstore/postgres_store.go`:把 `InsertAuditEvent` 与 mutation(create/rotate/delete/refresh) 包一个 sqlc `WithTx`;production audit 失败 → tx 回滚 + 返回 typed error。
- 同步修 C-05:加 `actionForStateTransition(old, new state)` 函数,按 state 迁移分类(`credential_state_activated` / `credential_state_disabled` / `credential_state_revoked` / `credential_state_attention` 等);`SetState` 调用此函数生成 event 类型,不再固定 `credential_disabled`。
- 测试:5 风险测试 + mutation 自检。

### C2: channelhealth signer 强制 + auth refresh 审计 fail-closed

- `channelhealth/store_postgres.go`:`AppendAudit` 在 production policy + signer == nil 时返回 typed error(而非 nil no-op);`NewPostgresStore` 加 production 模式 signer required 校验。
- `auth/antigravity_token_provider.go`:多处 `_ = writeAudit` 改为 `if err := writeAudit(...); err != nil` + production fail-closed(refresh 不旋转);`audit.go` 的 `NoopAuditWriter` 加 production-禁用守护或仅 test build tag 可用。
- `cmd/gateway`:启动期 channelhealth signer / antigravity audit writer 注入校验,缺时 fail-fast。
- 测试:4 风险测试 + mutation 自检。

### C3: gatewayhttp admin pool 增改审计同事务（冻结包,只改既有 2 文件）

- `admin_pools_handler.go`:createPool / updatePool 把 `InsertPool`/`UpdatePool` + `writeAdminPoolAudit` 包一个 tx(sqlc `WithTx`);tx 失败时回滚 + 503 + structured ERROR。
- `admin_pool_accounts_handler.go`:同模式。
- 测试:追加既有 `_test.go`(frozen 不加新文件),2 风险测试 + mutation 自检。

### W5 收尾:docs W5 收尾对照参照项目

- `docs/process/research/2026-05-24-w5-ref-recompare.md`(参考 LiteLLM / Portkey / Helicone / sub2api 等的 audit 写入原子性 + 凭据 audit 同事务模式)。
- 3 plan 文件(claude/codex/synthesis) + 1 research 文件入 docs commit。

## 4. 冻结包合规

- credentialstore / channelhealth / auth / `cmd/gateway`:非冻结,可加新文件。
- gatewayhttp:冻结,C3 只改既有 `admin_pools_handler.go` 与 `admin_pool_accounts_handler.go`(W4 已确立惯例)。
- gateway / proto:本切片不触。

## 5. 关键设计（D 决策待 Owner 拍）

### D1: audit-write fail-closed 时 mutation 怎么处理

a) 同事务(sqlc `WithTx`) + audit insert 失败回滚 mutation(推荐,干净的不变量)。

b) 先 audit 后 mutation(反向序列,audit 不可逆但 mutation 可控)。

c) mutation success + audit-DLQ pending(复杂,引入 reconciliation 路线图)。

### D2: audit-write 失败 DLQ 入队

a) 复用 `audit_ledger_entry` `EventKind` + 扩 payload 标记(但 W4c D3 schema gate 已证明 payload decoder `PreparedEntry`-only,这里需要 forward-compatible 包装)。

b) 新 `EventKind`(`credential_audit_event` / `channel_health_audit_event` / `admin_audit_event`) + DLQ CHECK migration。

c) 不入 DLQ,仅结构化 ERROR + RR-W5-001 路线图(同 W4c fallback)。

### D3: SetState 事件分类（C-05）

a) action 字段 enum 扩(`credential_state_activated` / `disabled` / `revoked` / `attention`)。

b) 新 `event_type` 字段细分。

c) 复合(action = `credential_state_changed` + `state_transition` jsonb 字段)。

### D4: production signer 强制（C-10,C-03）

a) 启动期 fail-fast(`cmd/gateway` 启动校验 signer/audit writer != nil,prod 不通则 panic)。

b) 运行期 `AppendAudit` / `writeAudit` fail-closed(运行期发现 nil 拒绝)。

c) 两者都做(启动期校验 + 运行期 fallback)。

## 6. 风险测试（≥10 条,每条 mutation 自检,判别 fixture）

- T1:credentialstore Create + `InsertAuditEvent` fail(mock DB error) + production policy → tx 回滚,凭据未持久化。
- T2:`SetState` revoked → active → event type = `credential_state_activated`(不是 `credential_disabled`);discriminating:翻 old/new state 顺序应得 disabled。
- T3:`SetState` active → revoked → event type = `credential_state_revoked`。
- T4:channelhealth signer == nil + production + `AppendAudit` → typed error(mutation:signer == nil 时 return nil 旧逻辑 → fixture 通过,测试变红)。
- T5:channelhealth signer != nil + production + `AppendAudit` → 正常签名追加。
- T6:antigravity refresh + `writeAudit` err(mock) + production → refresh fail-closed,token 不旋转。
- T7:antigravity refresh + `writeAudit` err(mock) + dev → refresh 成功(dev 豁免)。
- T8:admin pool create + `InsertPool` ok + `writeAdminPoolAudit` fail(production) → tx 回滚 + 503 + ERROR。
- T9:admin pool update + 同上。
- T10:admin pool create + 两步全 ok → 200 + audit 行已写。

## 7. 验证

- `cd backend && GOCACHE=$HOME/.cache/go-build go build ./...` 退出 0。
- 改动包 race + count=1:credentialstore / channelhealth / auth / gatewayhttp / `cmd/gateway`。
- 全量 `go test ./...` 退出 0。
- 每个新测试 mutation 自检,codex per-commit review 无 S0/S1。
- 关键:integration test 真 PG(per memory `feedback_full_suite_verification` + `risk_based_testing` 真依赖)。

## 8. 范围外

- C-06(Azure `mock_token_endpoint` payload,MED) — 留 W10(protocols 生产协议注册)。
- C-08(Serializable slot acquire 无 retry,MED) — 留 W7(routing 容量收紧)。
- C-09(channel health fail-open,HIGH) — 留 W7。
- W4a-4 P2 ② `[DONE]`/`message_stop` 定稿 — 独立切片。
- DLQ reconciliation 机制本体 — 留 RR-W4-001 + RR-W5-001(若 D2 选 fallback)。

## 9. 风险

- R1:gatewayhttp 冻结包测试加在哪 — 同 W4c 走既有 `_test.go` 加 cases,不加新文件(`admin_pools_handler_test.go` 已存)。
- R2:sqlc `WithTx` 对 credentialstore 影响面 — `InsertAuditEvent` + Create/Rotate/Delete/Refresh 5 处都得改,大手术;各处 DB roundtrip 数变化可能影响性能,需要 race test 验证。
- R3:production signer/audit-writer required 校验 — 现有 dev/test 环境若无 signer 会 fail-fast,需要 dev 默认 noop writer 与 production 启动校验分离。
- R4:P2 commit `336fc87` 已合的部分不能重写,只补 GW-10 同事务化 — 必须 grep 已存代码确认不撞 commit `336fc87` 的 `admin_audit_events` 写入逻辑。
- R5:同事务 audit insert 失败回滚会让客户端看到 503 — 与 W4c 客户端语义对齐(Owner D2 锁定的 abort+500 pattern)。

## 10. 时间估

计划合成:30-45 min。实施(3 commit):4-6 h,其中 C1 credentialstore 90-120 min,C2 channelhealth+auth 60-90 min,C3 gatewayhttp 45-60 min,测试+mutation 60-90 min,codex review 60-90 min。W5 整波 2.5d ≈ 18-20 h 含 Owner 决策等待 + reviews。
