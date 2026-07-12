# 2026-07-10 openai-compat OAuth Grok S1（合成执行计划）

## 合成来源

- PM/Owner 侧：2026-07-10 本任务指令，已明确生产改法、判别性要求、live E2E 目标和四道验证门。
- Codex 侧：`2026-07-10-openai-compat-oauth-grok-s1-codex.md`，在未读取同名 Claude 计划的前提下独立形成。
- 仓库中不存在同任务 Claude 独立计划；本文件不伪造该产物，而以 Owner 已批准的逐项任务指令作为 PM 侧执行契约。

## 交叉结论

### 一致项

1. 最窄生产改动是在 openai-compat adapter 的凭据白名单和请求头分支同时加入 OAuth access token，并按 Bearer 发送。
2. 不改 OAuth 获取/刷新、凭据物化、provider 注册、端点选择或 SSRF 守卫。
3. 单元测试必须同时证明 OAuth 新路径和 API key、upstream passthrough 两条旧路径的精确 Authorization 语义。
4. live E2E 必须删除旧 S1 记录/预期失败逻辑，改为 HTTP 200 后对用量、计费、余额与 hold、并发槽、配额及并发请求进行硬断言。
5. 只格式化改动文件，执行 Owner 指定四道门，不 commit。

### 冲突项

无。

### 一侧补充项

- Codex 计划补充：先用旧生产代码运行新增聚焦测试，保留它确因“不支持的凭据形态”失败的证据；最终检查 diff 不含凭据值日志，也不改变 `EndpointForBuildInput` 边界。
- Owner 指令补充：最终报告逐项给出六类 live E2E 颗粒度、变异为何转红及 OAuth→Bearer 对其它 provider 无影响的安全论证。

## 权威执行顺序

1. 完成只读补丁契约：确认 registry、凭据物化、adapter 和 live E2E 的当前接缝。
2. 在既有 adapter 测试中加入一条表驱动判别测试，覆盖 OAuth、API key、upstream passthrough；先证实 OAuth 用例在旧代码上失败。
3. 最小修改 adapter 白名单与 Authorization 分支，添加中文机制注释。
4. 删除 live E2E 旧阻塞记录逻辑，确认主路径和并发子用例均为通过型硬断言，并保留每类断言的中文变异说明。
5. 仅对改动 Go 文件执行 `gofmt`，审查 diff 和安全边界。
6. 执行 `go test -count=1 ./internal/provider/`、`go vet ./internal/provider/ ./cmd/gateway/`、`go build ./...`、`go build -tags e2e_grok_live ./cmd/gateway/`，如实记录输出与退出码。
7. 核对最终行号、工作区和 `git diff --check`；不暂存、不提交；用中文报告。

## 停止条件

若修复需要 schema、认证核心、计费/配额生产逻辑、依赖、真实凭据、端点或 SSRF 守卫变更，则停止并请求 Owner；当前已核验路径不需要这些扩展。
