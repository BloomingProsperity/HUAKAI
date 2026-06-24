# 2026-06-24 backend quality renew round115 relaybody limit convergence

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；目标文档指出多个 relay handler 各自硬编码 `maxRequestBodyBytes`，兄弟端点不读 `HUAKAI_MAX_REQUEST_BODY_MB`，存在 env 死开关不一致。 |
| Scope | 将 `relaybody` 扩展为兄弟 relay 端点的统一入站请求体上限配置点；让 `completionshttp`、`embeddingshttp`、`rerankhttp`、`imageshttp`、`geminihttp` 使用该统一上限；`cmd/gateway` 启动时用同一个 `HUAKAI_MAX_REQUEST_BODY_MB` 同步配置 `gatewayhttp` 与 `relaybody`。 |
| Out of scope | 不改 `gatewayhttp` chat 主链读取实现；不改 audio multipart 上限；不改 provider/dispatch/billing 行为；不改 schema；不触碰另一目标计划文件。 |
| Success criteria | 兄弟 relay 端点不再各自用本地 `maxRequestBodyBytes` 常量控制入站 body；`relaybody` 有默认 32MiB 与可配置 setter；`cmd/gateway` 对 `HUAKAI_MAX_REQUEST_BODY_MB` 同时配置 chat 主链和 `relaybody`；新增测试覆盖 setter 与读取上限。 |
| Time estimate | 约 30-40 分钟；单 agent 小切片。 |
| Blast radius | 中。影响多个 relay handler 的 body limit，但默认值与 chat 主链保持 32MiB，且只改变合法大请求不再被 2MiB/4MiB 硬拒；超限仍由 `MaxBytesReader` 拦截。 |
| Failure modes | 1. import 漏改导致编译失败；2. 包级配置在测试间污染；3. 错误地放大非 relay 控制面上限；4. 本地无 Go 工具链无法编译验证。缓解：新增 `relaybody` 单测、只改使用 `relaybody.ReadLimitedRequestBody` 的数据面 handler、静态 token 检查、`git diff --check`。 |
| Decision points | 无需 Owner 额外确认；这是 medium-risk 小实现改动，未触碰高风险文件。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 acceptance-test-writer 技能；3. 已确认 `relaybody.ReadLimitedRequestBody` 已被兄弟 relay handler 使用；4. 已确认 `cmd/gateway` 已从 `HUAKAI_MAX_REQUEST_BODY_MB` 得到 `maxRequestBody`；5. 编辑后做静态验证。 |

## 执行顺序

1. 在 `relaybody` 中新增默认 32MiB、`ConfigureRequestBodyLimit`、`RequestBodyLimit` 与测试。
2. `cmd/gateway/middleware.go` import `relaybody` 并在启动阶段同步配置。
3. 将 completions/embeddings/rerank/images/gemini 的 `ReadLimitedRequestBody` 调用改为 `relaybody.RequestBodyLimit()`。
4. 移除这些包里本地 `maxRequestBodyBytes` 常量。
5. 静态验证：搜索硬编码残留、import token 检查、clean-room 禁词、`git diff --check`；尝试 `gofmt`/`go test` 并如实记录工具链缺失。
