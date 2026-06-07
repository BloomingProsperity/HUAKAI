# 2026-06-07 W2 alert delivery quick win

| Field | Plan |
| --- | --- |
| Owner directive | "TASK: W2 quick-win — wire alert DELIVERY to the existing notify.Notifier on the FIRING EDGE. Branch fix/qw-alertdeliver. HUAKAI-internal (NO reference reads). EDIT-only, NON-frozen (internal/alerting + internal/notify + cmd/gateway/wiring.go). No migration. No shortcuts." |
| Scope | In: `backend/internal/alerting`, `backend/internal/notify`, `backend/cmd/gateway/wiring.go`, and focused unit tests. Out: migrations, frozen packages, reference-project source reads, auth/billing/quota core changes, commits. |
| Success criteria | A newly created firing alert event triggers exactly one best-effort notification through the existing notifier path; repeated still-firing evaluations do not notify; active silences suppress delivery; nil deliverer is safe; notify alert firing dispatches through configured channels; required Go build/vet/test commands pass or any blocker is reported honestly. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus local verification. |
| Blast radius | Medium: alerting store interface signature changes affect memory/postgres stores and evaluator callers. Notify payload additions affect notification transport tests only; no DB schema or runtime dependency changes. |
| Failure modes | Store newness calculation could misclassify repeat firings; mitigate with discriminating edge test. Delivery could bypass silences; mitigate with silence test. Notify could fail evaluation; keep delivery best-effort and test nil safety. Alerting could import notify and create coupling; use an interface seam and wiring adapter. |
| Decision points | No Owner sign-off expected unless implementation requires schema migration, new dependency, frozen package file addition, auth/billing/quota changes, or production secret/deployment changes. |
| Pre-execution checklist | Read local rules and target code; confirm no reference reads; confirm target packages are non-frozen; write red tests before production code; run required verification. |
| Concrete execution order | 1. Add failing alerting tests for edge delivery, silence suppression, nil deliverer. 2. Add failing notify dispatch test. 3. Extend `UpsertFiringEvent` to return `created bool` in interface and stores. 4. Add alerting deliverer option and best-effort firing-edge call. 5. Add notify alert firing event data and dispatch method. 6. Wire notify-backed deliverer in gateway. 7. Run targeted and required checks. |

