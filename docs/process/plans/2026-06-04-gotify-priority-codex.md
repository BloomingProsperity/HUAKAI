# 2026-06-04 gotify-priority-codex

| Owner directive | "Gotify 投递目前硬编码 priority=5。补齐成可配置 GotifyPriority(默认 5,范围 0-10),让用户能定告警紧急度。" |
| --- | --- |
| Scope | In: HUAKAI-owned `backend/internal/notify` settings/type/store/notifier/HTTP handler/tests, unlanded migration `backend/sql/migrations/0089_user_notification_settings.up.sql`, and `docs/openapi/openapi.yaml` schema field. Out: `/home/ubuntu/refs`, reference-source reading, git commit, new runtime dependencies, frozen-package new files, auth/billing/quota core changes, landed migration creation. |
| Success criteria | Gotify settings can carry `gotify_priority`; missing request value defaults to 5; Gotify delivery sends configured priority; gotify priority outside 1..10 is rejected for active gotify settings; default `none` remains inert; requested backend gate passes or blockers are reported. |
| Time estimate | 30-60 minutes wall clock, one Codex work unit. |
| Blast radius | Low-to-medium: additive field in an unlanded notification settings table and local notification JSON payload. Existing non-Gotify notification paths should remain unchanged. |
| Failure modes | Hardcoded priority remains: discriminating delivery test expects 8, not 5. Range validation missing: validation test expects `ErrInvalidSettings` for 99. HTTP default missed: handler test expects omitted `gotify_priority` to save/respond with 5. Store SQL mismatch: run build/tests and requested gate. |
| Decision points | None expected after Owner's explicit PM spec. Stop if implementation would require reading `/home/ubuntu/refs`, adding a dependency, touching real secrets, changing auth/billing/quota core, destructive commands, or creating a new landed migration. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Confirm `/home/ubuntu/refs` is not read. 3. Check and claim coordination locks. 4. Read HUAKAI-owned `internal/notify` flow. 5. Write discriminating tests first and verify red. 6. Implement minimal additive field. 7. Run requested gate. |

## Clean-room note

Codex implementer lane uses only the Owner/PM specification and HUAKAI-owned code. Reference-source inspection is explicitly prohibited for this work unit.
