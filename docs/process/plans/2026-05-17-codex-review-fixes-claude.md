---
plan_id: 2026-05-17-codex-review-fixes-claude
lane: claude (PM)
status: drafted
triggers:
  - codex review b02lrm0rq (uncommitted F-AUDIT-1-A + R-3-A-fix-2-deeper) 报 4 P1 + 1 P2 阻塞
utc: 2026-05-17T14:25:00Z
---

# Codex Review 修复批 — Claude 计划

## 0 缘起

Codex review (per CLAUDE.md #8 per-commit review) 在 staged audit + Rust 改动上跑出 5 个问题:
P1 #1/#2/#3/#4 + P2 #5. P1 都是 commit 前必须修的 HIGH severity. 本 plan 仅记录修复路径,
执行让 codex executor 跑 (Claude 自己只动 §4 中 Claude plan §7 的占位说明).

## 1 待修问题清单 (from codex review b02lrm0rq)

| 编号 | 文件 | 严重 | 内容 |
|---|---|---|---|
| P1 #1 | vendor/boring/boring-sys/patches/boring-pq.patch:208-225 | HIGH | R-3 deeper 把 HUAKAI strict_mode 行加进 boring-pq.patch context, 让 fresh BoringSSL 上 patch 失败 |
| P1 #2 | backend/internal/audit/receipt_formatter.go:360 | HIGH | receiptInputsSQL 用 logical_request_id JOIN, 跟 audit_ledger_entries.request_id 语义不对齐 → 普通请求 receipt 派生失败 |
| P1 #3 | backend/sql/migrations/0028_user_cost_receipts.up.sql:6 | HIGH | request_id UUID NOT NULL 拒绝 chi text 形式 (e.g. req-t10-e2e), 跟 ledger TEXT 不对齐 |
| P1 #4 | docs/process/plans/2026-05-17-f-comm-001-invitation-referral-spec-claude.md:108-112 | HIGH | Claude plan §7 uncited Sub2API claims, 违 CLAUDE.md #12 first-cite 必读 source 规则 |
| P2 #5 | backend/internal/audit/receipt_formatter.go:262 | MED | JSON 字段 cost_total_microcents 但实际值是 micro-USD 单位 (1 USD = 10^6 micro-USD, ≠ microcents) |

## 2 修复负责人

- P1 #1 / P1 #2 / P1 #3 / P2 #5: **codex executor** (sub fix-A/B/C/D, 1 dispatch)
- P1 #4: **Claude 本人** (Claude plan 是 Claude 写的, 自己修占位说明, 不动 codex 实施层)

## 3 codex executor 任务 (单 dispatch)

Sub 拆分见 `/tmp/codex-r3-deeper-fix-and-audit-fix.txt`:
- fix-A (45 min): boring-pq.patch 撤 HUAKAI 上下文
- fix-B (1 hr): receipt JOIN audit_request_id 关联 (可能要 migration 0029)
- fix-C (30 min): migration 0028 request_id TEXT
- fix-D (15 min): cost 字段名 → cost_total_micro_usd
- fix-E (15 min): go build + go test + cargo check + cargo test verify

总 2.75 hr. 完了再 commit (单 commit 或拆 2 commit 看 codex 选).

## 4 Claude 自修 P1 #4 (已完成)

Edit docs/process/plans/2026-05-17-f-comm-001-invitation-referral-spec-claude.md §7 表格:
- 表头从 "Sub2API 做法 (paraphrase)" 改 "Reference 普遍做法 (占位, 待 codex 调研重写)"
- 表内每行 reference 列改占位文字 "(待 codex 读 source 确认)"
- 表前加 importance note: Claude 二手认知占位, 不构成 source-backed 评估, codex 写 spec 必须读 source 重写并加 cite

(已 Edit, 见 docs/process/plans/2026-05-17-f-comm-001-invitation-referral-spec-claude.md 当前版本.)

## 5 验证 + 后续

修完跑:
- `cd backend && GOCACHE=/tmp/go-cache go build ./...` + `go test ./internal/audit/... -race -count=1 -timeout 120s`
- `cd exploratory/rust-core-gateway/merged && CARGO_TARGET_DIR=$HOME/.cargo-target cargo check -p core_gateway --features mimicry-boring`
- `... cargo test --features mimicry-boring --lib` 看 4 vendor wire 状态

全 PASS 后再 codex review 一遍 (CLAUDE.md #8), 直到无 HIGH. 然后才 commit.

## 6 跟 Owner 的 commit 顺序

Owner 直令 "等 3 codex 完后一次性改" 指 bulk rewrite 23 老 commit. 这批 fix commit 是 NEW
convention 直接合规, 不进 rewrite 范围. 流程:
1. 修这 5 个问题 (P1+P2)
2. 重跑 codex review 直到 PASS
3. commit (Conventional Commits 新格式)
4. R-3 deeper + F-AUDIT-1-A + F-COMM-001 spec 全部 commit 落地后
5. 才做 23 commit bulk rewrite + force push

## 7 风险

- **R-FIX-001**: fix-B 可能要加 migration 0029 (billing_events.audit_request_id 列), 影响生产 schema. Mitigation: 让 codex 加 migration 时 down 也写, 复用 0027 trigger 模式不引新.
- **R-FIX-002**: 修 boring-pq.patch 可能误把 R-3 deeper 真正想要的 strict_mode 行删了. Mitigation: HUAKAI strict_mode 行该作 vendored deps/boringssl/ 直接编辑保留 (因为 deps/boringssl/ 是我们的 fork), boring-pq.patch 只管 post-quantum.
- **R-FIX-003**: cargo build 可能因 disk quota 跪. Mitigation: TMPDIR=$HOME/tmp CARGO_TARGET_DIR=$HOME/.cargo-target.
