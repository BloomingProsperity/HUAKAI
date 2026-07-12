# 片2f 弧:codex-cli 全局加固层 — Codex 独立计划(specifier lane)

> #10 平行:codex 未见 Claude 计划、独立起草。留档(轻编排)。UTC 2026-07-07。

## 总判断
做成 HUAKAI 自有的入站客户端准入策略快照,不搬 sub2api 结构/命名/字段。

## 1. 策略读取/解析/缓存/接线
- 新增内部策略对象 `CodexClientAccessPolicy`,从 `platform_settings` 读入解析成**不可变快照**;运行时网关只依赖快照,不在请求路径解析 JSON/访问 DB。
- 键(HUAKAI 命名空间,加进 internal/platformsettings 允许清单,不触 schema):`codex_client_access.{blacklist,whitelist,min_version,max_version,allow_app_server,engine_fingerprint_signals,force_allow}`。
- 缓存:启动加载 + 变更刷新 / 短 TTL;`atomic.Value` 只读快照。**解析失败不静默放宽**——保留上一份有效快照 + 运维告警;首启无有效快照用安全默认。
- **不塞进 officialclient.GateDecision**(否则从"官方身份检测"膨胀成"平台策略执行器");新建 detector/executor `codexclientaccess.Evaluate(...)`,接在 gatewayhttp enforceOfficialClient 附近,作片2e 账号开关之后的第二层。
- AND 组合:账号 CodexCLIOnly=false→整层不生效;=true→先官方门再全局层;force_allow=true 只旁路全局层,不关账号开关存在性检查。

## 2. 切片(7 片)+ 默认口径(默认全开,空配=当前行为)
| Slice | 范围 | 默认 |
|---|---|---|
| 2f-1 策略快照+settings键 | typed policy/解析/缓存/允许清单/默认值测试 | 无配置不影响现有流量 |
| 2f-2 黑白名单 | deny OR / allow 双因子 AND / 安全失败 | 黑空=不拒;白空=不额外放行 |
| 2f-3 版本门 | UA 解析 codex 引擎版本 + min/max semver | 未配=不检查;配后官方候选不可解析=拒 |
| 2f-4 app-server 闸 | app-server 身份候选开关 | allow_app_server=true 默认 |
| 2f-5 引擎指纹门 | 信号 AND/变体 OR/白名单跳过 | 信号空=放行;配后缺信号=拒 |
| 2f-6 gateway 接线+审计 | 接 enforceOfficialClient + 拒因+日志/指标 | 不翻转账号开关,仅增强已开账号 |
| 2f-7 判别+变异测试 | 单元/handler/配置解析/误配保护 | 无外部 runtime 依赖 |

默认值:blacklist/whitelist/signals=`[]`,min/max=空,allow_app_server=true,force_allow=false。

## 3. 关键语义
- 版本门:仅官方候选;配了 min/max 时 UA 不可解析=拒(不当通过);semver 规范比较;min>max=配置错拒绝发布快照。
- 白名单双因子 AND:originator 精确 + 所有 UA marker 在;空/空白 marker 安全失败永不命中;多条 OR;命中只成候选不自动跳版本门(除非条目显式跳指纹)。
- deny/allow 非对称:黑名单宽 deny,白名单窄 allow。固定短路序:账号开关→force_allow→黑名单→候选→版本→指纹→放行。

## 4. schema/money/默认翻转
不触 schema(platform_settings)/不触 money;不改普通账号与 CodexCLIOnly=false 账号行为;对 CodexCLIOnly=true 账号新增约束需分阶段+审计,空配=当前行为(可选 shadow/audit 模式)。

## 5. 判别测试/变异点(每片)
2f-1 缺 key 用默认、malformed 不换快照、未知 key 被拒、min>max 失败(变异:解析失败改空策略放行→红)。2f-2 黑名单任一命中拒/白名单缺一因子不过/空白 marker 永不中(变异:AND→OR→红)。2f-3 未配不检查/低拒高拒/范围内过/不可解析拒(变异:不可解析放行→红)。2f-4 allow 开关/黑名单优先(变异:app-server 绕黑名单→红)。2f-5 空放行/缺信号拒/变体 OR/白名单跳过(变异:AND→OR→红)。2f-6 账号未开不拒/开则黑名单拒/force 只旁路全局/拒因可观测不泄配置(变异:全局对所有账号生效→红)。

## 6. 引擎指纹门判断
**不属 Owner 排除的「逐请求指纹轮换绕 WAF」**。区别:入站校验(判请求是否像允许的 codex 客户端,不帮出站伪装)vs 出站规避(主动轮换指纹绕第三方 WAF)。本方案不生成/轮换伪装指纹、不发绕过 header,只拒伪造客户端。风险记录:指纹信号不写成"教用户伪造官方客户端"的文档;日志/UI 不完整回显敏感匹配细节(只展示规则名/启停/命中计数)。

## 爆炸半径/风险/成本
最大风险=误杀真 codex-cli(尤其版本解析不兼容新 UA);次=配置错致全局拒(旧快照保留+校验+审计缓解);三=策略逻辑塞既有大包(拆独立小包 gateway 只接线)。成本:快照+settings 0.5-1 天,黑白名单+版本门 1-1.5 天,app-server+指纹门 1 天。
