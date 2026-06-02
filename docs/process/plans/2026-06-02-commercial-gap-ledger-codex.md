# 2026-06-02 Commercial Gap Ledger Codex Plan

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: sub2api / new-api

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| Owner directive | "HUAKAI 商业功能三镜核实 + 权威缺口账本 (RESEARCH/VERIFY lane;只读分析,除账本 .md 外不改任何源码)。" |
| Scope | In: clean-room 只读核实 `sub2api` 与 `new-api` 商业功能、只读核实 HUAKAI `origin/fix/hermes-phase-1-e33d940` 与 `origin/work/quota-subsystem`、写 `docs/process/commercial-gap-ledger-2026-06-02.md`、提交并推送 `work/commercial-gap-ledger`。Out: 不修改源码、不改 schema、不合并分支、不改 landing、不复制参考源码/结构/标识符。 |
| Success criteria | 账本主表每行一个商业功能；参考项目每条能力有 `repo@sha:file:line` 证据；HUAKAI 两条线每条判定有本地 `file:line` 证据或明确缺口；第三方 renew 审计逐条判定；结论列出并入 quota 后仍缺且不能少的赚钱闭环功能；通过基本文档/引用自检；提交并推送到 `origin HEAD:work/commercial-gap-ledger`；最终输出 `LEDGER_DONE`。 |
| Time estimate | 约 2-4 小时墙钟；主要时间用于源码定位、line citation、交叉核实和账本整理。 |
| Blast radius | 低到中。预期只新增/修改计划与账本文档。若误改源码、误复制非 MIT 源码细节、或引用未核实证据，会破坏 clean-room 和决策可信度。 |
| Failure modes | 参考源不在预期路径：先按 Owner 指令 `ls ~/refs reference_sources backend/../reference_sources 2>/dev/null` 定位，必要时只读 `find`；不联网补源码，除非本地缺失并记录。引用过时/commit 不匹配：读取 `git rev-parse HEAD` 与目标 commit，记录实际 SHA；若无法 checkout 目标，使用可达 commit 并标明。功能证据不足：标为缺口/开放问题，不编造。clean-room 风险：只写行为与产品能力，不写源码、schema、函数名、独特标识符。 |
| Decision points | 若需要修改源码、schema、支付/配额/认证/账本逻辑、删除文件、拉取未存在参考源、或切换/重写分支，停止并请求 Owner 确认。当前任务预期不触发这些高风险动作。 |
| Pre-execution checklist | 1. 读取项目规则和 clean-room 约束。 2. 定位参考源与两个 HUAKAI 分支。 3. 记录参考项目实际 SHA。 4. 建立功能清单与引用采集表。 5. 核实 landing 证据。 6. 核实 quota-subsystem 证据。 7. 写账本。 8. 自检引用、缺口、第三方 renew 判定、中文总结。 9. `git diff --check` 与目标文件检查。 10. `git add`、按规则执行 Codex review（若 CLI 可用）、commit、push。 |

## Concrete Execution Order

1. 只读检查 `docs/RULES.md`、clean-room 相关文档、当前 `git status`。
2. 按 Owner 指令定位 `~/refs`、`reference_sources`、`backend/../reference_sources`。
3. 对 `sub2api` 与 `new-api` 读取 `git rev-parse HEAD`、必要时验证目标 commit 是否可达；只读搜索用户列出的商业功能入口。
4. 对每个参考项目提取行为证据：支付门户/订单/退款/回调/订阅/联盟返佣/兑换码/渠道运维等，只记录行为和 `repo@sha:file:line`。
5. 只读核实 HUAKAI landing 分支：优先用 `git grep`/`git show origin/fix/hermes-phase-1-e33d940:<path>` 获取 `file:line`。
6. 只读核实 HUAKAI quota 分支：优先用 `git grep`/`git show origin/work/quota-subsystem:<path>` 获取 `file:line`。
7. 写 `docs/process/commercial-gap-ledger-2026-06-02.md`，包含主表、第三方 renew 审计核实、并入 quota 后仍缺功能、风险/假设、中文结论。
8. 自检：确认无参考源码片段、无 schema/函数名抄录、每个参考能力和 HUAKAI 判定都有证据或明确缺口。
9. 运行文档级检查：`git diff --check`，并用 `rg` 检查目标账本核心章节/引用格式。
10. 暂存计划与账本，运行 per-commit Codex review（若当前 CLI 支持且不会修改文件），处理 S0/S1。
11. Commit 并 push `origin HEAD:work/commercial-gap-ledger`。

## 执行门记录

- 计划正文在参考源码读取与账本写入前创建；本次 review 修正只补充 clean-room tail 与该执行门记录。
- Owner 批准例外依据: Owner 原始指令在同一消息中明确授权本次 `RESEARCH/VERIFY lane`、写账本、commit、push；当前线程未提供 Claude 并行计划产物。本记录只覆盖这次只读研究账本的单 Codex 运行，不覆盖任何后续实现、发布、schema、支付、配额、认证或账本改动。
- 后续基于该账本的实现切片，必须由 Owner 补齐或确认 Claude/Codex parallel plan 与 synthesized plan gate；本计划不能被当作实现切片批准。
- 下方 tail 时间戳表示计划产物元信息补齐时间，不表示计划正文晚于账本执行。

Source files read: 本计划产物未读取参考源码；源码读取记录见后续账本产物。
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-06-02T10:08:46Z
