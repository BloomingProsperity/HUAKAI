# 2026-06-24 后端质量刷新 round121：mimicry Go uTLS 强制 H1 防回退

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” / “不要触碰到另一个目标，你做你的，他做他的” |
| --- | --- |
| Scope | 仅处理 `internal/transport/mimicry` Go uTLS 活出口的 H1/H2 姿态一致性；允许新增静态 guard；不触碰 Rust sidecar、部署脚本、auth/billing/quota/schema/LICENSE。 |
| Success criteria | Go uTLS 直连和代理 `http.Transport` 均显式 `ForceAttemptHTTP2=false`；默认 `HUAKAI_TRANSPORT_FORCE_H1` 为空时强制 H1；自定义 uTLS spec 与 `tls.Config.NextProtos` 都能收窄到 `http/1.1`；后续回退会被测试挡住。 |
| Time estimate | 10-20 分钟墙钟时间；单 agent 小闭环。 |
| Blast radius | 只新增静态 guard；当前生产行为不变。 |
| Failure modes | guard 过窄漏掉代理路径；guard 过宽误伤测试注释。缓解：只扫描生产 `utls_dialer.go`，绑定关键赋值片段。 |
| Decision points | 若要修改真实出口协议策略、启用 Rust sidecar 或清理 H2 sidecar 文件，另开计划并请求 Owner 确认；本轮不做。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 读取 production-scenario-review 与 acceptance-test-writer 技能；3. 核实当前源码和白盒测试；4. 新增 codebudget guard；5. 执行静态检查与可用测试并记录 Go 工具链缺失。 |

## 执行顺序

1. 核对 `utls_dialer.go` 是否已关闭 `ForceAttemptHTTP2` 并默认强制 H1。
2. 核对现有白盒测试是否覆盖 ALPN 收窄、默认 env 和代理路径。
3. 新增 `codebudget` 静态 guard，防止后续生产代码回退。
4. 运行 `git diff --check`、禁词扫描、静态模拟；尝试 Go 测试并如实记录环境缺口。
