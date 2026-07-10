# R1A(Claude OAuth/session 端到端 serving)对抗审查裁定 — 2026-07-10

**审查者**:codex(ultra,reviewer lane,GPT-5),对照本机真实 Claude Code 2.1.199 + 三镜(sub2api / CLIProxyAPI / new-api)生产码 + 官方 Messages/Token-Count API 契约。
**裁定**:REJECT。0 个 S0,**7 个 S1 blocker** + 3 个 S2。R1A 不可安全落地,未提交。
**根因**:R1A 的 handler 级 E2E fixture 不代表真实 Claude Code 流量,给了假信心;严格门同时"过严(误拒真 CC)+ 过松(可被 curl 伪造)"。这正是 Owner「闭环必须真实测验」要抓的。

## S1(必修,复验须用真实 CC 2.1.199 + 确定性 PG 并发)

1. **policy.go:54 — UA/X-App 锁死单形态误拒真 CC**:官方 2.1.199 的 IDE/Agent SDK 入口生成非 `cli` 后缀,后台请求发 `X-App: cli-bg`;当前恒判 Reject 且反转账号无 fallback → 403。三镜容纳多入口形态(sub2api claude_code_validator.go:50-59 / CLIProxyAPI claude_executor.go:1722-1743)。修:按真实版本样本建入口/`cli-bg` 白名单。

2. **policy.go:101 — body 门要求所有 /v1/messages 带 beta/非空 system/metadata/max_tokens>0**:真 2.1.199 会发无 system 的 quota/模型验证/token-count fallback 请求;count_tokens.go:55 返 501 → fallback 到 max_tokens=1 的 /v1/messages 继续被 403。官方契约:system 可选、max_tokens=0 合法。修:按主请求/探测/计数分别建 contract,用真实录制样本测。

3. **policy.go:104 — "严格鉴真"可被 curl 完整伪造**:补齐自报 UA/SDK 头 + 任意非空 system + 任意非空 device_id/session_id + 合法 messages 即得 OfficialDirect;正向测试自身用虚构 system 就放行。修:加版本化稳定语义标记;若无不可伪造证明,明确这是**启发式门**,叠加账号级授权/限流/风控(honest 访问控制,非伪装成不可伪造鉴真)。

4. **chat_completions_attempt.go:376 — OfficialDirect 绕过 model remap**:public alias(如 `claude-team`)映射上游 `claude-sonnet-*` 时,出站仍发 alias 作 model → 上游未知模型;E2E fixture 刻意让 alias==provider-model 掩盖。修:先做标准 model remap 再进字节等价 composer;加 alias≠provider-model 三路判别测试。**(已读码证实)**

5. **accountcreate/atomic.go:63 — FOR KEY SHARE 不锁可变 upstream_protocol**:管理端可并发更新该非键列;创建事务读旧 family 后,更新事务提交新 family,旧事务插入不兼容账号(TOCTOU)。修:改用与协议更新冲突的锁,协议变更同时校验+锁定现存账号。

6. **contracts.go:162 — Released/TrafficAllowed 假绿**:闭合矩阵只查六组件存在,未消费官方门/辅助端点/完整账号资格+模型可售卖不变量;验收表自认 account-eligibility 与 model-sellability 生产消费为 SCAFFOLD。修:readiness 改端点级并纳入真实门+账号资格+模型可售卖;闭合前保持非 Released 或 feature flag。

7. **atomic_concurrency_integration_test.go:60 —(本 PM 亲写测试)变异非确定性**:start channel 只同步 goroutine 启动,不保证两事务读 peers 早于任一 INSERT;删锁后调度器可能自然串行仍得"一成一拒"→变异误绿。修:读 peers 后设可控 barrier,或用两显式事务验证第二事务确实等锁。

## S2

- oauth_session.go:88 — 入站真实指纹出站被覆盖为固定旧版 claude-cli/2.1.63,版本不一致;应 per-account profile 同步所有版本相关字段。
- accountcreate/atomic.go:91 — 原子创建只提交 account 行,credential 在锁释放后才写;相同 provider/vendor/auth 并发时后者读到首行空 vendor/auth 误判 mixed risk 返 400。应让风险判断所需非秘密凭据元数据与账号在同一锁生命周期内可见。
- oauth_session.go:127 — 本地过期防线对畸形时间 fail-open,未消费 credential record 权威 `AccessExpiresAt`;真实存储形态下退化为上游 401。应投影权威时间,畸形非空时间 fail-closed,补真 PG vault→adapter 测试。

## 已核实无 finding(codex 确认干净)
advisory key 与风险模型一致;Reject 走 detached abort 且 terminal、OfficialDirect 仍进原 billing/quota settle 无漏释放/重复扣;三路 body 独立克隆(除 model remap);migration 0174 up 集合与 registry 精确对齐、down 守卫+0172 回退正确;clean-room 干净、无新依赖、无凭据泄漏;ParseMetadataUserID 能解析新 JSON 形态(问题在门错误强制所有辅助请求都带它)。

## 三镜引用(clean-room reviewer lane)
sub2api@12d811bd:claude_code_validator.go / gateway_upstream_request.go / gateway_claude_oauth_body.go;CLIProxyAPI@26d45fd4:claude_executor.go / code_handlers.go;new-api:relay/claude_handler.go / relay/channel/claude/adaptor.go。

---

## 第二轮 codex 复核(修复后)+ 二次修复

第一轮 7 个 S1 修复后再审:2 FIXED(S1-1/S1-4)、4 PARTIAL(S1-2/3/5/6)、1 NOT-FIXED(S1-7)+ 2 S2。二次修复全部处理:

- **S1-2**(count_tokens 门是死代码+矩阵自相矛盾):去掉 countTokensCore 门路径,门只管 `/v1/messages`;501→/v1/messages 回退由放宽的 MessagesCore 覆盖;矩阵 AT-R1A-004 改正。
- **S1-3**(声称账号级授权不实):doc/矩阵改为诚实描述真实授权层 = API key 认证 + tenant 隔离 + model allowlist + 凭据状态 + 限流;**无 per-反转账号 ACL**。
- **S1-5**(FOR SHARE 只修一向):补管理端改协议守卫 `ensureAccountsCompatibleWithProtocol`——改协议事务内校验存量账号与新协议兼容,不兼容整体回滚;新查询 `ListProviderAccountsForProviderCompat`;真 PG 集成测试 + 变异证(去守卫→改 session 成功→红)。运行时二次防御(AT-R1A-006)仍是安全网。
- **S1-6**(诚实定界未落到操作面):更新陈旧 runtime-logic 文档(原写 beta/system/metadata 必填、cache>4 拒、绕 remap,均已被重写反转);evaluate.go TrafficAllowed 语义注释。
- **S1-7**(测试抓不到锁序变异):重写为**锁序敏感**确定性测试 `TestInsertLockPrecedesPeersRead`——外部持锁期间插冲突对端,证正确态 A 拿锁后才读到对端被拒;变异(锁移到 peers 读后)→ A 早读空 peers 成功 → 红(确定性,旧持锁测试在此变异下仍绿=新测试严格更强)。
- **S2-1**(bodymodel null panic):nil map 判 + 测试。

### 待 Owner 决策(非缺陷,产品/安全边界)
- **per-反转账号 ACL**:当前完整伪造 Claude Code 形态 + 同租户且模型已授权的有效 API key,会正常使用该池反转账号(跨租户/禁用模型仍拦)。是否需要"哪个 key 能用池内哪个具体反转账号"的细粒度 ACL,是 Owner 产品/安全决策,未建。

### Roadmap(需真实产物)
- **S2-2** 录制 fixture:当前"真实 2.1.199"测试是手写字符串,非录制流量;真上游 canary + 录制回归属队列的"IDE/ChatGPT 整链 E2E"层。

---

## 第三轮 codex 复核 + 三次修复

第二轮修复后再审:S1-2/3/6 + S2-1 **FIXED**;S1-5、S1-7 仍 PARTIAL。三次修复:

- **S1-7**(sleep 猜测非确定性):`TestInsertLockPrecedesPeersRead` 的 `time.Sleep(300ms)` 换成**确定性 barrier**——轮询 `pg_locks` 直到观测到 Insert 后端真的阻塞在未授予的 advisory 锁上(`waitForBlockedAdvisoryLock`)。变异(锁挪到读 peers 之后)×4 全确定性红、还原绿。
- **S1-5 多凭据遮蔽**:`ListProviderAccountsForProviderCompat` 从 `LATERAL LIMIT 1` 改为**全连接逐条**(一个账号的每条活跃凭据都出一行),守卫逐条校验,较新兼容凭据不能遮蔽较旧不兼容凭据。新增 `TestUpdateProviderProtocolChecksEveryActiveCredential`(oauth 账号+兼容+不兼容双凭据→拒),变异回 LIMIT 1 →红;测试真 seed account_credentials,补上"守卫只 seed api_key 没走 vendor/auth 路径"的缺口。
- **S1-5 独立 credential 创建路径**(codex 要求闭合的第二绕过):**判定为需层次重构的独立切片**。`admin_credentials_handler` 无 provider 协议访问路径;完整 race-free 校验须在 `credentialstore.Create` 事务内做,但校验逻辑依赖 `servingcapability.ValidateAccountCompatibility`,而 servingcapability 导入 credentialstore→成环,credentialstore 内无法无环调用。**运行时安全网**:`ErrCredentialAmbiguous`(多活跃模式)+ 运行时兼容检查(AT-R1A-006)确保不兼容凭据**不被服务**——故为数据卫生/fail-fast-UX 缺口(非 serving/钱泄漏)。列为独立 slice(AT-R1A-009 follow-up)。

### S2 文档漂移(已修/记录)
- AT-R1A-006"credential write 前 400"过度声称 → 改正为账号创建路径,指向 AT-R1A-009 follow-up。
- OpenAPI `/v1/messages` 缺 403、`count_tokens` 缺条件性 501 → 记录待补(非阻塞)。

### 收敛裁定
DISPATCH/serving 闭合(registry/六站/官方形态门/model remap/选号/计费)已充分测试;所有 **serving-blocking** S1 已修+变异证。剩一条 credential-创建路径 fail-fast 守卫是运行时兜底的独立切片,+ per-账号 ACL(Owner 决策)+ 录制 fixture(需真流量)。这三项均不阻断 dispatch 闭合,交 Owner 定提交时机与切片排期。
