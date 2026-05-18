# HUAKAI 多窗口并行开场 prompt (2026-05-18)

**当前生效**: 只开 **窗口 2** (R-4 L3 Rust, 零冲突). 窗口 3/4 排队
等 F-COMM-001 impl Phase 1 (bram417j8) 完 + migration 号确定后再开.

Owner: 开新 Claude Code conversation, 复制对应窗口整段贴进去即可开跑.
窗口之间文件互不冲突 (按目录隔离). main.go merge 由当前 PM 窗口 (窗口 1)
统一处理.

当前 git 状态: branch `claude/phase-1`, HEAD = `2d1dc84`.
已 push: F-AUDIT-1-A/B/C + R-3-A-fix-2/3/4-deeper (4 vendor wire 全 PASS).

---

## 窗口 1 (PM, 本): 我自己

跑 F-COMM-001 impl Phase 1 (codex background) + receive review 闭环 + main.go merge 协调 + push.

---

## 窗口 2: R-4-A L3 device fingerprint (Rust)

> 复制下面整段到新 conversation:

你是 HUAKAI 多窗口并行的 **窗口 2** — Rust 反封禁 L3 device fingerprint impl.

工作目录: `/home/codex/HUAKAI`, branch `claude/phase-1`. HEAD 已含 R-3-A-fix-4-deeper.

**任务范围 (1 sub-phase, 2-3 天)**:
- 只动 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/fingerprint/` (新目录)
- 实现 TLS+HTTP/2 canonical fingerprint hash (复用 R-3-A 已抓的 ClientHello)
- 新增 module `fingerprint::canonical_hasher` + `fingerprint::types`
- 单测 `AT-FP-001-001..005` 覆盖 5 vendor profile 的 stable hash

**spec**: `docs/specs/device-fingerprint-binding.md`
**plan**: `docs/plans/2026-05-17-rust-r4-r-7-l3-l6-impl-claude.md` (L3 部分)

**硬约束**:
- 不读 rquest / curl_cffi / wreq / utls / chrome-impersonate / fingerprint-collector source
- 不动 backend/ Go / vendor/boring / 已落 mimicry 主干
- 不动 docs/specs (其他窗口在动) — 写 plan 可
- 不动 backend/cmd/gateway/main.go (PM 窗口 1 协调)
- 注释中文
- 不提交不 push — 写完 surface diff 给 Owner, Owner 审完叫我 commit + push

**完成报告**:
- 改动文件 list
- `cargo test -p core_gateway --features mimicry-boring` 结果
- 残留 ≤ 400 字中文
- Source files read / Lane = implementer / Agent / UTC

---

## 窗口 3: F-AUDIT-1-D 老板 dashboard (Go backend)

> 复制下面整段到新 conversation:

你是 HUAKAI 多窗口并行的 **窗口 3** — F-AUDIT-1-D 老板 dashboard impl.

工作目录: `/home/codex/HUAKAI`, branch `claude/phase-1`. HEAD 已含 F-AUDIT-1-A/B/C 全.

**任务范围 (3 天)**:
- 新 migration `0035_audit_dashboard_views` 加 4 只读 view (refund_rate /
  mismatch_pending / top_cost_users / pricing_changelog)
- 新 `backend/internal/gatewayhttp/admin_audit_handler.go` 5 admin endpoint:
  - GET /admin/audit/refund-rate
  - GET /admin/audit/mismatch-pending
  - GET /admin/audit/top-cost-users
  - GET /admin/audit/pricing-changelog
  - GET /admin/audit/health
- 所有 endpoint 经 `admin.RolePlatformAdmin` guard
- AT-AUDIT-001-035..040 单测
- OpenAPI 加 5 endpoint
- backend/cmd/gateway/main.go 只加 admin route, 不动其他 route wire (PM 窗口 1 协调 merge)

**spec**: `docs/specs/user-consumption-transparency.md` §7

**硬约束**:
- 不读 sub2api / new-api / litellm 等参考反代 source
- 不动 frontend / Rust / vendor/boring
- 只动 `backend/internal/admin/` 和 `backend/internal/gatewayhttp/admin_audit_*.go`
- 不动 `backend/internal/audit/` 已落代码 (只调 API)
- 不动 `backend/internal/community/` (F-COMM-001 在动)
- 注释中文
- 不提交不 push — 写完 surface diff 给 Owner

**verify**:
- `cd backend && GOCACHE=/tmp/go-cache go build ./...` PASS
- `cd backend && GOCACHE=/tmp/go-cache go test ./internal/gatewayhttp/... ./cmd/gateway -race -count=1 -timeout 180s` PASS

**完成报告**:
- 改动文件 list / test PASS 列 / 5 endpoint curl 示例
- 残留 ≤ 400 字中文
- Source files read / Lane / Agent / UTC

---

## 窗口 4: F-TRUST-1-C 用户验签 4 endpoint (Go backend)

> 复制下面整段到新 conversation:

你是 HUAKAI 多窗口并行的 **窗口 4** — F-TRUST-1-C 用户验签 4 endpoint impl.

工作目录: `/home/codex/HUAKAI`, branch `claude/phase-1`. HEAD 已含 F-TRUST-1-A schema +
F-TRUST-1-B append-only + ed25519 signer.

**任务范围 (3-5 天)**:
- 新 `backend/internal/auditledger/reader.go` 加只读 method (GetEntry / GetChainHead)
- 新 `backend/internal/gatewayhttp/ledger_handler.go` 4 endpoint:
  - GET /v1/ledger/entries/{request_id}
  - POST /v1/ledger/verify
  - GET /v1/ledger/pubkey
  - GET /v1/ledger/chain-head
- 跨 tenant guard (session.tenant_id 不匹配 → 404 避免存在性 oracle)
- verify endpoint body ≤ 10KB
- pubkey + chain-head 公开 (不 auth)
- AT-TRUST-001-007..012 (6 test)
- OpenAPI 加 4 endpoint
- backend/cmd/gateway/main.go 只加 ledger route (PM 窗口 1 协调 merge)

**spec**: `docs/specs/trust-chain-user-verifiable-ledger.md` §4

**硬约束**:
- 不读 sub2api / sigstore / certificate-transparency / trillian 源码
  (可读 Sigstore 公开 spec doc)
- 不动 frontend / Rust / vendor/boring
- 不动 `backend/internal/audit/` (F-AUDIT-1-D 在动)
- 不动 `backend/internal/community/` (F-COMM-001 在动)
- 不动 `backend/internal/auditledger/` writer 路径 (只加 reader.go)
- 注释中文
- 不提交不 push — 写完 surface diff 给 Owner

**verify**:
- `cd backend && GOCACHE=/tmp/go-cache go build ./...` PASS
- `cd backend && GOCACHE=/tmp/go-cache go test ./internal/gatewayhttp/... ./internal/auditledger/... ./cmd/gateway -race -count=1 -timeout 180s` PASS

**完成报告**:
- 改动文件 list / 6 AT PASS 列 / 4 endpoint curl 示例
- 残留 ≤ 400 字中文
- Source files read / Lane / Agent / UTC

---

## 协调约定

1. **谁先 commit 谁先 push**. 后到的窗口 `git pull --rebase` 处理 main.go 顺序冲突.
2. **main.go merge 冲突**: 各窗口写完不 push, surface diff 给 PM 窗口 1 (我). 我合并 + push.
3. **禁强推**. 全部 fast-forward push.
4. **codex review --uncommitted** 每窗口完后必跑, address all P1 才 surface 给 Owner.
5. **memory `feedback_tmp_quota_prevention`**: 4 窗口 ≤ 3 codex 同时跑 — 错峰派 codex.
6. **memory `feedback_codex_exec_stdin_redirect`**: 所有 `codex exec` 末尾 `< /dev/null`.
