# 片2f 弧:codex-cli 全局加固层(黑白名单/版本门/引擎指纹/app-server)— Claude 计划草案

日期 2026-07-07。Owner 指令「他们(sub2api)做了肯定有道理,我们也做」。在片2e 每账号 codex_cli_only 开关之上,叠一层**全局**(对所有 codex 账号统一)的准入加固策略。

## §16 形态清单(亲读 sub2api 源码,HEAD 87dfc66)
sub2api 把 codex-cli 限制分两层:账号侧只有开关本身(= HUAKAI 片2e 已建);黑白名单/最低版本/指纹门/app-server 全在**全局设置**。检测入口 `OpenAICodexClientRestrictionDetector.Detect`(backend/internal/service/openai_client_restriction_detector.go:74)门控顺序(每步可短路):
1. 账号未开 codex_cli_only → 不限制(Disabled)。
2. 全局 force_codex_cli → 旁路放行(ForceCodexCLI)。
3. **黑名单**命中 → 立即拒(OR 宽 deny,门内 deny 最先)。
4. **身份候选**:官方 UA(IsCodexOfficialClientRequestStrict)/ 官方 originator / **全局白名单**(双因子 AND)/ **app-server 闸**(全局开关 OR 账号开关);都不命中 → 拒(NotMatchedUA)。
5. **版本门**(仅官方候选):ParseCodexEngineVersion 必须可解析(否则 VersionUndetectable);< Min → TooLow;> Max → TooHigh(semver 比较)。
6. **引擎指纹 AND 硬门**:EvaluateEngineFingerprint 按 EngineFingerprintSignal 列表勾选 AND(行内变体 OR);无勾选信号=门关放行;白名单条目可显式 SkipEngineFingerprint。

数据结构(backend/internal/pkg/openai/allowed_client.go:11):
- `AllowedClientEntry{Originator string; UAContains []string; SkipEngineFingerprint bool}`。白名单=双因子 AND(originator 精确等值 + 每个 UA marker 都出现),防伪造;黑名单=OR 宽 deny(任一字段命中即拒),挡可疑。**非对称是刻意的**(allow 严防伪造、deny 宽挡可疑)。空/含空白 marker 的白名单条目安全失败(永不命中)。
- `EngineFingerprintSignal`(engine_fingerprint_signal.go:15)——指纹信号(勾选 AND / 变体 OR)。默认种子只勾 x-codex-。

全局设置键(sub2api domain_constants.go:417-427):blacklist / whitelist / min-version / max-version(推断)/ allow-app-server-clients / engine-fingerprint-signals。

## HUAKAI delta(架构/算法/生态)
- **已有**:片2e 账号开关 `AccountInfo.CodexCLIOnly` + `officialclient.GateDecision`(片2d);`clientid.Detect→IdentityCodexCLI`(官方 UA/x-client 检测);`platform_settings` 通用 KV 表(迁移 0077,挂新键不触 schema,但需加进 internal/platformsettings 允许清单)。
- **新增(算法升级)**:AllowedClientEntry 黑白名单匹配(双因子 AND / OR 宽 deny 非对称)、ParseCodexEngineVersion(从 UA 解析 codex 引擎版本)+ semver 比较、引擎指纹信号 AND/OR 评估。
- **新增(架构升级)**:CodexRestrictionPolicy 全局策略快照(从 platform_settings 解析注入)+ 检测器接进入站门(与片2e 账号开关 AND 组合)。
- **新增(生态升级)**:admin 读写这些全局 setting 键(平台级运维配置);拒因(reason)可观测。

clean-room:不逐字抄 sub2api 标识符/结构顺序;HUAKAI 自定类型名与门控函数,paraphrase。

## 默认口径(Owner 哲学「默认全开、控制交运维」)
**全部门默认关/放开**:空黑白名单、无 min/max 版本、无指纹信号要求、app-server 默认放行——out-of-box 开了账号开关(片2e)只要求「官方 codex 客户端」(= 片2d/2e 现状),全局加固各门只在运维显式配置后才收紧。运维 opt-in 每道门 = 给运维能力、不是我们硬卡。非默认翻转(默认=现状)。

## 切片拆分(建议序,每片独立过门 + 变异 + 审查)
- **片2f-1 骨架 + 黑白名单**:CodexRestrictionPolicy 结构 + AllowedClientEntry(HUAKAI 自定)+ 双因子 AND / OR deny 匹配 + platform_settings 键(blacklist/whitelist)+ 允许清单 + 接进门(账号开关 AND 全局策略)。默认空=放行。
- **片2f-2 版本门**:从 UA 解析 codex 引擎版本 + semver 比较 + min/max 键。默认无界=放行。
- **片2f-3 引擎指纹门**:EngineFingerprintSignal + AND/OR 评估 + 信号列表键 + SkipEngineFingerprint。默认无信号=放行。
- **片2f-4 app-server 闸 + force 旁路**:全局 app-server 开关(OR 账号级)+ force_codex_cli 旁路。默认 app-server 放行(与片2e IsCodexCLIOnlyAppServerAllowed 组合)。

## 爆炸半径 / 风险 / 决策点
- 触入站鉴权门(auth 邻域)但**默认=现状**(非默认翻转)、**不触 schema**(platform_settings KV)、不触 money。
- 风险:双因子 AND 白名单若配置退化(空 marker)会静默失效→安全失败设计(永不命中)必须复刻;deny/allow 非对称语义必须保。版本解析容错(未知 UA→VersionUndetectable 拒,仅官方候选)。
- **决策点(surface Owner)**:引擎指纹门是不是靠近你排除的「指纹」类?——澄清:这是**校验入站客户端指纹以拒伪造**(准入控制),不是**出站伪装轮换指纹绕 WAF**(你排除的 5 项之一)。方向相反,属正当准入。若你认为仍不做指纹门,片2f-3 可砍,只留黑白名单+版本门。

## 成本
中-大(4 片)。骨架+黑白名单 ~1.5 人日;版本门 ~1 人日;指纹门 ~1.5 人日;app-server+force ~0.5 人日。

---

## #10 平行综合(Claude + Codex,2026-07-07)
两份独立计划(见 -codex.md)**高度收敛零冲突**:同用 platform_settings KV 不触 schema、同默认全开(空配=当前行为)、同 AND 组合(账号 CodexCLIOnly=false→整层不生效)、同门控短路序、同黑白名单非对称语义、同「指纹门=入站准入非出站规避」判断(codex 独立确认不属排除 5 项)。

**采纳 codex 三点精修**:
1. **独立 detector 包**:不塞进 officialclient.GateDecision(避免它从"官方身份检测"膨胀成"平台策略执行器",§13)——新建 `codexclientaccess` 包 `Evaluate(...)`,接在 enforceOfficialClient 后作第二层。
2. **快照 keep-last-valid**:platform_settings 解析成不可变快照(atomic.Value),**解析失败保留上一份有效快照 + 运维告警**,不静默放宽;首启无有效快照用安全默认。min>max=配置错拒绝发布快照。
3. **更细切片(7 片)**:骨架快照(2f-1)独立于黑白名单(2f-2),gateway 接线+审计(2f-6)、测试(2f-7)独立。采纳此粒度。

**最终切片序**(默认全开,指纹门按 Owner 已批「都建」但排最后):
- 片2f-1:codexclientaccess 包 + CodexClientAccessPolicy 快照 + platform_settings 键 + 允许清单 + keep-last-valid 加载。默认空=放行。
- 片2f-2:黑白名单(双因子 AND / OR 宽 deny + 安全失败)+ 接进 detector。
- 片2f-3:版本门(UA 解析 codex 引擎版本 + semver + min/max)。
- 片2f-4:app-server 闸 + force_allow 旁路。
- 片2f-5:引擎指纹门(排最后;Owner 已「都做」+ codex 独立确认属入站准入非排除项;若 Owner 临时叫停可砍)。
- 片2f-6:gateway 接线 + 拒因审计/指标(接进 enforceOfficialClient)。

**默认值**:blacklist/whitelist/signals=[],min/max=空,allow_app_server=true,force_allow=false。

## §17 冷却↔计费配合(片3a 附带核查,2026-07-07)
借片2f 起步前,并行核了片3a 冷却在「429→换号」的模块配合(调研 adb72d7f + PM spot-check degradeFailureIfAbortFailed attempt.go:169-182):**hold 释放与并发槽释放在 abort Tx2 内原子完成;abort 失败经 degrade 门禁掉 SwitchAccount/重试→"旧hold未释+新hold又扣"不可达;冷却写与 abort 解耦、写失败不影响 hold 释放。无漏钱/冻钱/双扣/槽泄漏。** 唯一 S3=raw 路径 applyAccountCooldown=false vs canonical/streaming=true 的冷却激进度非对称(pre-existing 非片3a 回退、仅 HUAKAI_DISPATCH_HCSF=0 非默认路径、非钱非泄漏),defer。
