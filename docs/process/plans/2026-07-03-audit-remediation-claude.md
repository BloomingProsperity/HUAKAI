# 颗粒度模块配合缺陷 · 修复计划(2026-07-03)

来源:§17 模块配合审计(工作流 w3p36lzsw,8 链路×亲读→猎缺陷→三镜头对抗验证)+ 全项目功能审计(wy3l2q11k,进行中,findings 落地后并入本计划 §B)。全部缺陷 file:line 均经审计 agent 亲读取证;S1 两条已由本人二次亲读复核确认。

分支 `feat/fe-wire-users-mod`,基线 HEAD `7b4e9dfb`(C-1)。Owner 已授**全权**(含 money/schema/auth 自主推进 + 安全网),高危项落地后 surface 复核。

## 进度(2026-07-03)
- ✅ **A#1**(e95a6eee)completion 总线用全装饰后 settler——移出 buildSettlementServices、挪到 quota/budget/notify 装饰之后;变异证明测试;**顺带解 A#4(budget)**。端到端集成断言(真 pg)排 follow-up。
- ✅ **A#2**(df71c432)completionshttp 非流式 settle 脱钩 ctx + DLQ;变异证明测试。
- ✅ **B2 + B7**(8988a302)退款回执改签 trust.v1 canonical + base64(与验签/正常结算同口径),修死签名;伪绿测试改走生产验签口径;变异证明。删死码 v2 签名 helper。
- ✅ **B1**(0470fefd)提前续费窗口(renew-ahead grace 30min)+ 到期路径锁内 due 复查(堵 TOCTOU)。三镜研究(agent a54cbd13)证实首选方案 A:sub2/new-api 均无自动续费 worker、到期查询无排除、到期写点带时间条件。5 新判别测试(3 unit + 2 PG)全变异证明(MUT-A/B/C + MUTPG-1/2 逐一变红);干净基线整包 unit + 全 subscription 集成绿。对抗审查(agent a1c545,独立复跑两处核心变异+确认 6 处推进时钟非伪绿)裁定**可合并,零 S0/S1/S2**。
  - **「测所有相关点」抓到关联缺陷**:到期复查改动使 6 处既有守卫测试(ChainedExpiry/ExpiryGuard/admin extend/change ×PG+unit)悄悄假绿——它们把 ExpireSubscription 当"强制关闭"捷径用在未到点订阅上,被复查 no-op 后断言因"什么都没发生"而通过、守卫变异不再变红。已全部改为「推进时钟到到期后」让到期真实发生、守卫真跑。生产语义核实:ExpireSubscription 只由 worker(ProcessDueExpiries,只喂到点行)调用,admin 用 Revoke/Cancel(不同终态不受复查约束)。
  - 爆炸半径:全在 subscription 包内(ListAutoRenewDue 参数/autoRenewRecord 字段均包内/未导出),无包外调用者;cmd/gateway 编译绿。
  - **S3 follow-up(记录,不阻断)**:①到期复查 no-op 时 ProcessDueExpiries 仍 processed++,ExpiredTotal 指标微高计(罕见 TOCTOU,无害;精确需 store 返回「是否真置终态」布尔)②DefaultAutoRenewLeadWindow 硬编码 30min 不随 interval 派生(已加运维约束注释;默认 interval=5min 安全)③reminder worker 不排除 auto_renew(pre-existing,B1 未触及)。

**🎉 审计修复 S1 四条全清**:A#1(e95a6eee)、A#2(df71c432)、B2/B7(8988a302)、B1(0470fefd),全部 mutation-proven + 对抗审查零 S0/S1 + 零回归。下一步:S2 批 → S3 收尾 → 额度恢复重跑 feature 审计未验证域。

- ✅ **B6**(aad36b49)续费扣款写统一账本:迁移 0170(事件类型+关联列+配对约束+FK+回链)+ 续费事务内写事件行(钱包流出负号,沿退款先例)+ 管理端余额历史三处联动(SQL 源/手改生成码/openapi enum)。对抗审查零 S0/S1,唯一 S2(管理端渲染缺环=审查抓到的 §17 关联漏改)已同切片修;4 处变异证明;subscription/payment/voucher/billing/adminuserhttp + openapi 一致性 + 迁移往返全绿。
  - **审查抓到的关联漏改教训**:写账本行只完成一半,「唯一按用户渲染账本的管理面」的五路 JOIN/COALESCE/CASE + openapi 枚举 = 事件类型新增时的固定关联产物清单,后续加类型必须同步五处。
  - S3 follow-up(已顺手收进):回链部分唯一索引、down dirty 恢复指引、fixture 单事务禁触发器、A6 并发补账本断言、B6-2 注释如实。codebudget 门既有红(gatewayhttp 4 项 + store_memory.go)与本切片无因果,另案处理。
  - 剩 S2:A#3/A#5/A#6/A#8 + B3/B4/B5。

- ✅ **B4 + B5 前半 + B9**(423b36c2)账号状态复核不一致家族:①B4 2FA 完成路径补资格门(GetProfile+EnsureLoginEligible,403 account_not_active 对齐 passkey 反枚举)②B5 封禁撤既有会话(镜像删除路径)③B9 twofa 失败回填身份供审计归因。三处变异逐一证红;gatewayhttp 全包+五邻包 unit+两包集成绿。
  - **三镜研究裁定(agent a2316a)**:多路登录完成步漏复核是惯发病(new-api 2FA 同漏、passkey 双检;其 cookie 会话封禁后可用满 30 天=B5 同病);sub2api=完成步显式复核+签发点 respondWithTokenPair 再兜一道+全车道每请求复核 status+封禁靠惰性复核生效(TokenVersion bump 留给登出所有设备)。
  - ✅ **B5 后半**(1e07adc6):usersession UserGate seam——Validate/Refresh 每次复核会话主体资格,ineligible 拒+机会式撤家族(sub2api RefreshTokenPair 同款);wiring 注入 userauth 适配器,只拒 disabled/deleted/软删,locked/时间锁放行(防失败锁定被当会话 DoS);fail-closed 但瞬时故障不撤家族;Refresh 端点 403 account_not_active 同口径。两变异证红+适配器映射矩阵+fail-closed 测试;全基线绿。**B5 双管齐下全部闭环。**
  - follow-up 记录:签发点收口(sub2api respondWithTokenPair 模式,防未来新登录路径漏门)——Validate 惰性复核落地后已有结构性兜底,收口属加固非必需;TokenVersion 版本号列(将来做「登出所有设备」self-service 的底座)。

- ✅ **B3**(eea7178d)会话漂移 Medium/Low 接消费者:usersession DriftObserver seam(纯观测),Validate/Refresh 非 None 漂移统一交观察者(High 同流,撤销不变);wiring 注入 zap 结构化观测(low=Info,medium/high=Warn),事件记 IP/UA class 无 PII。两变异证红+四档矩阵测试。指标带读者部分并入 B10。**B 系 S2 全清(B3/B4/B5/B6),剩 S2 仅 A 系四条。**

- ✅ **A#3**(2a86f850)并发槽租约 90s→与 claim 租约同源派生(billing.DefaultClaimLeaseWindow 30min,统一租约推导=加固#5):acquire DB 函数 COUNT 前清扫过期槽,90s 租约使长流(600s)中途被扫位顶替、并发上限静默突破。变异退回 90s 双断言红;quota PG 集成绿。剩 S2:A#5/A#6(storm 同文件)+ A#8。

### B1 设计定稿(2026-07-05,亲读全链后;三镜研究并行中,回来后校准)
- **方案 = 提前量续费(renew-ahead grace window),到期判据不动**:`ListAutoRenewDue` 扫 `expires_at <= now+lead`(PG+memory 同改);`tryAutoRenewOnce` 锁行复查同用 `DueCutoff`(autoRenewRecord 加字段);`ProcessAutoRenewal` 算 cutoff 下传。lead 取 30min(5min 节拍 ≥6 次尝试,余额不足可重试;对比 Apple 提前 24h 扣款,30min 属保守)。
- **为什么不选「ListDueExpiry 排除 auto_renew=true」**:续费持续失败(余额不足/套餐停用)的订阅将永不到期 → 白嫖;要堵这个洞需加失败计数/宽限状态机 = schema 变更。提前量方案零 schema、到期兜底天然保留:续费失败订阅照常在 expires_at 到期降级。
- **提前续费用户零损失**:activation.go:85-89 续期基准 = `max(now, 现到期)`,提前续费从原到期日累加;且未过期续期走 reconcileCapsTx(保留用量计数,防重置白嫖,对齐 sub2api 期中续期语义)。幂等锚 periodKey=续费前 expires_at,提前/准点同锚。
- **顺带堵第二竞态(#17 配合点)**:closeSubscriptionOnce 锁行后只复查 status 不复查到期——「到期扫描→锁行」间隙内续费提交后,订阅仍会被误置 expired。修:Expire 路径锁行后 `!IsExpiredAt(now)` → no-op(Cancel/Revoke 无条件语义不受影响)。
- **旋钮不动**:HUAKAI_SUBSCRIPTION_AUTO_RENEW_ENABLED 默认 false 保持(默认翻转=Owner-gated);旋钮关时本修复零行为变化(到期路径的 due 复查纯防御)。
- **判别测试**:①lead 窗口内(未到期)订阅进续费候选且成功续期、到期日=原到期+validity(变异:退回 `<=now` → 红);②续费失败(余额不足)订阅仍准点到期(守「永不到期」陷阱,变异:ListDueExpiry 排除 auto_renew → 红);③expire 锁行后 due 复查:已续期行 no-op(变异:去掉复查 → 红);④两 worker 同跑集成:auto_renew=true 有钱续期、没钱到期。
- ⏳ **额度恢复后重跑 feature 审计未验证域**(resume wf_bfe5c8d5-e10):payment/apikey-access/quota-budget/pricing/moderation/media/billing-settle/hermes/mimicry-egress/credential/notify/platform-obs/frontend。

## 安全网(每切片必过)
1. 亲读真码定位根因(不信 grep) 2. §14 变异证明测试(改坏必红) 3. 干净基线 `-count=1` 整包+相邻包绿 4. 对抗自审爆炸半径(配对另一半/兄弟/装饰器链)。

## 六类共性根因(修复要连根,不只补单点)
1. **装饰器链顺序 vs 旁路总线**:completion 事件总线在中间层捕获 settler,晚于 quota/budget/notify 装饰 → 异步结算静默跳过外层装饰器(结构性顺序陷阱)。
2. **多资源获取只测单维释放**:一个请求 reserve 出 hold+账号槽+quota预留+quota并发槽+budget预留,释放分散在不同装饰层,无一测试断言"全部归零"。
3. **跨模块租约窗口不协同**:billing claim=30min(正确>600s流),quota 并发槽=90s(<请求时长),media claim=20min 但单 worker 无法先于 sweeper。
4. **请求 ctx 用于交付后/提交后持久写**:断连即回滚/半提交(C-1 同类,已逐调用点手抄脱钩,漏点即退回不安全)。
5. **补偿链只认"失败入队"不认"从未运行"**:quota reconciler 只重放失败 job;动作被旁路/从未调用则无 job 无孤儿记录。
6. **best-effort 释放吞错 + 一次性保护**:持久 DB 计数器的 -1 吞错 + sync.Once → 一次瞬时失败永久泄漏。

## A. relay 链确认缺陷(9 条)· 修复优先级

| 序 | 缺陷 | Sev | 默认触发 | file:line | 修法 | schema | Owner-gated |
|---|---|---|---|---|---|---|---|
| 1 | 异步总线持未装饰 settler → 成功请求漏 quota 预留+并发槽 | **S1** | 是 | cmd/gateway/middleware.go:402 / wiring.go:1096-1107 | 总线构造挪到全装饰链之后(持最终 settler)+ wiring 断言;顺带解决 #6 | 否 | money/quota wiring |
| 2 | completionshttp 非流式 settle 用 ex.ctx → 断连漏资源+漏计费 | **S1** | 条件(断连窗口) | completionshttp/attempt.go:127 | 改用 `ex.billingCtx()` 脱钩 + DLQ 兜底(照抄四兄弟/流式路径) | 否 | money |
| 3 | quota 并发槽租约 90s 死钉、无续租 → 长流(600s)静默突破并发上限 | S2 | 是(>90s请求) | quotaenforce/settler.go:18 | slot lease 从 STREAM_TOTAL_TIMEOUT+grace 派生(或 = claim lease) | 否 | quota |
| 4 | media claim 被 sweeper 抢先 abort → media_tasks 卡非终态+每~30s 重试失败死循环 | S2 | 背压/停机 | mediatask/store_money.go:156 | ErrClaimNotReserving 时强制 media_tasks 终态(claim_swept)+ 落孤儿记录,不回滚整事务 | 否(复用 orphans 表) | 媒体计费 |
| 5 | storm release 吞错+sync.Once → current_in_flight 永久泄漏死号 | S2 | 瞬时DB错/failover | auth/storm_controller.go:84 | release 不吞错(失败进 DLQ/待对账)+ current_in_flight reaper | reaper 加列才需 | auth-core 凭据刷新 |
| 6 | storm acquire scan 在 +1 提交后报错 → 无 release 闭包 → 永久泄漏死号 | S2 | 瞬时DB错 | auth/storm_controller.go:74 | scan 失败即补偿 -1 | 否 | auth-core |
| 7 | budget 结算同被异步总线绕过 | S2 | 否(默认关) | wiring.go:488 | 随 #1 同根修复 | 否 | 预算强制 |
| 8 | credential finalize 非原子:Create 提交后 MarkFinalized 断连失败 → 活凭据孤儿+flow卡死 | S2 | 条件(断连窗口) | credentialacq/finalizer.go:74 | Create+MarkFinalized 同事务(或脱钩 ctx)+ flow 孤儿对账器 | 否(复用表) | auth-core 凭据 |
| 9 | DefaultSelector 槽释放用可取消 ctx(90s 孤儿扫兜底) | S3 | 条件 | pool/router/default_selector.go:235 | 照 PASR 改 `WithoutCancel`+限时 | 否 | pool |
| 9b | credentialacq BeginFinalize 补偿 MarkFailed 用可取消 ctx | S3 | 条件 | credentialacq/finalizer.go:58 | 补偿写脱钩 ctx | 否 | auth-core |

## 系统性加固(随修落地,防同类再生)
1. **统一"最终结算 settler"单一注入点** + wiring 断言"异步 handler settler == d.Settler 装饰深度"(治根因1)。
2. **per-资源生命周期集成测试**:reserve 全部资源 → 驱动同步+真实异步总线 settle/abort → 断言每个计数器回基线;删任一装饰器变红(治根因2)。
3. **抽公共交付后结算骨架**:五兄弟 settle/abort 收进共享 helper,恒脱钩+恒 DLQ,消灭逐点选 ctx(治根因4,防 #2 再生)。
4. **持久计数器一律配主动 reaper+不吞错**:接线 ExpireConcurrencySlots、current_in_flight reaper、quota reservation 陈旧扫;折进 LeaseSweeper tick 默认开(治根因5/6)。
5. **租约窗口统一推导**:所有模块 lease 从单一"操作最大生命周期"源派生(治根因3)。
6. **补偿改状态扫描**:孤儿状态扫描器补"动作从未运行"缺口(治根因5)。

## 执行顺序
1. **S1-A(#1)先修** — 纯 wiring 重排,顺带解 #7;写变异证明的 wiring/集成断言。
2. **S1-B(#2)次修** — 机械照抄既有脱钩范式 + DLQ。
3. S2 批(#3/#4/#5/#6/#8)按域推进,每条带判别测试。
4. S3(#9/#9b)收尾。
5. 系统性加固 1/2/3 随对应切片落地。

每切片:亲读→修→变异测试→干净基线→对抗自审→commit(中文)→报进度。S1/S2 落地后 surface Owner 复核。

## B. 全项目功能审计缺陷(wy3l2q11k,10 条确认)

> ⚠️ 该审计**撞周额度上限中断**(165 verify agent 失败 + synthesize 失败),确认的 10 条是完成验证的子集;`payment/apikey-access/quota-budget/pricing/moderation/media/billing-settle/hermes/mimicry-egress/credential/notify/platform-obs/frontend` 域各有候选未跑完验证——**额度恢复后需重跑这些域的 verify**(resumeFromRunId=wf_bfe5c8d5-e10 可复用已完成 agent 缓存)。

| 序 | 缺陷 | Sev | file:line | 修法 | Owner-gated |
|---|---|---|---|---|---|
| B1 | ExpiryWorker(1min,无 auto_renew 过滤)抢先把 auto_renew=true 订阅置 expired → 自动续费几乎永不发生,付费用户被停服降级还没扣续费款 | **S1** | subscription/store_postgres.go:540 | ListDueExpiry 排除 auto_renew=true(或续费带 grace 窗口先扣费续期);补两 worker 同跑的判别集成测试 | money/订阅 |
| B2 | 退款回执签名用 v2 canonical(32B 哈希)、验签用 trust.v1 完整字节 → **所有退款回执验签恒 Valid=false(死签名)** | **S1** | audit/refund_worker.go:565 | 退款签名改用 `trustreceipt.SignReceipt`(trust.v1),与结算侧/验签侧同口径 | 审计签名 |
| B3 | 会话漂移 DriftMedium/DriftLow(仅IP变/仅UA变)被算却零消费者:不撤销/不审计/不日志/不指标 → token 重放最常见形态检测全盲 | S2 | usersession/rotation.go:114 | medium/low 至少落审计事件+指标(或按策略 step-up);Service 补 logger | auth-core(会话) |
| B4 | 2FA 登录完成路径不复检账号资格 → 被禁用/锁定/删除用户在 challenge 窗口内签发全新长效会话(passkey 路径有 EnsureLoginEligible,2FA 缺) | S2 | gatewayhttp/auth_handler.go:382 | verify 后 Sessions.Create 前补 `EnsureLoginEligible`(镜像 passkey) | auth-core |
| B5 | 用户封禁不吊销会话 bearer,Validate/Refresh 从不复核账号状态 → 被封用户 self-service 会话+续期存活最长 30 天 | S2 | usersession/rotation.go:238 | 封禁 handler 调 SessionRevoker.Revoke(对齐删除路径);或 Validate/Refresh 复核 users.status | auth-core |
| B6 | 自动续费扣 user_balances 只写 subscription_auto_renewal_charges,不写 billing_events 统一账本 → 与充值口径断裂、对账缺一环 | S2 | subscription/store_postgres_auto_renewal.go:142 | 续费扣款同写 billing_events(与充值/结算同账本) | money |
| B7 | 退款回执验签的两处测试用「非生产签名路径」造 fixture → 伪绿掩盖 B2 的 canonical 断裂 | S2 | cost_receipt_handler_test.go:320 | 测试改用生产签名路径(与 B2 一起修,变异证明) | 否 |
| B8 | /v1/auth/me 面板归属用 UserRole(无 status 过滤)→ 被封/锁 admin 仍被 /me 判 admin 面板(与 ActiveUserRole 口径不一致) | S3 | controlhttp/panelauth_handler.go:95 | /me 改用 ActiveUserRole(带 status 过滤) | auth-core |
| B9 | 2FA 校验失败审计事件 tenant/user 记 0(challenge 携带身份未回收)→ 失败与锁定无法归因 | S3 | gatewayhttp/auth_handler.go:375 | 失败审计从 challenge 回填 tenant/user | 否 |
| B10 | AutoRenewWorker money 指标(Renewed/Skipped/Failed/Tick)无读者,admin worker-stats 不暴露 | S3 | cmd/gateway/subscription_worker_stats_adapter.go:20 | 接线指标到 admin worker-stats 端点 | 否 |

**共性延续 A 的根因**:B1(跨模块 worker 判据不协同=根因3)、B2/B7(签名/验签 canonical 配对断裂+伪绿测试=根因2/missing-associated)、B3(死开关)、B4/B5/B8(账号状态复核在各登录/会话/面板路径不一致=inconsistent-state,同一不变量 passkey/删除有、2FA/封禁/面板缺)、B6(账本写入配对缺失)。

### B6 scope(2026-07-05 亲读核实,下一切片)——**属实非假缺口**
- **确认 billing_events 是钱包统一账本**:payment topup(store_postgres.go:464 `payment_credited`)、refund(store_postgres_refund.go:211 `payment_refunded`)、余额券(voucher/store_postgres.go:317 `voucher_redeemed`)都真 INSERT。0023 迁移已把 claim_id 放宽 nullable、event_type CHECK 扩到 money 类型。自动续费扣 user_balances 却只写 subscription_auto_renewal_charges、漏写 billing_events → 对账缺一环属实。
- **当前 event_type 全集**(0092):claim_committed/aborted/reconciliation_appended + voucher_redeemed/balance_recharged/payment_credited/payment_refunded。**无订阅续费扣款类型** → 修复需**新迁移**:①加 event_type(拟 `subscription_auto_renewed`)②加关联列(拟 `subscription_auto_renewal_charge_id`)③更新 `billing_events_claim_or_voucher_check` 约束允许新类型带新 ref。
- **待定的实现问题**(下一轮先解):续费是钱包**扣款**(money out),而 payment_credited 用 +amount(money in);要读 refund(payment_refunded)的 actual_cost_signed 符号约定,确定续费扣款该记正还是负(对账 delta 语义)。§16 先看三镜是否在钱账本记订阅续费(sub2/new-api 无自动续费 worker,可能无先例→按 HUAKAI 充值/退款账本符号约定自洽即可)。
- **切片形态**:migration(补 openapi/迁移一致性测试)+ tryAutoRenewOnce 内 INSERT billing_events + 回填 link + PG 判别集成测试(续费后断言 billing_events 一行、类型/金额/符号正确;变异:去掉 insert → 红)+ 对抗审查。money+schema,Owner-gated 但在全权授权内,落地后 surface。

## 执行顺序(合并 A+B,19 条)
1. **S1 四条**(A#1 配额旁路、A#2 completions settle ctx、B1 订阅续费、B2 退款死签名)——最先,各带判别测试。
2. **S2 批**按域推进(A#3-8 + B3-7)。
3. **S3 收尾**(A#9/9b + B8-10)。
4. 系统性加固随对应切片落地。
5. 额度恢复后重跑 feature 审计未验证域(resume wf_bfe5c8d5-e10)。
