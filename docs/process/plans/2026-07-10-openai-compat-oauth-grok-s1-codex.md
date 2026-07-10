# 2026-07-10 openai-compat OAuth Grok S1（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “修 S1——openai-compat 出站 adapter 接受 OAuth access token（打通 Grok 账号→API 整链）”；“代码注释全中文、报告全中文”；“禁止 commit”。 |
| Scope | 范围内：`internal/provider/openai_compat_passthrough.go` 的凭据白名单与 Bearer 注入、同包判别性单元测试、`cmd/gateway/grok_live_e2e_test.go` 中旧 S1 阻塞记录的移除及通过型整链断言校准、指定构建/测试门。范围外：OAuth 获取或刷新、凭据存储格式、provider 注册、端点选择、SSRF 守卫、数据库结构、真实 live 执行、提交。 |
| Success criteria | OAuth access token 能通过 openai-compat adapter 并严格生成 `Authorization: Bearer <token>`；API key 与 upstream passthrough 旧语义均由正向断言保护；删除 live E2E 的预期失败/仅记录逻辑后，HTTP 200、用量、计费、余额与 hold、并发槽、配额以及并发路径仍为硬断言；四条 Owner 指定门全部通过；仅格式化改动文件；不提交。 |
| Time estimate | 约 25–45 分钟墙钟时间；单 agent 约 30–60 分钟，主要取决于全仓构建与测试耗时。 |
| Blast radius | 生产行为只扩展 `OpenAICompatPassthroughAdapter` 的一种已存在凭据类型；错误实现可能泄露或错误拼接 Authorization、破坏 API key / 透传语义，或让 live E2E 继续假绿。测试文件会触及真实网关整链验证，但不在普通 build tag 下执行 live 请求。 |
| Failure modes | 1. 只改 switch 未改白名单，仍在前置检查失败：用 OAuth 正向测试同时钉住接受列表和 BuildRequest。2. 错给 passthrough 加 Bearer：分别断言 API key 与 passthrough 精确头值。3. 放宽端点控制：不改 `EndpointForBuildInput` 及其调用顺序，并在 diff 复核。4. live E2E 仍保留 `Error`/跳过/xfail：定向搜索所有阻塞文案与 `Skip`。5. 改动覆盖用户未跟踪文件内容：先逐段阅读，只做局部补丁，最终逐文件 diff。6. 全仓既有失败：逐条记录真实命令、退出码与首个错误，不伪报绿色。 |
| Decision points | Owner 已明确 OAuth→Bearer 语义及测试门，无待决产品选择。若必须改 schema、认证核心、计费/配额生产逻辑、添加依赖或更改端点/SSRF 守卫，立即停止并请求 Owner；当前计划不包含这些动作。 |
| Pre-execution checklist | 1. 阅读规则、技能与目标实现/测试；2. 确认工作区用户改动；3. 写本独立计划且不预读 Claude 同名计划；4. 查找并核对 Claude 独立计划或 Owner 已批准的合成计划；5. 建立补丁契约与最小失败测试；6. 修改前再次确认不触及高风险范围。 |

## 具体执行顺序

1. 精读 adapter 既有测试与 live E2E 全部断言，列出旧 S1 记录分支和六类颗粒度证据。
2. 先补 OAuth 判别测试及 API key、upstream passthrough 兼容断言；在可行范围内先运行聚焦测试，确认旧代码因“不支持的凭据形态”失败。
3. 最小修改 adapter：接受 OAuth access token，并与 API key 同样注入 Bearer；中文注释只描述 HUAKAI 自身凭据语义。
4. 移除 live E2E 旧接缝记录/预期失败逻辑，保留或强化真正通过型的 HTTP 200、六类结算证据与并发断言及中文变异说明。
5. 仅对实际改动的 Go 文件执行 `gofmt`，审阅 diff，确认端点与 SSRF 守卫未变化、无凭据值进入日志。
6. 依次运行聚焦单测、`go vet`、全仓 build、live build-tag build；如某门失败，仅修本任务引入的问题，并如实留存输出。
7. `git diff --check`、`git status --short` 和最终行号核对；不暂存、不提交；用中文报告固定结果与未执行的真实 live 限制。
