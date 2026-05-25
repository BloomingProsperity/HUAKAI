# 2026-05-21 audit-w1-phase1-retry-failover Codex Plan

| Owner directive | "回溯审计任务 W1 —— HUAKAI 方向 1 Phase 1...现在补做对比,挖 HUAKAI 漏掉的细节和小功能。specifier lane,不写 HUAKAI 代码。" |
| Scope | In: read HUAKAI Phase 1 router/gateway/gatewayhttp files and local `sub2api-latest` / `CLIProxyAPI-latest` source for retry, failover, cross-pool routing, error taxonomy, streaming retry, and small operational details. Out: HUAKAI code changes, git stage/commit/checkout, implementation plans beyond remediation sizing. |
| Success criteria | Produce `docs/process/research/2026-05-21-audit-w1-phase1-retry-failover.md` in Chinese with version posture, six-dimension comparison, gap matrix, severity-ordered remediation advice, source-file tail, lane, and UTC timestamp. Every non-HUAKAI behavior claim has file:line evidence. |
| Time estimate | 2-3 wall-clock hours depending on source density; one Codex session. |
| Blast radius | Low: research document only. Risk is inaccurate claims or clean-room contamination, not runtime behavior. |
| Failure modes | Missing a relevant source region: mitigate with targeted `rg` across retry/failover/error/router/stream/cooldown words and source coverage tail. Clean-room leakage: mitigate by paraphrasing behavior only, no code blocks, no upstream internal names in prose. Over-claiming HUAKAI gaps: mitigate by reading HUAKAI code before each verdict. |
| Decision points | High-risk implementation is out of scope. If a finding suggests schema/auth/billing/quota changes, record as Owner-confirmation-needed remediation, do not modify code. |
| Pre-execution checklist | 1. Confirm output directories exist. 2. Record read-only version posture for all three repos. 3. Read HUAKAI target files with line numbers. 4. Locate and read relevant source regions in both references. 5. Build six-dimension notes with evidence. 6. Write report. 7. Verify report contains required sections and clean-room tail. |

## Concrete execution order

1. Create this Codex plan artifact before source mining.
2. Inspect repo version metadata using read-only commands only.
3. Read HUAKAI Phase 1 implementation files and capture line anchors for capabilities already present.
4. Search both references for retry, account selection, pool routing, stream, HTTP error, timeout, cooldown, and rate-limit behavior.
5. Compare each detail against HUAKAI evidence and classify as present, stronger, weaker, or missing.
6. Draft the Chinese report with a matrix and severity-ranked remediation.
7. Run a final grep/section check on the report path before final response.
