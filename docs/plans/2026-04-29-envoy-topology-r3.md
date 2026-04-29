# 2026-04-29 envoy topology R3 source-verified decomposition
| Owner directive | "拆 7 项目深度必须够 + 保证真实，不造假" |
| Scope | Produce one source-verified clean-room decomposition for envoy-ai-gateway topology, route reconciliation, backend lifecycle, quota attachment, and status lifecycle at `docs/decompositions/envoy-ai-gateway/topology-crd-source-verified.md`. Do not read companion Claude deep output. |
| Success criteria | Critic findings are addressed one by one; at least 12 source regions are read and listed; every §2 behavior claim cites a source region; open questions are explicit; output ends with Chinese Owner summary. |
| Time estimate | 60-90 minutes wall clock; one Codex lane. |
| Blast radius | Documentation only. Incorrect claims could contaminate implementer-lane requirements. |
| Failure modes | Source pages unavailable: mark affected behavior as open question. Over-specific upstream implementation leakage: redact names and rewrite as HUAKAI vocabulary. Unsupported behavior claim: remove or move to open questions. |
| Decision points | No Owner sign-off expected unless source reading would require restricted credentials or changes outside docs. |
| Pre-execution checklist | 1. Read companion critic first. 2. Read HUAKAI glossary/rules needed for vocabulary. 3. Read public upstream source/doc regions. 4. Draft decomposition with observed/inferred distinction. 5. Verify citation coverage against §10. |
