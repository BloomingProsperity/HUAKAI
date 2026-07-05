# 2026-07-05 codebudget 历史超标六项拆分 Codex 计划

| Owner directive | “codebudget 历史超标六项拆分(行为零变化)” |
| --- | --- |
| Scope | 仅处理 `internal/codebudget` 当前红灯中的六项体量超标：`internal/gatewayhttp/chat_completions_attempt.go`、`internal/gatewayhttp/chat_completions_dispatch.go`、`internal/gatewayhttp/chat_completions_stream.go`、`internal/payment/store_postgres.go`、`internal/subscription/store_memory.go`、以及 `internal/gatewayhttp` 包级非测试行数。禁止修改 `baseline.json` 数值过关；禁止 commit/push；避开 Owner 列出的禁改路径。 |
| Success criteria | `go test ./internal/codebudget -count=1` 绿色；六项行数低于规则阈值，`internal/gatewayhttp` 包级非测试行数压到 `<=13700`；被搬移的代码块逐行在新文件中可核对；指定 build/vet/test 门禁完成并记录结果。 |
| Time estimate | 墙钟约 60-120 分钟；主要耗时在结构阅读、最小导出面选择、逐行核对和全量门禁。 |
| Blast radius | `gatewayhttp` 的 chat completions 请求分发、重试、流式响应路径；payment PostgreSQL store；subscription memory store。预期行为零变化，风险来自跨包搬移导致可见性或循环 import。 |
| Failure modes | 跨包搬移形成 import cycle；导出符号过多导致 API 面扩大；搬移时误改逻辑；测试环境缺少 PostgreSQL；既有其他车道改动与本次拆分冲突。缓解：先读结构和调用关系；优先整块搬移纯 helper；保留原函数体顺序与内容；使用 diff 和逐行核对；集成测试失败时区分环境问题与代码问题。 |
| Decision points | 若必须触碰 Owner 禁改文件、高风险路径、数据库 schema、认证/计费账本/配额核心、或新增运行依赖，则停止并请求 Owner 确认。本次预期不需要。 |
| Pre-execution checklist | 1. 读取 `docs/RULES.md` 相关启动与执行规则。2. 记录当前 git 状态，避免覆盖他人改动。3. 运行或读取 `internal/codebudget` 当前输出确认六项。4. 读取五个目标文件结构和 `gatewayhttp` 调用关系。5. 选择仅靠整块搬移即可降线的 helper/职责块。6. 编辑后用 diff 与行数统计核对。7. 运行 Owner 指定门禁。 |
| Concrete execution order | 1. 读取规则、baseline、codebudget 测试与目标文件。2. 对 `gatewayhttp` 三个文件识别可搬入子包的自洽块，优先搬移无 `*Server` 私有状态依赖或可用最小接口承载的辅助逻辑。3. 对 `payment` 与 `subscription` 按查询域拆同包新文件。4. 用 `apply_patch` 搬移整块代码，不做逻辑改写。5. 逐项统计前后行数并执行逐行核对。6. 运行 build/vet/codebudget/相关包测试/可用 PostgreSQL 集成测试。7. 输出中文报告。 |

备注：按项目并行计划规则，理想流程应有 Claude 与 Codex 各自独立计划后合成。本次 Owner 直接派发给 Codex 且要求完成具体门禁，当前先记录 Codex 独立计划并将执行过程限制为低风险/中风险的机械拆分；若执行中出现高风险决策点，将停止等待 Owner。
