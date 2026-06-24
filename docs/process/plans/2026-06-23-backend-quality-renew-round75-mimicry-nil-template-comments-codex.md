# 2026-06-23 backend quality renew round75 mimicry nil template comments

| Owner directive | “做完了？ 这么快？ 这么大的项目你这么快？”；继续执行 `/home/ubuntu/.codex/attachments/d57bb3d8-3863-4495-8d9b-df5562c0eb27/goal-objective.md` 的后端代码质量与架构刷新审查 |
| Scope | 仅限 `backend/internal/transport/mimicry` 的小型质量闭环：uTLS nil 模板前置拒绝、同包 Go 注释中文化。明确不触碰另一个 backend security plan。 |
| Success criteria | `UtlsDialer.DialTLS` 在模板为空时不发起网络拨号并返回明确错误；新增判别式测试覆盖该路径；同包本次接触注释不再出现英文散文或外部项目名。 |
| Time estimate | 15-25 分钟墙钟；1 个 Codex 小切片。 |
| Blast radius | 传输伪装层构造错误路径；正常非 nil 模板拨号不应受影响。 |
| Failure modes | nil 模板 guard 放在拨号之后导致测试假绿；注释改动误碰外部项目名；Go 工具链缺失导致无法本地 gofmt/go test。缓解：用白盒测试禁止拨号、`rg` 扫描敏感词、`git diff --check` 检查空白。 |
| Decision points | 若需要改变真实协议行为、启用/删除 sidecar、改 H2/H1 策略，停止等待 Owner；本轮不做这些。 |
| Pre-execution checklist | 1. 已重新读取 goal objective；2. 已核 `utls_dialer.go` 与现有 ForceH1 测试；3. 确认本轮不读取或编辑 `2026-06-23-backend-security-scan-codex.md`；4. 修改前先定位 `dialRaw` 与测试放置点。 |

## Concrete execution order

1. 读取 `utls_dialer.go` 的 `DialTLS` / `dialRaw` 区域和现有测试结构。
2. 在 `DialTLS` 开头增加 nil 模板前置拒绝。
3. 新增测试，使用会立即 `t.Fatal` 的 `ProxyDialer` 证明 nil 模板不会进入拨号层。
4. 中文化本轮接触到的英文 Go 注释。
5. 运行 `git diff --check`、敏感词扫描；尝试 `gofmt` / `go test`，如工具链缺失则如实记录。
