# 2026-07-11 B 类持久结算意图阶段 1 第 2 轮修复（Codex 独立计划）

> 合成状态：Codex 独立稿完成后，已与既有 Claude 阶段设计计划交叉比对；本轮以 Owner 当前对抗审清单为权威合成结论。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “本轮在现有改动基础上修下列 2 个 S1 + 3 个 S2，不重做已修好的部分。money-path 旁路，精度最高。” |
| Scope | 范围内：`internal/settlementintent` 的 Store 边界 panic 隔离、异步等待硬上限、禁用态零热路径；`internal/gatewayhttp` 流式与非流式调用顺序、配额拒绝生命周期、判别测试；本切片测试注释洁净。范围外：Reserve/hold 准入、余额扣减、结算金额、actual cost 权威来源、billing 核心、数据库 schema、依赖、部署与提交操作。 |
| Success criteria | 六种 Store 操作的真阻塞/忽略 ctx 与 panic 场景均不阻断交付、不跳过或无界延迟主结算/Abort；流式和非流式均有判别证据；quota deny 无未插入终态误告警且无孤儿 intent；禁用态不创建异步协调对象；注释扫描覆盖本切片相关测试；用户指定门禁全绿。 |
| Time estimate | 代码与测试约 90—150 分钟；全量、race、vet 与 codebudget 门禁约 20—50 分钟，受机器负载影响。 |
| Blast radius | `settlementintent.Tracker` 是所有结算意图状态写入边界；调用顺序错误可能污染状态序列，等待错误可能拖慢 HTTP 请求；网关调用调整若越界可能影响钱路，因此只移动旁路等待，不改主结算输入或实现。 |
| Failure modes | Store 忽略 ctx 导致 goroutine 长驻：测试使用可释放阻塞夹具并在清理阶段放行；panic 原文泄漏：warning 仅记录固定 operation、租户/claim/request 身份和恢复值类型；主结算仍等待旁路：用带时间线/调用次数的 HTTP 级测试证明；异步状态与终态竞争：终态前只做有硬上限的有界等待，并将该等待放在主钱路完成之后；耗时断言抖动：采用明显分离的阻塞时长与上限区间，race 下保留裕量。 |
| Decision points | Owner 已明确采用“未 InsertPending 的终态静默跳过”或“提前 InsertPending”二选一。本计划优先选择显式 `inserted` 生命周期位，避免在 quota 准入前制造孤儿 intent；不触及高风险钱路、schema 或依赖。若现有接口无法在不改钱路的情况下形成判别证据，再停下请求 Owner 决策。 |
| Pre-execution checklist | 1. 保存并核对当前脏工作树，不覆盖既有改动；2. 读取 Tracker、流式、非流式、Abort、quota 与测试夹具；3. 核对调用顺序和所有 Store 边界；4. 先补判别测试并确认其确实覆盖目标失败模式；5. 以最小实现修复；6. 每个失败门禁最多三轮；7. 不执行 `git add/commit/push`。 |

## 具体执行顺序

1. 在 Tracker 建立显式“成功插入”状态，并为 Insert 与全部 Mark 更新函数建立统一 panic 恢复边界；恢复日志不包含 panic 值。
2. 让禁用态 `AfterDeliveryAsync` 直接返回共享 no-op 闭包；启用态等待使用独立硬上限，且阻塞 Store 的 goroutine 可在测试结束时释放。
3. 审核流式与非流式交付路径，把 intent 等待安排到主结算/Abort 完成之后；主钱路的输入、金额和调用语义保持不变。
4. 增加六类操作的 panic 与真阻塞判别矩阵，覆盖 `stream=true` 和非流式；明确断言 HTTP、主结算/Abort 次数、warning 脱敏与整请求耗时。
5. 增加 quota deny 生命周期与禁用态流式零状态迁移测试；静态/白盒断言仅用于补充，不能替代真实 HTTP 行为证据。
6. 清理 `adminquotahttp` 测试注释，并扩大注释扫描范围。
7. 先跑定向判别测试，随后依次跑 gofmt、build、vet、指定 race、全量测试和 codebudget；每项最多三轮，保留实际输出摘要。

## 生产场景与验收不变量

- 主钱路优先：任何 intent error、timeout 或 panic 都不能改变响应结果、结算金额、主结算/Abort 次数，也不能让请求无限等待。
- 可观测但脱敏：每次真实旁路故障有固定操作名和请求身份字段；panic 内容、密钥和底层错误文本不进入 warning。
- 生命周期诚实：没有成功 Insert 就没有可推进的 intent，终态调用静默跳过；成功 Insert 后仍按交付证据推进。
- 关闭即无热路径：默认禁用不产生 Store 调用、warning、goroutine 或每请求通道。

## 假设与风险记录

- 假设用户给出的第 2 轮清单是本次权威验收范围；不重新设计阶段 1 schema 或已修项目。
- Go 无法安全终止一个完全忽略 ctx 的外部 Store 调用；硬上限保护 HTTP 主链路，但失控实现仍可能留下后台 goroutine。测试夹具必须可释放，生产剩余风险在报告中如实说明。
- 计划使用不超过 `terminalOperationTimeout` 的独立等待上限；若选择与前置写超时相同的短量级，可降低钱路延迟，但可能允许 delivering 与终态持久化乱序。实现与测试需同时验证主钱路优先及状态序列风险。

## 独立计划交叉差异与合成结论

### 一致点

- 两份计划都要求意图写 fail-open，不改变 Reserve、主结算、Abort、金额或权威账本结果。
- 两份计划都把 pending 建立在 Reserve 成功之后，并要求意图身份绑定账本返回的 claim/attempt。
- 两份计划都允许通过 feature flag 关闭旁路，关闭时不应影响既有交付与结算。

### 分歧

- 既有 Claude 阶段计划强调首字节前必须已有 pending，但没有处理 quota deny 发生在 Insert 之前的生命周期。当前 Owner 给出“提前 Insert”或“未插入静默跳过”二选一；合成选择显式未插入静默跳过，避免 quota deny 产生无交付价值的孤儿意图，也不改变 quota 准入顺序。
- 既有阶段计划把 intent 更新视为普通 fail-open 数据库写，没有覆盖 Store panic、实现忽略 ctx、异步等待无硬上限及默认关闭的通道/goroutine 开销。当前计划补齐这些生产失败模型。
- 为同时满足状态顺序与钱路优先，流式路径不在主结算前等待 delivering；主结算先完成，旁路等待只允许在独立硬上限内发生，之后再尝试终态。该顺序以钱路优先为最高不变量。

### 单方补充的缺口

- Codex 独立稿补充了六操作 panic/真阻塞判别矩阵、warning 脱敏、race 下耗时裕量和可释放阻塞夹具。
- Claude 阶段稿强调持久 intent 必须在首帧前可观察；本轮不改变 InsertPending 的正常成功位置，只修 quota deny 的未插入误告警，因此不削弱该正常路径目标。

### 权威执行结论

- Owner 当前清单已经明确范围、二选一权限、门禁和禁止项，故以上分歧无需新增高风险授权；按本文件具体执行顺序实施。
