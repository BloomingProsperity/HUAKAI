# 2026-05-10 feature parity audit codex
| Owner directive | Owner 2026-05-09 quote: "功能是否缺失这一点也要算进去" |
| Scope | In: reviewer-lane parity audit from HUAKAI internal specifier outputs and planning docs. Out: reading `~/refs/`, editing implementation, changing feature disposition docs. |
| Success criteria | Produce `docs/research/2026-05-09-codex-feature-parity-audit.md` with cited HIGH/MED/LOW findings, three-way feature mapping, hosted-tools checks, axis 1/5 disposition checks, commercialization disposition checks, and P-0/P-0c silent-drop risk review. |
| Time estimate | 45-60 minutes wall clock; one Codex reviewer lane. |
| Blast radius | Low: documentation-only audit output. Risk is incorrect gating advice if citations or feature matching are incomplete. |
| Failure modes | Missing a feature due to incomplete reading; mitigated by reading all required files and using grep for target feature names. Overstating a missing disposition; mitigated by recording file:line evidence and marking uncertainty as MED/LOW rather than HIGH when a valid roadmap exists. Clean-room breach; mitigated by not opening `~/refs/` or upstream source. |
| Decision points | Owner must decide whether HIGH `MISSING_DISPOSITION` findings block P-0c-A dispatch or require updating parity/roadmap docs first. |
| Pre-execution checklist | Confirm reviewer lane constraints; read required internal source files; extract reference feature inventory; compare against HCSF v0.4/P-0/P-0c and parity roadmap docs; write cited report; do not modify implementation. |
| Concrete execution order | 1. Read required docs and line-numbered evidence. 2. Build feature-to-disposition matrix. 3. Check hosted tools/protocol surfaces. 4. Check axis 1/5 and commercial L0 dispositions. 5. Check P-0/P-0c silent-drop risk docs and code surface. 6. Write final audit report. |
