# 2026-07-05 mimicry 出口两处 S2 健壮性修 Codex 计划

| Owner directive | "任务:mimicry 出口两处 S2 健壮性修(ME-1 锁 convoy + ME-2 基础设施故障误判账号冷却)"；"禁止 git commit/push"；"先亲读三镜(sub2api/new-api/CLIProxyAPI 的出口/transport 故障分类)再改"；"每处配判别测试+§14 变异证红(cp 备份还原)" |
| Scope | 只改 `backend/internal/transport/` 与 `backend/internal/gatewayhttp/` 中完成 ME-1/ME-2 所需的最小代码和测试；可新增本计划文档与最终报告引用；不改 `internal/billing/`、`internal/billingreconhttp/`、`internal/mediatask/`、`cmd/gateway/routes.go`、`internal/quota/`，不 commit/push。 |
| Success criteria | ME-1 并发挂起 sidecar 时探测不随请求数线性重复，测试能在旧行为下变红；ME-2 sidecar/本地基础设施错误不产生 per-account 冷却信号，真实上游 401/403 等账号错误仍产生冷却信号，测试能在旧行为下变红；指定门禁全绿。 |
| Time estimate | 墙钟约 2-3 小时；主要耗时在三镜源码定位、最小修法、变异验证与全量 `go build ./... && go vet ./...`。 |
| Blast radius | ME-1 触碰 transport factory 缓存/探测路径，风险是正常 sidecar 发现被误缓存或失败恢复变慢；ME-2 触碰 gatewayhttp dispatch error 到冷却信号的映射，风险是真账号故障被误判为基础设施故障导致不冷却。 |
| Failure modes | 参考源码证据不足：只写已读 file:line 的观察，缺口写 open question；ME-1 负缓存 TTL 太长：使用短 TTL 且只缓存失败探测结果；ME-2 分类过宽：基于 HUAKAI 现有错误类型做白名单式 infra 判断，保留 HTTP 401/403/额度类账号信号；测试不判别：用 cp 备份还原制造旧行为并记录红灯。 |
| Decision points | Clean-room 规则存在张力：通用规则要求实现车道不读非 MIT 源码，但本轮 Owner 明确要求 Codex 亲读三镜后再改；本计划按本轮任务执行，只抽取行为级证据与 file:line，不复制上游命名、结构、注释或测试。若发现必须改高风险禁区文件，停止并请求 Owner 确认。 |
| Pre-execution checklist | 1. 读取 HUAKAI 规则与 clean-room policy；2. 检查当前工作树，避免覆盖他人改动；3. 定位三镜源码与相关出口/transport 故障分类区域，记录 HEAD SHA 和 file:line；4. 读取 HUAKAI 现有 transport factory、dispatch error、冷却信号定义与测试模式；5. 先补判别测试，再做最小修复；6. 运行变异证红；7. 恢复正确实现后运行门禁。 |

## 具体执行顺序

1. `git status --short` 与相关文件读取，确认是否有未归属改动。
2. 定位 `/home/ubuntu/refs/` 下 sub2api、new-api、CLIProxyAPI 的出口/transport 故障分类代码，仅记录行为观察、commit SHA、file:line。
3. 阅读 `internal/transport/factory.go`、`internal/gatewayhttp/chat_completions_error.go`、相关 error 类型、cooldown/signal 测试。
4. 为 ME-1 写并发挂起 sidecar 判别测试，先确认当前行为红或可通过变异红证明。
5. 最小修复 ME-1：优先采用短 TTL 失败负缓存或现有结构最贴合的 per-target 去重，避免大重构。
6. 为 ME-2 写基础设施错误与真实账号错误的判别测试。
7. 最小修复 ME-2：只在 `signalFromDispatchError` 或其近邻分类处增加 HUAKAI 本地 infra 错误识别，账号错误路径保持不变。
8. 使用 `cp` 备份/还原执行两处变异证红，记录命令和失败测试摘要。
9. 恢复正确实现并运行：
   - `go build ./...`
   - `go vet ./...`
   - `go test ./internal/transport ./internal/gatewayhttp ./internal/codebudget -count=1`
10. 输出中文报告：三镜对照、缺口确认、修法、变异证据、门禁摘要、风险与需 Owner 确认事项。
