# 2026-06-24 backend quality renew round102 codebudget rewrite guard

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 新增低风险静态测试，禁止 CI / 普通脚本调用 `HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1` 或 `quality-gate.sh --update` 自动重写 codebudget 基线；不改生产代码、不改 baseline、不读不编辑另一个目标计划文件。 |
| Success criteria | `internal/codebudget` 新增 guard；扫描 `.github/`、repo `scripts/`、`backend/scripts/` 中的工作流/脚本调用面；允许 `backend/scripts/quality-gate.sh` 自身定义人工 `--update` 入口；当前源码扫描通过。 |
| Time estimate | 约 20-30 分钟；单个 Codex 小切片。 |
| Blast radius | 仅测试门；如果未来确需人工重写基线，应直接运行专门脚本而不是让 CI/普通脚本自动带 update。 |
| Failure modes | 路径扫描漏掉新脚本目录；误伤 `quality-gate.sh` 自身说明文字；无法运行 Go 测试。缓解：显式扫描常见自动化入口并豁免专用脚本本体，用 Python 模拟验证。 |
| Decision points | 若要移除 `quality-gate.sh --update` 能力本身，需 Owner 单独确认；本轮只防止 CI/普通脚本调用它。 |
| Pre-execution checklist | 1. 已重新读取 objective；2. 已扫描当前 workflow/scripts 相关字符串；3. 已读取 `acceptance-test-writer` 技能；4. 不读取不编辑 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；5. 修改后运行 `git diff --check`、禁词扫描、Python 模拟，并尝试 `gofmt` / `go test ./internal/codebudget`。 |
