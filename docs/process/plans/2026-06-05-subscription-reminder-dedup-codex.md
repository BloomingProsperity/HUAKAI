# 2026-06-05 subscription reminder dedup codex plan
| Owner directive | "订阅到期/低余额提醒 先发邮件后 RecordReminder... 改顺序: 先 claim 再发" |
| Scope | In: HUAKAI-local subscription reminder send/record ordering, focused tests, comments. Out: reference source, reminder contents, trigger windows, notify low-balance behavior, frozen packages, schema changes. |
| Success criteria | Two concurrent ReminderService instances sharing the same store can race the same subscription tier and only one sends. Existing reminder behavior still passes. Requested backend build/vet/test gates pass. Commit is created locally and not pushed. |
| Time estimate | 45-75 minutes wall clock, one Codex worker. |
| Blast radius | `backend/internal/subscription` reminder behavior. Failure could suppress a retry after a send failure; this is explicitly accepted as at-most-once for this task and is documented in code/tests. |
| Failure modes | Test fixture may not expose the send-first race; mitigate with a blocking mailer that waits for both concurrent sends. Claim may be recorded with `sent` before a failed send; mitigate by treating the durable row as an at-most-once attempt and leaving failure as a returned tick error/log path rather than deleting the claim. Existing tests expecting retry-on-failure may need intentional update to the new requirement. |
| Decision points | No Owner sign-off expected unless the durable unique key cannot be found, a frozen package must be changed, a schema change becomes necessary, or a high-risk auth/billing/quota/payment file is implicated. |
| Pre-execution checklist | 1. Confirm only HUAKAI repo is read. 2. Locate `ReminderService` send and `RecordReminder`. 3. Add discriminating concurrent test before production edit. 4. Run the focused test and observe failure. 5. Move claim before send and document at-most-once choice. 6. Run gofmt, build, vet, focused tests. 7. Commit. 8. Mutate order back locally, prove test fails, then `git checkout` only the mutated file back to committed state. |

## Concrete execution order

- Add a unit test in `backend/internal/subscription/reminder_test.go` using two `ReminderService` instances, one shared `memoryStore`, and a mailer that blocks until two send attempts occur. Under the old send-first order both goroutines enter the mailer and the test fails with two sends.
- Update `backend/internal/subscription/reminder.go` so non-empty-recipient reminders call `RecordReminder` before rendering/sending. If `RecordReminder` returns `inserted=false`, skip the mailer. If the mailer returns `ReminderRetry` or `ReminderSkippedUnconfigured`, return a non-fatal error so the worker records the failed tick while keeping the claim as an at-most-once attempt.
- Adjust tests that encoded the old retry-after-failure behavior to the new at-most-once requirement without changing reminder content or tier selection.
- Run the requested backend gates with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`.
- Commit locally with the requested co-author trailer, then run the mutation check and restore the mutated file with `git checkout --`.
