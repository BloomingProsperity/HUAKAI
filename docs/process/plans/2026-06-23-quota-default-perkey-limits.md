# #1 修复:新建 API key 种保守默认配额(RPM+并发),堵"单 key 烧池子"

- 日期:2026-06-23
- 分支:`fix/quota-default-perkey-limits`(off `feat/frontend-portal` @ 9be79d65)
- 决策来源:当时按部署者直属用户售卖 API 额度的场景确认该 day-1 blocker;当前身份边界已更新为三身份、单层租户，但所有用户 Key 均需默认限额的安全结论不变。Owner「开始」绿灯 + 认可默认值(并发 5 / RPM 60)
- 关联:[[business-model-relay-saas-decision]]、[[owner-prefers-operator-switches]]

## 一、问题(对外卖额度的真 day-1 blocker)

relay 入站只按 client IP 限流;真正的 per-key/per-user 限流在 quota 子系统,但**出厂没种任何默认策略**→
新自助注册用户的 key 默认 0 限制,唯一硬约束是钱包余额 → **单 key 能在烧完余额前并发猛刷最贵模型,
打爆上游账号池(429/封号)、抢占其他付费用户容量**,多 IP 即绕过每 IP 兜底。

## 二、三镜对照(#16,clean-room,只引生产行为)

| | sub2api @e34ad2b1 | new-api @1ac0f580 | CLIProxyAPI @2a050dc9 | HUAKAI 现状 / delta |
|---|---|---|---|---|
| 频次(RPM) | 三层(用户×组/组/用户),**默认 0=不限** | 仅用户级,**总开关默认关** | 无(个人单用户 relay,无等价物) | quota 有 MetricRequests,但**无默认策略** |
| 并发 | 用户级**默认 5**、账号级默认 3 | 无 | 无 | quota 有 MetricConcurrency,**无默认** |
| TPM | 无(用 USD 花费窗口) | 无 | 无 | quota 有 MetricTokensEstimated(两镜都无) |
| 新用户默认 | 频次默认不限,唯并发=5 兜底 | 频次默认全开,纯靠钱 | 无 | 无任何默认 |

**两镜共识 = "频次默认 unlimited,只靠钱兜底"——正是 HUAKAI 同款洞;两镜都明确建议反着设默认。**
sub2 唯一出厂兜底是并发=5,挡得住"开几百路并发"但挡不住"单 key 持续高 RPM"。

**HUAKAI 升级 delta(生态)**:quota 子系统比两镜都全(请求数/估算token/并发/花费四维 + Enforce/Observe/
ManualFirst 三模式 + 真窗口预留),机器都现成,唯一洞=没种默认。修=**给新 key 种保守默认(出厂即保护),
正是两镜都缺的**;默认值做成运维开关(贴 Owner 偏好)。

## 三、修法(复用现成机器,无 schema)

亲核真码定关键事实:
- relay 转发前真调 `QuotaReserver.Reserve`(`gatewayhttp/chat_completions_dispatch.go:522`、
  `completionshttp/billing.go:66`)→ evaluate api_key scope 策略 → 超限 deny。**入口真拦,机器现成。**
- api_key scope 的 `scope_id = strconv.FormatInt(apiKeyID, 10)`(`quotaenforce/settler.go:89`),
  与现成 per-key 策略写口 `userkeycontrols/key_control_service.go:75` 完全一致 → 种的默认策略必命中。
- `quota_windows.policy_id` FK `REFERENCES quota_policies(tenant_id,id)`(0070:93)→ **必须真策略行**
  (合成内存策略对 RPM 窗口类 FK 失败),故走"种真行",非 ResolvePolicies 合成。
- 现成 `UpsertAPIKeyQuotaPolicy`(`sql/queries/userkey_controls.sql`)即 per-key 策略写口,直接复用。

**实现**:
1. **config 运维开关**(默认值内置):`HUAKAI_QUOTA_DEFAULT_KEY_LIMITS_ENABLED`(默认 true)、
   `HUAKAI_QUOTA_DEFAULT_KEY_RPM`(默认 60)、`HUAKAI_QUOTA_DEFAULT_KEY_CONCURRENCY`(默认 5)。
2. **seeder**:给定 (tenantID, apiKeyID) 种两条 Enforce 策略——MetricRequests(window=每分钟,limit=RPM)+
   MetricConcurrency(windowless,limit=并发),scope_kind='api_key',scope_id=FormatInt(keyID),复用 UpsertAPIKeyQuotaPolicy。
3. **接线**:用户自助建 key(`userkey/userkey.go:278` INSERT api_keys 拿到 keyID 后)**同事务**种默认策略,
   key 与其默认策略原子落库(无未保护窗口)。**仅自助 key 种默认;admin key 是运营自己的、保持不限运营可控。**
4. 已存在的 key(上线前无真客户 key):一次性 admin backfill 作非阻塞 follow-up,不进本切片。

## 四、money/security-safety

- 保守默认、出厂即保护;运维可上调(开关)→ 既堵滥用又不误伤正常用户(60 RPM≈1 req/s)。
- 不改钱路、不改 schema、不动 Reserve 执行逻辑(只喂数据);非碰撞包(userkey/userkeycontrols/config)。
- default-behavior flip(新 key 从"不限"变"默认限")属 Owner-gated → 已 surface,Owner「开始」绿灯。

## 五、测试(变异证 RED,-count=1;integration_pg 真库)

- **单元**:seeder 产出策略参数正确(scope_id=FormatInt(keyID)、metric/window/limit/mode=Enforce);开关关→不种。
- **integration_pg**(真库):自助建 key → quota_policies 真出现两条默认 Enforce 行(RPM+并发,scope_id 对)→
  对该 key 构造 ReserveRequest:第 RPM+1 次请求/分钟被 deny、第 并发+1 路被 deny;**判别对照**:开关关时建 key
  无默认策略、Reserve 全 allow。变异:把 seeder 接线删掉 → 默认策略不出现 → enforce 断言 RED。
- 接线 chokepoint 测试:确认自助建 key 路径真调 seeder(admin 路径不调)。

## 六、碰撞与协调

userkey / userkeycontrols / config 均非碰撞包(碰撞面=pool/registry/proxy/channel/gateway*/rate/admin/gatewayhttp/tlsfp)。
不动 quota 子系统执行逻辑、不动 relay 转发。claim 锁覆盖改动文件。
