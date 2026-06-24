# 2026-06-23 backend-quality-architecture-review-codex

| Owner directive | `/goal Read the Codex goal objective file at /home/ubuntu/.codex/attachments/d57bb3d8-3863-4495-8d9b-df5562c0eb27/goal-objective.md before continuing.` |
| --- | --- |
| Scope | 只做 HUAKAI 后端代码质量、可维护性、架构与包纪律、死代码与重复、复杂度、测试质量、重构债务审查。读取真实 Go/Rust 代码与测试。审查正文直接返回对话，不写 findings `.md` 报告。 |
| Out of scope | 不做前端审查；不做安全专项展开；不审 `backend/internal/hermes*`；不审 Rust 草稿 lane 的具体实现；不修改生产代码、测试、schema、部署脚本、LICENSE、真实 secrets。 |
| Success criteria | 每条 finding 都有绝对路径、行号、函数或类型名；按 S0/S1/S2/S3 分区；给出可执行修法；优先覆盖 objective 点名的高债务热区；结尾给重构优先级排序表；全中文输出。 |
| Time estimate | 约 1.5-3 小时 agent 时间，取决于可核实证据数量与仓库规模。 |
| Blast radius | 计划文件是唯一写入；后续审查以只读命令为主。若误判代码状态，会影响 Owner 对重构优先级的判断。 |
| Failure modes | 1. 文档陈旧导致误判：以 `.go`/`.rs` 真码和测试为准。2. 行号漂移：实际打开文件核实。3. 范围过大导致泛化：只产出证据足够的高信号 finding。4. clean-room 风险：不读取或复述上游非 MIT 源码，只审 HUAKAI 本仓代码。 |
| Decision points | 若发现需要删除文件、改 LICENSE、改 schema、改 auth/billing/quota 核心或部署脚本，只在报告里标注，不执行。若需要 Claude 独立计划或 synthesized plan，Owner 另行触发；本次 Codex 只提交独立计划并执行只读审查。 |
| Pre-execution checklist | 1. 不读取同名 `*-claude.md` 计划，保持 Codex 独立判断。2. 查看 git 状态和分支。3. 必要时确认是否与 `origin/feat/frontend-portal` 存在明显漂移，但不主动 merge。4. 用 `rg`/`wc`/`sed`/`nl` 读取 objective 点名文件。5. 核实每条 finding 的行号与代码上下文。 |

## 具体执行顺序

1. 建立仓库基线：`git status --short --branch`、关键目录文件数与行数、codebudget/deadcode/CI 配置位置。
2. 核查架构与包纪律：`gatewayhttp`、`payment`、`billing`、`credentialstore`、`credentialworker`、`pool`、`cmd/gateway`、`gateway`、`proto`、`auth`。
3. 核查重复与死代码线索：扫描 record scan、quota 三指标分支、请求体读取、HMAC envelope、租户解析、buffer 上限常量、deadcode baseline。
4. 核查复杂度与生命周期：forwarder、eventbus、fail-open 策略、后台 worker、预算/账本双轨、settlement recovery。
5. 核查 money/cache 与测试态势：integration_pg CI、DB env 名、典型钱路测试判别力、no-op skip 测试。
6. 核查传输伪装层：Go uTLS ALPN/H2 决策一致性、sidecar dead-code 候选、Go/Rust 边界资源处理。
7. 汇总 findings：只保留证据足够、修法明确、对 Owner 有重构决策价值的条目，并给 Top N 优先级表。
