# 2026-07-12 后端切片 B2+B4 独立 Codex 计划

| 项目 | 内容 |
| --- | --- |
| Owner directive | “你是 HUAKAI 的写码 agent……任务：后端切片 B2+B4 完整版（同 sqlc 文件域，按顺序做；做全，不留缺口）” |
| Scope | 范围内：`backend/` 的 SQL 真源、sqlc 生成镜像、B2 管理端查询与路由、B4 缓存观测字段与运行时设置接线、判别性测试；明确要求的 `docs/openapi/openapi.yaml`、中文运维文档及本计划；`/tmp` 交付报告。范围外：全部前端、数据库表结构、认证/计费/配额核心、提交与推送、DeepSeek 缓存计费修复。实现者车道不读取非 MIT 参考源码，只依据 Owner 已给出的行为契约和 HUAKAI 内部模式实现。 |
| Success criteria | B2 查询同时受账号与租户约束，端点返回规定字段且正确处理默认/钳制 limit、时延、TTFT、越权 404；B4 两个缓存创建 token 字段透出，设置默认关闭，开启只改自动注入的 Anthropic Messages 断点且通过 TTL 排序，客户端原有 `cache_control` 保持字节直通；OpenAPI 一致；指定 build/test/staticcheck 门禁通过或如实记录环境阻碍；DeepSeek 核对结论有证据。 |
| Time estimate | 墙钟约 90–150 分钟；单 agent 工时约 2–3 小时，主要取决于 sqlc、集成 PostgreSQL 与全量静态检查耗时。 |
| Blast radius | SQL 行类型会影响 5 个既有 `ListUsageRecords` 消费者；`ChatHandlerDeps` 接线会影响网关所有聊天协议入口；设置默认值会影响平台设置列表；OpenAPI 会影响契约一致性测试。默认关闭与尾部追加列用于压低运行时风险。 |
| Failure modes | sqlc 不可用：先改 `.sql` 真源，再按生成器现有字节风格同步生成文件并核对接口；列扫描错位：生成后编译并逐个核查消费者；租户越权：SQL 双谓词与 handler 404 双层防御；TTL 顺序非法：只在 Anthropic Messages 自动注入路径设置并复用 `ValidateTTLOrdering`；设置未接线：增加删除注入即失败的判别测试；无 PostgreSQL：保留集成测试并在报告记录未运行原因；脏工作树：只触碰本切片文件，不改已有前端变更。 |
| Decision points | 当前无新增 Owner 决策点：零新表、默认关闭、只读端点均已明确授权。若发现必须改变数据库 schema、认证/计费/配额核心、增加运行时依赖或删除文件，立即停止并请求 Owner；否则按现有模式完成。 |
| Pre-execution checklist | 1. 确认分支与脏工作树边界；2. 阅读 SQL、handler、路由、wiring、platformsettings、缓存注入和既有测试模式；3. 确认 sqlc 可用性与生成配置；4. 先完成 B2 真源→生成→handler→路由→OpenAPI→测试；5. 再完成 B4 尾列→生成→映射→设置→dispatcher/wiring→测试→文档；6. 核对所有消费者与 DeepSeek usage 解析；7. 执行格式化、构建、单测、集成测试（环境允许时）、staticcheck；8. 复核仅预期文件变化并生成中文报告。 |

## 具体执行顺序

1. 盘点 `observability.sql`、sqlc 生成类型与 5 个消费者，确认尾部追加策略和已有索引/测试夹具。
2. 增加 B2 独立查询并生成 sqlc；编写双账号、跨租户、稳定倒序的 PostgreSQL 集成测试。
3. 按既有 provider-account 管理端租户解析模式实现 handler，补齐路由、单元测试与 OpenAPI。
4. 为 `ListUsageRecords` 尾部增加两个缓存创建 token 列，生成后逐个核对消费者并在观测响应映射透出。
5. 增加模块化 bool 设置键及默认值/类型化 getter；把 getter 注入 `ChatHandlerDeps`，仅在 Anthropic Messages 自动断点路径启用 1h TTL，并在改写后校验排序。
6. 增加默认关闭字节等价、开启 1h、客户端已有控制字段直通、设置接线缺失变异、缓存字段透出的判别测试。
7. 补 OpenAPI 与中文运维文档，核对 DeepSeek 命中/未命中 token 的解析和计价路径。
8. 执行要求的门禁，记录命令、结果、未运行项和 `file:line` 证据到指定报告。

## 假设与风险记录

- 这份 Owner 指令已是经过产品侧收敛的执行切片；本计划不扩张需求，也不读取参考项目源码。
- 现有前端改动与本切片并行，任何前端文件都不触碰。
- `docs/` 改动仅限 Owner 明确要求的 OpenAPI、运维页以及项目硬规则要求的计划；不属于前端接线。
- B4 仅改变自动注入的缓存断点；客户端显式提供的控制对象保持原始请求语义，避免协议代理越权改写。

## 实施时收敛记录

- TTL 设置的唯一消费点位于 `UpstreamDispatcher`，因此实际把类型化 getter 直接注入 dispatcher，没有给 `ChatHandlerDeps` 增加重复字段。这样所有调用该 dispatcher 的路径共享同一运行时策略，也避免出现 handler 已接线但 dispatcher 未消费的死开关。接线判别测试直接守住 `wiring.go` 的注入行。
- 执行期间共享工作树出现另一条凭据项目后端切片；本任务不修改、不恢复、不评审那些并行文件，门禁结果会区分本切片与并行工作树状态。
