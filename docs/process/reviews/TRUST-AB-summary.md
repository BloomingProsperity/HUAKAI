# TRUST-A+B Summary

| Field | Value |
| --- | --- |
| Lane | implementer (docs only) |
| UTC | 2026-05-28T02:39:40Z |
| Scope | Retrospective summary for TRUST-A plus TRUST-B-1..B-4; no backend/frontend code changes in this TRUST-B-5 docs lane. |
| Source basis | Local `git log`, TRUST plans under `docs/process/plans/`, and docs updated in TRUST-B-5. |

## Commit Ledger

Local git history confirms six commits from TRUST-A through TRUST-B-4:

| Commit | Local subject |
| --- | --- |
| `67659a6` | TRUST-A trust wire contract + UX panel + ledger 3 fields |
| `5ab1609` | TRUST-B plans and Owner D decisions |
| `e35ff4c` | TRUST-B-1 canonical receipt contract |
| `8a3c38e` | TRUST-B-2 signer integration + provisional receipt + D-8 fail-open |
| `8eadc3e` | TRUST-B-3 final billing detached signature + `signed_hash` reuse |
| `780f7d6` | TRUST-B-4 well-known pubkey + `/v1/trust/verify` + receipt verify CRL + CLI TOFU |

Owner brief said "5 commits" but listed six identifiers. This summary preserves the six identifiers verified by `git log` and treats the "5 commits" phrase as an off-by-one wording issue.

## Review Rounds

Owner brief listed: TRUST-A 2 + B-1 1 + B-2 3 + B-3 2 + B-4 4. Those counts sum to 12, while the brief also said "8 round review". This file records the discrepancy instead of inventing a reconciliation. The material point for release notes is that each slice closed its S1 blockers before the next TRUST-B slice proceeded.

## Current Capability

- TRUST-A: response-level trust headers and visible status vocabulary are wired.
- TRUST-B-1: `trust.receipt.v1` canonical bytes are deterministic and mutation-guarded.
- TRUST-B-2: non-streaming inline provisional receipts can be signed; signer/ledger failure is fail-open with `unverified` and DLQ reference.
- TRUST-B-3: final settled receipts reuse `user_cost_receipts.signed_hash` and reject one-sided signature state.
- TRUST-B-4: public JWK Set, detached verify endpoint, receipt verify CRL overlay, and `huakai-verify` TOFU are implemented.
- TRUST-B-5: docs now align spec, acceptance matrix, risk register, reference evidence ledger, and feature parity matrix.

## Deferred / Mandatory Roadmap

| Item | Severity / type | Reason |
| --- | --- | --- |
| Phase 2 full C Merkle/checkpoint proof | Mandatory Roadmap | TRUST-A+B intentionally ships dual-rail signatures first; Trillian-style inclusion/consistency proof and chain-head publication remain future work. |
| OpenSSH source-cited TOFU evidence | S2 docs evidence gap | `openssh-portable` source checkout was not available locally in this lane; spec/ledger do not fabricate a `<repo>@sha>` citation. |
| Helicone AI Gateway source-cited negative parity | S2 docs evidence gap | Correct `Helicone/ai-gateway` checkout was not available locally; local `helicone` checkout is a different repo. |
| S2 deferred review list | Open audit note | No TRUST-specific `docs/process/reviews/DEFERRED-*trust*` file was found in this pass. Existing S2 items should be preserved if later recovered from per-commit review transcripts. |

## Source Files Read

- `git log --oneline 67659a6^..780f7d6`
- `docs/process/plans/2026-05-27-trust-b-synthesis.md`
- `docs/process/plans/2026-05-27-trust-chain-ab-synthesis.md`
- `docs/process/plans/2026-05-28-trust-b-5-docs-codex.md`
- `docs/specs/trust-chain-user-verifiable-ledger.md`
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/07_REFERENCE_EVIDENCE_LEDGER.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`

Source files read: listed above.
Lane: implementer (docs)
Agent: GPT-5 Codex
UTC timestamp: 2026-05-28T02:39:40Z

### Owner 中文摘要

TRUST-A+B 当前本地历史验证为 6 个 commit, 不是 Owner brief 文字里的 5 个; review round 计数也存在 "8" 与明细相加 "12" 的不一致, 本文照实记录不编造。功能上 Phase 1 lite 已覆盖 header/status、canonical、inline provisional、final detached、well-known/verify/CLI/CRL; 没有功能缩水。Phase 2 full Merkle、OpenSSH source cite、Helicone AI Gateway source cite 留为后续补证/roadmap。
