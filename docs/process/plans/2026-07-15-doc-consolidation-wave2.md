# 2026-07-15 文档归并后续波（Owner 指令落地执行稿）

## 计划门状态

- Owner 已在 2026-07-15 本任务中直接给出目标、三条最高优先级硬规则、保护边界、本波领域顺序与交付物，并明确要求“照它持续推进”。本文件据此作为本波权威执行稿。
- Codex 独立计划已写入 [2026-07-15-doc-consolidation-wave2-codex.md](2026-07-15-doc-consolidation-wave2-codex.md)。
- 2026-07-15 尝试启动独立 Claude plan lane；Claude Code 在模型调用阶段返回 `FailedToOpenSocket`，零模型 turn、零仓库改动，未产出 Claude 草案。故本文件**不声称**完成 Claude × Codex 交叉讨论。
- 安全等价处置：严格执行 Owner 已直接批准的窄波次；只改/删文档，不改实现、部署脚本、schema、认证/计费/配额核心，不 push；本波交付后停下，由 Claude 对 DRIFT（尤其“代码疑似缺陷”）和本执行稿补做事后复核。

## Owner directive

> “判文档是否过期，一律以代码为准、必须真读实现代码判定——绝不能拿文档判代码，也绝不能拿 grep 命中当证据。”
>
> “该删就删，自主执行，不必每份停下等人工核准。”
>
> “发现文档与代码不一致/有问题的，单独标出来、分类归档。”

## 执行范围与成功标准

| 项目 | 本波口径 |
| --- | --- |
| 领域 | `frontend`、`observability-logging`、`deployment`。证据量过大时完整做完两个领域优先于浅做三个。 |
| 输入 | `DOC-CONSOLIDATION-MANIFEST.md` 三节中的 `SUPERSEDED`、`HISTORICAL-DELETE`、`NEEDS-CODE-VERIFY`；`CURRENT` 用于决策边界核对，不因归并擅删。 |
| 输出 | 三个领域 SSOT、`PROJECT-SSOT-INDEX.md` 骨架、`DOC-CONSOLIDATION-DELETION-LOG.md`、`DOC-CODE-DRIFT.md`、经代码证据支持的分批 `git rm`。 |
| 成功标准 | 每份候选均有逐份处置；实现断言均回指亲读生产代码 `file:line` 与必要调用链；删除日志逐文件完整；保护边界零删除；漂移分类真实；链接与 diff 校验通过；未提交变更完成规定 review。 |

## 保护与禁止项

- 不碰 trust-chain、`docs/research/**`、`docs/decompositions/**`、`docs/architecture/egress-tls-mimicry-SSOT.md`。
- 不改 `.go/.rs/.tsx` 实现，不改部署脚本/生产配置，不改 `LICENSE`、schema、认证/计费/配额核心。
- 不读取非 HUAKAI 参考项目源码；本任务只需 HUAKAI 内部代码与决策证据。
- 不修改、不暂存既有未跟踪文件 `docs/process/plans/2026-07-15-hermes-deployment-architecture-codex.md`。
- grep/`rg` 只作定位和清单工具；任何最终判定前必须打开并阅读生产实现、条件分支、调用方和装配点。测试只作交叉验证，不替代生产逻辑。

## 执行顺序

1. 固化 `HEAD`、分支、工作树与三个 manifest 分节清单；为每份候选建立“关键断言—实现入口—调用链—证据—处置”记录。
2. `frontend`：从 package/build、router/nav、auth/token 选择、页面/API client 追到后端 route/handler；核完才判保留、删除或 DRIFT；形成 `frontend-SSOT.md`。
3. `observability-logging`：从日志 facade/sink、请求日志与脱敏、指标写入/采集、暴露/查询、告警与前端消费追完整链；形成 `observability-logging-SSOT.md`。
4. `deployment`：联读 Dockerfile/Compose、启动入口、配置解析、迁移/首启、健康检查、webui embed 与构建脚本；形成 `deployment-SSOT.md`，绝不修改部署文件。
5. 每个领域先写证据和处置，再分批 `git rm`；每删一份立即保证删除日志含“文件→理由→代码 file:line”。Owner-gated/路线图项读取风险登记与 DR 后保留为 CURRENT。
6. 把所有文档↔代码冲突写入 `DOC-CODE-DRIFT.md`；代码疑似缺陷不修，只分类并给 Claude/Owner 建议。
7. 建 `PROJECT-SSOT-INDEX.md`，只把本波核完领域标为已归并；其余列待处理，保护族单列。
8. 跑链接/表格/路径覆盖、`git diff --check`、相关只读测试；暂存本波变更并按仓库规则运行 Codex 未提交变更 review，修复 S0/S1，记录 S2/S3。
9. 可按领域做本地小 commit，但不 push；完成本波后停下提交中文报告，等待 Claude 复核 DRIFT。

## 风险与缓解

详细失败模式、时间估算、blast radius、逐领域核验维度及前置清单以 Codex 独立计划为补充。本执行稿的关键止损线是：证据不足就保留并标待核；疑似 Owner 决策就查 DR/风险登记；疑似代码问题只记 DRIFT；任何保护路径交集立即停止该删除项。
