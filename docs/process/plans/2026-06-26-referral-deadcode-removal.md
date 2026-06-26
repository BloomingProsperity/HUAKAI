# referral_reward 死代码裁决(community/invitation 平行实现)— 删除计划

日期:2026-06-26 · 作者:Claude(Owner「解锁所有」+ Ultracode 后)· 状态:计划→执行(money 相邻删除,CLAUDE.md #9)

## 背景(已 grep 核真码)

referral 奖励在仓里有**两套平行实现**:

- **活路径(payment 包)**:`payment.PostgresStore.ApplyReferralReward`(referral_reward.go:67),被
  `audit/receipt_worker.go:434` 经接口 `referralRewardIssuer` 真调。这是生产在用的奖励发放路径,**不动**。
- **活路径(community/invitation 包,仅 qualify 半边)**:`Service.QualifyPendingReferral`
  (referral_qualification.go:31),被 `audit/receipt_worker.go:415` 经接口 `referralQualifier` 真调,
  只做 `UPDATE referrals SET status='qualified'`——纯 qualify、不发奖励。**保留**。
- **死路径(community/invitation 包,reward 半边)**:`referral_reward_store.go`(25 unreachable func)+
  `referral_reward_config.go`(9 unreachable func),共 36 条进 deadcode-baseline。这是一套**自写
  payment_orders / payment_credits / 余额 credit** 的奖励发放实现,**全仓零生产消费**(已 grep 证:
  仅死文件互引 + 测试 fake 自引)。

## 为何删(风险定性)

这套死 reward 代码是**休眠的双花地雷**:它与活路径(payment.ApplyReferralReward)对同一 referral 做奖励,
但走自己的 payment_orders/payment_credits 写入。一旦将来有人误把 `qualifyPendingReferralWithReward`
接进 receipt_worker(而非现有的 qualify + payment.ApplyReferralReward 两步),同一 referral 会被**两条路
各 credit 一次** = 真金双花。删掉它消除这个隐患,且缩 deadcode baseline 36 条。

**关键:删除零行为变更**——这 36 个 func 当前 unreachable(deadcode 工具证),删它们对任何活路径无影响。

## 精确手术(blast radius:community/invitation 包内 3 文件 + 0 个包外文件)

1. **删** `internal/community/invitation/referral_reward_store.go`(整文件,17KB,25 dead func)。
2. **删** `internal/community/invitation/referral_reward_config.go`(整文件,3.9KB,9 dead func)。
3. **改活文件** `referral_qualification.go`:移除仅被上述死文件引用的孤儿类型
   `qualifyReferralInput` / `referralTierThresholds` / `referralRewardConfig`(活的 QualifyPendingReferral
   不用它们,已核)。保留 `referralQualificationStore` 接口 + `Service.QualifyPendingReferral` +
   `PostgresStore.qualifyPendingReferral`(活)。
4. **改测试** `referral_qualification_test.go`:删 fake 的 `qualifyPendingReferralWithReward` 方法
   (引用死类型)+ `TestQualifyPendingReferralRemainsQualifyOnly`(其变异守卫目标=死 reward 路径,删后
   该 mutation 结构上不可能,且活的 qualify 行为仍由 TestQualifyPendingReferralQualifiesPending 等覆盖)+
   随之孤儿的测试本地类型/字段(qualificationReward/qualificationRewardAudit/qualificationTierProgress +
   qualificationStore 的 reward* 字段)。保留活 qualify 测试 + ReferralSummary 测试 + 迁移契约测试。

## 验证(穷尽)

- `go build ./...` + `go vet`(改动包)绿。
- **全后端 `go test ./...`** 绿——尤其 `internal/audit`(受 receipt_worker 影响)+ `internal/community/invitation`。
- `internal/payment` 集成测试(活奖励路径)绿——证活路径零影响。
- deadcode baseline 缩 ~36 条(community/invitation referral_reward 全消失);quality-gate PASS,DC_MAX 同步降。
- **对抗审查**(CLAUDE.md #8/#7 的 ultracode 替代):money 相邻,重点核活的两条路径(QualifyPendingReferral
  + payment.ApplyReferralReward)零受损 + 删除自洽无悬空引用。

## 不在本切片(留 Owner / 后续)

- **「翻开关真启用 referral 奖励」**(env 默认关→开)= money default-flip,CLAUDE.md #2 Owner-gated,本切片不碰。
- receipt_worker `applyReferralRewardIfEnabled` 的静默吞错补可观测 = 独立 follow-up(scoping 提的),
  不混入本删除切片(保持纯减法 + 易审)。

## 决策点(surface Owner)

- 删 vs build-tag 隔离:选**删**(彻底消除地雷;build-tag 隔离留孤儿类型且地雷仍在仓)。若 Owner 偏好保留
  代码(将来想接这套而非 payment 那套),说一声改 build-tag 隔离。
- 此为 money 相邻删除,虽零行为变更,仍 surface 此计划;Owner 不反对即按上执行 + 对抗审查后合并。
