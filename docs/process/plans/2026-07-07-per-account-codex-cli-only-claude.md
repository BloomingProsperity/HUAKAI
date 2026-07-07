# 片2e:每账号 codex-cli-only 收紧开关 — Claude 计划草案

日期 2026-07-07。Owner 已批形态(采纳 sub2api:codex 默认放开 + 每账号可选收紧开关)。本片实现该「每账号可选收紧」。

## 背景(亲核 file:line)
- 片2d 已把入站官方客户端门对 codex 放开:`officialclient/policy.go:44` `vendorEnforcesOfficialClient` 只对 anthropic/claude 返 true,codex/openai/chatgpt 走 `GateDecision`(:84)的 `ReasonVendorNotEnforced` 分支放行。
- 门能强制 codex-cli:`RequiredIdentity`(policy.go:29)已支持 openai/codex/chatgpt→`IdentityCodexCLI`;`IsReverseAccountType`(:76)覆盖 codex OAuth 账号类型(片2d 已让 codex 账号到达 vendorEnforce 判定,证明它过 reverse 门)。
- 存储不触 schema:`provider_accounts.extra`(迁移 0110,`jsonb NOT NULL DEFAULT '{}'`)是通用扩展列;解码器 `decodeProviderAccountExtra`(postgres_vault.go:230)已支持 bool。
- 门控点:`enforceOfficialClient`(chat_completions_dispatch.go:582)在 `resolveCredential`(:573)调用,用 `ex.accInfo`(`provider.AccountInfo`)+ `clientid.Detect`;`AccountInfo` 当前**不带** extra/任何 per-account settings。
- `AccountInfo` 构造点 = vault `Resolve` 内 `postgres_vault.go:131`(与 extra 加载同处),可就地填新字段。
- **三镜(§16)**:sub2api 同款——`accounts.extra["codex_cli_only"]`(JSONB map,非列),`IsCodexCLIOnlyEnabled()` 读之(account.go:1655);new-api / CLIProxyAPI 无等价(已 source-cite,前者 codex 是渠道类型无入站客户端门,后者纯 relay 只出站伪装)。§16 tiebreaker 默认对齐 sub2api「挂 extra」。

## 目标
每个 provider account 可选设 `extra.codex_cli_only=true`:该 codex 账号也进官方客户端门(非 Codex CLI 入站→403)。默认缺省/false=维持片2d 放开。**opt-in、非默认翻转**(默认行为不变)→ 不触 CLAUDE.md §0「default-behavior flip」Owner-gate;不触 schema。

## 方案(Option A:专用 AccountInfo 字段,策略标志不进出站凭据)
选 A 不选「复用 cred.Extra」:cred.Extra 是**出站凭据语义**(base_url/aws_session_token/org_id 按 key 取,adapter.go:96 等),`mergeCredentialAccountExtra`(postgres_vault.go:214)会把账号 extra 键并进 cred.Extra→策略标志混入出站凭据,语义脏 + 未来整体遍历会泄漏。故用专用字段隔离。

1. **`provider.AccountInfo` 加 `CodexCLIOnly bool`**(adapter.go:57 区块)。注释:仅入站门控用,不出站。
2. **vault Resolve 填充**(postgres_vault.go 两条路径:legacy `Resolve` 的 info 构造 :131;新 `resolveFromStore` 的 AccountInfo 构造):解码 extra 后 `info.CodexCLIOnly = extraBool["codex_cli_only"]`(extra 解码器返 `map[string]string`,值为 `"true"/"false"`,判 `== "true"`)。**排除该键并进 cred.Extra**(mergeCredentialAccountExtra 加跳过 `codex_cli_only`,防出站泄漏)。
3. **`GateDecision` 加 `perAccountEnforce bool` 入参**:`if !vendorEnforcesOfficialClient(vendor) && !perAccountEnforce { return false, ReasonVendorNotEnforced }`。即 perAccountEnforce=true 时,即使 vendor 不在硬编码强制表也进门(前提 RequiredIdentity 有该 vendor 的官方身份,codex 有)。
4. **`enforceOfficialClient` 传参**:`GateDecision(ex.accInfo.AccountType, ex.accInfo.Platform, identity, ex.accInfo.CodexCLIOnly)`。
5. **admin 读写**:创建/更新 handler 已能读写整个 extra 对象(admin_pool_accounts_handler.go:190/211/528),操作员今天即可设 `{"codex_cli_only":true}`;本片不新增字段级 API(extra 自由 KV 已够)。UI toggle 属前端页面级=Owner-gated,本片不做,记 roadmap。

## 模块配合验证(§17)
- 配合点 1:extra.codex_cli_only=true → vault 填 AccountInfo.CodexCLIOnly → 门读到 → 非 Codex CLI 入站 403、Codex CLI 入站放行。
- 配合点 2:extra 无该键/false → AccountInfo.CodexCLIOnly=false → GateDecision perAccountEnforce=false → codex 仍放开(片2d 行为逐字节不变)。
- 配合点 3:该键**不进** cred.Extra → 出站请求不带 codex_cli_only(无泄漏);base_url 等既有 extra 键仍正常并入。
- 配合点 4:anthropic/claude 账号不受影响(vendorEnforcesOfficialClient 恒 true,perAccountEnforce 参数与它 OR,行为不变)。

## 测试(§14 判别 + §17 配合)
- GateDecision 单测:codex + perAccountEnforce=true + 非 CodexCLI 身份→(true, NonOfficialReject);codex + true + CodexCLI 身份→(false, OfficialClientOK);codex + false + 任意身份→(false, VendorNotEnforced);anthropic + false→仍强制(证 OR 不削弱既有)。变异:GateDecision 忽略 perAccountEnforce→codex+true+非CLI 仍放行→测试红。
- vault 单测:extra `{"codex_cli_only":true}`→AccountInfo.CodexCLIOnly=true;extra 无键→false;**且 cred.Extra 不含 codex_cli_only**(证不泄漏)。变异:填充漏读→CodexCLIOnly 恒 false→红;不排除并入→cred.Extra 含该键→红。
- 配合集成(gatewayhttp):codex 账号 knob 开 + 普通 OpenAI SDK 入站→403 CodeOfficialClientRequired;knob 开 + Codex CLI 指纹→放行;knob 关→403 不触发(200 路径)。

## 爆炸半径 / 风险 / 决策点
- 触入站鉴权门(auth-core 邻域),但**默认行为零变**(opt-in),非默认翻转、非 schema。风险=若 AccountInfo 填充漏某条 Resolve 路径→knob 设了不生效(fail-open 到放开,非 fail-closed,安全侧可接受但要测两条路径都填)。
- 不触 money / schema / 默认翻转 → 自主可落地(安全网:对抗审查 0 S0/S1 + 变异 + 基线)。
- roadmap:admin 前端 toggle UI(页面级 Owner-gated);sub2api 还有「全局加固策略」层(settings 表白/黑名单+版本门+引擎指纹)本片不做,记 roadmap。

## 成本
小(~50-70 行 + 测试)。核心=AccountInfo 加字段 + 两条 Resolve 路径填充 + 排除并入 + GateDecision 加参 + 门传参。

---

## #10 平行综合(Claude + Codex,2026-07-07)
两份独立计划(claude + codex,见 -codex.md)**高度收敛,零冲突**:同选 Option A 专用 AccountInfo 字段、同识别 cred.Extra 泄漏并同法过滤、同 GateDecision 加 bool 参(anthropic 不削弱/codex 默认放开)、同两条 Resolve 路径都填、同测试矩阵与变异点。

**采纳 codex 两点精修**:
1. **抽共享 helper** 解析 accountExtra→策略布尔,legacy 与 resolveFromStore 两路复用(减分叉、防漏一条,§13 DRY)。
2. **apikey 账号 + force=true 仍不拒** 的判别测例(证 IsReverseAccountType 前置门不被 force 越过)——GateDecision 逻辑第①步「非 reverse 不拒」保证之。

**最终落地口径**:AccountInfo 字段名 `CodexCLIOnly`(对齐存储键 codex_cli_only + sub2api 语义),GateDecision 新参名 `forceOfficialClient`(通用)。字段仅入站门控消费,经共享 helper 从 extra 填,并从 mergeCredentialAccountExtra 过滤(不进出站 cred.Extra)。

---

## 实现落地 + PM 验收(2026-07-07)
沙箱约束下 codex 作者产出可套用代码文本、PM 逐块套用 + 自跑门禁(分工不变:codex 作者、我验收)。

### 落地内容(6 处)
- `provider/adapter.go`:AccountInfo 加 `CodexCLIOnly bool`(仅入站门控、禁出站,中文注释)。
- `provider/postgres_vault.go`:新 `codexCLIOnlyFromAccountExtra` 共享 helper + const `codex_cli_only`;`mergeCredentialAccountExtra` 过滤该键防出站泄漏;**两条 Resolve 路径都填**(legacy :132 抽出 accountExtra 变量后填;resolveFromStore :172 就地填)。
- `officialclient/policy.go`:GateDecision 加 `forceOfficialClient bool`(`!vendorEnforcesOfficialClient && !force`→放行;force=true 扩到有官方映射的 vendor;非 reverse 前置门不越过;无映射 fail-open)。
- `gatewayhttp/chat_completions_dispatch.go`:enforceOfficialClient 传 `ex.accInfo.CodexCLIOnly`。
- 测试:officialclient TestGateDecisionForceOfficialClient(6 例矩阵含 apikey+force 不越前置门)+ provider TestProviderAccountExtraCodexCLIOnlyAndMerge(helper 解析 + 泄漏隔离 + 不缩水)+ 既有两处 GateDecision 调用补 false 参。

### PM 复核 codex 产出(报告不信,逐条对真码)
- codex 诚实标注 legacy Resolve 无 accountExtra 变量——**属实**,我抽出 `accountExtra := decodeProviderAccountExtra(row.extra)` 再填。
- codex 只提一处 GateDecision 调用需补参——**实为两处**(policy_test.go :168/:179),我都补了。
- codex 纯函数测试原拟放 postgres_vault_test.go(`//go:build integration_pg` 需 DB)——我改放 postgres_vault_unit_test.go(无 tag 进标准门)。
- codex 用的常量全核实存在(VendorAnthropic/OpenAI、AuthModeClaudeAIOAuth/CodexCLIOAuth/APIKey、IdentityClaudeCode/CodexCLI/CurlScript)。

### PM 门禁(全自跑)
gofmt 干净 / build 0 / vet 0 / quality-gate PASS(staticcheck 94、deadcode 879)/ 全量 233 包全绿。
**变异证红 3 发**:①GateDecision 忽略 force→强制测例红;②去 mergeCredentialAccountExtra 过滤→泄漏断言红;③helper 恒 false→"true 开启"红。还原后逐字节一致基线复绿。

### 判别覆盖与已知缺口
- 覆盖:helper 解析(真值/假值/缺省/空)、泄漏隔离(cred.Extra 不含 + 其余键不缩水)、GateDecision 全矩阵(anthropic 拒/放、codex 默认放/force 拒/force CLI 放、apikey force 不越前置门)。
- admin 前端 toggle UI 属页面级 Owner-gated,记 roadmap(后端/DB 写路径已就绪,操作员可经 admin API 直接设 extra)。

### 独立 codex 审查(换 lane)+ PM 复核补测
独立 codex 会话审查片2e diff,出 2×S1+2×S2;逐条对真码复核后**全部闭合**(非降级敷衍):
- **S1(补测):dispatch 门控无测试守**——加 gatewayhttp `TestEnforceOfficialClient_CodexCLIOnlyGatesNonOfficialClient`(构造 chatExecution:knob 开+非CLI→403、knob 开+Codex CLI→放行、knob 关→放行)。变异传 false 证红。
- **S1(补测):Resolve 填字段无测试**——加 integration_pg `TestPostgresCredentialVault_ProviderAccountExtraCodexCLIOnly`(真 DB 设 extra→Resolve→断言 info.CodexCLIOnly=true + cred.Extra 不泄漏 + 其余键正常),复用既有 fixture;`go vet -tags integration_pg` 编译核过(未破坏集成构建),集成 lane 跑时守 legacy 路径。
- **S2(采纳强化):泄漏过滤只挡 merge 键、不挡凭据载荷预带键**——mergeCredentialAccountExtra 改绝对 `delete(cred.Extra, key)` scrub(即便凭据载荷误带同名键也清),补 `TestMergeCredentialAccountExtraScrubsPreexistingPolicyKey`。变异去 scrub 证红。
- **S2(补测):无官方映射 vendor+force fail-open 无守**——加测例(gemini reverse+force→不拒 ReasonVendorNoOfficial)。变异去 !has 早返证红。

补测后:gofmt/build/vet/quality-gate/全量 233 包全绿;新增判别测试变异证红 3 发(单元门)+ 1 集成编译核。codex 裁定「schema/money/默认翻转均无、anthropic 不削弱、apikey+force 不误拒」经我复核属实。
