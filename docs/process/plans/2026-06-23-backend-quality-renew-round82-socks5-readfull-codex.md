# 2026-06-23 backend quality renew round82 socks5 readfull

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；继续后端代码质量 renew，按真实源码推进，不把小闭环误报为总完成。 |
| Scope | 仅限 `backend/internal/transport/mimicry/proxy_dialer.go` 的 SOCKS5 握手读路径与 `proxy_socks5_test.go` 判别测试；不触碰另一个 security 目标，不触碰 auth、billing、quota、schema、部署、`LICENSE`、真实密钥。 |
| Success criteria | SOCKS5 握手所有固定长度读取都使用可识别零进展读的 helper；异常 `Read` 返回 `0,nil` 时 fail-loud 而非可能卡住；新增测试覆盖该路径；可用检查执行并记录结果。 |
| Time estimate | 约 10-15 分钟；一个 Codex 小闭环。 |
| Blast radius | 低。只改变异常连接语义；正常 SOCKS5 协议字节与成功路径不变。 |
| Failure modes | helper 替换时漏掉某个读取点；移除 `io` import 时误删测试所需 import；本地没有 Go 工具链导致无法跑单测。缓解：逐行检查 `socks5Handshake`，使用 `rg` 验证生产文件无残留 `io.ReadFull`，缺工具链如实记录。 |
| Decision points | 无需 Owner 中途确认；若发现需要删除文件或改高风险核心，停止并请求确认。 |
| Pre-execution checklist | 1. 已重新读取 goal objective；2. 已确认不读取/修改 security scan 计划；3. 已读取 gateway risk review skill；4. 已查看当前 `proxy_dialer.go` 与 SOCKS5 测试；5. 修改后跑 `git diff --check`、clean-room 关键词扫描、Go 工具链探测。 |
