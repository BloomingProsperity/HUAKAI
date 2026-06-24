# 2026-06-23 backend quality renew round34

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查生产出口传输伪装层 `backend/internal/transport/mimicry`、直接接线处、以及 parked Rust sidecar 的 Go 边界与明显 dead-code 信号。重点是 uTLS/H1 决策一致性、ALPN/ForceAttemptHTTP2、代理链路、sidecar 默认关闭后的残留质量、测试判别力。不进入另一个 security scan 目标，不读取或修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 45-90 分钟代理时间，按源码阅读、生产接线核对、测试覆盖核对推进。 |
| Blast radius | 只新增本计划 artifact；业务代码、测试、schema、部署脚本均不改。 |
| Failure modes | 1. 把安全/反检测专题展开过深：本轮只看代码质量与生产可靠性。2. 用旧文档替代源码：必须读 `utls_dialer.go`、`template.go`、`sidecar_client.go`、测试和生产接线。3. 把 parked sidecar 当作必须上线：只评估 parked 代码质量和死代码清理候选。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改变出口协议姿态、删除 Rust H2 桥、调整生产 HTTP transport 或 sidecar 部署默认值，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 确认只处理 quality renew，不处理 security scan。4. 用 `rg` 定位 `ForceAttemptHTTP2`、`ALPN`、`sidecar`、`h2_bridge`、`TransportErrorClass`、测试覆盖。5. 核对本机 `go`/`cargo` 是否可用后再决定能否运行测试。 |

## Concrete Execution Order

1. 量化并读取 `backend/internal/transport/mimicry` 生产 Go 代码与测试。
2. 从 `cmd/gateway` 或相关 wiring 核对 mimicry/sidecar 是否生产接线、默认是否开启。
3. 检查 uTLS 模板、ALPN、`ForceAttemptHTTP2` 与"强制 H1、不做 H2"决策是否一致。
4. 检查 parked Rust sidecar 与 Go sidecar client 的边界、错误分类、dead-code 清理候选。
5. 输出中文 findings，并如实说明本机测试是否可运行。
