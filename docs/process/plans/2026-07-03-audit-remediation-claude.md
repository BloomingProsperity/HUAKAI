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

- ✅ **A#5 + A#6**(4bf18522)storm 槽持久计数器泄漏三件套:①A#6 回读失败就地补偿 -1(GREATEST 钳位净安全)②A#5 release 3 次退避重试+脱离 ctx+全败 slog,Once 幂等保留 ③陈旧 reaper 15min 归零(last_updated_at 现成列零 schema),挂 credentialworker 每 tick——加固#4 落地。三变异证红(含语义变异:去陈旧条件→新鲜行误伤断言红);StormController DB 路径零测试一并补齐。**剩 S2 仅 A#8。**

- ✅ **A#8 + A#9 + A#9b**(241485ac)交付后写与补偿释放全部脱钩:①A#8 MarkFinalized 脱钩+3 次重试(Create 后是记录既成事实;全败由既有 expires_at 惰性过期兜底——亲读 BeginFinalize Get 回退路径核实该语义已存在,审计建议的"孤儿对账器"由此覆盖)②A#9b 两个 MarkFailed 补偿脱钩 ③A#9 selector 槽补偿 releaseSlotDetached(镜像 pasr HIGH-2)。四变异证红;判别关键=ctx 敏感 DB 包装(fake 忽略 ctx 则测试恒绿伪判别)。
  - **勘误**:进度行「A#1 顺带解 A#4(budget)」应为 #7(budget);#4(media claim 被 sweeper 抢先 abort)仍待修。**剩:A#4(S2)+ B8/B10(S3)。**

- ✅ **A#4**(7167b5a8)media claim 被 sweeper 抢先 abort → 强推终态(succeeded/failed+error_class=claim_swept)+ 事务内幂等落孤儿线索(复用 Manual-First 对账),跳过 billing 假账,不再卡 in_progress 每 30s 死循环。两 PG 变异证红。
- ✅ **B8 + B10**(75af01aa)收尾:B8 /me 面板走 ActiveUserRole(封/锁 admin 降级 user 面板不给 admin,锁定不 403 防 DoS,已删→403);B10 AutoRenewWorker money 指标接 admin worker-stats(带 Enabled,openapi 同步),drift 指标 B3 已由 zap 日志消除死指标。变异逐一证红。

## 🎉🎉 审计修复全部 19 条闭环(2026-07-05)
A 系 9 条(A#1/2/3/4/5/6/7/8/9/9b)+ B 系 10 条(B1-B10)全部 mutation-proven + 干净基线 + 对抗审查(S1 切片)零 S0/S1 + 零回归。11 提交:e95a6eee/df71c432/8988a302/0470fefd/aad36b49/423b36c2/1e07adc6/eea7178d/2a86f850/4bf18522/241485ac/7167b5a8/75af01aa。系统性加固#4/#5(持久计数器 reaper+不吞错、租约统一派生)随 A#3/A#5/A#6 落地。
### 扫尾进度(2026-07-05)
- **A#1 端到端集成断言尝试**:写真 pg e2e(handler→quota 装饰 settler→断言预留 settled)时撞到一个与 A#1 无关的怪相——刚经 `service.Reserve` 提交的预留,`svc.Settle(ReservationID=0)`(经 claim 解析)在其 Serializable 事务内报 `no rows`,而带 ReservationID 的直接 settle 能查到;getFinalizationReservation 的查询本忽略 ReservationID(只按 tenant+claim),两路本该一致。疑为测试 harness 的连接快照制品。因加了诊断直接 settle 会使 handler 只命中幂等路径=**伪绿**,按 B7 教训删除该 e2e、不留误导测试。**A#1 回归由既有单测 `TestWiring_AsyncBillingHandlerSettlesThroughQuotaDecorator`(spy+mutation-proven)充分守护**;真 pg e2e 与该快照怪相留作独立 follow-up(需单独查 quota settle 的 Serializable 可见性,非本 arc)。

**剩扫尾(非审计条目)**:①额度恢复重跑 feature 审计未验证域(resume wf_bfe5c8d5-e10)②A#1 端到端集成断言(真 pg)③签发点收口/TokenVersion 加固(B4/B5 follow-up)④codebudget 既有红(gatewayhttp 4 项+store_memory.go)另案 ⑤加固#1/2/3/6(统一结算 settler 注入点+per-资源生命周期集成测试+补偿状态扫描)。

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

## feature 审计未验证域重跑(2026-07-05,替代 wf_bfe5c8d5-e10)
用同一 §17 模块配合审计法(单 agent 深审,亲读取证)逐域重跑。

### payment + 结算恢复域(agent a55c10)——已审已修
- ✅ **S2-1**(bee4f34f)订阅单履约确定性失败(降档拒绝/套餐停用)转终态 failed + 审计留痕,不再永久卡 recharging 悬空(webhook 重投/admin 重点撞同一堵墙、无清扫器认领);退款 Manual-First 走 admin。变异证红。
- 📋 S3-1(follow-up):observability DLQPayload 未镜像 Handle 身份兜底 → 潜在零 ClaimID 毒消息落盘(当前所有 caller 都令 SettleRequest.ClaimID==event.ClaimID 故未触发);修:DLQPayload 生成前做同款兜底 + eventbus.writeDLQ 入队前 Validate。
- 📋 S3-2(产品确认):payment 派生「支付来源余额」(SUM credits-refunds,不扣消费)vs user_balances「可花余额」分叉,若前端当可用余额展示会误导。
- ✅ 核心配合链 9 子域审计无问题:结算 Tx2 原子性、DLQ 三证 proof 防双扣、post_delivery_settlement 精确路由、充值三写同事务、退款不可超退、回调验签/金额/租户/重放、履约幂等不双开。
- 次要观察:admin 支付端点从请求体取 tenant_id(对全局 platform_admin 是操作对象选择器非身份,单租户不可越权;多租户启用前需改按身份限定,与 CLAUDE.md #4 软张力)。

### credential + apikey-access 域(agent adf29b + 泄漏扫查 a2f5b8)——已审,零 S0/S1/S2
- ✅ **S3 修**(f8499ba5):chat_completions_pricing.go 比率解析失败日志原样落 err(全仓唯一绕过 privacy 脱敏的 err 出口)→ 收口为 error_class/error_type,判别测试变异证红。
- **审计 agent 主结论被亲读推翻**:agent 称「无 forward 时过期闸」,实际 credentialstore/types.go:265 `RuntimeMaterial` 有闸返回 ErrCredentialExpired,只是 `allowGrace` handler(Anthropic OAuth 等)豁免。豁免路径语义自洽:过期 token 端出 → 上游 401 → 网关同步热刷新(30s 去重+storm 预算)自愈,auth 车道把账号冷却到刷新成功本就是正确行为;残余成本=降级场景(worker 停/退避中)每 30s 白烧一次 attempt。定 **S3 记录不修**(加闸反而会破坏「上游为准」语义且过期闸下无热刷新触发点)。
- 📋 S3 记录:①grace 状态机 inert(`refreshing_with_grace`/`grace_until` 全仓无人设置,SQL serving 分支死代码,原子刷新已覆盖该窗口);②Gemini `auth_in_query=true` 明文 key 进 URL query(当前无日志化点故不泄漏;未来任何 URL 级日志/HopChain endpoint 接线前必须先脱敏;收敛到 header 是行为变更不自主动);③HopChain builder 预留 endpoint 接线口同上;④KEK 单钥无 re-wrap worker=已知 Owner-gated(2026-06-26 已 surface)。
- ✅ 核实干净:hk_ key 解析零缓存(封禁/吊销即时生效)、16 字符前缀无 bcrypt fanout、key 过期逐请求硬校验+worker 翻状态双保险、DR-001 双侧租户绑定、解密 fail-closed(按存量 KeyID)、热刷新与预扣释放均 detached ctx、AAD 版本配对往返一致、凭据注入链无活跃泄漏(privacy 三出口全覆盖+Zeroize)、legacy 明文回落路径零日志。

### quota-budget + pricing 域(agent adc148)——已审,2 S2 + 4 S3 + 死代码
- ✅ **C-1【S2】已修**(d082c569):补偿环崩溃窗口——进程死于 billing Tx2 与 quota settle 之间时补偿 job 从未入队,预留永久卡 reserved 冻结窗口 headroom(manual/none 累计窗无滚动自愈)。修:补偿 worker 每轮新增孤儿预留清扫段(lease 过期+claim 已终态→committed 按 claim actual_cost Settle/aborted Release,SQL 终态过滤防 LIMIT 饿死)。六组变异证红。
- ✅ **C-1 对抗审查加固**(8d2b65aa):审查坐实 2 S2 + 4 S3,全落地——①S2 无索引全表扫→0171 partial 索引;②S2 清扫零退避每轮 re-enqueue 击穿 job 退避/终态停靠(去重唯一索引只含 queued/running,failed 后每分钟净插新 job)→清扫只取无 job 史孤儿行,失败行自动移交 job 段获得退避;③S3 stale 快照×aborted 复活链竞态→每行动作前复核现状(预留未终态+lease 仍过期+claim 仍终态),注释去绝对化;④S3 job 段/清扫段金额分叉→job 重放同样优先 claim 实结额;⑤S3 sqlc 全局 override(billing_ledger_claims.actual_cost→NullDecimal)漂移→手写码对齐;⑥幂等命中不计 processed。三组新变异证红,integration_pg 全包绿。审查驳回项:actual_cost=0 合法(cache-hit/非计费 attempt)、无双冲减(actual_cost 唯一写点 WHERE status='reserving')、Serializable+行锁幂等主线成立。
- 📋 **S3-4 记录(审查发现,预存在)**:退款先于补偿落地时 ReverseCost 只对 settled 态冲减、其余 skip 不重试→孤儿窗口内退款冲减永久丢失,随后补偿按未冲减全额入窗(成本窗多计,过度限流非丢钱)。修法需 ReverseCost 对 reserved/reconciliation_needed 行留冲减备忘或重试——排 follow-up。
- 🔑 **C-1b Owner-gated(surface 首位)**:HUAKAI_QUOTA_RECONCILER_ENABLED 默认仍关(2026-06-24 死开关普查裁定配额激活=Owner-gated);不翻开,默认部署下 C-1 修复不生效。建议翻默认开(worker 分钟级、限 200 租户/轮、动作全幂等)。
- 🔑 **C-2【S2】Owner-gated(money)**:pending_reconciliation 有写无读死区——①ratio 后端错 fail-open→1 的行金额永久定格(配折扣/溢价组的租户少收/多收);②流式结算时价表读故障→ActualCost=0 落账=真漏钱,注释自认无重算 worker(completionshttp/attempt.go:185-201);唯一自动消费者只认 no-usage 全零行且从不清 flag。修法=补价 worker(按 usage_records token×现价重算+reconciliation 事件),动钱需拍板。
- ✅ **C-3 已修**(98ca3cf5):可复活预留(released/expired)重放只校验 fingerprint(请求内容身份),predicted/scopes 允许随重试更新(billing 幂等口径对齐:pooling_group 不入指纹、复活接受新 predicted);reactivate 用新值重跑策略评估与窗口判定,强制面不缩;非复活态维持严格比对。两组变异证红(严格比对挡回→更新值重试 429;删 fingerprint 校验→异内容重放放行)。
- ✅ **C-4/C-5/C-6 已修(codex gpt-5.5+xhigh 实现,PM 亲检验收)**:五包(completions/images/embeddings/audio/rerank)对齐 chat——①ReservedTokens 补传(completions 用 inputEstimate、images 用 prompt token 估算、audio 仅 token 方案、rerank 无估算不造假);②completions 预测价补输出估算(MaxTokens/缺省 1000 与 chat 同口径);③quota fail-open 五包补 expvar 计数+Warn;④completions ratio 走 ResolveWithSignal,pending 进结算 snapshot(marker 与 chat 同串);⑤quota deny 五包补 Retry-After/window_kind。codex 12+ 测试逐条变异红/绿;PM 独立复验输出估算与 pending 两组变异红+全门禁绿。
- ⚠️ codebudget 回归(C-1 切片自伤):db/quota/quota.sql.go 超基线 5% 余量、quota/pg_store.go 642>600——拆文件修复(派 codex)。
- 📋 C-7【S3】死代码:quotaDenyFromBudget/IncrementWindowRequestCount/ExpireConcurrencySlots 无调用方。
- 📋 存疑-3:reconciler job 重放用 predicted 当 actual(保守高估);job 表有 claim_id 可 join 权威值,清扫段已用 claim actual,job 段同法可改进。
- ✅ 核实干净(agent 亲读证据):检查/累计同真相源同事务行锁+Serializable 重试、窗口边界按 reserve 时刻 snapshot 归属无跨窗污染、billing/quota/budget 三方同一 CostForAttempt 值、abort 补偿闭环(deny→abort claim/释放同事务/budget 逆序回滚/lease sweeper 走全装饰 settler)、退款链防双退+逐窗钳制+wiring 已注入(旧账「配额退款不冲减」已闭环)、订阅 caps=真 quota_policies 最严者胜+期中续期不清用量、单价缺失 fail-closed 无 0 价白吃、工具附加费默认开真累计、用户可见读与强制同源无表分叉、budget Redis 故障内存 fallback+响亮告警。

### moderation + notify + platform-obs 域(agent a7946c)——已审,4 S2 + 一批 S3
亲读取证。分三类处置:

**A. 纯技术缺陷·自主修(派 codex)**
- **PO-1【S2】DualRunReconciler 每请求内存泄漏**:billing_persister_handler.go:96-98 无条件 RecordAsync,reconciliation_handler.go:107-143 record() 只插不删、无淘汰;读端(Compare/ExpiredMismatches)与 legacy 写端(RecordLegacy)生产全零调用=对账单边名存实亡。默认 eventbus 开→每成功请求泄漏一条(含 decimal+error 串)→缓慢 OOM。处置:生产默认不装 reconciler(读端与 legacy 端皆死,移除零功能损失,消除泄漏),保留类型+测试待将来真恢复双跑再接;加守护测试断言生产 wiring 不注入。**改前 codex 须 grep 复核 reconciler 无其它真读端。**
- **PO-2【S2】DLQ ErrNoHandler 不 quarantine**:dlq/service.go:152-157 无 handler 返回 ErrNoHandler(非 ErrUnretryable)→ 按普通失败烧满 10 次/15min 重试才转 operator_review,replay 仍 ErrNoHandler 永不可恢复;最痛=audit_event_replica(money 审计 ref 缺失行)默认部署躺尸+抬高 dlq_depth 噪音。处置:ErrNoHandler 视同结构性失败直接 quarantine(NextFailureForErr 特判 or wrap ErrUnretryable),replay 侧同理。
- **NT-3【S3】限流先占后发+map 无淘汰**:notifier.go:126-128/191-196 Allow 先写 last[key] 再投递,投递失败不回滚→一次瞬时故障静默压制一整小时;last map(:538-550)只写不删。处置:投递成功才记槽位(或失败回滚)+ map 周期淘汰过期键。
- **MO-3【S3】config 读取故障 fail-closed 波及未启用租户**:screener.go:49-53 err 短路发生在 Enabled 检查之前→moderation 全关的租户在 moderation_configs 查询抖动时也吃 403;且 config 无缓存(keyword/hash 有 30s TTL)。处置:err 短路移到 Enabled 判定之后(未启用租户 config 故障放行)+ config 加 30s TTL 缓存对齐 keyword/hash。
- **死代码清理(PO-4/PO-5/MO-2 API 面)**:obs/dlq refund_worker.go 双通道残留(真通道在 internal/dlq)、MetricsAggregator 聚合无读端、moderation ViolationFeeUSD/DecisionFeeCharged/BillingEventID 死字段(Owner 已裁「违规罚款不接」,清 admin API 面死字段防误导)。逐项 grep 证零调用再删。

**B. 默认翻转/新功能面·Owner-gated(surface)**
- **NT-1【S2·信息泄露】运维广播扇出给终端客户**:notifier.go:163-199 broadcast→store.go:83-108 ListActiveSettings 无角色过滤;routes_notifications.go:33-36 普通 session 可 PUT /v1/users/me/notifications→任意登录用户开 webhook 即收 provider_account_down(含 provider_account_id/vendor/health_state)+alert_firing。上游账号池=商业内核,泄露给客户。修法需决策:①最小堵漏=运维广播只发 admin/运维角色用户(需角色来源 join);②架构级=独立运维接收者配置表(schema)。**信息泄露不宜拖,surface Owner 择方案(带三镜对照)。**
- **NT-2 + MO-1【S2/S3·静默死链】控制面恒在、执行面默认关无提示**:HUAKAI_ALERTING_EVAL_ENABLED 默认关但 alertinghttp CRUD 恒挂→规则建了永不评估;HUAKAI_CONTENT_MODERATION_ENABLED 默认关但 admin config PUT Enabled=true 静默无效。默认翻转=Owner-gated;可自主的最小改善=CRUD 回显执行器状态/补部署文档(不翻默认)。
- **PO-3【S2】obs/dlq dlq_events 只写不读**:store_postgres.go:176-178 写、全仓无 SELECT/admin 面/worker→email 死信(SMTP 故障超 5 次重试)终态丢失且运维零可见;internal/dlq 侧有 /admin/v1/dlq 面形成对比。补 admin 列表/replay 面=功能新增,surface 排期。

**C. 核实干净(教科书级正确接线,agent 亲读证据)**
- moderation 审核在 reserveClaim **之前**(handler.go:529-537),拒绝时钱未动无需补偿=最干净解;拒绝路径审计不打折、只存 payload_hash 不存 body;auto-ban 原子 CTE 强制同事务写审计;管理面全 resolveAdmin+租户 scope。
- notify 三处失败均不阻断主业务(async+context.Background/WithoutCancel,非请求 ctx post-commit);密文 AES 信封加密+AAD 绑 tenant/user,GET 只回 *_configured 布尔;出站 SSRF 校验+HMAC 签名+模板 html.Escape;email 重试链完整(outbox PriorityCritical+textproto 数值码判临时/永久+退避 15min 上限)。
- platform-obs 审计导出 IDOR 已修双层防御(路由 session+handler 派生 scope,吊销表加载失败 503 不跳检);统计写读同源租户强隔离不选凭证列;DLQ 三泳道 panic-recover+quarantine+dlq_depth 每 30s 刷;日志门面旧 S1 已修 accesslog 不记 query/header/IP/body。

## 🔥 端到端测试阶段(2026-07-05,Owner 拍板「现在起核心端到端+并发实测」)
- ✅ **smoke 冒烟通过**(mock 上游+真 PG+子进程网关):HTTP 正确+5 项 PG 计费断言全过。
- ✅ **第一跑即挖出核心 billing↔quota 配合缺陷并修复(68194e32)**:quotaenforce 结算只传 ClaimID(ReservationID=0),Settle/Release/CommitCacheHit 的写点误用 req.ReservationID → WHERE id=0 → 0 行 → 裸 no rows 回滚 quota 结算事务。直连路径每请求失败(fail-open warn 掩盖),reserved_value 靠 reconciler 按 claim 重解析兜底才收敛(带退避窗口占用+DB churn);A#1 扫尾时的「no rows 怪相」由此坐实为真缺陷而非 harness 制品。修:6 写点一律用 getFinalizationReservation 解析出的权威 reservation.ID。两组变异证红;smoke 复跑 warning 消失。
- ✅ **对抗审查(agent a007d7)裁定驳不倒、零 S0/S1/S2**:mismatch 拦截/残留 0 值无害(reconciler 按 claim 重解析)/窗口定位不依赖 ID/其它 requireAffected 均已改/Serializable+FOR UPDATE 无并发落空。2 条 S3 测试缺口(变异实测证绿):①CommitCacheHit claim-only 零覆盖②3 个 ReleaseConcurrencySlots 写点无判别(槽泄漏只有 lease 兜底)。fix-in-place 排下一 codex 批。
- 🔄 **阶段2:真上游 e2e(codex 搭建中)**:照 smoke 模板写 e2e_upstream tag,混元(hunyuan-lite,压 max_tokens)真转发+计费金额+quota 结算(守 68194e32)+并发槽断言;真 key 走 env 注入(Owner 提供 ARK/HUNYUAN 两 key,已定位于 scratchpad/e2e-keys.env,权限 600)。

### 🔴 端到端测试第2个真缺陷(S1 上线阻塞):models CHECK 缺 19 个已注册 vendor family
- **根因**:internal/provider/registrydefault/default.go MustRegister 了 39 个 protocol family 的 adapter,但 models 表 `models_protocol_family_check`(0008/0011 迁移定义)只允许 20 个 → 缺 19 个:国内厂 doubao/hunyuan/qwen/minimax/glm/ernie/baichuan/kimi/step/yi + vertex_anthropic/vertex_gemini/cohere/dify/ollama_chat/ollama_native/gemini_code_assist/replicate_image/anthropic_claude_session。
- **后果**:这 19 个 vendor 的 model 无法录入 models 表(protocol_family 违反 CHECK)→ ResolveModel 拿不到 → **19 个 vendor relay 生产完全不可用**,含 Owner 2026-07-03 明确裁定要上线的国内厂六厂([[cn-vendor-domestic-key-verdict]])。
- **性质**:cn-vendor 接入(7ec6da57 端点+代码)的关联产物漏改——adapter/端点/credential vendor handler 都建了,唯独 models CHECK 白名单没同步(典型「只顾眼前不看关联产物」)。credential 是 api_key 类型不走 upstream_passthrough,endpoint 靠 adapter 硬编码,必须专属 family 路由,openai_chat 兜不住。
- **修法**(schema,授权内:补齐已注册能力+Owner 明确要+放宽白名单低风险,对齐 sensitive-modules 给能力非守门):迁移扩 CHECK = registrydefault 全部 MustRegister family;同步 internal/adminhttp/provider_catalog_mutation_handler.go 的 Go 白名单;迁移往返+国内厂 model 录入测试。派 codex。
- e2e 库已临时 ALTER 验证(models 过);正式迁移随 codex。
- **e2e 打通残留**:混元真转发卡 no_capacity(选号候选空,激活字段全对,codex seed 漏 model resolve→pool→account 链某配置),随 codex 修 seed 后我注入真 key 验证。

## 🏛️ 原 Owner-gated 六项裁定(2026-07-05,Owner 授权「查借鉴项目做法、选合适的」;12-agent 三镜调研+逐项对抗核证全过,证据全文 wf_79018afd-1de journal)

**裁定原则**:镜像市场验证做法优先;正确性兜底默认开、性能旋钮默认关(new-api/sub2api 两家一致的默认值哲学);能力给足默认开、保留 env 逃生开关;不 bolt-on 镜像没有的开关。

1. **C-1b 裁定=翻默认开**:quota reconciler(RunOnce=对账重放+孤儿清扫)属正确性兜底非性能旋钮——new-api 超时任务清扫 UPDATE_TASK 默认 true(common/init.go:147)、sub2api 并发槽清扫 DI 无条件启动(wire.go:239-248,默认 30s);HUAKAI 同 wiring 函数里 billing 侧三个同性质兜底(replayJanitor/leaseSweeper/pendingReconciler)全无条件启动,唯 quota 被 gate 是历史遗留。改 cmd/gateway/wiring.go:1210 语义反转(unset/非法值→开,显式 false→关)+wiring 判别测试(变异翻回关证红)+三个 compose/部署文档补变量。CLIProxyAPI 无计费无对应(五组关键词取证)。
2. **NT-1 裁定=最小堵漏(S2 自主修)**:notify ListActiveSettings(store.go:90-108)JOIN users 限 role='admin' AND deleted_at IS NULL——运维广播(provider_account_down/alert_firing)只达 admin;客户自身事件(低余额)单发路径不动;**不加**「发全体」开关(两镜都没有,加了是 bolt-on)。判别测试:普通 user 配 webhook 不收广播/admin 收,删 role 过滤证红。sub2api 独立运维收件人配置(recipients+min_severity+限流)排 roadmap 方案②。
3. **NT-2+MO-1 裁定=两执行器都翻默认开**:①HUAKAI_ALERTING_EVAL_ENABLED 默认 true(无规则时空转,LeaderLock 已防重复);②contentModerationRuntimeEnabled 默认 true(租户级 DefaultConfig Enabled=false 保证未配置租户放行,admin PUT Enabled=true 真生效——语义与 sub2api 一致:执行器恒接线+配置默认关)。**MO-1 必须与 MO-3(config TTL 缓存+fail-closed 时序)同批落地**,否则每请求裸查+查询抖动 403 波及未启用租户。配套:两 admin 面回显 executor_runtime_enabled + env 文档。
4. **PO-3 裁定=建 obs/dlq 管理面(自主,零 schema 零开关)**:GET /admin/v1/obs-dlq(dlq_events JOIN outbox_events,limit 100/200,platform_admin)+ POST /admin/v1/obs-dlq/{id}/replay(原子 UPDATE outbox status='failed_dead'→pending,0 行→409,镜像 sub2api RetryFulfillment 原子状态机);死信行保留作历史;openapi.yaml 同步+obs 死信深度并入 dlq_depth expvar+Hermes 只读源。前端页 Owner-gated 不建。
5. **S3-4 裁定=分级**:互斥机制零缺口不加新机制(行锁+Serializable+终态幂等已强于两镜)。第一级立即做:ReverseCost skip(预留非 settled)从完全静默改为 WARN+expvar/审计事件,先看发生频率。第二级(job_kind 扩 CHECK 迁移+冲减备忘重试)按数据决定是否做,依附 reconciler 开关不另设新 knob。
6. **C-2 裁定=不建自动补价 worker**:现有四层不动(预扣 fail-closed 严于三镜/ratio 冷启 fail-open→1.0 与 new-api 一致/流式 0 价+pending 落账优于 sub2api)。第一级自主:pending_reconciliation 未定稿行数进 admin worker-stats+expvar+部署文档。第二级 Manual-First:RepriceUsageRecord+admin dry-run 端点(usage_record_reconciliation_events 表字段现成零迁移),差额报告人工走既有 admin 调整,不自动动钱——排在第一级之后独立切片。

**落地批次**(等 codex②三域批收工避免同文件冲突):批A=C-1b+NT-1+PO-3+S3-4 一级+C-2 一级;批B=NT-2+MO-1(叠在 codex② 的 MO-3 之上);批C=C-2 二级 Manual-First reprice + S3-4 二级(视一级数据)。

## 🔬 剩余四域 §17 模块配合审计(2026-07-05,17-agent Workflow wf_769e95f6-c97;13 条发现全部核证存活 CONFIRMED/ADJUSTED,0 REFUTED;无 S0/S1,最高 S2)

**media(3 条,全 money 相关,Claude 亲核 MEDIA-1 成立)**——孤儿追扣子系统与 claim 状态机/sweeper 配合断裂:
- MEDIA-1【S2·钱】captureOrphanHold(store_orphan_backcharge.go:201)只 billing.Capture 扣余额,**不推进 claim 到 committed**(Capture 函数体亲核只动 balance_holds+user_balances 不碰 billing_ledger_claims);claim 停 reserving,~TaskTimeout+grace 后被 LeaseSweeper abort → 账本记 claim aborted+退款事件,与用户实际被扣矛盾。修:追扣 Capture 成功须同事务 UpdateClaimCommitted+写 claim_committed 事件,对齐正常 CompleteSuccess 路径。
- MEDIA-2【S3·钱】自超时 abort(store_money.go:191 正常路径)已提交上游的任务只 Release 退款不 persistOrphanTx → 上游成本无对账入口(swept 路径建线索、自超时不建,不一致)。修:ExpireTask 且 ProviderTaskID 非空时也建孤儿线索。
- MEDIA-3【S3·钱】swept 来源孤儿(hold 已被 sweeper released)追扣时 HoldCapturable 恒 false → 对真正代表上游漏扣的孤儿追回 0,admin 只能 mark ignored。与 MEDIA-1 同根(追扣与 sweeper 释放时序错配),随 MEDIA-1 修复方向一并考量。

**frontend(3 条,Claude 亲核 FE-1 成立)**:
- FE-1【S2】tokenForPath.ts:49 admin 路径只用手贴 adminToken、不 fallback session token;后端 adminsessionauth.Resolver 明确支持 session admin 通道(role=admin→platform admin)→「登录即管理员」在 UI 失效,session 登录的 admin 访问运营台恒 401。修:admin 路径 adminToken 空时 fallback sessionToken。纯接线 bug,非页面设计,自主。
- FE-2【S2】多标签页 token 轮换后内存快照不跨标签同步 → refresh family 重放 → 所有标签强制登出。修:BroadcastChannel/storage 事件跨标签同步 setSessionTokens。
- FE-3【S3】裸 fetch 下载器(audit/usagerecords api.ts)绕过 maybeProactiveRefresh → 临近 session 到期误 401。修:下载前先 ensureFreshSession。

**mimicry-egress(4 条)**:
- ME-1【S2】sidecar 就绪探测在全局锁内做网络 I/O 且失败不缓存(transport/factory.go:287-344)→ sidecar 挂起时并发 mimicry 出口串行化(锁 convoy)。修:探测移出锁/失败负缓存。
- ME-2【S2】基础设施故障(sidecar 不可用)被误记 per-account SignalChannelError(chat_completions_error.go:204)→ sidecar 抖动引发全池 5min 冷却+failover 甩打。修:区分 infra 故障 vs account 故障,infra 不发 per-account 冷却。
- ME-3【S3】自定义上游端点+绑定出口代理=每请求恒 fail-closed 无前置校验(passthrough_endpoint_guard.go:166),叠加 ME-2 反把账号冷却。
- ME-4【S3】Go-native uTLS 回退腿只认 env force-h1、不继承 config SidecarForceH1(factory.go:332)→ env 关 force-h1 时回退腿广告接不住的 h2。

**hermes(3 条,全 S3,非钱非安全)**:
- HERMES-IP-01 propose 开关与 mutating 总开关解耦(wiring.go:897)→ PROPOSE=on+MUTATING=off 时 operator 确认必 403 死胡同。
- HERMES-IP-02 confirmCache 进程内单例(hermesconfirm/cache.go:39)→ 多副本部署 propose→confirm 跨副本必失败,无 re-dry-run 逃生阀。
- HERMES-IP-03 已投递 dlq 记录的 confirm 幂等正确但回泛化 503 掩盖真因(hermesops/tools_mutating_dlq_renew.go:78)。

**落地批次**:批D=frontend(FE-1/2/3,独立)+mimicry(ME-1~4);批E=media(3 条,money,等 codex⑦ 补价让出 internal/billing 后派,Claude 重点验收+对抗审查);批F=hermes 3×S3(fix-in-place)。

## 🔬 §17 全链路端到端并发压测(2026-07-05,账号并发槽,提交 572d1c87)
- ✅ 新增 e2e_concurrency 压测:账号 cap_concurrency=3、8 并发,验证在途峰值严格≤3、成功=3、超额 5 个 429/queue_wait 干净拒绝、槽全释放无泄漏。mock 上游加可选延迟制造重叠。
- ✅ **挖修真实健壮性缺陷**:Reserve 有 40001 重试(retryReserve)但 Settle/Abort 主路径无→并发结算冲突 abort_failed/settle_failed 噪音(不亏钱,hold_released=1,靠 DLQ/lease 兜底)。修=Settle/Abort 加 retryTx2 对齐 Reserve;对抗审查零 S0/S1(幂等=Serializable 全回滚+status 守卫)。
- ⚠️ **Owner 决策项(产品语义,已挡住 codex 擅改)**:queue_wait(账号全满)当前是 **retryable 本地失败**——同一 HTTP 请求内会内部重试一轮(每 attempt 一条 claim_aborted 审计),最终 429+Retry-After。codex 曾擅自把它改成 terminal(直接 429 不内部重试),被 Claude 回退(超范围+改产品 failover/限流语义,风险涉 model-fallback 交互)。**是否把 queue_wait 内部无间隔重试改成 terminal 429**(理由:全满时立即重试撞同一窗口是噪音,Retry-After 让客户端延迟重试更合理;风险:需确认不影响 RetryableEndClasses 白名单里 model-fallback 的换号)=Owner 拍板的产品决策,未自主改。
- 剩:per-key RPM / 用户级并发的全链路压测(组件层已覆盖,全链路只做了最核心的账号槽);其它 §17 子系统配合审计(pool failover↔健康回流 / auth 采集流状态机 / credential 物化↔转发)可续。

## 🔬 §17 剩余核心子系统配合审计(2026-07-05,10-agent Workflow wf_fb124cef;7 条全核证存活 0 REFUTED,无 S0/S1,最高 S2)
**pool-failover(选号↔健康回流↔failover)**:
- PF-01【S2·钱 CONFIRMED】流式路径漏即时账号冷却:buffered 路径(dispatch.go:702)对 429/5xx 调 forceCooldownFromUpstreamRateLimit 立即 park 到 Retry-After,但流式(stream.go:222 classifyStreamingUpstreamFailure / :290 forwardSSEAndSettle)只 recordChannelHealthSignal(FSM 需≥10样本才冷却)→被限流账号跨请求反复重打、无视 Retry-After、损耗付费账号资产。同缺陷两路径行为相反。两镜(new-api relay.go:221 processChannelError / CLIProxyAPI 流式非流式共用冷却)都一视同仁。修=流式路径补即时冷却回流对齐 buffered。
- PF-02【S2 CONFIRMED】client 4xx(400/413/422)记 SignalChannelError 计入账号健康失败率→误杀健康账号:producer(error_normalize.go:82)从不产出 consumer 已备的豁免类 SignalClientMalformed(全仓零发射点),client 坏输入(context length exceeded 等在任何账号都失败)反复打同一 sticky 账号→errorRateDecision 冷却健康账号拖垮池容量。核证补:signal_classifier.go:72-73 同款缺陷。两镜都不把 client 4xx 计入渠道健康。修=client-caused 4xx 归 SignalClientMalformed 不计健康。

**credential 物化↔转发**:
- F-1【S2→鲁棒性 CONFIRMED(Claude 亲核降级)】Bedrock aws_region 直拼上游 host 无白名单校验(passthrough.go:94 region 取自 Credential.Extra=运营者账号配置,**非客户可控**,故非客户 SSRF;但配错/污染可拼坏 host,aws_sigv4 endpoint 绕过 dispatcher 统一 SSRF 守卫)。修=加 region 白名单校验对齐 vertex 的 validVertexLocation。
- F-2【S3 ADJUSTED→**修复方案否决,维持现状**】legacy 回落物化不回填 AccountCredentialID/CredentialVersion→channel-health 对未迁移账号盲(核证:死号降级权威非 channel-health FSM,危害降级)。**2026-07-05 验收裁定:codex 的"回填 provider_accounts.id"方案被 Claude 亲核否决并回退**——channel_health_state 建表(0022)对 account_credential_id 有强外键 REFERENCES account_credentials(id),且 uq_channel_health_credential_version(account_credential_id, credential_version) 是**不带 tenant 的全局唯一索引**;借用 provider_accounts.id 回填,该 id 在 account_credentials 不存在时健康落库外键违约,恰好存在时**串写其他凭据(甚至跨租户)的健康行**,把 S3 盲区变 S2 污染。禁 schema 约束下无合法回填值(任何值要么违约要么撞真实凭据)。正解=legacy 账号补建 v2 account_credentials 行(数据迁移)或放宽外键,均触 schema=**Owner-gated**;现状(healthKeyOK=false 健康回流不落)已在 postgres_vault.go 注释说明。
- F-3【S3 CONFIRMED】google_sa 物化 Value 空无转发 adapter(核证:v2 credentialstore vertex_sa 有正常路径,legacy 路径缺)。**2026-07-05 修:mapServiceAccount 改明确 ErrCredentialFormat fail-closed(亲核:全仓生产代码零消费 Extra["auth_kind"]/google_sa,旧路径产出的空 Value 凭据本无人能转发,非砍活功能)。**

**auth 采集流状态机(上游账号凭据采集,非用户登录 auth-core)**:
- ACF-1【S2 CONFIRMED→**已修 2026-07-05**】OAuth 回调无逐 flow 串行化:并发双回调把已 validated 的 flow 覆写成 failed(核证更宽:晚到回调的 callback_received 写 oauth.go:160 就已覆写)→有效凭据采集丢失/活凭据孤儿。修=session_store 新增 UpdateStatusFrom(UPDATE … WHERE status = ANY(前置合法态) 的 CAS),CompleteOAuthCallback 每步状态写限定前置态+入口 replay 守卫,晚到/并发回调在 exchange 前得 ErrFlowReplay,validated 不可被覆写。判别测试=真实 PG 通道门控并发(第一个回调停在 exchange 内时第二个到达);Claude 亲自变异抽查(删守卫+CAS 改无条件→红)复验。
- ACF-2【S3 ADJUSTED→**已修 2026-07-05**】Create 成功但 MarkFinalized 失败→活凭据孤儿(核证:"重复凭据"不成立,uq_account_credentials_active 唯一约束防住;危害降级为孤儿元数据)。修=类型化 CredentialCreatedFinalizeError 携带 flow_id/credential_id/tenant/vendor/auth_mode 非密钥对账元数据,Unwrap 保留错误链,handler 只记日志也有对账线索。

**落地(2026-07-05 全部收官)**:批G=pool-failover(PF-01 money+PF-02,已合 8bf1b692+8f7a1ee6);批H=credential(F-1 白名单+F-3 fail-closed 已修 dc26b948,F-2 方案否决维持现状待 Owner);批I=auth(ACF-1 CAS 串行化+ACF-2 孤儿透出,已修)。§17 剩余子系统审计 7 条全部处置完毕:5 修 1 否决待 Owner(F-2)1 无需改(PF-02 死代码路径回退)。
