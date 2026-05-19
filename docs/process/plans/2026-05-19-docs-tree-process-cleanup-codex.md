# 2026-05-19 docs tree process cleanup

| Owner directive | "整理 docs 树 (audit list LOW: 正式文档和过程文档混杂)" |
| --- | --- |
| Scope | In: 盘点 `docs/` 根级文档，创建 `docs/process/{plans,reviews,decisions}/`，用 `git mv` 移动过程文档，更新仓库内 `docs/process/plans/` 路径引用。Out: 不改 `.go` / `.rs` / `.sql` / 测试代码，不改历史 commit message，不碰 `LICENSE`、schema、auth、billing、quota。 |
| Success criteria | legacy plan/review/decision Markdown 过程文档被移动到 `docs/process/{plans,reviews,decisions}/` 下；`docs/` 内可安全修改的过程文档引用改为新路径；`cd backend && GOCACHE=/tmp/go-cache go build ./...` 通过；受硬约束无法修改的残留引用被如实报告。 |
| Time estimate | 20-35 分钟 wall clock；Codex 单轮执行。 |
| Blast radius | 低风险 docs-only 整理；主要风险是移动路径后内部文档链接过期。若误改 Go 文件会违反本次 lane 硬约束。 |
| Failure modes | 移动路径导致引用遗漏：用 `rg` 全仓核查并修正。Git 工作树已有用户改动：不覆盖、不 revert。`.go` 中若存在引用：记录为需 Owner 确认，不直接编辑。Go build 失败：区分是否由本次 docs-only 改动导致并报告。 |
| Decision points | 是否允许修改 `.go` 注释中的旧路径引用；是否后续继续清理根级非编号正式文档如 `PROJECT_MASTER_PLAN.md`、`PHASE_3_SKELETON.md`、`dependency-policy.md`、`dev-tests.md`、`parallel-window-prompts-2026-05-18.md`。 |
| Pre-execution checklist | 1. 读取 `docs/RULES.md` 和当前 `docs/` 结构。 2. 确认工作树脏文件并避免覆盖用户改动。 3. 统计待移动文件。 4. 创建目标目录。 5. 用 `git mv` 移动过程文档。 6. 更新允许范围内的路径引用。 7. 运行 grep/rg 与 Go build 验证。 |
| Concrete execution order | 先把 legacy plan/review/decision Markdown 文件移动到 `docs/process/` 对应子目录；随后用文本替换更新 `docs/` 下引用；最后核查 `backend/ docs/` 残留并构建 backend。 |
