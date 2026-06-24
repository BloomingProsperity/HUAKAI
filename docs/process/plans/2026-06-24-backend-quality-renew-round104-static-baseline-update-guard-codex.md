# 2026-06-24 backend quality renew round104 static baseline update guard

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” 与 objective 中 “staticcheck / deadcode baseline 只挡新增 findings，不得用 `--update` 静默洗白” |
| Scope | 扩展现有自动化守门，禁止 CI / 普通脚本直接引用或改写 `scripts/staticcheck-baseline.txt`、`scripts/deadcode-baseline.txt`；允许 `backend/scripts/quality-gate.sh` 作为人工维护入口保留 `--update` 定义 |
| Success criteria | `backend/internal/codebudget/baseline_rewrite_guard_test.go` 能同时拦截 codebudget baseline 自动重写、`quality-gate.sh --update` 自动调用、以及自动化入口直接触碰 staticcheck/deadcode baseline 文件 |
| Time estimate | 10-20 分钟；1 个小切片 |
| Blast radius | 只影响测试守门；失败时阻止未来 CI/脚本静默洗白静态分析或 deadcode baseline，不影响生产二进制 |
| Failure modes | 文本扫描误伤专用维护脚本：继续豁免 `backend/scripts/quality-gate.sh` 本体；扫描面漏掉新自动化目录：沿用 `.github/`、repo `scripts/`、`backend/scripts/` 入口，并让后续新脚本落入这些路径 |
| Decision points | 不移除 `quality-gate.sh --update` 能力本身；若 Owner 要彻底删除人工 rebaseline 入口，应另开确认 |
| Pre-execution checklist | 已读取 objective；已读取 acceptance-test-writer 技能；已核 `backend/scripts/quality-gate.sh` 真实 baseline 路径；不读取不编辑另一个目标计划文件 |

## 执行顺序

1. 扩展 `baseline_rewrite_guard_test.go` 的扫描逻辑。
2. 对自动化入口中的 `staticcheck-baseline.txt` / `deadcode-baseline.txt` 直接引用报错。
3. 用 Python 复刻扫描逻辑验证当前仓库无违规。
4. 尝试 `gofmt` / `go test ./internal/codebudget`，若工具链缺失则如实记录。
