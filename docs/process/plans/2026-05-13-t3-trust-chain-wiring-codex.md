# 2026-05-13 T3 Trust Chain Wiring（Codex）

| 项 | 内容 |
| --- | --- |
| Owner directive | "HUAKAI trust-chain T3 wiring — forwarder + handler 写入 HopChain + ModelChain 字段" |
| Scope | In: `backend/internal/gateway` forward/finalize trust-chain helper, `backend/internal/gatewayhttp` model-chain/header helper, focused tests. Out: dispatcher, DB schema, auth/billing/quota core, external reference-project source. |
| Success criteria | `Forward` / `FinalizeUpstream` 根据 `ForwardRequest` 写 4-hop chain；chat handler 写 requested/route model；non-streaming 响应有 `X-HUAKAI-Model-Requested` / `X-HUAKAI-Model-Delivered`；detail 不含 prompt/completion/messages/content；新增 4-6 个单测；目标 go test 与 go vet 通过。 |
| Time estimate | 1-2 小时 wall clock；Codex 单工作流。 |
| Blast radius | Streaming forwarder draft envelope metadata、non-streaming HTTP response headers、测试桩。 |
| Failure modes | 误把敏感 request body 放进 hop detail；model header 从 wrong source 取值；破坏 existing forwarder tests；helper 文件超过 250 LoC。Mitigation: detail 白名单、header helper 单测、跑 gateway/gatewayhttp tests 和 vet。 |
| Decision points | 无需 Owner 追加确认；Owner 已明确“不要问 Owner”。若发现必须改 DB schema/auth/billing/quota/dispatcher，则停止。 |
| Pre-execution checklist | 1. 写 `/tmp/codex-t3-wiring.txt` stub；2. 读 `ForwardRequest`、`StreamForwarder`、chat handler、proto trust-chain 类型；3. 新增 helper 文件；4. 最小改入口接线；5. 加 4-6 单测；6. gofmt；7. `go test ./internal/gateway/... ./internal/gatewayhttp/...`；8. `go vet ./internal/gateway/... ./internal/gatewayhttp/...`；9. 写 `/tmp/codex-t3-wiring-final.txt`。 |

Clean-room note: this task only reads HUAKAI internal code and docs. No non-MIT reference-project source is in scope, so CLAUDE.md #11 lane guard is not triggered.
