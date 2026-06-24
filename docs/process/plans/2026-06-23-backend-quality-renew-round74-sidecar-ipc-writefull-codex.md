# 2026-06-23 backend quality renew round74 sidecar-ipc-writefull

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 修复 Go sidecar IPC control frame 写入只调用一次 `net.Conn.Write` 的短写风险，并补一个判别式测试。 |
| Out of scope | 不改 Rust sidecar H2 行为；不改生产出口协议；不碰 auth/billing/quota/schema/部署脚本；不处理另一个 security 目标。 |
| Success criteria | `writeSidecarFrame` 对 prefix/body 都写满或返回错误；新增测试能模拟 partial write 并验证完整帧仍能被写出；clean-room 扫描无参考项目名。 |
| Time estimate | 约 15-25 分钟。 |
| Blast radius | `backend/internal/transport/mimicry/sidecar_client.go` 与对应测试；影响仅在启用 sidecar socket 的 Go bridge 控制帧写入。 |
| Failure modes | 测试 fake conn 写法不符合 `net.Conn` 契约；因本机缺 Go 工具链无法执行 Go test。缓解：用最小 fake conn，并如实记录无法运行。 |
| Decision points | 无需 Owner 中途确认；这是低风险可靠性补丁。 |
| Pre-execution checklist | 1. 读取目标文件；2. 读取技能；3. 读取当前实现与测试；4. 用 `apply_patch` 修改；5. 尝试运行 Go 测试；6. clean-room 定向扫描。 |

## Concrete execution order

1. 读取 `sidecar_client.go` / `sidecar_client_test.go` 当前状态。
2. 新增 `writeFullConn` 并让 `writeSidecarFrame` 使用。
3. 新增 partial-write fake conn 测试。
4. 运行可用检查并输出中文结果。
