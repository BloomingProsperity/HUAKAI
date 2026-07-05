# 2026-07-05 auth 采集流两处修 Codex 计划

| Owner directive | "任务:auth 采集流 2 处修(ACF-1 OAuth 回调串行化 + ACF-2 finalizer 孤儿)" |
| Scope | 仅处理 `internal/credentialacq` 的 OAuth 回调 flow 状态转移防覆写、finalizer 在凭据已创建但 flow 标记失败时透出可对账元数据，以及对应判别测试、cp 备份式变异证红、指定门禁。禁止 git commit/push；不改 `internal/gateway/`、`internal/gatewayhttp/`、`internal/channelhealth/`、`internal/provider/`、`internal/billing/`、`cmd/gateway/account_slot*`。 |
| Success criteria | 亲读 sub2api/new-api/CLIProxyAPI 三镜 OAuth 采集/回调并发处理后给出 file:line 行为对照；ACF-1 每步状态写带合法前置状态守卫，不能把已 `validated`/`finalized`/`failed` flow 覆写回中间态；ACF-2 `Create` 成功但 `MarkFinalized` 失败时返回值仍携带已建 credential id/元数据，handler/运维可对账；新增判别测试能在去掉修复点后变红；指定 build/vet/unit/integration_pg 门禁全绿或诚实记录失败原因。 |
| Time estimate | 约 2-4 小时墙钟；主要耗时在三镜定位、HUAKAI session store 状态机阅读、并发 integration_pg 夹具、变异证红与全量门禁。 |
| Blast radius | 上游账号凭据采集状态机、OAuth 回调重入/并发处理、凭据创建后的 finalization 观测性；错误可能造成有效凭据丢失、活凭据孤儿、重复操作误判。 |
| Failure modes | 上游源码污染本地实现：只记录行为事实与行号，不复制上游标识符、结构、注释或算法顺序；状态守卫太窄导致正常回调失败：先读现有 flow 状态路径并覆盖合法状态；测试假绿：每个测试写明变异点并用 cp 备份临时回退验证；MarkFinalized 失败处理过度设计：不新增 worker/schema/dependency，只做最小可对账透出；若发现 finalized 也可被覆写并影响计费/鉴权，立即停止扩修并报告。 |
| Decision points | 若需要改数据库 schema、auth-core、billing/quota、禁区目录、新 runtime dependency、删除文件、生产部署脚本或真实 secrets，停止请求 Owner 确认；若三镜行为对照需要写入长期 evidence ledger，另起计划，不混入本修复。 |
| Pre-execution checklist | 1. 读取项目规则与 `reference-project-miner` 技能。2. 确认工作区状态并避开非本任务变更。3. 用 clean-room lane guard 心智模型亲读三镜 OAuth 采集/回调并发处理，只摘行为事实。4. 读取 HUAKAI `oauth.go`、`session_store.go`、`finalizer.go` 与现有测试。5. 先补 ACF-1/ACF-2 判别测试并确认当前缺陷可复现。6. 做最小实现。7. 跑目标测试。8. 用 cp 备份式变异分别去掉状态守卫/返回元数据，确认测试红，再恢复。9. 跑指定门禁并输出中文报告。 |
| Cross-discussion status | 本会话无法让 Claude 并行独立起草计划；本文件是 Codex 独立计划。Owner 本轮已直接给出实现任务与范围，记录该限制后继续执行低/中风险闭环修复。 |

## 具体执行顺序

1. 定位或更新 `~/refs` 下三镜仓库，确认默认分支 HEAD，读取 OAuth 采集/回调/状态推进相关源码区域。
2. 阅读 HUAKAI credential acquisition 的 session store、OAuth callback、finalizer、handler 输出契约和现有 PG 测试夹具。
3. 为 ACF-1 增加 integration_pg 并发/终态覆写判别测试；必要时增加 unit 级状态守卫测试。
4. 为 ACF-2 增加 finalizer unit 测试，模拟 `Create` 成功而 `MarkFinalized` 失败，断言返回 credential 可对账。
5. 实现 flow 状态更新守卫与 finalizer 返回语义的最小改动，代码注释只描述 HUAKAI 自身机制。
6. 跑 `go test ./internal/credentialacq -count=1` 与 integration_pg 目标测试。
7. 做两处 cp 备份式变异证红，恢复后复跑相关测试。
8. 跑完整门禁：`go build ./... && go vet ./...`、`go test ./internal/credentialacq ./internal/codebudget -count=1`、integration_pg。
