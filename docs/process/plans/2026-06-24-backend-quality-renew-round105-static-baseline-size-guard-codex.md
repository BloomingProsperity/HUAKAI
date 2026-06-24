# 2026-06-24 backend quality renew round105 static baseline size guard

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” 与 objective 中 “deadcode-baseline.txt 787 条祖父豁免是否在膨胀” |
| Scope | 新增低风险静态测试，锁住 `scripts/staticcheck-baseline.txt` 与 `scripts/deadcode-baseline.txt` 当前行数上限，并要求文件保持排序去重；不重写 baseline、不运行 `quality-gate.sh --update` |
| Success criteria | `backend/internal/codebudget` 中存在守门测试：`staticcheck-baseline.txt` 不超过 93 行、`deadcode-baseline.txt` 不超过 787 行，且两者保持 sorted unique |
| Time estimate | 10-20 分钟；1 个小切片 |
| Blast radius | 只影响测试守门；未来清理 baseline 可自然通过，未来扩大豁免池会红灯 |
| Failure modes | 合法 rebaseline 后行数下降但常量未同步：不影响通过；若确需增加 baseline，必须 Owner/评审显式接受并更新测试常量 |
| Decision points | 无需 Owner 中途确认；本轮不删除 baseline 现有条目，也不判断每条祖父豁免是否仍真实 |
| Pre-execution checklist | 已读取 objective；已读取 acceptance-test-writer 技能；已用 `wc -l` 与 Python 确认当前 baseline 规模和排序去重状态；不读取不编辑另一个目标计划文件 |

## 执行顺序

1. 新增 `backend/internal/codebudget/static_baseline_size_test.go`。
2. 读取 `backend/scripts/staticcheck-baseline.txt` 与 `backend/scripts/deadcode-baseline.txt`。
3. 断言行数不超过当前规模，并断言行集合排序去重。
4. 用 Python 复刻逻辑验证；尝试 `gofmt` / `go test ./internal/codebudget`，缺工具链则记录。
