# W4 强制账本引用与完整性 —— 实施 spec

> 补救波 W4。源:`docs/process/plans/2026-05-22-audit-remediation-wave.md` 第 54 行。
> 覆盖 6 个发现:GW-07(Zone A)、B-12 / B-13 / B-15(Zone B #12/#13/#15)、
> C-13 / C-14(Zone C #13/#14)。**信任链核心敏感波。**
> Owner 决策(2026-05-22)已定:账本写失败 = **启动 fail-closed + 运行时
> durable-DLQ**(见项目记忆 `project_trust_ledger_failclosed_policy`)。
>
> **rev1(2026-05-22)**:codex 交叉评审 REJECT,7 条 must-fix 全部核实属实
> (B-15 原 spec 有事实错误)。本版重写:DLQ 改存「待写意图」非「已签名条目」
> + 补 DLQ 基建与重放 worker;补 direct-settle / cache-hit 旁路;补
> `AuditLedgerResult` 数据流;C-13 改 trailer;修正 B-15;切片改 W4b→W4a→W4c。
>
> **rev2(2026-05-22)**:codex 复审 rev1 仍 REJECT(7→3,收敛)。本版补最后 3 条:
> (1) `AuditLedgerResult` 改显式三态 `Persisted/Deferred/Disabled` 消解不变式
> 矛盾;(2) 新增 `PrepareEntry` 预备步,脱敏从 `Append` 内部抽出,DLQ intent
> 有确定来源;(3) DLQ envelope 字段定死 + 「Append 失败且 DLQ 也失败」fail-closed
> 地板。另:line-ref 修正、DLQRef 用独立 trailer。
>
> **rev3(2026-05-22)**:codex 复审 rev2 = APPROVE-WITH-CHANGES,可进入实现。
> 本版折入 3 条非阻断小修:(1) `PrepareEntry` error 语义写明 —— 脱敏失败但
> 哨兵替换成功仍返回 `(PreparedEntry, nil)`,只有无法构造安全 prepared intent
> 才返 error;(2) §9 grep 清单加 `AppendInTransaction`/`AppendInTx` 低层事务
> 调用面;(3) `audit_ledger_entry` 的 DLQ `ReplicaStatus` 定为 `ReplicaStatusNone`
> (它是主写意图,非副本)。另:`DLQRef` 格式定为 `audit_ledger_dlq:<id>`。
>
> **rev4(2026-05-22)**:codex per-commit 复审 rev3 报一条 P2 —— signer 轮换下
> Deferred 条目不能预先钉死 fingerprint。本版修:`PreparedEntry` **不带**
> fingerprint;fingerprint 由 `Append` 在签名那一刻按 active signer 注入(DLQ
> 重放同理);`Deferred` 三态 `Fingerprint==""`;completion 事件不为 Deferred
> 宣告 `AuditSignatureFingerprint`;「有效账本引用」校验对 DLQRef 分支不查
> fingerprint。
>
> **rev5(2026-05-22)**:切片归属精修 —— §6.0.a 的 `PrepareEntry` 抽取归 W4a
> (与 §3、§11 一致);W4b 只就地修 B-13/B-15、并把哨兵构造写成 `auditledger`
> 内可复用 helper 供 W4a 抽取时复用,W4b **不改 `Append` 签名**。无设计变更。
>
> **rev6(2026-05-22)**:codex per-commit 复审 W4b 实现报一条 P2 —— B-13 把
> `sanitizeLedgerEntry` 返回的**任何 err** 都当脱敏失败写哨兵,但 redactor 做
> 字段级脱敏成功时也会返回 `ErrUnsafePayload`(且 entry 已是已脱敏态)。本版
> §5 B-13 精修:只有 redactor **产不出可用脱敏 payload**(`len(raw)==0` /
> 脱敏 bytes unmarshal 坏)才是真失败 → 哨兵;字段级脱敏 → 照写已脱敏 entry。
>
> **rev7(2026-05-22)**:codex per-commit 复审 B-13 修复版又报一条 P2 —— B-15
> 让 `scanLedgerEntry` 遇损坏即报错后,公开 verify 端点对**别租户**的损坏行
> 会在 tenant 归属校验前抛 `ledger_corrupt`/500,从「500 vs 404」差别泄露他人
> request_id 存在。本版 §5 B-15 补:损坏只在归属校验通过后才暴露 ——
> `scanLedgerEntry` 遇 corrupt 仍回带可靠 `TenantID` 的 entry,
> `GetByRequestIDAndTenantScope` 先查租户再决定 500 / 404。
>
> **rev8 / rev9(2026-05-22)**:rev8 修 `PrepareEntry` 的 `TenantID==0` 精度
> (`0` 是合法「无租户」语义,系统级审计本就无租户)。rev9 应 codex per-commit
> 复审 W4a-1 —— `PreparedEntry` 字段改为**不可导出(sealed)**,只能由
> `PrepareEntry` 产出,杜绝外部包直接构造未脱敏 entry 绕过隐私边界
> (`Append` 已不再脱敏);W4a-2 的 DLQ 序列化因此改用自定义 JSON marshal。

## 1. 背景与核心风险

审计/信任账本是 HUAKAI「商家不能做假」的命根子:每笔请求都要有一条**签名的
账本条目**证明它真实发生过。W4 修六个让信任链可被静默绕过 / 被污染 / 被误读
的口子:

- 账本/签名器没配 → 请求照样计费交付,无签名条目(GW-07 / C-14)。
- 账本 append 失败 → 只 warning,不补救(C-14)。
- money-path settle 不要求账本引用 → 计费可在无账本时发生(B-12)。
- 脱敏失败被忽略 → 未净化链路数据被永久写进 append-only 账本(B-13)。
- 读取时吞掉 JSON / Merkle 结构错误 → 损坏行当正常条目返回(B-15)。
- 流式账本用首字节时间当完成时间 → 签名记录与真实交付窗口不符(C-13)。

## 2. 关键架构事实(rev1 修正,实现前必读)

- **账本是 Merkle 链**:每条持久化行的 `prev_merkle_root` / `merkle_root` 都是
  **恰好 32 字节**(`sql/migrations/0013_trust_chain_audit_ledger.up.sql:27-28`
  有 `CHECK octet_length = 32`)。一条条目的 root 依赖**上一条**的 root + 本条
  hash,只有在 `Append` 写入那一刻、相对当时链头才能算出。
- **推论**:**不可能离线预先算好一条「最终签名条目」再塞队列**。`Append`
  (`auditledger/postgres.go:108` `AppendInTransaction`)内部做:取 advisory lock
  → 算 `nextLedgerID` → 读链头 root → 算本条 root → 签名 → 写。失败时调用方
  根本拿不到「最终条目」。
- 因此 **DLQ 必须存「待写意图」(append intent),由 worker 重放完整 `Append`**。
- 签名公钥 fingerprint(`Signer.Fingerprint()`)标识哪把 key,但 signer **会
  轮换**(`LoadPublicKeysFromEnv` 支持 rotated key)。一条 entry 的 fingerprint
  只有在 `Append` **真正签名那一刻**才确定 —— 持久化(`Persisted`)条目用
  append 时刻的 active signer,DLQ 重放(`Deferred`→持久化)用重放时刻的
  active signer。**推论(rev4 修正)**:`Deferred` 条目在持久化前**没有
  fingerprint**;completion 事件对 `Deferred` 只携带 `AuditLedgerDLQRef`
  作待补凭证,**不**宣告 `AuditSignatureFingerprint`(详见 §6.1 / §7)。

## 3. 切分(小切片闭合纪律)—— rev1 重排顺序

codex 评审指出:W4a 的 DLQ intent 必须已脱敏,否则未净化链路数据会被固化进
DLQ。故顺序改为 **W4b 先,W4a 次,W4c 末**:

- **W4b — auditledger 存储完整性**(B-13 / B-15)。先把脱敏 fail-safe 与读取
  损坏检测做对,W4a 的 DLQ intent 才能安全复用脱敏结果。全落 `auditledger` 包。
- **W4a — 账本写入路径 fail-closed + DLQ 基建 + 重放 worker + 流式完成时间**
  (GW-07 / C-13 / C-14)。
- **W4c — completion 事件强制账本引用,堵所有 settle 旁路**(B-12)。

合计 ~3.5 天(rev1 比原估多 0.5 天:DLQ 基建与 worker)。

## 4. 核心决策 —— 账本写失败 = 启动 fail-closed + 运行时 durable-DLQ

Owner 2026-05-22 已定。展开:

- **production 判定**:`HUAKAI_RELEASE_MODE=production`(现有
  `cmd/gateway/config.go:53-55` 已这样判);非 production(dev/test)豁免。
- **启动期**:production 模式下断言 `AuditLedger != nil && Signer != nil &&
  !isNoopLedger(AuditLedger)`,否则 `cmd/gateway` 启动直接失败并打印原因。
- **运行时**:某次 `AuditLedger.Append()` 失败 → 把**已脱敏的 append intent**
  投 durable DLQ;**请求放行**;worker 事后重放 `Append` 补写。
- 不阻断请求、不返 503(可用性);信任链靠「intent 一定在 DLQ + worker 一定
  重放」保证不断。

## 5. W4b 逐发现 spec(先做)

### B-13 — 脱敏失败被忽略,污染 append-only 账本(S1)

- **严重度说明(rev1 补)**:原审计标 MED;W4 升 S1,因为未净化的隐私链路
  数据一旦写进 **append-only** 账本就**永久不可删除/不可逆**,是不可挽回的
  隐私泄露,符合 S1(真实泄露)定义。
- 证据:`auditledger/privacy.go:17-37` `sanitizeLedgerEntry` 在 redactor 失败 /
  `len(raw)==0` 时返回**原始 entry** + err;`auditledger/postgres.go:113`
  `entry, _ = sanitizeLedgerEntry(...)` 丢弃 err;Memory append 在
  `auditledger/ledger.go:97` 同样 `entry, _ = sanitizeLedgerEntry(...)`。
- 修复:**脱敏产不出可用 payload → 写「脱敏丢弃哨兵」;字段级脱敏成功 →
  照常写已脱敏 entry**。
  - **rev6 关键澄清(codex per-commit 复审 P2)**:`sanitizeLedgerEntry` 返回
    err **不等于**脱敏失败。`privacy` default redactor 剥掉违禁字段后会返回
    「**已脱敏的 bytes** + `privacy.ErrUnsafePayload`」—— 这是脱敏**成功**
    (redactor 正常工作),`sanitizeLedgerEntry` 已把已脱敏内容套进 entry。
    只有 redactor **产不出任何可用脱敏 payload** 才是真失败:`len(raw)==0`、
    或脱敏 bytes `json.Unmarshal` 坏 —— 这两种 `sanitizeLedgerEntry` 返回的是
    **原始未脱敏 entry**。把「任何 err → 哨兵」会让每次正常字段脱敏都丢掉
    整条审计链路,违背 F-AUDIT 透明卖点。
  - `sanitizeLedgerEntry` 在上述两个「真失败」分支返回一个**可识别 sentinel
    error**(如 `ErrLedgerSanitizeUnusable`,wrap 原因);字段级脱敏 / 干净两
    种情况返回 `ErrUnsafePayload` / nil,**不**是该 sentinel。
  - `postgres.go` / `ledger.go` 不得再 `_ =` 丢弃 sanitize err。改为:
    `errors.Is(err, ErrLedgerSanitizeUnusable)` → 写哨兵;否则(含
    `ErrUnsafePayload`)→ 用 `sanitizeLedgerEntry` 返回的**已脱敏 entry**。
  - 写哨兵:把 entry 的 `HopChain` 换成**单条哨兵 `HopAttestation`**(可识别、
    内容「redaction_dropped」的合成 hop),`ModelChain` 置 `nil`,
    `TenantScopeRef` 置空。哨兵在 `hop_chain` jsonb 列内(已有列,**无 schema
    变更**),进 `EntryHash` 的 canonical 形式 → **哨兵被签名覆盖**,不可伪造;
    读回也能识别。
  - **顺序硬要求**:哨兵替换必须发生在 `EntryHash` **之前**,签名才覆盖它。
  - **已知难点**:`proto.HopAttestation` 须能表达该哨兵 —— W4b 已实证可承载
    (`SchemaVersion`/`HopKind`/`Actor`/`DecisionRef` 组合,无 schema 变更)。
- 风险测试(判别性 + mutation 自检,**两条路径都要测**):
  1. **真失败**:注入一个对特定敏感 payload 返回 `(nil, err)` 的 fake redactor,
     entry 的 HopChain 含敏感 marker → 断言写进账本的 entry:HopChain 是哨兵、
     不含 marker、ModelChain 为 nil、TenantScopeRef 空、条目仍有有效签名且
     签名覆盖哨兵。mutation:恢复 `entry, _ =` → 账本含 marker → 变红。
  2. **字段级脱敏(rev6 新增)**:注入一个返回「已剥违禁字段的 bytes +
     `privacy.ErrUnsafePayload`」的 fake redactor → 断言写进账本的 entry 是
     **已脱敏的真链路**(HopKind **不是** `redaction_dropped`、违禁字段已剥、
     其余 hop 结构保留)。mutation:把调用方改回「`err != nil` 一律哨兵」→
     此条变红(证明字段级脱敏不误触哨兵、不丢审计证据)。

### B-15 — 读取吞掉 JSON / Merkle 结构错误(S2)

- 证据:`auditledger/postgres.go:380-384` `scanLedgerEntry` 对 `hop_chain` /
  `model_chain` 的 `json.Unmarshal` 用 `_ =` 丢弃错误;root 长度处理同段。
- **B-15 修正(rev1 —— 原 spec 此处有事实错误)**:持久化行的
  `prev_merkle_root` / `merkle_root` **永远是 32 字节**(DB CHECK 强制,见 §2)。
  原 spec 写「长度 0 合法(首条目)」是**错的** —— 首条目的 prev 是 **32 个
  零字节**,不是长度 0。结论:**`scanLedgerEntry` 扫描出的任何 root,长度
  非 32 即为损坏。** 长度 0 是损坏。(「无 prev」只存在于 `readLatestMerkleRoot()`
  查不到行的内存态,不是持久化行。)
- 修复:
  - `scanLedgerEntry`:`hop_chain` / `model_chain` 的 `json.Unmarshal` 返回 error
    → 整个 scan 返回新 sentinel error `ErrLedgerEntryCorrupt`。
  - `prev_merkle_root` 或 `merkle_root` 长度 != 32 → 返回 `ErrLedgerEntryCorrupt`。
  - **scanLedgerEntry 遇 corrupt 仍回带 `TenantID` 的 entry(rev7)**:`tenant_id`
    是 bigint 列,在 `hop_chain`/`model_chain`/root 损坏点**之前**就由 `row.Scan`
    扫出。corrupt 分支不再返回空 `LedgerEntry{}`,而是返回一个**只带可靠扫出的
    标量字段(`TenantID` 等)、不带半解析 JSON 内容**的 entry —— 供下游做归属
    校验。
  - verify handler(`gatewayhttp/audit_verify_handler.go`)把 `ErrLedgerEntryCorrupt`
    映射为 HTTP 500 + 稳定 audit JSON code `ledger_corrupt`,经该 handler 自己的
    `writeAuditJSONError()`(**不进 clienterr 目录** —— verify handler 用独立的
    audit JSON 错误体),并打 ERROR 日志。
  - **跨租户泄露防护(rev7 —— codex per-commit 复审 P2)**:公开 verify 端点的
    `GetByRequestIDAndTenantScope` 是「按 request_id 取行 → 比对 tenant scope」。
    scan 改成遇损坏即报错后,若被探测的 request_id 属于**别的租户**且那行恰好
    损坏,corrupt error 会在 tenant 比对**之前**逃出 → 返回 500 `ledger_corrupt`
    而非 404,从「500 vs 404」差别泄露他人 request_id 存在。修复:
    `GetByRequestIDAndTenantScope` 在 `GetByRequestID` 返回 `ErrLedgerEntryCorrupt`
    时,**先**用 corrupt entry 携带的 `TenantID` 跑 `tenantScopeMatches`:不属于
    调用方 → 返回 `ErrLedgerEntryNotFound`(404,不泄露);属于调用方 → 才放行
    `ErrLedgerEntryCorrupt`(500,owner 能看到自己条目损坏)。即「损坏只在归属
    校验通过后才暴露」。
- 风险测试(判别性):
  1. `hop_chain` 是坏 JSON 的行 → `scanLedgerEntry` 返回 `ErrLedgerEntryCorrupt`,
     不是 HopChain 为空的「正常」entry。
  2. `merkle_root` 长度 16 的行 → 返回 `ErrLedgerEntryCorrupt`。
  3. 正常 32 字节 root 的行 → 不报 corrupt。
  4. **跨租户损坏行(rev7)**:一行损坏、其 `tenant_id` 属租户 A;以租户 B 的
     `tenant_scope_ref` 调 `GetByRequestIDAndTenantScope` → 得
     `ErrLedgerEntryNotFound`(不是 corrupt);以租户 A 自己的 scope 调 → 得
     `ErrLedgerEntryCorrupt`。
  mutation:测试 1/2 —— 恢复 `_ =` 吞错误 → 变红;测试 4 —— 把「corrupt 时先
  查 tenant」改回「corrupt 直接抛」→ 跨租户那条变红。

## 6. W4a 逐发现 spec

### 6.0 Prepare 预备步 + DLQ 基建(rev2 重写 —— codex 复审 must-fix 1/3)

#### 6.0.a Prepare:把脱敏从 `Append` 内部抽出来(rev2 新增)

**问题(codex 复审)**:当前脱敏在 `Append` 内部执行(`postgres.go:113`、
`ledger.go:97`);调用方构造的是**原始** entry,`Append` 失败只拿到 error,
**拿不到已脱敏的形态**,DLQ intent 无从产生。

**修复(rev5:此抽取归 W4a)**:把脱敏 + B-13 哨兵逻辑抽成显式
`auditledger.PrepareEntry` —— **W4b** 先就地修 B-13(§5)并把哨兵构造写成
`auditledger` 内可复用 helper;**W4a** 再据此抽出 `PrepareEntry` 并改 `Append`
签名 + 适配调用方:
```
PrepareEntry(ctx, rawEntry) (PreparedEntry, error)
// 内部:sanitizeLedgerEntry → 若脱敏失败按 B-13 换哨兵 → 产出 PreparedEntry。
// PreparedEntry 持有已脱敏的 HopChain/ModelChain/TenantScopeRef + RequestID +
// TenantID + CreatedAt。不含 LedgerID/root/signature/SignerFingerprint。
// rev4:PreparedEntry **不含 SignerFingerprint** —— 签名公钥 fingerprint 由
// `Append` 在真正签名那一刻按 active signer 注入(进 EntryHash 的 canonical
// 形式之前;详见 auditledger/canonical.go),DLQ 重放时同理用重放时刻的
// active signer。理由见下方「rev4:为何 PreparedEntry 不带 fingerprint」。
```
**rev9:`PreparedEntry` 必须 sealed(codex per-commit 复审 P2)**:`PreparedEntry`
作为 distinct type 的全部意义是「已脱敏」的编译期保证。若字段可导出,外部包
能 `auditledger.PreparedEntry{HopChain: 原始敏感数据}` 直接构造,而 `Append`
已不再脱敏 → 隐私边界被绕过。故 `PreparedEntry` 字段**全部不可导出**,只能由
同包的 `PrepareEntry` 产出。**rev10 精修**:seal 防的是外部**构造**未脱敏
entry,**不**是防读 —— 读一个已脱敏的 `PreparedEntry` 无安全风险。故
`PreparedEntry` 另提供一个**导出的只读投影方法** `AsLedgerEntry()`,供外部
测试 spy / 其它读取方使用;拿到投影也无法反向构造 `PreparedEntry`(构造仍只能
经 `PrepareEntry` 脱敏),故只读投影不破坏 seal。**rev11 修正**:`AsLedgerEntry()`
返回的投影必须对 `HopChain` / `ModelChain` 做**深拷贝**(新 slice + 逐元素
深拷 `Detail`/`FeatureRefs`、新 `ModelChain` 指针)—— 仅 struct 值拷贝会让投影
的 slice/指针与 `PreparedEntry` 内部别名,调用方改投影即改内部,`Append` 不再
脱敏会把改动签进账本,seal 仍被绕过。W4a-2 的 DLQ 要序列化它:`auditledger`
包提供公开的 `MarshalJSON`(marshal 是只读投影,安全)+ 一个**包内不公开的
解码函数** `decodePreparedEntryJSON`。**rev13:不提供公开 `UnmarshalJSON`**
—— 公开 `UnmarshalJSON` 会让外部包 `json.Unmarshal(任意 JSON, &pe)` 构造出
未脱敏的 `PreparedEntry`,重开 W4a-1 封死的 seal 漏洞;不提供公开
`UnmarshalJSON` 时,外部对 `PreparedEntry` 做 `json.Unmarshal` 因字段不可导出
只会得到空值,非旁路。DLQ 重放 worker 在 `auditledger` 包内,用包内
`decodePreparedEntryJSON` 解码即可。

`Append` 改为接收 `PreparedEntry`(不再内部脱敏)。调用方流程统一为:
`prepared, err := PrepareEntry(...)` → `Append(prepared)`。
`Append` 失败 → DLQ 投递的 intent **就是这个 `prepared`**(同一对象,已脱敏)。
DLQ worker 重放时直接 `Append(prepared)`,不重复脱敏。
→ 这条让 §6.0.c 的「DLQ intent 从哪来」有确定答案,也保证 DLQ 里**绝不**
出现未脱敏链路数据。

**rev4:为何 `PreparedEntry` 不带 fingerprint(codex per-commit 复审 P2)**:
签名器可以**轮换**(`LoadPublicKeysFromEnv` 支持 rotated key)。若一条 entry
在请求时刻 `Append` 失败、进 DLQ,worker **在轮换之后**才重放,则该 entry 会
被**重放时刻的 active signer**(可能是新 key)签名。若 `PreparedEntry` 在请求
时刻就钉死一个 fingerprint,重放后持久化行的真实 fingerprint 会与之不符 ——
要么签名对不上、要么逼迫保留旧 signer 才能重放。结论:fingerprint 只能在
`Append` 真正签名那一刻确定,`PrepareEntry` 不碰它。`Deferred` 条目在持久化前
**没有 fingerprint**,completion 事件也**不**为 `Deferred` 条目宣告
`AuditSignatureFingerprint`(见 §6.1 / §7)。

**`PrepareEntry` 的 `error` 语义(rev3;rev8 修 `TenantID` 精度)**:脱敏
失败本身**不是** error —— B-13 要求脱敏失败时换哨兵继续(§5 B-13),只要哨兵
替换成功,`PrepareEntry` 返回 `(PreparedEntry, nil)` 并另记一条 redaction-loss
warning(可观测信号,不阻断)。`error` **只**在根本无法构造安全 prepared intent
时返回 —— 结构性前置缺失,即 `RequestID` 为空(请求审计条目必须有 request id)。
**rev8 修正**:`TenantID == 0` **不是** error —— `0` 是既有合法语义「无租户
上下文」(系统级审计:凭据刷新、通道健康等本就无租户);money-path 审计条目
永远有真实 `TenantID > 0`,故 `TenantID==0` 不威胁信任链。`PrepareEntry`
对 `int64` 的 `TenantID` 不设零值校验。`RequestID` 为空这种 error 由调用方按
§6.0.c production fail-closed 处理(连安全 intent 都没有,无从投 DLQ,不得 settle)。

#### 6.0.b DLQ kind 与基建

1. **schema 迁移(需 Owner 确认)**:新增 migration
   `0050_dlq_audit_ledger_entry_kind`(下一个空号,现有最高 0049)扩展
   `usage_record_dlq.event_kind` 的 CHECK 约束,增加允许值 `audit_ledger_entry`。
   这是**唯一一条 schema 变更**,additive、低 blast(对照既有
   `backend/sql/migrations/0032_audit_mismatch_refund_pending` 同形态 ——
   drop+re-add CHECK)。down 迁移须带 0032 同款守卫:若已有
   `event_kind='audit_ledger_entry'` 的行则 `RAISE EXCEPTION` 拒回滚,
   避免静默丢账本待补意图。迁移 SQL 草案随 spec surface Owner,确认后才落。
2. `dlq.EventKindAuditLedgerEntry EventKind = "audit_ledger_entry"`
   (`internal/dlq/types.go`,非冻结包)。
3. `LaneForKind` / `ReplicaStatusForKind`(在 `internal/dlq/types.go:97` 一带
   —— rev2 修正:不在 `service.go`)为新 kind 补映射:lane = HIGH。
4. **重放 worker**:`cmd/gateway/lifecycle.go` `buildDLQRuntime()` 注册
   `audit_ledger_entry` 重放 handler —— 从 claim payload 解码,调
   `auditledger.Append` 重做写入(root/签名由 `Append` 在重放时刻按当时链头算)。
   **handler 必须把 `Append` 的 error 原样返回**(DLQ 框架 `ProcessClaim` 见
   error 会 `MarkFailed` 并按 `retry.go` 退避重试);**严禁吞错后标 delivered**。
   - **rev14:重放路径必须重跑脱敏(codex per-commit 复审 P1)**:DLQ payload
     是 `usage_record_dlq` 表里的**持久化数据**,不是「活的 sealed 值」。worker
     **不得**把解码出的 payload 直接当 `PreparedEntry` 喂 `Append`(`Append`
     已不再脱敏)。正确流程:解码 payload → 一个**原始 `LedgerEntry`** →
     `PrepareEntry(ctx, le)` **重跑脱敏** → `Append`。理由:坏入队路径 / 手工
     改的 DB 行可能带原始 prompt/密钥;重跑 `PrepareEntry` 对已脱敏的好数据
     幂等(redactor 无可剥则原样、sentinel 仍 sentinel),对坏数据兜底脱敏。
     即:DLQ 解码出口是 `LedgerEntry` 不是 `PreparedEntry`,seal 仍只由
     `PrepareEntry` 一个入口产出。
   - **rev15:重放前校验租户一致(codex per-commit 复审 P2)**:DLQ `Record`
     自带 `TenantID`(信封字段,enqueue 时按 §6.0.c 设);payload JSON 里也
     嵌了 `tenant_id`。正常两者相等。坏入队 / 手工修的 DB 行可能不等 ——
     worker 若信 payload,会把账本条目记到**另一个租户**名下,造成跨租户
     审计证据。修复:worker 解码后、`PrepareEntry`/`Append` **之前**校验
     `rec.TenantID == 解码出的 entry.TenantID`;不等 → 返回 error(不猜、不
     静默,进 DLQ MarkFailed → operator review)。
   - **rev16:信封是身份字段的唯一权威,payload 不可信(codex per-commit
     复审 P2 + 同族主动收口)**。DLQ payload 是持久化数据,凡**信封里也有的
     身份字段**都不能信 payload:
     · **`tenant_scope_ref`**:`canonical.go` `canonicalPayload` 把
       `tenant_scope_ref` 写进**签名哈希**(`entry.TenantScopeRef` 非空则直接
       用,空则派生 `TenantScopeRef(tenant_id)`)。但 `audit_ledger_entries`
       **没有 `tenant_scope_ref` 列** —— verify 时 `scanLedgerEntry` 扫不出
       该列、`TenantScopeRef==""` → canonical 派生。若坏 payload 带一个非空
       错 scope,`Append` 用错值签名、verify 用派生对值重算 → **签名永久验不过**
       (或签出别租户 scope 的伪证据)。修复:worker 在校验通过后**强制
       `entry.TenantScopeRef = ""`**,让 canonical 从已校验的 `tenant_id`
       派生 —— 与所有正常(非 DLQ)append 行为一致。
     · **`request_id`**:DLQ 信封 `IdempotencyKey` 按 §6.0.c 是
       `"audit_ledger:" + requestID`,是 request_id 的权威来源。坏 payload 的
       `request_id` 会让 `GetByRequestID` 查错请求 → 真实请求的账本条目被漏写
       或错挂。修复:worker 校验
       `rec.IdempotencyKey == "audit_ledger:" + entry.RequestID`;不等 → 返回
       error(进 operator review)。
     原则:身份字段(tenant / request_id / scope)以**信封**为准;payload
     只供给信封没有的内容字段(`created_at` / `hop_chain` / `model_chain`,
     且后两者经 `PrepareEntry` 重脱敏)。
   - **duplicate request_id**:重放时若 `request_id` 已有持久化账本条目
     (并发或上轮已补),worker 先 `GetByRequestID` lookup;命中 → 直接判
     delivered,不重复 Append(避免 commit-unknown 后无限重试)。
   - **rev14:`Append` 重复键契约统一(codex per-commit 复审 P2)**:worker 靠
     `ErrDuplicateRequestID` 判竞态,但 `PostgresLedger`/`AppendInTransaction`
     当前把 request_id 唯一冲突包成通用 pg insert error。修复:
     `AppendInTransaction` 须把 PG `23505 unique_violation`(request_id 唯一
     约束)翻译成 `ErrDuplicateRequestID`,使 `Ledger.Append` 契约在
     Memory / Postgres 两实现一致 —— worker 的 `errors.Is(ErrDuplicateRequestID)`
     竞态分支才在生产环境真正生效。
     **rev12:「命中」= `GetByRequestID` 返回 `nil` 或 `ErrLedgerEntryCorrupt`**
     —— 损坏行也是「该 request 已有持久化行」,重复 Append 会造重复条目;
     损坏本身是 verify 路径的问题(B-15),不归重放 worker 修。
     `ErrLedgerEntryNotFound` → 继续 Append;`Append` 若返回
     `ErrDuplicateRequestID`(竞态)→ 同样判 delivered;其它 `Append` error
     → 原样返回让框架重试。

#### 6.0.c DLQ event envelope(rev2 新增 —— codex must-fix 3)

DLQ 事件字段(`dlq.Event`,`store` 要求 `TenantID>0`、`IdempotencyKey!=""`、
JSON 合法):
- `EventKind` = `audit_ledger_entry`;`TenantID` = entry tenant;
- `IdempotencyKey` = `"audit_ledger:" + requestID`(同请求重试不重复入队);
- `SourceTable` = `"audit_ledger"`;
- `ReplicaStatus` = `ReplicaStatusNone`(rev3 —— codex 复审非阻断小修 3:
  `audit_ledger_entry` 是**主写意图**,不是某张已写表的副本;现有
  `MarkDelivered` / `MarkFailed` 只对 `billing_event_replica` /
  `audit_event_replica` 更新 `replica_status`,若给本 kind 设 `pending`,
  重放成功后主 `status` 会变 `delivered` 而 `replica_status` 永远停在
  `pending`,造成后台误判 —— 故定为 `none`);
- `Payload` = `PreparedEntry` 的 JSON;
- `DLQRef` 格式 = `"audit_ledger_dlq:" + <dlq event id>`(rev3 —— 与既有
  `*_dlq:<id>` 形态一致,避免 trailer/event/log 里出现裸数字)。

**DLQ enqueue 失败 = fail-closed(rev2 新增,decision B 的兜底)**:
Owner 决策 B 是「Append 失败 → 进 DLQ + 放行」,前提是 DLQ 收得下。若
**`Append` 失败且 DLQ enqueue 也失败** —— 此时账本没写、DLQ 也没接住,
再放行就是真静默绕过。故:production 模式下这种「双失败」**必须返回 error、
不得 settle/commit**(退回 fail-closed)。这不是改 decision B,是补它的地板。

### 6.1 `AuditLedgerResult` 数据流(rev2 重写 —— codex 复审 must-fix 1)

**问题(codex 复审)**:rev1 的不变式自相矛盾 —— 既要求 `LedgerID XOR DLQRef`
恰一个非空,又规定 dev/test nil ledger 两者皆空。rev2 用**显式三态**消解:

定义统一返回类型(`auditledger` 包):
```
type LedgerResultState int  // Persisted | Deferred | Disabled

AuditLedgerResult {
  State       LedgerResultState
  LedgerID    string  // 仅 State==Persisted 非空
  DLQRef      string  // 仅 State==Deferred 非空
  Fingerprint string  // 仅 State==Persisted 非空(rev4:Deferred 未签名)
}
```
三态不变式(按 State 分,不再有矛盾):
- `Persisted`(Append 成功):`LedgerID!="" && DLQRef=="" && Fingerprint!=""`。
- `Deferred`(Append 失败、intent 已入 DLQ):`DLQRef!="" && LedgerID=="" &&
  Fingerprint==""`(rev4:Deferred 条目尚未签名,无 fingerprint —— 见 §2)。
- `Disabled`(dev/test 无 ledger/signer):三者皆空。**`Disabled` 只在非
  production 模式合法**;production 模式由启动检查(§4)保证不会产生 `Disabled`。

`submitAuditLedgerEntry()` 签名:`(AuditLedgerResult, error)` —— `error` 只表示
「连 DLQ 都没接住的双失败」(§6.0.c fail-closed),正常三态都走 `AuditLedgerResult`、
`error==nil`。

- `submitAuditLedgerEntry()` 返回 `(AuditLedgerResult, error)`。
- `StreamForwarder` 的 `LedgerCallback` 从 `(entryID, fingerprint)` 改为携带
  `AuditLedgerResult`。
- buffered 与 streaming 两路的 completion 事件构造(`streamingCompletionEvent()`
  等)按 `AuditLedgerResult.State` 写事件的账本引用字段(见 §7 W4c):
  `Persisted`→写 `AuditLedgerID` + `AuditSignatureFingerprint`;
  `Deferred`→写 `AuditLedgerDLQRef`,`AuditSignatureFingerprint` **留空**
  (rev4:未签名);`Disabled`→三者皆空(W4c 校验只在 production 拒)。

### GW-07 — buffered 路径账本静默跳过(S1)

- 证据:`gatewayhttp/chat_completions_billing.go:237-248` `submitAuditLedgerEntry`
  在 `d.AuditLedger == nil` / `d.Signer == nil` 时 warning + `return nil,nil`;
  `:266-269` `Append` 失败 `return nil,err`。
- 修复:
  1. **启动检查**:见 §4(production 强制 ledger+signer+非 Noop)。
  2. nil 分支:production 由启动检查兜住;运行期到此只可能 dev/test →
     返回 `AuditLedgerResult{State: Disabled}`(三者皆空,§6.1),warning。
     下游 W4c 校验对 `Disabled` 仅在 production 拒、dev 放行。
  3. 正常流程:`prepared := PrepareEntry(rawEntry)`(§6.0.a)→ `Append(prepared)`。
     - 成功 → `AuditLedgerResult{State: Persisted, LedgerID, Fingerprint}`。
     - `Append` 失败 → 把 `prepared` 投 DLQ(§6.0.c)→ 成功入队 →
       `AuditLedgerResult{State: Deferred, DLQRef}`(rev4:Deferred 无
       fingerprint),记一条 `audit_ledger_deferred` 协议损耗,请求继续。
     - `Append` 失败**且 DLQ enqueue 也失败** → production 返回 `error`,
       调用方**不得 settle**(§6.0.c fail-closed 地板)。
- 风险测试(判别性):
  1. fake ledger `Append` 必失败、DLQ 正常 → 请求仍交付、DLQ 收到 `prepared`
     intent、结果 `State==Deferred && DLQRef!=""`。mutation:删 DLQ 入队 → 变红。
  2. fake ledger `Append` 失败**且** fake DLQ enqueue 也失败 + production →
     `submitAuditLedgerEntry` 返回 error、**未 settle**。mutation:把双失败
     改成「照样返回 Deferred」→ 变红(此测试守 fail-closed 地板)。

### C-14 — 流式路径账本缺失/失败只 warning(S1)

- 证据:`gateway/forwarder.go:640-680` `emitStreamingLedger` 对 nil/Noop/
  signer-nil 与 `Append` 失败都只 `warnLedgerLoss` + return。
- 修复:与 GW-07 同。nil/Noop/signer-nil 由启动检查兜(production);
  `:676` `Append` 失败 → 投 DLQ(同一 intent 通路),`warnLedgerLoss` 保留作
  可观测信号但不再是唯一动作;`LedgerCallback` 收到 `AuditLedgerResult`。
- 风险测试:fake ledger 失败 → 流式请求仍完成、DLQ 收到 intent、callback 拿到
  `DLQRef`。mutation:删 DLQ 入队 → 变红。

### C-13 — 流式账本用首字节时间当完成时间(S1)

- 证据:`gateway/forwarder.go:601-632` `streamingLedgerHeaderWriter` 在
  `WriteHeader`/首次 `Write` 调 `ensureLedger(time.Now())` → 首字节时间被当
  response completion。
- 修复:**账本在流真正终结时发,用真实终结时间**。
  - `streamingLedgerHeaderWriter` 不再在 `WriteHeader`/`Write` 触发账本完成。
  - `emitStreamingLedger` 的调用点移到流终态(`Forward` 扫描到 terminal 事件 /
    `Forward` 返回点),`completedAt` = 真实终结时间。**正常终结、上游中途
    错误、客户端断连**三条终结路径都必须各发一次(`Forward` 返回点统一发)。
  - **header 时序(rev1 新增 —— codex must-fix 6)**:账本移到终态后,
    `X-HUAKAI-Ledger-ID` 在流已开始后无法再作普通 header 发出。改为
    **预声明 trailer**,并入现有 `declareStreamBillingTrailers()`
    (`gatewayhttp/chat_completions_stream.go:524-530`,该函数已声明
    StreamState/DeliveredTokens 为 trailer)。**rev2:用两个独立 trailer** ——
    `X-HUAKAI-Ledger-ID`(State==Persisted 写)与 `X-HUAKAI-Ledger-DLQ-Ref`
    (State==Deferred 写),不把 DLQRef 混进 LedgerID。同步更新会被打破的
    旧测试(见 §9)。
- 风险测试(判别性):构造首字节早、终结晚的流(mock:首 chunk 后 sleep
  再终结)→ 断言账本 hop chain completion 时间 ≈ 终结时间,与首字节时间差
  > 明确阈值。mutation:把账本触发改回首字节 → 变红。

## 7. W4c 逐发现 spec —— 堵所有 settle 旁路

### B-12 — completion 事件/settle 不强制账本引用(S1,trust-chain bypass)

- 证据:`eventbus/types.go:212-233` `normalized()` 只校验 `Kind`/`ID`/
  `TenantID>0`;`observability/audit_logger_handler.go:69` 只在 `requireRef`
  时拒空 `AuditLedgerID`,gateway 默认未启用 `WithRequiredAuditRef()`
  (`cmd/gateway/middleware.go:192-194`)。
- **旁路(rev1 新增 —— codex must-fix 4/5)**:仅改 `normalized()` 不够 ——
  - `gatewayhttp/chat_completions_billing.go:155-168` `settleCompletion()` 在
    bus nil / 无 handler / closed / 队列满时**直接 `Settle()`**,绕过事件总线
    与 `normalized()`。
  - `gatewayhttp/chat_completions_handler_headers.go:183-199` 缓存命中走
    `CommitCacheHit()` 直接写 money,也不经事件总线。
- 统一定义「**有效账本引用**」:money-path 必须满足以下**任一**:
  - `AuditLedgerID != "" && AuditSignatureFingerprint != ""`(账本已持久化
    且已签名),或
  - `AuditLedgerDLQRef != ""`(账本 intent 已在 DLQ 待补 —— rev4:此分支
    **不**要求 `AuditSignatureFingerprint`,Deferred 条目要等重放才签名,
    见 §2;DLQRef 本身即「平台已持久承诺补签」的凭证)。
  两个分支都不满足 + production 模式 → 拒绝。dev/test 模式放行(无信任链需求)。
- 修复:
  1. **新增事件字段** `RequestCompletionEvent.AuditLedgerDLQRef string`
     (`eventbus/types.go`)。
  2. 把「有效账本引用」校验抽成一个函数 `validateMoneyPathAuditRef(event,
     releaseMode)`,**三处都调用**:
     - `normalized()`(`Kind==RequestCompletion` 时);
     - `settleCompletion()` 的 direct-settle 分支 —— 进 `Settle()` **之前**校验;
     - cache-hit 的 `CommitCacheHit()` 路径 —— 进 commit **之前**校验。
     **rev2**:`releaseMode` 作为**形参**传入(`normalized()` 与校验函数都加参),
     不让 `eventbus` 反向 import `cmd/gateway` —— 校验函数放 `eventbus` 包、
     `releaseMode` 由调用方注入,避免依赖倒置。
  3. production 模式下 bus 不可用导致 direct-settle:校验照跑;校验过
     (有 DLQRef 也算过)才 settle。校验**不过**(两引用皆空)→ 不 settle,
     记一条 mandatory reconciliation(若无现成 reconciliation 机制,落 DLQ
     `operator_review` 或最次记结构化 ERROR + 路线图 RR-W4-001)。
  4. `cmd/gateway` 注册 audit logger handler 默认启用 `WithRequiredAuditRef()`;
     该 handler 的 `requireRef` 检查同步改成「`AuditLedgerID` 或
     `AuditLedgerDLQRef` 之一非空」。
  5. 灰度逃生口:一个 feature-flag 可临时放宽,启用即写 mandatory
     reconciliation 记录(RR-W4-001)。
- 风险测试(判别性,**三条路径都要测**):
  1. money-path event 两个账本引用皆空 + production → `normalized()` 拒。
  2. direct-settle 路径(注入 bus=nil)+ 两引用皆空 + production → 不 settle。
  3. cache-hit 路径 + 两引用皆空 + production → 不 commit。
  4. 带 `AuditLedgerDLQRef`(账本在 DLQ)→ 三条路径都放行(证明「待补」合法)。
  5. dev 模式 + 两引用皆空 → 放行(证明 dev 豁免)。
  mutation:删 `validateMoneyPathAuditRef` 在 direct-settle 的调用 → 测试 2 变红;
  删 cache-hit 的调用 → 测试 3 变红。

## 8. 已知难点清单(review 不必再"发现")

1. 账本是 Merkle 链 → DLQ 存「待写意图」非「已签名条目」,worker 重放 `Append`(§2)。
2. 切片顺序 W4b→W4a→W4c:W4a 的 DLQ intent 必须复用 W4b 修好的脱敏。
3. B-12 有三条 settle 路径(总线 / direct-settle / cache-hit),校验必须三处都加。
4. W4a↔W4c 契约:`AuditLedgerResult` 显式三态 `Persisted/Deferred/Disabled`
   (§6.1)+ 事件新字段 `AuditLedgerDLQRef`;脱敏经 `PrepareEntry` 预备步(§6.0.a)。
5. C-13 账本移终态 → `X-HUAKAI-Ledger-ID` + `X-HUAKAI-Ledger-DLQ-Ref` 两个
   预声明 trailer,并入 `declareStreamBillingTrailers`。
   「Append 失败且 DLQ 也失败」= production fail-closed 地板(§6.0.c)。
6. B-13 哨兵替换必须在 `EntryHash` 之前(签名要覆盖哨兵);哨兵放 `hop_chain`
   jsonb 内,无 schema 变更。
7. B-15:持久化 root 恒 32 字节,长度非 32(含 0)即损坏。
8. 仅一条 schema 迁移(DLQ `event_kind` CHECK 扩 `audit_ledger_entry`),
   additive,需 Owner 确认,SQL 草案随 spec surface。
9. 新文件只能进非冻结包(`auditledger` / `cmd/gateway` / `eventbus` / `dlq`
   可加;`gateway` / `gatewayhttp` / `proto` 冻结,只改既有文件)。
10. signer 轮换 × `Deferred` 条目(rev4):`Deferred` 条目的 fingerprint 在
    请求时刻**未定**,由 DLQ 重放时刻的 active signer 决定 —— `PreparedEntry`
    不带 fingerprint;completion 事件不为 `Deferred` 宣告
    `AuditSignatureFingerprint`;「有效账本引用」校验对 DLQRef 分支不查
    fingerprint(§2 / §6.0.a / §6.1 / §7)。

## 9. 会被新语义打破、需同步修改的旧测试(rev1 新增)

- `gateway/forwarder_test.go:512-545` `TestStreamingLedgerCallbackBeforeFirstChunk`
  —— C-13 把账本从首字节移到终态,此测试语义反转,须改写为
  「callback 在流终态触发」。
- 任何断言 `X-HUAKAI-Ledger-ID` 为普通 header 的流式测试 → 改断言 trailer。
- 实现者须全仓 grep `submitAuditLedgerEntry` / `LedgerCallback` / `Append` /
  `AppendInTransaction` / `AppendInTx` 调用点(rev3 —— codex 复审非阻断小修 2:
  低层事务接口也有直接调用面,不只 `.Append(...)`,如
  `channelhealth/store_postgres.go:302`、`audit/refund_worker.go:474`,
  实现者须确认这些是否触达 auditledger 写入面并同步适配),返回类型从
  `*LedgerEntry,error` 改 `(AuditLedgerResult, error)`、`Append` 改收
  `PreparedEntry` 后逐处适配。

## 10. 验收标准

W4b / W4a / W4c 各自:
- `cd backend && GOCACHE=$HOME/.cache/go-build go build ./...` exit 0。
- 改动包 + 受影响包 `go test ... -race -count=1` exit 0;最后全量 `go test ./...`。
- 每个发现的「风险测试」全部新增并通过;**每个测试过 mutation 自检**
  (`AGENTS.md` §Test Quality Discipline),codex 交付报告逐测试写明自检结果。
- §9 列的旧测试已同步改并通过。
- DLQ 迁移:`migrate up` + `migrate down` 都验证过(本地 PG,`integration_pg`)。
- codex per-commit review 无 S0/S1 真实缺陷。

## 11. 提交方式(一 commit 一模块)

- W4b:`auditledger` 一个 commit(privacy + postgres scan + ledger + sentinel);
  verify handler 的 corrupt 映射在 `gatewayhttp` 冻结包改既有文件,单独 commit。
- W4a:`auditledger`(intent 类型 + DLQ helper)/ `dlq`(EventKind + lane)/
  迁移 / `cmd/gateway`(启动检查 + worker 注册)/ `gateway` `gatewayhttp`
  (调用点)—— 按模块拆 commit。
- W4c:`eventbus`(字段 + normalized + 校验函数)/ `gatewayhttp`(三处校验)/
  `cmd/gateway`+`observability`(requireRef)—— 按模块拆。
- 标题 `<英文模块> <中文说明>`,无 type/阶段号/PASS;结尾 Co-Authored-By。

## 12. clean-room

W4 全部改 HUAKAI 内部代码(`backend/`),不读参照项目源码 —— 无 clean-room
约束。W4 整波闭合后才做收尾对照。

---
作者:Claude。日期:2026-05-22(rev16)。源波计划已 parallel-draft + 交叉评审;
本 spec 经 codex 评审 rev0→rev1→rev2(must-fix 7→3→0,rev2
**APPROVE-WITH-CHANGES**)+ rev3 折入 3 条非阻断小修 + rev4 修 P2(signer 轮换
下 Deferred 不钉死 fingerprint)+ rev5 切片归属精修 + rev6 修 P2(B-13 字段级
脱敏不误触哨兵)+ rev7 修 P2(B-15 损坏行跨租户存在性泄露)+ rev8 修
`PrepareEntry` 的 `TenantID==0` 精度(0 是合法「无租户」语义,不作 error)+
rev9 修 P2(`PreparedEntry` 字段 sealed,杜绝外部绕过脱敏)+ rev10 精修
(sealed 仍提供导出只读投影 `AsLedgerEntry()`,seal 防构造不防读)+ rev11 修
P2(`AsLedgerEntry()` 投影须深拷贝,否则别名仍可绕过 seal)+ rev12 精修
DLQ 重放 worker 的 duplicate-request_id 边界(损坏行也算命中)+ rev13 改
`PreparedEntry` 不提供公开 `UnmarshalJSON`(否则外部 json.Unmarshal 重开
seal),DLQ 解码改包内函数 + rev14 修 P1(DLQ 重放路径解码后须重跑
`PrepareEntry` 脱敏)+ P2(`AppendInTransaction` 把 PG 23505 翻译成
`ErrDuplicateRequestID` 统一契约)+ rev15 修 P2(DLQ 重放前校验
`rec.TenantID` 与 payload 租户一致,防跨租户审计证据)+ rev16 修 P2
(`tenant_scope_ref` 强制从 tenant_id 派生防签名验不过、`request_id` 校验
信封 `IdempotencyKey`;身份字段以信封为准)。
Owner 已定 fail-closed 决策(§4)并已确认 schema 迁移 `0050`(§6.0.b)。
