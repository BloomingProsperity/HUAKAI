# 2026-07-12 F5 计费重算与信任验证中心前端计划（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner 指令 | “切片 F5——计费重算入口(F10)+ 信任与证明验证中心(F11)……纯前端,后端零改动。做全,不留缺口。” |
| 范围 | 新增用户端信任验证 feature、导航与路由；在既有计费台账页聚合重算入口；复用并验证既有回执详情验签；补真实 API 契约、闸门和结果映射测试。明确不改后端、不碰 `features/mediatasks`、不提交或推送。 |
| 成功标准 | 前端严格使用 handler 的真实路径、方法、请求体与响应；实际重算必须经过原因必填、知情勾选和范围二次确认；信任页展示当前公钥、轮换历史、审计链验证、粘贴证明验证与 Merkle 根；三道门禁全绿；指定报告完整。 |
| 时间估算 | 约 90–150 分钟墙钟时间，单 agent 完成。 |
| 影响范围 | 用户壳导航与路由、运营台计费台账页、共享组件样式；错误可能导致危险按钮失守、误报证明可信或请求契约不匹配。 |
| 失败模式 | 误把两种 verify 混为一条、把未持久化原因伪装成审计、金额差额浮点失真、含斜杠回执 ID 路由错误、并行覆盖共享文件。分别以 handler 证据、诚实提示、定点字符串求和、逐段编码、协作锁和判别性测试化解。 |
| 决策点 | Owner 已指定页面归属、后端零改动与危险闸门，无待确认产品岔路。后端重算没有审计原因字段和客户端幂等键是已观察缺口：前端不得虚构字段；原因仍作为 UI 必填确认信息，并明确不会入后端审计。服务按记录的唯一重算事件保持业务幂等。 |

## 前置真实契约

- `POST /admin/v1/billing/reprice` 只接收两种互斥范围：单条 `usage_record_id`，或 `tenant_id + from + to`，可带 `limit` 与 `dry_run`。`dry_run` 缺省为 `true`，实际写入必须显式为 `false`。响应为 `object/dry_run/items/summary`，差额只存在逐条 `cost_delta` 中。
- `/v1/audit/verify` 的 GET/POST 都只按 `request_id + tenant_scope_ref` 查 HUAKAI 审计账本并返回 `ledger_entry + chain_proof`；它不接收任意 proof JSON。
- `/v1/trust/verify` 接收 `{payload, signature, pubkey_fingerprint}`，用于离线式信任回执证明验签。
- `/v1/receipts/{request_id}/verify` 空 body 表示验证已存储回执；单斜杠 ID 使用 host/tail 变体，前端按段编码。

## 三镜形态清单

参考项目范围（REFERENCE PROJECTS IN SCOPE）：CLIProxyAPI + sub2api + new-api。仅做功能形态核对，不把其标识符、结构或实现带入 HUAKAI 代码；HUAKAI handler 是本次 HTTP 契约唯一真相源。

| 项目 | 观察到的相邻形态 | 对本切片的结论 |
| --- | --- | --- |
| sub2api@`12d811bd7657` | 管理路由覆盖用量、余额、运营与订阅；用户路由覆盖用量查询，但路由清单未暴露“按现价重算”或面向用户的 Merkle/公钥轮换/证明验签中心（`backend/internal/server/routes/admin.go:12-109,245-268`；`backend/internal/server/routes/user.go:81-95`）。 | 无等价完整路径；HUAKAI 保留自身 F10/F11 形态，不缩水。 |
| CLIProxyAPI@`26d45fd46a2d` | 暴露面集中于模型转发、凭证、配额与日志管理，完整管理路由区未见账单重算或用户证明验证族（`internal/api/server.go:507-565,685-829`）。 | 无等价完整路径；不套用纯 relay 控制面。 |
| new-api@`246d62aa5ed3` | 有账户安全的通用验证入口、订阅计费和用量日志；未见当前价表重算，也未见公钥历史 + Merkle 锚点 + 审计/回执证明校验的组合（`router/api-router.go:64-65,150-179,246-277`）。 | 相邻能力不等价；HUAKAI 的信任中心按真实内部端点显性聚合。 |

## 具体执行顺序

1. 固化 F10/F11 TypeScript DTO 与 API，测试锁定真实路径、方法、query/body 和空 body 回执验签。
2. 在 `billingadmin` 增加范围校验、危险动作状态机/发送闸门、八位小数定点求和，并先写判别性测试。
3. 在既有计费台账页加入有分量的重算卡片：预演与实际执行分开；实际执行须原因、知情勾选、二次确认；结果按响应 summary/items 展示。
4. 新建 `features/trust/`，聚合平台公钥、轮换历史、审计账本验证、粘贴信任证明验证与 Merkle 锚点；所有结论通过纯函数映射并测试。
5. 将 `/trust` 挂用户壳导航与路由；现有用量回执验签入口不重复实现，仅补契约测试并确认内联结果。
6. 运行 `npx tsc --noEmit`、`npx vitest run`、`npm run build`，修复至全绿。
7. 复核 diff 未触碰后端与 `features/mediatasks`，写指定中文报告并释放协作锁。

## 执行前检查

1. 已完整阅读项目规则与协作协议。
2. 已核对所有目标 handler，而非根据 OpenAPI 或命名猜测。
3. 已确认当前后端和媒体 feature 的并行未提交改动并避让。
4. 已申领预计修改文件；没有活锁冲突。
5. 已记录重算原因/客户端幂等字段不存在的诚实降级边界。
6. 新文件全部落在现有前端 feature 模式内，不引入依赖。
