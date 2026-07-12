# HUAKAI 上线就绪说明(运营者视角)

> 本文是**上线总览**:系统能做什么、上线前确认什么、上线后怎么运维、法律边界在哪。
> 具体起栈步骤见 [production-bootstrap.md](./production-bootstrap.md);本文只讲"上线该知道的全貌"。
> 涉及 deploy/prod 的实际改动按仓库规则属 Owner-gated——本文是运营对照,不代表已执行部署。

最后更新:2026-07-12(前端全设计+接线波闭环后:用户/运营两面 UI 补齐 + 邮件模板 + 运行日志入库)。

---

## 1. HUAKAI 是什么 / 卖什么

HUAKAI 是一个 **clean-room、MIT 许可的 API 中转站(relay)**:把一批上游大模型账号(Claude / OpenAI / Gemini / 国内六厂 / …)聚成池 → 签发可转售的 API key → 网关转发请求 → 按用量计费/配额/账本记账。

- **核心是 relay 链**:账号池 → key → 网关转发 → 计费。支付/充值是**周边商业模块**,手动 admin 充值已可替代真支付,真支付 provider 是可选增强、非上线前置。
- **单租户多用户 SaaS**:自跑一个实例对外卖 API 额度。
- **运维控制台 UI 随镜像可用**:React SPA 在发货镜像里由 vite build → `go:embed`(-tags embed)打进同一个二进制,运行时网关 `webui.Handler` 直接服务(无需外挂前端/静态反代)。首次运营用 **bootstrap 管理员令牌 + admin API**;拿 admin 身份后 UI 与 API 皆可运营。

## 2. 上线前必确认(三道生产启动门)

`HUAKAI_RELEASE_MODE=production` 下 gateway 启动期 fail-loud 自检,任一不过即拒启(有意的安全护栏,详见 bootstrap §3):

1. **数据库迁移已应用**:compose 的 `migrate` one-shot 在 gateway 前把 `sql/migrations` 应用到库;裸二进制单实例可设 `HUAKAI_AUTO_MIGRATE=true` 进程内自迁移。
2. **审计就绪**:`HUAKAI_AUDIT_LEDGER_BACKEND=postgres` + `HUAKAI_AUDIT_PRIVATE_KEY_PATH` 指向有效 ed25519 私钥。
3. **email 门(默认已软化)**:未配 SMTP 的租户注册按"验证关闭"放行;设 `HUAKAI_REQUIRE_EMAIL_GATE=true` 恢复严格。

密钥四件(`.env.prod.example` 顶部有生成命令):`HUAKAI_CREDENTIAL_KEY_B64` / `HUAKAI_SESSION_SIGNING_KEY_B64` / `HUAKAI_ADMIN_BOOTSTRAP_TOKEN`(hk_admin_<24base32>,首用后轮换)/ `POSTGRES_PASSWORD`。

## 3. 默认行为汇总(尤其"默认开"的执行器)

以下 knob 均**默认开、留 env 逃生阀**。判据:正确性兜底/常在执行器默认开(对齐 new-api/sub2api 成熟做法),纯性能旋钮才默认关。**运维应知道这些默认开着**:

| knob | 默认 | 作用 | 关掉的后果 |
|---|---|---|---|
| `HUAKAI_QUOTA_RECONCILER_ENABLED` | **开** | 补偿结算/释放失败、清扫孤儿预留 | 结算/崩溃窗口产生的预留永久卡 reserved,冻结额度→客户被误限流(429) |
| `HUAKAI_ALERTING_EVAL_ENABLED` | **开** | 告警规则评估循环(无规则时空转 no-op) | 配了告警规则却不评估,静默失效 |
| `HUAKAI_CONTENT_MODERATION_ENABLED` | **开** | 内容审核执行器(**租户级配置默认 Enabled=false,未配置租户仍放行**) | admin 在面板开了审核也不生效 |
| `HUAKAI_TRANSPORT_FORCE_H1` | **开** | 出口锁 HTTP/1.1(Go-native uTLS 只广告 h1) | 仅换 BoringSSL sidecar 能自洽 h2 时才关 |
| `HUAKAI_BUDGET_FAIL_CLOSED` | 关(fail-open) | budget 基础设施故障时是否拒绝 | 设 true=额度强一致但故障可能误拒 |

## 4. 接入上游账号(含国内厂 key 的两种形态)

上游厂商已注册 39 个 protocol family(Claude/OpenAI/Gemini/国内六厂/…),models 表 CHECK 已放行全部已注册 family(迁移 0172)。接账号经 admin API:provider → pool_group → channel → provider_account → credential → model_binding → alias。

**国内厂 key 有两种接入形态,别混:**

- **官方直连(api_key 类型)**:厂商官方 key,走适配器**内置默认端点**(如豆包=`ark.cn-beijing.volces.com`、混元官方=`api.hunyuan.cloud.tencent.com`、通义=`dashscope.aliyuncs.com` 国内站)。凭据类型 `api_key`,**不吃 base_url 覆盖**(有意设计:官方 key 必须打官方端点防误发)。国内六厂默认端点已对齐国内站(定价普遍更便宜)。
- **第三方中转(upstream_passthrough 类型)**:经中转商(如某些 TokenHub 类中转)拿的 key,只在中转商端点有效、打官方端点会 401。凭据类型 `upstream_passthrough`,`Value` 存 **完整 `Bearer <key>`** 串,`provider_accounts.upstream_static.base_url` 填中转端点——此时端点覆盖生效。

> 实测佐证(2026-07-05 双厂真实上游 e2e 全绿):豆包官方 key + 混元 TokenHub 中转 key 两条链路,网关转发→计费 claim committed→quota 结算→并发槽释放全部端到端跑通。

## 5. 运维可见面(本轮新增)

- **配额对账 worker**:自动跑(§3),补偿卡死预留;`/v1/admin/notifications/worker-stats` 含 pending 计数。
- **usage 补价(Manual-First)**:`POST /admin/v1/billing/reprice`,`dry_run` 默认 true 只出差额报告;apply 追加对账事件、**绝不自动动余额**(追扣/退款由运维走既有 admin 余额调整人工决定)。
- **obs 死信管理**:`GET /admin/v1/obs-dlq` 列表 + `POST /admin/v1/obs-dlq/{id}/replay` 重放(platform_admin,原子状态机防双重放)。
- **media 孤儿对账**:掉租/超时的媒体任务落孤儿线索,admin reconcile 面处置;追扣成功账本一致(claim committed),已释放 hold 的孤儿标注 `hold_released_needs_manual_charge`(真实新扣款属人工决策)。
- **运维广播受众隔离**:账号故障/告警广播只发 admin 角色;客户只收自己的事件(如低余额)。
- **持久结算意图(默认关,`HUAKAI_SETTLEMENT_INTENT_ENABLED`)**:一条与主账本平行的"意图→交付→结算"证据链,采 **fail-open 旁路**——写意图失败/超时/panic 绝不阻塞主结算(billing_ledger_claims 始终权威)。开启后:①阶段 1 记录每笔请求的意图生命周期(pending→delivering→settling→settled/aborted),便于对账取证;②阶段 2 后台 sweeper 兜底 fail-open 漏标——扫描非终态且陈旧的意图行,按权威 claim 追平(committed→settled 复制权威金额、aborted→aborted、在途 reserving 跳过、claim 已进入更高 attempt 则旧意图 superseded),守卫式 CAS 保证多副本单胜者、attempt proof 防止拿新尝试金额冒充旧证据。真上游 E2E 账目零漂移(7=7=7=7),真 PG + `-race` 并发实测通过。**默认关**:仅取证/对账增强,核心结算不依赖它。

## 5b. 用户/运营两面 UI(2026-07-12 波,全接线全测试)

本波把「后端有、前端没建」与「三镜前端有、我们没有」的缺口全部收口(细节见
`docs/process/plans/` 当日计划与各 commit 正文);全部经 tsc 0 错 / vitest 全绿 /
生产构建 / OpenAPI 一致性 / 变异判别测试:

**用户侧(卖 Key 的开箱体验)**
- **用量可视化对齐 Claude/Codex 官方形态**:配额窗口小方格进度条(24 格 + 重置倒计时)、
  费用/缓存双日历热力图、缓存命中率格条;Key 级分析(30 天窗、费用条、token 汇总)。
- **逐笔明细**:每请求端到端时延列、状态、流式终止原因、verify_hint 信任链入口;
  `GET /v1/generation?id=<request_id>` 单笔下钻。
- **免登录凭 Key 查用量**:公开页 `/key-usage`(Key 即凭证,无会话也可查)。
- **接入指引页** `/integration`:Claude Code(ANTHROPIC_BASE_URL)/ OpenAI SDK / curl
  三形态配置一键复制,site_api_base_url 自动注入。
- **项目管理 / 配额探针 / 缓存 TTL 设置**:project_id 管理、5h/周配额窗口探测
  (sub2api 同口径)、Anthropic 缓存 5min/1h 档运行时开关(设置键,默认关)。

**运营侧**
- **账号池**:健康聚合巡检(`/admin/v1/provider-accounts/health-summary`,
  needs_attention 口径=非 healthy 或禁用)、账号最近请求明细、7 天会话窗利用率、
  临时不可调度规则编辑回填、凭证 project_ref 解析。
- **鉴权邮件模板**(§sub2api 跟法):四类鉴权邮件(验证/重置密码/新设备确认/补邮箱验证码)
  主题+HTML 正文租户级自定义,零 schema(存邮件设置 k/v);`{{占位符}}` 渲染,
  **fail-safe 三重回退**(store 读错/覆盖为空/未知占位符→内置默认),模板问题绝不阻断
  auth 邮件送达;管理端 PUT templates + 预览端点(样例值纯渲染);设置中心「邮件」tab 聚合编辑器。
- **运行日志入库+查询**(§sub2api ops_system_logs 跟法):两栈(zap+slog)warn+ 异步
  批量入库(表 ops_runtime_logs,迁移 0180),fail-open 铁律(队列满/DB 故障/panic 只
  丢弃计数,绝不反压业务);admin 键集分页查询(level/component/**request_id** 过滤)+
  cleanup 保留策略 + sink 健康观测;前端「日志与诊断」页实时轮询(3s 增量合并)面板。
  三镜均无服务端推送日志流,轮询即业界形态。
- **DeepSeek 缓存命中计价修复**:命中价=同版本 input 1/10(迁移 0179)。

## 6. 能力边界与法律免责

HUAKAI 对标成熟中转站给**同等能力**,能力默认全开、控制权交使用者;运营者对如何使用这些能力、以及由此产生的与上游厂商服务条款/当地法律的关系,**自行负责**:

- **上游账号合规**:接入的上游账号(官方 key / 第三方中转 key)是否符合该厂商服务条款、是否有转售授权,由运营者自负。HUAKAI 只提供转发与记账,不代表对任何上游的授权背书。
- **订阅反转车道(experimental,默认关)**:除官方 key / 第三方中转外,HUAKAI 支持把**个人订阅的 OAuth 凭据**反转成可转发上游(如 Antigravity/Gemini Code Assist 走 cloudcode-pa、ChatGPT/Codex session、Claude session 等),逐 family env-gate、**默认全部不注册**,部署方须显式 opt-in 才构造对应 adapter(env 形如 `HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER=true`,cursor/copilot/kiro/windsurf/gemini-advanced 等车道同构;凭据经 CLI 导入解析后由 credentialworker 自动纳入 refresh 健康探测)。这类车道属"给能力非控制":HUAKAI 提供 OAuth 刷新 + 转发的温和实现(仅刷 token、按客户端既有形态转发,**不做**设备指纹/机器码重置/关联封规避等激进 ban-evasion),**个人订阅是否允许这样反转、是否违反该服务的用户协议,由运营者自负**。凭据 AES 信封加密存储,刷新走 SSRF 防护出口。
- **TLS 出口伪装**:sidecar 的 BoringSSL 指纹伪装**默认关**;Go-native 出口是温和 uTLS(仅 ClientHello 姿态,锁 h1)。HUAKAI **不做**激进 ban-evasion(逐请求指纹轮换/设备码伪造/软限流规避等——见项目"不做清单")。
- **内容审核**:审核执行器默认开但租户配置默认关(§3);是否对客户流量启用审核、按什么标准,由运营者按其合规义务配置。
- **数据与隐私**:accesslog 只记 URL.Path、绝不记 query/body/headers/credentials;凭据 AES 信封加密(AAD 绑租户);审计链 ed25519 签名。运营者仍需按其司法辖区履行数据保护义务。
- **转售定价与计费**:计费按 token×价表×倍率;价缺失时 fail-closed 拒绝(不 0 价白吃),ratio 冷启故障 fail-open→1.0+pending 标记待人工补价(§5)。

> 本项目开源不牟利;部署即代表运营者接受:HUAKAI 提供能力而非合规保证,一切使用后果由运营者承担。

## 7. 已知未覆盖 / roadmap(不阻塞上线)

> **上线前 S1 核实(2026-07-11)**:曾 surface 的 money/security/schema S1 已逐项核当前真码,
> **无未修 blocker**——media 计费白吃 / 配额退款不冲减 / request_id 丢账三项真 money 缺陷均已修
> 并接线生产,详见 [../process/reviews/2026-07-11-pre-launch-s1-verification.md](../process/reviews/2026-07-11-pre-launch-s1-verification.md)。两条**运营约束**须知:
> - ⚠️ **多版本密钥环落地前,禁止轮换 `HUAKAI_CREDENTIAL_KEY_ID` / `_KEY_B64`**:单版本 KEK 下轮换
>   会使旧密文解不开(凭证全瘫);已有启动期 fail-closed 自检兜底,但正确做法是等多版本密钥环切片
>   落地再轮换。不轮换即安全。
> - 工具按次附加费(OpenAI Responses/Gemini 服务端 web_search 等)当前保守计零(**少收非漏账**,
>   主流量 Anthropic 已全覆盖),稳定上游 usage 信号可用后接入。


- 真支付 provider 接入(手动 admin 充值可替代)。
- admin 用量列表按 request_id 过滤(触 billing sqlc 生成码漂移面,defer;运行日志表
  自身可按 request_id 查,用户侧 `/v1/generation?id=` 已可单查)。
- 运行日志自动清理 worker(当前靠 cleanup 端点手动/外部调度);chi access-log 的
  X-Request-Id 与计费链 logical_request_id 尚无关联(两套 ID,查日志用后者)。
- S3-4 二级:退款↔sweep 竞态的冲减备忘重试(job_kind 迁移)——一级可观测已上,视数据积累决定是否做。
- Hermes 提议-确认改动链默认关(`HUAKAI_HERMES_LLM_PROPOSE_ENABLED`);confirmCache 进程内,多副本需 sticky 路由(已加 re-propose 逃生阀)。
- 存量英文注释逐步转中文(分批工程,不影响功能)。
- 前端运营台页面级设计仍 Owner-gated 逐页确认。
- 持久结算意图 / 订阅反转车道默认关,翻 on 属 Owner-gated 的默认行为翻转;结算意图的在途续期 bump / leader 选主减重复扫描 / 运维人工裁决悬挂意图 UI,Antigravity 的 GOOGLE_ONE_AI credits 计费与额度语义 + 403 封号态建模,均属后续增强切片(不阻塞上线)。
