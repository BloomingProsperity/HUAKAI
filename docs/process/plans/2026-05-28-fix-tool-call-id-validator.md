# 2026-05-28 tool_call_id 校验修复
| Owner directive | 实现 `backend/internal/proto` 工具调用 ID 后缀校验修复（支持真实 provider ID）并更新判别性测试；运行 `go test ./internal/proto/...` 与 `go build ./...` 验证 |
| Scope | 仅编辑 `backend/internal/proto/tool_call_id.go` 与 `backend/internal/proto/proto_test.go`；不新增文件、不改其他包；不改 `LICENSE`、依赖或配置。 |
| Success criteria | `isHexID` 更名为 `isValidCallIDSuffix`；允许 `[A-Za-z0-9_-]` 并限定长度 <=256；错误返回 `ErrToolCallIDTranslationFail` 对空/非法/过长后缀。`TestAT_PROTO_002_12` 使用真实 upstream ID 且含判别性反例，旧实现会失败、新实现通过。 |
| Time estimate | 约 15 分钟编辑，5 分钟验证 |
| Blast radius | 受影响仅为协议层 tool-call ID 规范化与相关测试；失败可能影响多 provider tool-use SSE 的回合一致性。
| Failure modes | 1) 验证器仍然误拒真实 ID（长度或字符集错误）会再度破坏多轮工具调用；2) 测试仍非判别性（旧实现不失败）；3) 改动不触及前缀/后缀双向映射导致回传 ID 丢失。 |
| Decision points | 是否采用 256 作为长度上限；是否将长度与空值作为单独测试场景；是否立即补充更多 upstream ID 用例。 |
| Pre-execution checklist | 1) 阅读目标文件；2) 修改器并保持双向映射语义；3) 用真实 ID 与非法字符用例更新测试；4) 运行 `GOCACHE=/home/ubuntu/.cache/go-build /usr/local/go/bin/go test ./internal/proto/... -count=1`；5) 运行 `GOCACHE=/home/ubuntu/.cache/go-build /usr/local/go/bin/go build ./...`；6) 汇报变更与输出。 |
