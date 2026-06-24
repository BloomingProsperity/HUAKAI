# 2026-06-23 backend quality renew round57 gateway-forwarder

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/gateway` 流式 forwarder、上游 dispatch、HCSF 缓冲、`backend/internal/protosse` 流式恢复状态注册与直接关联测试；不修改生产代码，不触碰另一个 security-scan 目标。 |
| Success criteria | 产出直接面向 Owner 的中文 findings，每条都有真实 `file:line` 证据、问题说明、可执行修法；明确 timer/ctx/drain/补帧/恢复注册重复/包预算/测试运行状态。 |
| Time estimate | 约 45-70 分钟墙钟；单 agent 一轮源码取证与审查输出。 |
| Blast radius | 只新增本计划文件；后续为只读审查。若误改生产代码会污染当前 renew 目标，因此本轮不做实现改动。 |
| Failure modes | 误信旧文档而非源码：只以 `.go` 真码与测试为准；重复既有 round findings：优先核 forwarder 细节与新证据；把纯安全问题展开：只留 security 专项指针；环境缺 Go 工具链：如实记录无法运行。 |
| Decision points | 若发现需要拆包或改变 streaming 状态机，只输出建议，不直接改；若修法涉及协议合同或 money 结算语义，标注需要 Owner/主实现 agent 确认。 |
| Pre-execution checklist | 1. 已重新读取目标 objective；2. 已读取本轮相关 skills；3. 确认计划文件不存在后新增；4. 使用 `rg`/`nl`/`wc` 读取源码；5. 只读审查 `gateway`/`protosse` 相关文件；6. 运行可用检查并记录工具缺失。 |

## Concrete execution order

1. 量化 `internal/gateway` 与相关文件体量，确认 codebudget 预算状态。
2. 打开 `forwarder.go`、`upstream_dispatcher*.go`、HCSF 缓冲/clone 相关文件，定位 timer、ctx、drain、EOF/error 分类、补帧路径。
3. 打开 `protosse/reconstruct.go` 与 forwarder 状态构建点，核对协议族注册是否重复、是否有测试约束。
4. 搜索并读取相关测试，判断是否覆盖 timer 泄漏、ctx 取消、半帧/EOF、HCSF 恢复、非流 body 上限。
5. 运行当前环境可用的 Go 检查；若工具缺失，记录精确输出。
6. 输出中文 findings，不写 `.md` 报告。
