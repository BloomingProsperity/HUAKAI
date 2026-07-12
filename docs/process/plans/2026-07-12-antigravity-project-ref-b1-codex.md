# 2026-07-12 Antigravity project_ref B1（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “后端切片 B1 完整版——Antigravity project_id 接线 + 凭证级持久暴露（做全，不留缺口）” |
| Scope | 范围内：采集 finalize 的限时尽力补齐、刷新适配器缺口、租户隔离的手动解析动作、`account_credentials.project_ref` 迁移与三条写路径、凭证元数据读取、OpenAPI、判别测试、构建与静态检查。范围外：前端接线、热路径网络解析、其他供应商行为、提交与推送。 |
| Success criteria | 三个入口均可解析并持久化项目标识；解析失败按既有 `operator_attention` 语义降级；响应不泄漏令牌；租户越权失败；凭证载荷覆盖账号级 extra；迁移可逆；指定门禁通过。 |
| Time estimate | 墙钟约 2.5–4 小时；单 agent 约 3–5 工时，取决于现有测试桩和数据库集成测试复用程度。 |
| Blast radius | 凭证采集完成、刷新 worker、管理员凭证动作、凭证加密存储 SQL、管理员 OpenAPI。错误可能导致凭证创建受阻、租户越权、项目标识未持久化或令牌暴露。 |
| Failure modes | import 环：先画清依赖方向，将 enrich 放在不反向依赖 gateway 的小包；网络悬挂：finalize 与手动动作外包总超时；秘密残留：所有解密材料路径 `defer Zeroize`；SQL 漏列：同步 Create/Rotate/SaveRefreshSuccess/ListByAccount 及测试桩；DI 漏接：增加从构造到路由的判别测试；旧数据：保持 NULL，由刷新或手动动作渐进补齐。 |
| Decision points | Schema 迁移已由 Owner 明确授权；OpenAPI 虽在 `backend/` 外，但为 Owner 明确要求的唯一接口契约例外；若 `0176` 已占用则顺延；不新增 runtime dependency，不触碰认证核心、计费、配额、前端、`LICENSE`。 |
| Pre-execution checklist | 1. 确认分支与工作树并避开他人改动；2. 确认迁移号；3. 核验 credentialacq/provider/gateway 依赖方向；4. 盘点 resolver、finalize、refresh DI、credentialstore 事务与审计接口；5. 盘点路由与 OpenAPI 一致性规则；6. 先补判别测试或与实现同步小步落地；7. 格式化并跑定向测试；8. 跑完整指定门禁与 staticcheck；9. 检查 diff 仅含本切片文件并写报告。 |

## 具体执行顺序

1. 建立项目解析接口和限时调用边界，复用现有 Antigravity resolver，不把网络请求放入 vault 热路径。
2. 给采集 finalize 注入 enrich 服务；只在 Antigravity 且载荷缺项目标识时调用，失败沿用待解析/人工关注语义，并核对 correlation 日志与审计。
3. 给刷新适配器加入可选 resolver 并从 refresh mode 注入；成功合并项目标识，失败才转人工关注。
4. 增加 `project_ref` 迁移；在 Create、Rotate、SaveRefreshSuccess 同一事务内从载荷提取并回填，在 ListByAccount 暴露 nullable 元数据。
5. 实现 `resolve-project` 管理动作：SessionSafe 写门、租户作用域读取、限时解析、同款刷新成功事务写回、零化解密材料、审计动作固定为 `resolve_credential_project`，响应仅返回项目标识。
6. 更新 OpenAPI，并补刷新变异判别、端点持久化/越权/无令牌、merge 优先级、DI 接线与 SQL 路径测试。
7. 运行 `gofmt`、定向测试、`go build ./...`、指定测试集合和 `staticcheck`；若环境缺工具则如实记录。

## 验收证据设计

- 正常路径：resolver 返回项目标识后，刷新 payload 与 `ResolveActive` 物化结果均出现该值。
- 失败路径：resolver 失败时不覆盖已有值；缺值才进入 `operator_attention`，采集创建不被阻塞。
- 操作恢复：管理员手动动作可把 NULL/缺失状态修复为可物化项目标识，且响应全文不含 access token。
- 安全路径：篡改 tenant 无法读取或写入目标凭证；解密材料退出路径全部零化；审计包含固定动作与 correlation 键。
- 回归路径：凭证 payload 与账号级 extra 冲突时，凭证值胜出；删除 resolver 调用、反转 merge 次序或删除 wiring 注入均应令测试失败。

## 执行授权说明

Owner 本轮消息已明确要求落地全部条目，并单独授权 schema 迁移；该逐项任务书作为本轮已批准的执行约束。本文件为 Codex 独立计划，未读取同主题 Claude 计划内容。
