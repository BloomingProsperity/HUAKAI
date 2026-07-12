# 容量耗尽时精确 Retry-After(用池最早恢复时刻替硬编码 5 秒)

## 真码核实(workflow wf4qjytks + 本人逐行复核)
- gatewayhttp/chat_completions_handler.go:967 `classifyPoolSelectFailure`:无容量(ErrNoEligibleAccount/
  ErrNoSlotAvailable/ErrAllChannelsDegraded)的 503 把 `RetryAfterSeconds` **硬编码 5**。
- gatewayhttp/chat_completions_dispatch.go:628 的 no-account 兜底路径(selRes==nil||AccountID==0,err==nil)
  **连 Retry-After 都不设**(失败默认 0 → attempt.go 不写头)→ 客户端 503 无退避头盲目重试。
- 恢复时间数据**存在却被丢弃**:AccountSnapshot 带 HealthStateUntil(types.go:116)+
  ModelRateLimits[key].RateLimitResetAt(:137),health/model 门据此判 eligible;但 default_selector.go:95
  空 eligible 时只 return 裸哨兵,丢掉持有恢复时刻的 accounts,从不聚合 min。

## #16(specifier 读源码,workflow wf4qjytks)
- sub2api:持有最完整的逐账号限流恢复时刻(RateLimitResetAt + RateLimitRemainingSec),但用于 admin 可用性
  看板,**不在客户端耗尽响应回 Retry-After**(无容量直接回纯文本 503)。HUAKAI delta = 生态升级:把已有的
  逐账号恢复时刻复用到客户端 Retry-After。
- new-api:渠道选不到回笼统 429/"分组饱和",不算渠道冷却剩余透客户端。no-equivalent(精度)。
- CLIProxyAPI:纯 relay 无 pool 耗尽 Retry-After 概念。no-equivalent。

## 范围(纯 additive,collision low;不改任何选择/评分/eligible 语义)
1. pool/router/types.go:新增 `NoCapacityError{Cause, EarliestRecoveryAt}` 错误类型,实现 Unwrap → 
   `errors.Is(ErrNoEligibleAccount/ErrAllChannelsDegraded)` 仍成立,既有分类零影响。
2. pool/router/default_selector.go:空 eligible 两个 return 改为包 NoCapacityError + 携带
   `earliestPoolRecovery(accounts, modelKey, now)`(逐账号取健康冷却与模型限流"都需清除"的较晚者 max,
   再取所有账号最早者 min;全无未来时间阻断→零值)。新增 currentTime/modelCooldownKey/earliestPoolRecovery 辅助。
3. pool/types.go:`type NoCapacityError = router.NoCapacityError`(facade 透传)。
4. gatewayhttp/chat_completions_attempt.go(失败构造器所在文件,handler.go 已近文件预算):新增
   `poolNoCapacityRetryAfter(err, now)` —— errors.As 取恢复时刻算 ceil(差值)≥1,否则回退默认 5。
5. gatewayhttp/chat_completions_handler.go:967:`= 5` → `= poolNoCapacityRetryAfter(err, time.Now())`(净 0 行)。
6. gatewayhttp/chat_completions_dispatch.go:628:no-account 兜底补 `= noCapacityFallbackRetryAfter`(修无头缺陷)。

## 默认行为 / 零回归
- 无账号携带未来恢复时刻(stub/旧账号源)→ earliestPoolRecovery 返回零值 → poolNoCapacityRetryAfter 回退 5 →
  **完全等价原硬编码**。default-behavior 不翻转。
- 估算是 best-effort 退避提示(对"还被非时间门挡着"的账号偏乐观),不影响请求正确性/计费。

## 成功标准(变异可证,已验)
- earliestPoolRecovery:多账号取最早(min,变异 min→max 红);单账号双门取较晚(max);全过去→零。
- Select 空 eligible → errors.As 出 NoCapacityError 带恢复时刻 + errors.Is 哨兵仍成立(变异不包装→红)。
- poolNoCapacityRetryAfter:+2s→2、+30s→30、1500ms→ceil 2、零/过去/非本类→回退 5(变异删 errors.As→精确用例红)。
- pool/router + gatewayhttp + pool + dispatcher + cmd/gateway + codebudget 全绿。

## blast radius / 风险
- 改 pool/router(碰撞写面)但纯 additive:只丰富空 eligible 的错误返回 + 加辅助函数,**不动 filter/排序/
  评分/eligible 判定**。独立 worktree 独立 PR;合并前核 base,若与并行线程冲突则 cherry-pick。
- handler.go 已近文件预算,故 helper 落在 chat_completions_attempt.go(失败构造器同文件),codebudget 绿。
