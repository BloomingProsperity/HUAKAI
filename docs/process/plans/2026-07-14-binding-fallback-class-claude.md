# 绑定级 fallback_class 激活 · Claude 独立计划(双计划我方稿)

日期:2026-07-14。前提:Weight(@7145d14a)与 max_parallel_requests(@c94dc6c7)已激活,fallback_class 是 model_pool_bindings 最后一个死字段。

## 0. 语义正本清源(最关键裁定)

**fallback_class 不是"两级降级池",是按失败类型定向降级(typed fallback)。**证据:

- 建表 0008_model_registry.up.sql:166-169:`fallback_class text NOT NULL DEFAULT 'normal' CHECK (IN ('normal','context_window','safety','quota','manual'))`——枚举是**失败类型分类**,不是梯队序号。
- 同迁移 :189 出处注释明写该列借鉴 "LiteLLM typed-fallback (fallback_class)"。LiteLLM 的机制:`context_window_fallbacks` / `content_policy_fallbacks` / 通用 `fallbacks` 按上游失败类别把请求改投到指定的替补 deployment。
- sub2api 无此语义(其 fallback 是账号代理回退 proxy_fallback_origin_id,service/account_service.go:85-87,与模型路由无关)→ 按 #16 写明"无等价物"。new-api 的渠道重试是无类型的顺序重试(失败换下一渠道),也非 typed。
- **裁定:三镜里唯一同源是 LiteLLM,语义按 LiteLLM 对齐**:`normal`=主候选;`context_window`/`safety`/`quota`=只在主候选以对应类别失败后才启用的定向替补;`manual`=平时不参选、运营手工切换时启用的备胎。

## 1. 触发映射(失败类别 → fallback_class)

复用现有错误分类(不新造分类器),在 attempt 失败分类点映射:

| 失败类别(现有信号) | 启用的 class |
|---|---|
| 上游 4xx 明示 context/token 超限(gateway 错误族已区分 prompt 过长) | context_window |
| 上游内容安全拦截(safety/content_policy 错误族) | safety |
| 上游 429/配额耗尽(quota/rate 错误族;含账号池全冷却) | quota |
| 运营在绑定上手工切换 | manual(不自动触发,靠 enabled+class 组合人工启停) |
| 计费失败/auth 终态/客户端断连 | **不降级**(终态,与今天一致) |
| binding 并发闸 429(binding_concurrency_limit_exceeded) | **不降级**(用户侧限额,降级会绕过运营配的上限) |

## 2. 接入点(四层)

- **L4 运行时**:两处改动,都在既有 attempt 机制上,不新造循环。
  1. `DefaultRouter.Plan`(internal/router/default_router.go):候选过滤加一条——**主计划只含 fallback_class='normal'** 的绑定(今天所有绑定默认 normal → byte 级不变);把非 normal 绑定按 class 分桶带在 RoutePlan 上(新字段 `FallbackBuckets map[string][]Attempt` 或同等结构,Priority/Weight 规则在桶内同样适用)。
  2. dispatch 失败换号处(chat_completions_dispatch/attempt 的 failover 判定):attempt 失败被分类为上表某 class 且主计划耗尽(或该类失败本身不宜在主池重试,如 context_window 在同池必然复现)→ 从对应桶取 attempt 续跑;桶空 → 维持今天的终态错误。**context_window/safety 失败直接跳桶**(同池重试无意义,LiteLLM 同款);quota 失败先走完主计划剩余 attempt 再落桶。
- **L2 写口**:registry CRUD 已收 FallbackClass(bindings_admin.go:54/77/98,注释"仅存储兼容"要翻转)——补枚举校验(五值白名单,400 拒非法),create/update 都收。
- **L3 前端**(features/routing/):types.ts 注释翻转+BindingModal 加下拉(五枚举,文案讲清用途)+selection.ts PATCH 回填(镜像 max_parallel_requests 刚修的丢数据模式)。
- **OpenAPI**:绑定两端点 schema 补枚举;`go test ./cmd/gateway/` 一致性门。

## 3. 判别测试(#14/#17)

- 主候选纯净:存在 quota 桶绑定时,正常请求**绝不**碰它(变异=Plan 不过滤 class → 断言红)。
- 定向启用:主池被 429 打满 → 后续请求落 quota 桶绑定成功(真 PG,复用 binding E2E 骨架);context_window 失败 → 一次就跳 context_window 桶(变异=映射表删一行 → 红)。
- 隔离:safety 失败绝不启用 quota 桶(类型交叉断言)。
- 零默认翻转:全部绑定 normal(存量形态)时,Plan 输出与改前 byte 级一致(golden 对比;变异=默认值改错 → 红)。
- manual:enabled=true+class=manual 平时不参选;运营把它改回 normal 即刻参选(registry 缓存失效链核实)。
- 并发叠加:桶内绑定的 max_parallel_requests 依然生效(降级不绕并发闸)。
- money:降级续跑复用同一 claim 的 attempt 机制(BindingID 随 AttemptPlan 透传已就位),结算只结最终成功 attempt;失败链上每个 abort 不重复退款。

## 4. 风险与工作量

- 风险最高点:dispatch 失败分类 → 桶启用的判定与现有 failover/重试语义交织,必须先画清现有 attempt 状态机再动(#17);protocol 家族各端点(completions/embeddings/…)是否都接,首片只接 chat_completions,其余端点仍无降级(它们今天也没有,不缩水),报告写明。
- 工作量:后端 ~2-3 人日 + 前端 0.5 + 测试 1。
- 存量数据:全部 'normal',零迁移零行为变化;激活纯增量。

## 5. 与 codex 稿的预期分歧点

codex 派单时被我错误框架("sub2api 两级降级")带偏的可能性高;若其稿按两级梯队设计,以本稿 §0 的 schema 枚举+出处注释为准据纠偏;若其稿发现了我遗漏的消费接缝(如 selection_mode 与桶的交互),吸收。
