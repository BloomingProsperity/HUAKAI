# 绑定级 fallback_class 激活 · 综合裁定(双计划交叉讨论结论)

日期:2026-07-14。输入:`-claude.md` 与 `-codex.md` 两份独立稿。

## 一致点(直接采纳)

两稿独立收敛到同一核心语义:**typed fallback(按失败类型定向降级)**,非两级梯队。normal=唯一主类;class 分桶在 Router 编译、executor 看到规范化失败后才决定越级;Priority/Weight 只在同 class 内生效;零默认翻转(normal-only 时 RoutePlan/HTTP/审计逐字节不变,golden 锁定);money/auth/本地安全/已交付字节永不跨类;前端 PATCH 必回填防静默清值;每模型每请求最多一次 class 转移、目标类子预算=1。

## 分歧裁定(D1-D6)

- **D1 binding 并发 429 是否可降级**:采纳 codex——配置了 quota 目标类时,binding 并发/RPM 饱和触发 quota 降级(运营配容量车道的本意);无 quota 目标时保持今天的终态专用 429。Claude 稿的「一律不降级」否决:上限依然生效(该绑定不再接活),降级正是运维配 quota 类的目的。key/user/tenant 限额永不降级(两稿一致)。
- **D2 manual 语义**:采纳 codex 推荐——运维手工配置的「通用瞬态故障兜底」,由上游 5xx/连接超时/空响应自动触发;不新增客户端/管理 header 人工点选。
- **D3 safety 是否加 env flag**:否决 codex 的 flag-off 推荐。按 Owner 成文原则(敏感模块给能力非控制、默认全开、配置即控制):不加 env 开关,配置了 safety 绑定即生效;安全底线由「本地 moderation/租户策略永远终态、仅上游内容策略拒绝可触发」保障(codex 自己的缓解设计,保留)。
- **D4 发布原子性**:采纳 codex——全协议闭合为完成定义,按其 §11 顺序分小闭环落地;audio/images 需先补 bounded multi-attempt loop;若某协议确不可安全重放,显式 fail-closed+Mandatory Roadmap,不虚报支持。
- **D5 写口现状**:以 codex 取证为准——modelbindingadminhttp 已收/校验/默认 normal(routes.go:65-68 等),L2 只需翻注释+补 PATCH 契约说明;Claude 稿「补枚举校验」作废。
- **D6 无主类配置**:采纳 codex——运行时 fail-closed `no_primary_binding` + UI 红色诊断,不做写时事务校验、不隐式晋升 fallback。

## 实施顺序(codex §11 原样批准,四个 dispatch)

1. 证据登记+错误映射表+normal-only golden+failing tests(AT-BFC-001~014)+Router phase 编译(executor 未消费,零行为)。
2. Selector typed exhaustion(保持 errors.Is 兼容)+ chat executor class transition(queue/binding429/model-auth-billing 组合)。
3. 协议对齐:completions/embeddings/rerank/Gemini → audio/images 补安全重放 loop。
4. 控制面:注释翻转/OpenAPI/前端表单+PATCH 回填+badge/filter。

触发矩阵、不降级错误族、变异点 20 条、AT-BFC 测试矩阵均按 codex 稿 §4/§6 执行;Claude 稿 §3 触发表中与 codex §4.3 冲突处以 codex 为准(粒度更细且有三镜判别测试佐证)。
