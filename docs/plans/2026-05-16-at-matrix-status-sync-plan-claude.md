# AT Matrix Status Sync Plan (Claude Lane Plan)

| Owner directive | "docs/11_ACCEPTANCE_TEST_MATRIX.md AT status sync ... 分一个 codex 出来做" — Owner 2026-05-16 |
| --- | --- |
| Scope | In: `docs/11_ACCEPTANCE_TEST_MATRIX.md` Status 列 + Evidence column edit for AT 已 commit + test PASS. Out: 不动 spec / production code / 不加新 AT row / 不动其他 doc |
| Success criteria | (a) AT-AUTH-007-001..010 + AT-SESSION-001-001..008 + AT-CH-002-001..013 + AT-BILL-002-001..010 status 从 Planned → PASS or COVERED + Evidence 引 commit SHA + test file:line; (b) AT-AUTH-007-011 (cross-user refresh) 新加 row 标 PASS + evidence; (c) 跨 spec AT 没乱改 (AT-FP-001 / AT-PACE-001 / AT-NET-001 / AT-ADV-001 / AT-TRUST-001 / AT-PRIV-001 / AT-AUDIT-001 留 Planned, 因 spec done impl 未做) |
| Time estimate | 30-60 min codex lane |
| Blast radius | Very low (doc only); 不动 production / spec / migration |
| Failure modes | (a) 误标 AT (实际 weak coverage 但 codex 标 PASS); (b) commit SHA / test file:line 引错; (c) 跨 spec AT 误改 (改 reasoning-stage Planned 的不该改); (d) clean-room (codex 不 read sub2 source); (e) /tmp 爆 (但 read-only spec doc edit, 不占 /tmp 大) |
| Mitigations | codex 用 read-only 大部分 + 仅 Edit matrix file; success criteria 限定 4 wave AT 列表 + 1 个 new row, 不 touch 其他 AT; AT-AUTH-007-011 evidence 引 backend/internal/usersession/rotation_test.go + session_handler.go cross-user test; clean-room 提醒 codex; ≤3 codex 并行 (当前 2: F-PRIV + F-AUDIT, +1 sync = 3 OK) |
| Decision points | Stop if codex 想 modify spec doc (out of scope) 或加新 AT row (除 011 之外) |
| Pre-execution checklist | (a) 当前 worktree 仅有 2 plan draft pending (F-PRIV / F-AUDIT Claude draft 已 commit-ready); (b) 全 backend build/test PASS (158c421 已 verified); (c) ≤3 codex; (d) plan artifact 写 (本文件) |

## Concrete Execution Order

1. 本 plan artifact 写完 (本文件)
2. Codex prompt 写 (`/tmp/codex-at-matrix-status-sync.txt`)
3. Codex lane dispatch (read matrix + 4 wave test file + commit log → Edit Status 列 + Evidence column)
4. Wait codex done
5. Claude verify Edit (no spec / no new row 除 011)
6. Commit + push (跟 F-PRIV + F-AUDIT synthesis 一起或独立, by 时序)

## Clean-Room Guard

- Codex lane 不 read 上游项目源码
- 仅 Edit matrix Status + Evidence 列, 不 modify spec body / row 名 / AT 编号
- 引 evidence 仅 HUAKAI 内部 commit SHA + test file:line

## 中文摘要

AT matrix status sync plan artifact. Owner 选分 1 codex lane 做 (跟当前 F-PRIV + F-AUDIT codex 并行, 3 codex 合规). 任务范围: 4 wave 已 commit + test PASS 的 AT 标 PASS + Evidence 列 commit SHA + test file:line. 排除: 已 commit spec 但未 impl 的 AT (留 Planned). 1 个 new row: AT-AUTH-007-011 (cross-user refresh). 低风险 doc only, 不爆 /tmp. 30-60 min codex.
