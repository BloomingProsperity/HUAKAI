# 2026-06-23 backend quality renew round81 socks5 writefull

| Owner directive | "做完了？ 这么快？ 这么大的项目你这么快？"；继续按 `/home/ubuntu/.codex/attachments/d57bb3d8-3863-4495-8d9b-df5562c0eb27/goal-objective.md` 做后端代码质量 renew，小步闭环，不触碰另一个目标。 |
| Scope | 仅限 `backend/internal/transport/mimicry` 中 SOCKS5 握手写入完整性与对应测试；不触碰 auth、billing、quota、数据库 schema、部署、`LICENSE`、真实密钥，也不读取或修改 security scan 计划。 |
| Success criteria | SOCKS5 方法协商、认证帧、CONNECT 帧写入都使用完整写入 helper；短写入或零进展写入会 fail-loud；新增判别测试能覆盖该失败路径；clean-room 关键词扫描无命中；可运行的本地检查执行并记录结果。 |
| Time estimate | 约 10-20 分钟；一个 Codex 小闭环。 |
| Blast radius | 低。只影响代理握手错误处理；成功路径协议字节不变。若补丁错误，SOCKS5 代理拨号会提前失败，HTTP CONNECT 与 uTLS 指纹模板不受影响。 |
| Failure modes | 复用 helper 时引入过宽错误文案导致测试脆弱；测试 fake 不符合 `net.Conn` 语义；缺少 Go 工具链导致无法实际跑单测。缓解：只断关键错误类型/子串，fake 最小实现，缺工具链如实记录。 |
| Decision points | 无需 Owner 中途确认；若发现需要改 schema、auth、billing、quota、部署或删除文件，立即停止确认。 |
| Pre-execution checklist | 1. 已重新读取 goal objective；2. 已确认不碰另一个 security 目标；3. 已读取 gateway risk review skill；4. 读取 `proxy_dialer.go` 与现有 proxy 测试；5. 修改前先限定 diff 范围；6. 修改后跑 `git diff --check`、clean-room 关键词扫描、可用 Go 检查。 |
