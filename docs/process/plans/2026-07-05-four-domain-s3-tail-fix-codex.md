# 2026-07-05 四域审计 S3 尾批 fix-in-place

| Owner directive | “四域审计 S3 尾批 fix-in-place(hermes 3 条 + mimicry ME-3/ME-4)” |
| Scope | 仅处理 HERMES-IP-01、HERMES-IP-02、HERMES-IP-03、ME-3、ME-4；允许触碰 `cmd/gateway/`、`internal/hermesconfirm/`、`internal/hermeshttp/`、`internal/hermesops/`、`internal/provider/`、`internal/transport/` 与本计划文档；不改 `internal/mediatask/`、`internal/billing*`、`internal/quota/`、`frontend/`；不 commit/push。 |
| Success criteria | 每条先亲读确认缺口；缺口仍在则最小修复；每条配可判别测试；完成 cp 备份还原式 §14 变异证红；指定门禁全绿或如实记录失败。 |
| Time estimate | 约 2-4 小时墙钟；主要时间在补测、变异证据与 `go test ./...` 子集运行。 |
| Blast radius | Hermes 影响 admin/operator mutating dry-run/confirm 路径与 LLM propose 入口；provider 影响 upstream passthrough 自定义 endpoint 与代理组合的错误可诊断性；transport 影响 sidecar fallback 到 Go-native uTLS 时的 ALPN force-h1 决策。 |
| Failure modes | 错误码变更破坏既有测试；过度放宽 passthrough 代理安全语义；transport fallback 缓存复用导致 force-h1 测试不稳定；大文件继续膨胀触发 codebudget。缓解：只增加可区分错误，不放行已拦截组合；新 helper 放在小文件或既有小文件；运行目标包测试与 codebudget。 |
| Decision points | 不触碰高风险禁区；若发现需要 schema、auth、billing、quota、依赖或部署脚本修改，停止并请求 Owner 确认。本批未计划新增依赖。 |
| Pre-execution checklist | 1. 已读取涉及代码并确认当前缺口；2. 写入本计划；3. 先加最小测试锁定期望；4. 实现最小修复；5. 跑目标测试；6. 用 cp 备份/还原执行每条变异证红；7. 跑指定门禁；8. 输出中文报告。 |

## 亲读确认摘要

- HERMES-IP-01：`cmd/gateway/wiring.go:887` 与 `cmd/gateway/wiring.go:897` 分别解析 mutating 与 propose，`cmd/gateway/hermes_internal_tools_wiring.go:40` 会在 propose 开启时注入可提议目录，`internal/hermeschat/internal_tool_handler.go:252` 只检查 propose 开关，`cmd/gateway/routes.go:385` 在 mutating 关闭时不接线 mutator，存在 “propose 可走到 confirm 才失败” 的组合。
- HERMES-IP-02：`internal/hermesconfirm/cache.go:35` 仍是进程内 map，`internal/hermeshttp/tools_mutate_handler.go:152` 消费失败统一回 `hermes_tool_confirmation_invalid`，未知/跨副本/过期 token 不可区分。
- HERMES-IP-03：`internal/hermesops/tools_mutating_dlq_renew.go:86` 直传 Replay 错误，`internal/hermeshttp/tools_mutate_handler.go:396` 默认映射为泛化 `hermes_tool_failed`。
- ME-3：`internal/provider/passthrough_endpoint_guard.go:166` 对 `base.Proxy != nil` fail-closed，但只返回普通 blocked reason，运维不可用稳定错误码定位“不兼容代理+自定义 endpoint”。
- ME-4：`internal/transport/factory.go:348` sidecar 腿使用 `SidecarForceH1`，但 `internal/transport/factory.go:206` / `221` 的 Go-native fallback 经 `nativeMimicryRoundTripper`，底层 `internal/transport/mimicry/utls_dialer.go:42` 只读 env 默认。

## Concrete execution order

1. Hermes propose/mutating：新增有效 propose 开关归一化 helper 与 `cmd/gateway` 测试；在 wiring 解析后应用，冲突组合记录告警并关闭 propose。
2. Hermes confirm miss：为 `hermesconfirm.Cache` 增加可区分 consume 状态，HTTP 未命中/过期映射为 re-propose 错误码；保持绑定不匹配仍 fail-closed。
3. Hermes DLQ 幂等：为 DLQ replay 已处理增加 sentinel，`Replay` 返回 `dlq.ErrNotFound` 时转为非错误 ToolResult；补 `hermesops` 测试，必要时补 HTTP 映射测试。
4. ME-3：新增 provider sentinel / predicate，让代理+自定义 endpoint 的运行期拦截返回 `config_incompatible_proxy_custom_endpoint` 类原因；补 `internal/provider` 判别测试。
5. ME-4：新增 factory 内部构造 Go-native uTLS 的 force-h1 覆盖路径，不改变 env 默认；补 `internal/transport` 判别测试。
6. 运行目标测试、逐条变异证红、再运行完整指定门禁。
