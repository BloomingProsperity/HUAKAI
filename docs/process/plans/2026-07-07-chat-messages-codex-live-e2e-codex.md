# 2026-07-07 chat/messages 到 codex live e2e 子测试

| Owner directive | "写 chat/messages→codex 的 live e2e 子测试(探 D2 + 多能力),PM 亲跑真实账号" |
| --- | --- |
| Scope | 仅在 `backend/cmd/gateway/codex_live_e2e_test.go` 的 `e2e_codex_live` build tag 下新增 live 子测试与测试 helper；复用既有 codex live harness。 |
| Out of scope | 不改生产码、不改数据库 schema、不加依赖、不改 `LICENSE`、不提交、不推送、不使用 GitHub/web。 |
| Success criteria | `go build -tags e2e_codex_live ./cmd/gateway/`、`go vet ./cmd/gateway/`、`gofmt -l cmd/gateway/codex_live_e2e_test.go`、`go test ./cmd/gateway/ -count=1` 运行完成；新增 live 子测试覆盖 chat 流式、D2 字段探测、工具调用、非流式聚合、vision、Anthropic Messages 到 codex。 |
| Time estimate | 约 60-90 分钟墙钟；主要时间用于读 harness、补解析 helper、跑非 live 门禁。 |
| Blast radius | 变更限定在 build tag live 测试文件；默认非 live 测试不会编译该文件。若 helper 命名或导入错误，会影响 `e2e_codex_live` 编译门。 |
| Failure modes | chat SSE/JSON 响应解析不匹配现有转换层；D2 失败 body 没带足字段；非 live 门禁受仓库既有状态影响；live token/数据库缺失导致 PM 本地跳过或失败。 |
| Mitigation | 复用现有启动、seed、PG claim、usage、in-flight helper；HTTP 非 200 断言直接打印完整响应 body；只运行非 live 编译与回归，不代替 PM 打真实账号。 |
| Decision points | 若必须改生产 adapter 剥离字段、认证核心、计费/额度、schema 或新增依赖，立即停止等待 Owner 确认。本轮不做这些高风险动作。 |
| Pre-execution checklist | 1. 确认 cwd 在 `/home/ubuntu/HUAKAI/backend`。2. 读取 `docs/RULES.md` 与 acceptance-test-writer 技能。3. 读取现有 codex live e2e harness。4. 新增测试前确认已有 helper 可复用。5. 修改后 gofmt 与指定门禁。 |
| Concrete execution order | 1. 阅读现有 live harness 后识别可复用函数与结果结构。2. 新增 `TestChatToCodexLiveMatrix`，共用 seed/build/start/assert helper。3. 增加 chat/Anthropic 请求发送和响应解析 helper。4. 对 D2 子测试强化 400 body 输出。5. 运行门禁并记录真实输出。 |

备注：本轮是 Owner 直接派发给 Codex 的低到中风险 live 测试补丁。按任务要求不启动 GitHub/web，也不执行 commit/push；如果 Owner 要求完整平行计划交叉讨论，可在提交前补 Claude 独立计划与综合计划。
