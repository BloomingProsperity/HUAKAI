# 2026-07-16 能力激活快照 Phase 0（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “开始吧 修复”；“只要对我们有益处的，能将基础打扎实的都可以做”；“这些要求对全局生效” |
| Scope | 只在独立工作树 `HUAKAI-wt-activation-codex` 实现只读能力激活快照；扩展模块运维合同，使其能区分能力已声明、已构造、已注入、当前激活、多副本安全、可观测和已验证；首批覆盖路由选择器、等待、结算恢复、渠道健康、响应缓存和模型注册表。同时把 Owner 的收益优先执行要求同步到全局 `AGENTS.md` 和 `docs/RULES.md`。不得修改请求热路径行为、数据库 schema、鉴权核心、billing ledger、quota enforcement、生产密钥或前端页面。 |
| Success criteria | `/admin/v1/modules` 和 `/v1/admin/modules` 返回结构化 activation 数据；选择器快照包含真实启动模式和 canary/shadow 比例但不暴露采样盐；端点覆盖按真实 handler 依赖生成；本地后端必须明确标记为非 `shared_safe`；缺失依赖不得显示为 active；有判别力的单元和接线测试覆盖正常、缺失依赖、模式差异和隐私边界；现有 API 保持向后兼容。 |
| Time estimate | 约 1.5-3 小时，包括独立 implementer、测试、代码预算检查和 Codex review。 |
| Blast radius | 仅影响管理员只读模块诊断响应和相关内部类型。若字段判断错误，会误导运维，但不会改变实际路由、计费或鉴权。 |
| Failure modes | 把“非 nil”继续误报成 active：activation 必须逐阶段计算；把单机状态误报为全局：显式记录 backend 与 shared-safe；泄露配置秘密：不得输出采样盐、凭据、租户数据或自由文本配置；接口破坏：只做 JSON 加性字段；测试弱：fixture 必须让能力已接和未接产生确定不同结果。 |
| Decision points | 本 Phase 不需要高风险 Owner 决策。若实现发现必须新增 runtime dependency、修改 schema、改变路由/计费/配额/鉴权行为，立即停止并请求 Owner 确认。 |
| Parallel-plan status | Owner 已明确要求 Codex 独立执行且不要触碰另一个目标。本轮不读取、不修改 Claude 工作树或计划；此处记录为并行计划规则的 Owner 指定单 agent 例外。 |

## Pre-execution checklist

1. 使用新建分支 `fix/capability-activation-20260716-codex`，确认工作树干净。
2. implementer 只读取 HUAKAI 内部源码、此计划和项目规则；禁止读取任何参考项目源码或上一轮参考对照报告。
3. 复用现有 `moduleregistry`、`modulehttp` 和 handler deps 构造器，不新增运行时依赖。
4. activation 数据只允许稳定枚举、布尔值、比例、端点名和后端类别。
5. 新增测试先证明当前非 nil 探针无法表达的差异，再实现结构化字段。
6. 运行目标包测试、`go test ./internal/codebudget/` 和必要的 gateway 测试。
7. 暂存预期改动，执行只读 Codex review；S0/S1 必须修复。

## Concrete execution order

1. 在 `internal/moduleregistry` 定义稳定、可序列化的能力激活合同。
2. 在 `modulehttp.ModuleView` 加性投影 activation 数据。
3. 在 gateway composition root 保留选择器启动配置的无秘密快照。
4. 从真实 `deps` 和各 handler deps 构造器生成首批能力状态及端点覆盖。
5. 将结构化状态挂到现有模块 descriptor，不另建重复管理入口。
6. 增加模型层、HTTP 投影层和 gateway 接线测试。
7. 格式化、测试、代码预算、自检和 review。

## 明确不做

- 不把当前未接线能力改成“已接线”。
- 不新增 Redis、数据库表或迁移。
- 不修改计费倍率、资金事务、配额、鉴权和请求重试行为。
- 不实现前端。
- 不提交或合并另一个工作树的改动。

## 执行记录

- 已修复：诊断请求不再重新读取 selector 环境变量，改为投影进程启动时实际采用的配置快照。
- 已修复：activation 计算不再调用要求完整 `deps.cfg` 的 handler 构造器，最小依赖和降级状态不会空指针崩溃。
- 已修复：首轮 review 发现未接线模块仍会被标成 `verified=true`；现由实时 probe 结果决定，`ok=true`、明确失败为 `false`、未知则省略。
- 已固化：Owner 的收益优先、整链路检查和高风险决策包要求写入全局 `AGENTS.md` 与 `docs/RULES.md`。
- 验证通过：`go test ./cmd/gateway ./internal/modulehttp ./internal/moduleregistry ./internal/codebudget -count=1`。
- Review：Round 1 的 `verified` 真实性问题按 S1 修复；Round 2 未发现剩余 S0/S1。
- 工具差异：当前 Codex CLI `0.144.1` 不接受规则文档中的 `--sandbox` / `--full-auto` review 参数，本轮按 CLI help 使用 `codex exec review --uncommitted --ephemeral` 完成两轮审查。

## Renew 复核记录

Owner 选择“1”后，对暂存实现重新从真实构造链、handler 依赖和公开合同做了一轮独立复核。该轮不读取参考项目源码，也不采信旧状态文档。

- OpenAPI 接线缺口：三个模块知识端点原先仍是宽泛 `object`，无法约束 activation 字段。现统一引用 `ModulesResponse`，补齐模块、探针、catalog、activation 和端点状态 schema，并增加真路径合同测试。
- 等待模块误判：chat handler 在 composition root 未注入等待器时会自行创建默认执行器，因此“未注入”等于“未激活”的旧判断是假阴性。现明确区分 `composition-root` 与 `handler-default`，并分别报告 injected 和 active。
- 结算恢复半接线：仅有 DLQ service 不代表恢复 handler 已注册。`dlq.Service.Register` 现返回安装结果，composition root 保存该结果；恢复模块只有在 DLQ 与 handler 两段都完成时才 active/verified。
- 渠道健康副本安全误判：渠道健康服务同时使用 PostgreSQL 状态和进程内鉴权冷却状态。启用本地冷却存储时，后端标记为 `mixed` 且 `shared_safe=false`。
- 响应缓存作用域不清：真实调用只覆盖非流式 chat，现通过 `mode=non-stream` 明示，避免运维把它理解为全 chat 流量缓存。
- 最终 review 发现渠道健康端点覆盖少报：该服务进入 selector 的健康 gate，而六类公开能力都使用 selector；chat 还存在直接注入。现按真实链路区分“chat 直接接线”和“其余端点经 selector 接线”，并补齐六类端点断言。
- 兼容性：未新增 runtime dependency，`go.mod` 与 `go.sum` 未变化；所有字段均为加性只读诊断字段。
- 验证通过：`go test ./cmd/gateway ./internal/dlq ./internal/moduleregistry ./internal/modulehttp ./internal/codebudget -count=1`。
- 全仓后端验证通过：`TMPDIR=/home/ubuntu/.cache/huakai-activation-tmp GOCACHE=/home/ubuntu/.cache/go-build go test ./... -count=1`。
- 静态检查通过：`git diff --check`。
- Renew review：Round 1 的渠道健康端点少报按 S1 诊断真实性问题修复；行为修正后的 Round 2 未发现剩余 S0/S1，也未发现明确的兼容性缺陷。
