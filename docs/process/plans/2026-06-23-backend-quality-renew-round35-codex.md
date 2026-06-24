# 2026-06-23 backend quality renew round35

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查 relay 数据面热点：`backend/internal/gateway/forwarder.go`、`upstream_dispatcher.go`、`upstream_dispatcher_hcsf.go`、以及直接相关的 stream/protosse 测试与辅助代码。重点是流式循环 timer 生命周期、响应体关闭、drain/补帧、错误分类、timeout 包装、测试判别力。不进入 security scan 目标，不读取或修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings，按 S0/S1/S2/S3 分区；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 60-90 分钟代理时间，按源码阅读、生产调用链核对和测试覆盖核对推进。 |
| Blast radius | 只新增本计划 artifact；业务代码、测试、schema、部署脚本均不改。 |
| Failure modes | 1. 把纯安全问题展开成 security 审查：遇到跨租户/密钥泄露只标"转 security 专项"。2. 被目标文件旧线索误导：必须以当前源码为准。3. 只读 happy-path 流式测试而漏响应体关闭/错误帧/timeout。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改 streaming contract、billing settle 时机、provider adapter 接口或 production timeout 默认值，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 确认只处理 quality renew，不处理 security scan。4. 用 `rg` 定位 timer、Close、Drain、补帧、newUpstreamState、ResponseHeaderTimeout、NonStreamingBuffered。5. 核对本机 `go` 是否可用后再决定能否运行测试。 |

## Concrete Execution Order

1. 量化并读取 `internal/gateway` forwarder/dispatcher 相关生产代码与测试。
2. 检查流式主循环的 timer、ctx、EOF/error、drain、补帧语义。
3. 检查 dispatcher 对响应体关闭、timeout 包装、TLS/profile/proxy 叠加的资源语义。
4. 检查 protosse/newUpstreamState 分派是否有重复维护或测试漏洞。
5. 输出中文 findings，并如实说明本机测试是否可运行。
