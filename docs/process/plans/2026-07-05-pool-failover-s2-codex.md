# 2026-07-05 pool-failover S2 配合修 Codex 计划

| Owner directive | "任务:pool-failover 两处 S2 配合修(PF-01 流式即时冷却 + PF-02 client 4xx 不计账号健康)" |
| Scope | 仅处理 PF-01 流式上游 429/5xx 即时账号冷却、PF-02 client-caused 4xx 归 `SignalClientMalformed`，以及对应判别测试、变异证据、必要报告。禁止改 `internal/billing/`、`internal/quota/`、`cmd/gateway/account_slot*`、`internal/provider/bedrock/`、`internal/credentialacq/`，不做 git commit/push。 |
| Success criteria | 三镜源码亲读后给出 file:line 行为对照；流式 429/5xx 复用既有即时冷却回流；client 400/413/422 等请求问题不计账号健康，401/403/429/5xx 仍保持账号侧处理；新增判别测试覆盖两项缺口；按要求完成 cp 备份式变异证红；门禁命令全绿或诚实记录失败。 |
| Time estimate | 约 1.5-3 小时墙钟；主要耗时在三镜定位、测试夹具理解、门禁与变异跑数。 |
| Blast radius | `internal/gatewayhttp` 流式失败结算、`internal/gateway` 错误信号归类、`internal/channelhealth` 信号窗口统计；错误会导致账号误冷却或漏冷却。 |
| Failure modes | 上游引用污染本地实现：只记录行为对照，不复制标识符、结构或算法；测试夹具过宽导致假绿：写能被删除修复点打红的判别测试；`gatewayhttp` 超预算：优先复用既有 helper 与现有测试文件，不新增大块逻辑；RateService 测试难以观测：使用现有 fake/spy 或最小本地 stub。 |
| Decision points | 若必须触碰高风险目录、数据库 schema、计费/配额核心、认证核心或新增 runtime dependency，立即停下请求 Owner 确认；若发现非本两项真实生产缺陷，停下标注 finding，不顺手扩修。 |
| Pre-execution checklist | 1. 读取项目规则与相关技能。2. 确认工作区脏文件并避开非本任务变更。3. 使用 clean-room lane guard 心智模型亲读 sub2api/new-api/CLIProxyAPI 三镜账号冷却对流式/非流式处理。4. 读取 HUAKAI 相关 producer/consumer/streaming/buffered 代码与现有测试。5. 先写或定位判别测试夹具，再做最小实现。6. 跑目标测试。7. 做 cp 备份式变异证红并恢复。8. 跑全量门禁。9. 输出中文报告。 |
| Cross-discussion status | 本会话无法让 Claude 并行独立起草计划；本文件是 Codex 独立计划。因 Owner 本轮已直接下达实现任务，先记录该限制并继续执行低/中风险闭环修复。 |

## 具体执行顺序

1. 定位三镜仓库、确认 HEAD 与源码区域，只摘取行为事实与行号。
2. 核对 HUAKAI buffered 路径、streaming 路径、`SignalFromClassification` 与 `signal_classifier` 的当前行为。
3. 为 PF-02 增加分类与窗口/决策判别测试，再改两处 4xx 分类。
4. 为 PF-01 增加流式 429 `Retry-After` 即时冷却与 RateService 记录测试，再让流式非 2xx 复用既有冷却 helper。
5. 跑目标测试；若测试暴露额外生产缺陷，停下写 finding。
6. 分别用 cp 备份临时回退 PF-01/PF-02 修复点，确认新增测试变红，然后恢复并复跑。
7. 跑 `go build ./... && go vet ./...` 与指定 `go test` 门禁。
