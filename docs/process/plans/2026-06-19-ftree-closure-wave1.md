# 功能树后端闭环 —— 第 1 波(2026-06-19)

Owner 指令:按功能树把后端实现推到闭环(impl + wired + tested),只做后端(前端 UI 推迟),
money/schema/auth/deploy 模块全权授权,但带硬安全条件(先写计划 + 真码摸透;#16 真读三家源码;
schema 迁移只加列/加表、可逆,绝不删除/丢数据;money 路径幂等 + 全审计 + 不双扣;auth 无绕过 +
secret-mask + 身份取自认证上下文;默认行为翻转保留 env 开关;强变异测试 + 对抗审查零 S0/S1 + 干净基线)。
「最大算力,能并行就并行」→ 并行摸排 + 并行实现。

## 方法
两个只读摸排 workflow(24 个非-tested 后端模块,约 74 个 agent)各跑:真码核缺口 → #16 三镜像读源码
→ 对抗 refute(挑战"缺口是否真""是否 additive 安全""有无碰撞") → 排名。下表每条都带真码 file:line
证据、并通过对抗验证。

## 经对抗验证的实现队列(按价值降序)
| 模块 | 功能 | 分数 | 风险 | 缺口(一句话) | 状态 |
|---|---|---|---|---|---|
| dlq | F-OBS-005 | 82 | 无 | StatusQuarantined/DLQ 已定义、schema 允许、有索引、被 hermesadmin 消费,但从无代码"产生"它——毒消息要烧满 10 次重试/15 分钟才进 operator_review,而不是第 1 次就隔离 | **第1波 ✅** |
| hermes | F-OBS-004 | 82 | schema | 软删的会话行从不硬删——明文标题比消息和留存窗口活得更久;给已接线的留存 worker 加 purge,同 env 开关 | 第2波 |
| settlementrecovery | F-OBS-001 | 78 | money(测试) | 生产 money 路径幂等闸 PostgresCommittedProof.IsCommitted 零真 PG 覆盖(handler 全打桩);加 env 门控集成测试覆盖 4 个分支 + 每个变异 RED | 第2波 |
| eventbus | F-OBS-004 | 72 | 无 | 每 handler 的状态 map 在每次 money 路径结算时无界增长(无 cap/TTL/清扫)——进程内泄漏;加有限默认 cap + 驱逐最旧 + env 逃生阀 | **第1波 ✅** |
| credentialworker | F-AUTH-005 | 72 | auth | 已建+已测的凭证轮换那半是死开关(接线从不启用);加默认 OFF 的 max-age env,设置时才追加轮换扫描 | 第2波 |
| pricingeval | F-BILL-001 | 82 | money(可观测) | 静默 tiered→flat **错计费** 计数器没桥到 Prometheus/OTel/告警(它的 fail-open 同类已桥);让它可告警,纯加指标名、不动 money 计算 | **第1波 ✅** |
| pricingcatalog | F-BILL-001 | 82 | money(只读) | 价格倍率防篡改链验证器已建+已测但无路由可达;加 admin 只读 verify 端点,仿 auditledger /verify | **第1波 ✅** |
| observability | F-OBS-002 | 58 | deploy | exporter 不可运维选择(只有 Prometheus 拉,无 OTLP 推)——**需设计:新增运行时依赖(otlpmetric)是 Owner-gated**,先 surface | 已 gated |
| trust / trusthttp | F-TRUST-001 | 52 | 无 | CRL/吊销靠每请求懒加载(nil→env 兜底,每次重解析,配错只在运行时暴露)——提升为启动时一次加载 + fail-fast 的依赖 | 第3波 |
| billing | F-BILL-001 | 52 | money | 价格版本无运行时写路径(全在迁移里),运维不重新部署无法发布/接替价表;加 admin 单事务"接替+插入"发布器 | 第3波 |
| community | F-COMM-001 | — | money | 确实 0 行代码 + 与钱耦合(返佣兑付)→ 按 Owner 意见 **推迟** | 推迟 |

## 第 1 波(并行,包互不相交 → 合并干净)
- **dlq**(前台,我驱动):`dlq` + `settlementrecovery/handler.go`。无 schema(status CHECK 自迁移 0015 起已允许 `quarantined`)。详见下方切片说明。
- **eventbus**(agent):`eventbus` + `config/eventbus.go` + `cmd/gateway/middleware.go` 一行透传。
- **pricingeval**(agent):`otelbridge/expvarbridge.go` 告警桥(+ 测试)。
- **pricingcatalog**(agent):`pricingcataloghttp` 的 admin 只读 verify 端点 + `routes_pricing.go`。

每个模块:独立 worktree、#16 三镜像、变异可证测试、build/vet 绿、对抗审查零 S0/S1 后才合并。一模块一 PR。

## dlq 切片(本 PR)
根因:`RetryPolicy.NextFailure`(retry.go)只按"尝试次数/时长"升级,没有"结构性失败"分类,所以毒消息/
无法解码的事件(结算恢复的 Decode/Validate 失败、事件类型不匹配)要烧满整个重试预算才进 operator_review
——和瞬时故障无法区分。

修复(additive,无 schema):
1. `dlq.ErrUnretryable` 哨兵,标记结构性不可重试的失败。
2. `RetryPolicy.NextFailureForErr`(retry.go):当 handler 错误 `errors.Is` 命中时,第 1 次失败即短路到
   `StatusQuarantined`;否则原样委托 `NextFailure`,瞬时失败与所有既有调用者/测试行为零变更。
3. dlq Service 把 worker + replay 两条失败路径都经 `failureDecision`,它尊重 env 逃生阀
   `HUAKAI_DLQ_QUARANTINE_POISON`(默认开;设 false/0/off 回退旧的 NextFailure 路径),让这个默认行为翻转
   对运维可逆(符合 charter 硬条件)。
4. settlementrecovery 把三个真正毒消息的返回(Decode、Validate、wrong event_kind)裹上 `ErrUnretryable`;
   瞬时的 `ErrClaimNotReserving`/proof 路径与通用 Settle 错误**故意保留可重试**——money 安全:一次瞬时抖动绝不能
   隔离并丢掉一个真实结算意图。
5. store.go **不改**:`quarantined` 行靠既有 partial index `idx_usage_dlq_operator_review`(它本就覆盖
   `status IN ('operator_review','dlq','quarantined')`)出现在运维 backlog,无需戳 `operator_review_at`。

测试(变异可证,每个都抓到 RED):retry 的 `NextFailureForErr` 第 1 次隔离 + 瞬时/nil 委托等价;
settlementrecovery 的 Decode/Validate/wrong-kind 归类为 `ErrUnretryable`;两个 **money 安全控制测试**证明瞬时
结算错误 + `ErrClaimNotReserving` 保持可重试;以及 env 逃生阀开关(开→隔离、关→pending)及其 env 解析。
消费方(hermesadmin)本就把 quarantined 当可处理 backlog;迁移 0015 的 status CHECK + partial index 本就允许该终态。
