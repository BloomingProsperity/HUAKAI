# 2026-05-26 hermes Slice 2.3 Codex execution

| Field | Content |
| --- | --- |
| Owner directive | "你是 Slice 2.3 hermeshttp conversation/message CRUD executor... Gateway 直读 PG... 补完整 CRUD" |
| Scope | Implement Hermes conversation list/get/delete and message list via Gateway service/PG; add audit action and CHECK migration; add discriminating tests. Out of scope: frozen package new files, hermes-runner Python, hermeschat changes, non-MIT upstream source reads, git add/commit. |
| Success criteria | `/conversations` and `/conversations/{id}/messages` no longer proxy to runner; `GET /conversations/{id}` and `DELETE /conversations/{id}` are routed; owner/tenant/deleted semantics match Owner request; audit action works in service and DB CHECK; targeted and requested full Go verification run. |
| Time estimate | 1 focused Codex session; wall-clock depends mainly on Go race tests and local PG migration availability. |
| Blast radius | `backend/internal/hermes`, `backend/internal/hermeshttp`, `backend/internal/db/hermes` tests, and `backend/sql/migrations` only. New files are not added to frozen packages. |
| Failure modes | Weak owner-isolation tests; handler returning 403 instead of non-enumerating 404; delete audit outside transaction; migration CHECK rollback mismatch; local PG unavailable. Mitigation: write discriminating tests first, mirror profile audit tx pattern, inspect 0058 CHECK before adding 0059, report any unavailable verification honestly. |
| Decision points | No new Owner decision expected. A new 0059 CHECK migration is required because 0058 does not allow `hermes.conversation.delete`; this is within the explicit task. |
| Pre-execution checklist | Read CLAUDE.md, AGENTS.md, synthesis plan; inspect current handlers/service/store/sqlc/migrations/tests; write RED tests; implement scoped patch; run targeted then full checks; run `codex exec review --uncommitted` without staging because Owner requested no `git add`; report diff stat and evidence. |
