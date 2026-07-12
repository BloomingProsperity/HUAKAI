# 2026-07-10 登录审计事件来源 IP 与 User-Agent

| 项目 | 内容 |
| --- | --- |
| Owner directive | “登录审计事件补来源 IP + User-Agent（取证盲区，零 schema）”；“代码注释全中文、报告全中文”；“禁止 commit” |
| Scope | 在 `AuthEvent`、`recordAuthEvent` 调用链、生产 `zapAuthEventSink` 与既有认证 handler 测试中增加来源 IP / User-Agent 观测字段。明确排除认证成败判定、限流、锁定、会话、密码重置状态机、数据库、schema、sqlc、依赖与提交操作。 |
| Success criteria | 所有 HTTP 认证审计调用均经可信代理感知的既有 `clientip.Resolver` 集中补齐来源信息；失败登录与成功登录测试精确断言 IP / UA，并保留既有事件字段；指定 build/vet/test 门全部通过；最终 diff 只涉及审计观测维度。 |
| Time estimate | 墙钟约 30–60 分钟；代理合计约 1.5–2.5 小时（并行只读盘点、实现、测试与复核）。 |
| Blast radius | `recordAuthEvent` 签名是包内编译边界，漏改调用点会直接编译失败；sink 字段装配错误会造成日志字段缺失；测试夹具若不判别会掩盖原取证盲区。 |
| Failure modes | 漏掉调用点：用 `rg` 全量清点并由编译门复证。错误解析代理头：只调用既有 `ClientIPResolver.ClientIP(r)`。覆盖调用方显式值：仅在字段为空时补齐。nil 请求或 resolver panic：先核实所有现有调用点均有非 nil `r`/resolver，再按最小必要防御实现。认证逻辑漂移：逐段 diff 审计控制流不变。 |
| Decision points | 无待定产品决策；Owner 已锁定集中式签名方案、可信解析器、日志字段名、测试判别标准与零 schema 边界。若盘点发现无请求上下文的真实调用点，才暂停并明确报告。 |
| Pre-execution checklist | 1. 确认工作树与并行编辑锁；2. 清点所有定义、调用点及生产 sink；3. 找到现有 fake sink 与成功/失败登录测试夹具；4. 先确认旧实现确实不会产生 IP / UA；5. 认领准确文件后再编辑；6. 只 gofmt 改动的 Go 文件；7. 依次跑聚焦测试及 Owner 指定门；8. 检查最终 diff、状态与无 commit。 |
| Concrete execution order | 只读盘点 → 确定测试落点与判别夹具 → 添加事件字段与集中补齐 → 更新全部调用点 → 扩展 zap 字段 → 添加失败/成功登录判别测试 → gofmt → 聚焦测试 → build/vet/指定测试门 → diff 与调用点复核。 |
| REFERENCE PROJECTS IN SCOPE | CLIProxyAPI、sub2api、new-api（仅登记默认三镜）。本次为 Codex implementer 车道，依 CR-R-001 不读取任何借鉴项目源码、不作其行为断言；实施依据仅为 Owner 明确规格与 HUAKAI 内部代码。 |

## 风险与约束

- 安全不变量：每条由 HTTP 认证 handler 发出的审计事件，都通过同一个可信代理感知解析器取得客户端 IP，并携带请求 User-Agent。
- 兼容不变量：调用方已显式提供的 `IP` / `UserAgent` 不被集中补齐覆盖；原有 `event_type`、`outcome`、`reason_class`、主体与时间字段保持不变。
- 测试变异：删除集中补齐 IP 的语句后，失败登录测试必须因期望 IP 与空字符串不相等而失败；删除 UA 补齐同理。
- clean-room：不读取、不引用、不复述非 MIT 借鉴项目实现；无许可证污染面。
- 结构预算：初始方案不新建 Go 文件；若机器预算门证明 `gatewayhttp` 余量不足，则把认证审计事件数据职责抽到新子包，绝不调大预算基线。

## 独立计划与交叉讨论状态

本文件是 Codex 独立计划。当前任务由 Owner 直接给出精确实现边界和验收门，尚无 Claude 独立计划文件可供合并；不伪造另一代理产物。执行前将以子代理的独立只读调用点盘点与测试落点盘点做交叉验证，发现与本计划冲突时停止并报告。

## 执行中证据校正

- 只读盘点确认当前 checkout 已从任务背景中的“约 12 处”增长为 33 个 `recordAuthEvent` 调用点：`auth_handler.go` 29 个、`session_handler.go` 4 个；全部都有 `r` 与对应 resolver，无需传 nil。
- 另确认 OAuth 补邮箱的 3 类事件经 `oauthpendinghttp.Deps.RecordEvent` 直达生产 sink，绕过集中 helper。为满足“每条审计事件/OAuth”目标，将该回调仅扩展为携带既有 resolver 提取的 IP 与请求 User-Agent；不改变任何认证分支、状态或响应。
- `internal/codebudget` 首跑显示 `gatewayhttp` 为 13843 行，超过基线 13177 的 5% 余量；修改前约 13833 行，只剩约 2 行可用。按硬规则新增 `internal/authaudit/event.go` 承担来源补齐规则，`gatewayhttp.AuthEvent` 仍在原文件显式定义两个新字段；未改预算基线。
