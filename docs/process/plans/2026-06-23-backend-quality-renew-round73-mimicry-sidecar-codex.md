# 2026-06-23 backend quality renew round73 mimicry-sidecar

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 审查 Go 生产出口 `backend/internal/transport/mimicry/`、Go sidecar bridge、canonical parked Rust `exploratory/rust-core-gateway/merged/crates/tls-sidecar` 的代码质量、协议一致性、dead-code 与测试覆盖。 |
| Out of scope | 不审 front-end；不展开 security 专项；不逐个审并行草稿 lane；不建议上线 Rust sidecar；不补 H2。 |
| Success criteria | 产出带 `file:line` 的中文 findings，覆盖 H1/H2 决策一致性、配置 wiring、bridge 帧协议、Rust parked crate 可恢复路径、测试质量与 dead-code 候选。 |
| Time estimate | 约 35-50 分钟人工等效审查；本轮只完成一个切面，不声明整个 renew 完成。 |
| Blast radius | 只读代码与测试，新增本计划文件；不修改生产逻辑，避免影响另一个目标。 |
| Failure modes | 误把 parked sidecar 当生产路径；误读 README/状态文档；把纯安全问题展开；遗漏 Go 活出口。缓解：以 `.go`/`.rs` 真码为准，只按目标文件边界输出。 |
| Decision points | 若发现需要删除 Rust H2 模块、旧草稿 lane 或更改生产出口协议，先作为 Owner 确认项，不直接删除。 |
| Pre-execution checklist | 1. 读取目标文件；2. 读取 api-gateway-risk-review 技能；3. 搜索 mimicry/sidecar wiring；4. 打开关键实现与测试；5. 运行可用检查；6. 输出中文 findings。 |

## Concrete execution order

1. 读取 Go mimicry dialer、template、registry、proxy、sidecar client 与 cmd wiring。
2. 读取 canonical Rust `tls-sidecar/src` 的入口、H2 模块、帧/协议、错误处理与测试。
3. 搜索 H2/ALPN/ForceAttemptHTTP2 决策漂移、unwrap/expect、dead-code 引用。
4. 跑可用测试；若工具链缺失，如实记录。
5. 输出本轮 findings，不创建总结报告。
