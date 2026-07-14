# 2026-07-14 绑定级 MaxParallelRequests 并发上限 · Claude 综合裁定

## 交叉讨论:采纳 codex 计划,并解决其一处关键正确性改进

Claude 侧设计来自两路调研 + 派工单。codex 独立计划(-codex.md)在两点上**改进了**我的派工单,采纳:

1. **两层闸(关键正确性)**:我派工单只说"COUNT WHERE binding_id AND status=acquired"当闸,
   codex 正确指出**纯外层 COUNT 抗不住真并发**(两 goroutine 都读 K-1 都插入→K+1 超发)。
   采纳其两层设计:
   - 外层 COUNT 快速拒绝:选号前查一次,饱和早退,减无谓账号槽竞争;**不声称是硬闸**。
   - **事务内权威硬闸(唯一线性化点)**:在 DBSlotManager.acquireOnce 的 SERIALIZABLE 事务内,
     按 binding 串行化后重读 active count,>=K 返专用 sentinel,未达才更新账号槽+插入带
     binding_id 的 acquisition。**acquisition 插入 + binding 硬闸 + 账号 in_flight 增量同一事务,
     一起提交或一起回滚**。
2. **前端 PATCH 整行覆盖静默清空**(selection.ts:64-80 不回填 max_parallel_requests):
   激活字段后,运营编辑其他 binding 字段会把并发配置清掉。采纳:本片必须修 PATCH 回填。

其余全采纳:派生计数不设独立计数器(release/lease-sweep 翻转 status 自动降);计费回滚只复用
abortReservation/settler,不新造;断连复用 WithoutCancel detached abort;binding gate 拒绝显式
429 契约不落 500。

## D1–D8 决策裁定

- **D1 schema(0183)**:批准。pool_slot_acquisitions 加可空 binding_id + 部分索引
  `(binding_id,status) WHERE status='acquired'`;**只加列+索引,不改不删现有列/约束/数据**,
  存量行 binding_id IS NULL。实施前复核迁移号未被并行占用。
- **D2 两层线性化**:批准 codex 方案(外层 COUNT 预检 + 事务内 acquireOnce 权威闸)。
- **D3 端点范围**:**权威闸放 acquireOnce=所有走槽获取的端点自动覆盖**(真全局上限,非 chat-only);
  binding_id + K 贯穿到 acquireOnce。若某端点当前不传 binding 上下文到 acquireOnce,显式列出
  (不静默半覆盖),该端点作为紧邻后续补齐,不谎称已全局。
- **D4 NULL/0 语义**:NULL 或 0 = 不限(unlimited),零默认零行为变化;仅 K>0 强制。
- **D5 前端**:加配置入口 **且** 修 PATCH 回填(两者都做;回填是硬要求,否则激活即造数据丢失 bug)。
- **D6 429 契约**:binding concurrency 专用 sentinel + 稳定 client code + 独立 abort reason
  (不复用 key_rate_limited、不落 500)。
- **D7 生效边界**:降配不杀在途(invariant 7),新获取拒到派生数自然降到新上限下;配置变更下次
  获取生效(registry 缓存刷新,对齐 P2-b sensitive_words 即时生效契约)。
- **D8 运行时开关**:不加额外 env 开关。字段本身即开关(未设=关),零默认已是安全默认,不增旋钮。

## 裁定:批准实现

D1–D8 已定,方案无待决冲突。批准 codex 按其实现顺序落地。**分小闭环**:
①迁移0183+事务内权威闸+透传 → ②计费回滚复用+断连+lease → ③前端入口+PATCH回填 → ④重并发测试。
Claude 每闭环亲检 + 真 PG 重并发 + 四组变异证红(去闸超发/拒绝不abort残留hold/COUNT不过滤status死锁/
不写binding_id闸失效)+ money 不变量(不漏扣/不重扣/不漏账),全过关才提交。
