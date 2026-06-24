# 2026-06-23 Go 注释 clean-room 清理

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；AGENTS 硬规则要求 `.go` 生产代码与测试注释不得提及借鉴项目名 |
| Scope | 仅清理 `backend/**/*.go` 注释和测试说明中出现的参考项目名、上游行号、"mirrors/parity with" 等污染性表述；不改代码逻辑、不改测试输入输出、不改 docs/process 证据文档 |
| Success criteria | `rg -n "sub2api|new-api|CLIProxyAPI|all-api-hub|借鉴|参考某项目" backend -g '*.go'` 无命中；功能能力描述保留为 HUAKAI 自身机制；`git diff --check` 通过 |
| Time estimate | 约 25-40 分钟墙钟时间；单个 Codex 注释清理补丁 |
| Blast radius | 生产代码注释与测试注释；若误改非注释内容会影响编译或测试语义 |
| Failure modes | 把行为证据删成无信息注释；误改字符串常量或测试断言；遗漏大小写不同的项目名 |
| Mitigation | 每个命中先看上下文；只改注释行；最后做全局关键词复扫和 diff 审读 |
| Decision points | 本轮不删除功能、不更新风险登记；若发现疑似复制代码而非注释污染，另起 clean-room 风险处理 |
| Pre-execution checklist | 1. 已读取目标 objective；2. 已读取 clean-room-license-guard skill；3. 已全局扫描 backend Go 命中；4. 已确认另一个目标 plan 不读不改；5. 编辑后跑可用检查 |

