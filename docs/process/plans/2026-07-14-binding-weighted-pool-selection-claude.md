# 2026-07-14 绑定级 Weight 选号加权 · Claude 综合裁定

## 交叉讨论结论:采纳 codex 独立计划,零冲突

Claude 侧设计来自两路调研(HUAKAI 内部链路 af46 + 三镜 a9678),与 codex 独立计划
(2026-07-14-binding-weighted-pool-selection-codex.md)逐点一致:

**Agreements(全部一致)**
- Priority/Weight 从 BindingMetadata 透传到 PoolCandidateMeta,再到 default_router.Plan。
- 加权无放回洗序**仅在连续同优先级段内**(codex 的"分段 swap 只在同段内"是硬证明,
  优于泛泛"同层加权")。对齐 new-api 分层加权随机。
- Weight<=0 归一为 1(补底额防饿死),对齐 new-api;总权重用 int64。
- 复用 HUAKAI 账号级 weightedReservoirIndex 同款蓄水池抽样,不抄外部源码。
- 判别测试:字段消费链、1:9 分布(88-92% 容差)、硬优先级隔离、零权重补底额,
  各带变异证红点。

**codex 多出的严谨点(采纳)**
- 生产共享 *rand.Rand 加 mutex(math/rand.Rand 非并发安全)——必须。
- 缺元数据候选原位保留为单例,不参与洗序、也不因单条缺失关掉其余同层加权。

**决策点裁定**
1. SnapshotVersion 升版:采纳。核心选号策略变化,审计需可区分新旧策略,升版。
2. 部分元数据处理:采纳 codex 的"缺失候选原位单例"方案。

## 裁定:批准直接实现

交叉讨论完成,无待决冲突。按 codex 计划执行顺序实现,Claude 亲检(分布/优先级/
零权重三组变异证红)+ 全门禁后提交。
