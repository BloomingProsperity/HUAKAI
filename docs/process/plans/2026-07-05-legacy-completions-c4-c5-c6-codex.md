# 2026-07-05 legacy completions C-4/C-5/C-6 对齐计划

| 字段 | 内容 |
| --- | --- |
| Owner directive | “任务:legacy completions 链路三处与 chat 的配合分叉对齐(C-4/C-5/C-6)”；“不许 git commit”；“不改 internal/quota/ 与 internal/gatewayhttp/” |
| Scope | 仅实现 `internal/completionshttp/` 及必要同型端点包的低风险对齐；`internal/quota/` 与 `internal/gatewayhttp/` 只读取作口径核实，不修改；不改数据库 schema、部署脚本、真实密钥、`LICENSE`。 |
| Success criteria | C-5 预检传 `ReservedTokens`；C-6 预测价包含输出估算；C-4a fail-open 有计数与告警；C-4b ratio pending 信号进入 completions 结算 snapshot；quota deny 响应带 `Retry-After`；所有新增/修改测试有变异红与还原绿证据；指定 `gofmt`、`go vet`、`go build`、`go test` 门通过。 |
| Time estimate | 约 2-4 小时墙钟；其中阅读与口径核实 30-45 分钟，代码与测试 60-120 分钟，变异与全量验证 45-90 分钟。 |
| Blast radius | 配额预留、余额 hold、结算 usage snapshot、同型 HTTP 端点错误响应与可观测性。若实现错误，可能造成拒绝语义变化、计费金额偏差或测试假阳性。 |
| Failure modes | 误判 `inputEstimate` 口径导致 token 窗预留偏差；输出 token 默认值与 chat 不一致；pending marker 未写入实际结算记录；同型端点没有估算来源时强行造估算；测试只验证非空而非精确行为；变异后未完全还原。缓解: 先读基准与现有路径，逐项引用 file:line；用 fake 捕获结构化输入；每个变异后立即还原并跑绿。 |
| Decision points | 若需要改 `internal/quota/`、`internal/gatewayhttp/`、数据库 schema、真实计费账本写入模型或新增 runtime dependency，立即停止请求 Owner 确认；若同型端点没有现成输入估算器，不新增估算器，只报告原因。 |
| Pre-execution checklist | 1. 确认工作树已有非本人改动并避免覆盖。2. 只读核实 chat 基准实现、completions 现状、同型端点现状。3. 记录 `inputEstimate`、输出估算、pending marker、Retry-After 的口径证据。4. 小步修改实现。5. 补测试并先跑定向测试。6. 按缺陷逐项变异，记录红；还原后记录绿。7. 执行最终门。 |

## 具体执行顺序

1. 读取 `internal/gatewayhttp/` 相关只读基准、`internal/completionshttp/` 现有计费/定价/测试，以及 `imageshttp`、`embeddingshttp`、如存在的 `audio`/`rerank` 同型包。
2. 在 `completionshttp` 中补齐 `ReservedTokens`、预测价输出估算、fail-open 计数告警、ratio pending snapshot 标记、quota deny `Retry-After`。
3. 对同型端点逐包核实；只在有现成估算或缺失可观测性/响应头且不触碰禁改目录时补齐。
4. 增加聚焦测试，优先复用包内 fake 与 handler 测试惯例。
5. 执行指定变异: 分别去掉 `ReservedTokens`、输出成本、fail-open 计数、pending 信号、`Retry-After` 头，确认测试红；还原后确认测试绿。
6. 执行 `gofmt -l`、`go vet`、`go build ./...`、改动包 `go test -count=1`，并检查修改文件行数不超过 600。
