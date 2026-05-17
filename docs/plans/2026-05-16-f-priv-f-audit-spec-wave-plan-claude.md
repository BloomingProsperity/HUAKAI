# F-PRIV-001 + F-AUDIT-001 Spec Wave Plan (Claude Lane)

| Owner directive | "F-PRIV-001 + F-AUDIT-001 spec (6 大差异化剩 2 项 spec)" — Owner 2026-05-16 |
| --- | --- |
| Scope | In: 2 spec docs (F-PRIV-001 隐私保护 + F-AUDIT-001 用户消费透明). Out: 实施代码 / migration / 其它 spec / F-TRUST-001 已 commit / 反代各层 spec 已 commit |
| Success criteria | (a) 2 spec ready commit at `docs/specs/privacy-no-user-data-logs.md` + `docs/specs/user-consumption-transparency.md`; (b) Claude + Codex 平行 draft + PM synthesis (CLAUDE.md #10); (c) 跟 F-TRUST-001 closure partner reference 正确; (d) DR-001 tenant_id day-1; (e) AT 编号连续 + 加进 11/03 matrix |
| Time estimate | 1-2 hr (2 spec × 30-60 min synthesis) |
| Blast radius | Low (doc only, 不动 production code / migration / 已 commit spec) |
| Failure modes | spec scope creep (F-PRIV overlap F-TRUST redaction guard / F-AUDIT overlap F-BILL ledger); Claude self-write 违 CLAUDE.md #9-10 (已通过本 plan 解决); codex 写中性 spec 风格跟 HUAKAI 现有 spec 不一致; matrix add 漏 row |
| Mitigations | Claude 写 plan artifact (本文件) surface Owner; 2 codex lane parallel-draft (CLAUDE.md #10); Claude 2 spec draft; synthesis 4 draft; 跟 F-TRUST-001 spec 互引但 scope 分离 |
| Decision points | Stop if Owner 觉得 spec scope 跟 F-TRUST-001 重叠; 不需 Owner sign-off 因为 doc-only low-risk |
| Pre-execution checklist | Read F-TRUST-001 spec (closure partner) / memory project_core_trust_chain_differentiator / 03 + 11 matrix 锚定格式; 写 plan artifact (本文件); dispatch 2 codex parallel + Claude self-draft; synthesis after 4 draft ready |

## Concrete Execution Order

1. **本 plan artifact 写完** (本文件)
2. **2 codex lane dispatch (parallel)**:
   - F-PRIV-001 codex specifier draft → `/tmp/codex-f-priv-001-spec-codex-draft.md`
   - F-AUDIT-001 codex specifier draft → `/tmp/codex-f-audit-001-spec-codex-draft.md`
3. **Claude 2 spec parallel-draft** (在 codex lane 跑期间):
   - F-PRIV-001 Claude draft → `docs/plans/2026-05-16-f-priv-001-spec-claude.md`
   - F-AUDIT-001 Claude draft → `docs/plans/2026-05-16-f-audit-001-spec-claude.md`
4. **等 codex 2 lane done**
5. **PM synthesis** (合并 Claude + Codex 各 spec):
   - F-PRIV-001 → `docs/specs/privacy-no-user-data-logs.md`
   - F-AUDIT-001 → `docs/specs/user-consumption-transparency.md`
6. **Matrix add** (03 parity + 11 AT)
7. **Build/test smoke** (确保没 break — 但 doc-only 应该没 risk)
8. **Commit + push** (doc-only wave)

## Clean-Room Guard

- Claude + Codex specifier lane 都不读 sub2api / new-api / portkey / litellm / helicone 等参考项目源码
- 本 spec wave 不涉反代敏感 (跟 L3/L4/L5/L6 不同, F-PRIV/F-AUDIT 是 trust/transparency family, 中性 spec)
- 引用上游项目时 paraphrase + cite 类别 (不抄 verbatim identifier)
- 跟 F-TRUST-001 spec (commit 158c421) closure partner reference (cross-spec link 用 [[name]] 锚定)

## Source files read (Claude lane plan)

- docs/specs/trust-chain-user-verifiable-ledger.md (F-TRUST-001 spec, closure partner)
- docs/03_FEATURE_PARITY_MATRIX.md + docs/11_ACCEPTANCE_TEST_MATRIX.md (matrix add 格式锚定)
- memory project_core_trust_chain_differentiator (6 大差异化, F-PRIV 实施 2+5, F-AUDIT 实施 6)
- Owner directive 2026-05-16 "F-PRIV-001 + F-AUDIT-001 spec (6 大差异化剩 2 项 spec)"

## 中文摘要

F-PRIV-001 + F-AUDIT-001 spec wave plan artifact 落档 (Claude PM-Orchestrator lane). Owner 选 6 大差异化剩 2 项 spec 作下一波 (F-TRUST-001 已 commit 158c421). 本 plan 执行: (1) 2 codex specifier lane parallel-draft + (2) Claude 2 spec parallel-draft + (3) PM synthesis 合并 + (4) commit + push. 1-2 hr 估计. 低风险 (doc only). 不涉反代敏感, 中性 spec 可走 codex draft + Claude synthesis 模式. 跟 F-TRUST-001 (链路+模型校验+商家不能做假) 形成 6 大要求闭环 (F-PRIV 实施 2 无用户数据日志 + 5 日志只系统报错; F-AUDIT 实施 6 用户消费透明).
