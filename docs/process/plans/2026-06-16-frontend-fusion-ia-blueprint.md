# 前端融合布局蓝图(IA)+ 前后端一起补 执行计划 — 2026-06-16

**PM:** Claude (Opus 4.8)。**触发:** Owner「前后端一起做。一起补。定计划！我主要是不知道融合后这个布局和细小颗粒度怎么放」。

**一句话:** 布局不用纠结 —— HUAKAI 现有 30 路由骨架 ≈ sub2api + new-api 两个面板的融合;后端 20 域几乎全 rich;**真正缺的是每页的细颗粒深度(30 页只 3 页对齐过)+ 管理端几页没建(但后端已就绪)**。本计划把"颗粒度怎么放"落成一张可勾选的图,并按"前后端一页一页一起收口"排序。

> 调研已做(CLAUDE.md #16 三镜像 + #12 读源码 + #11 clean-room specifier lane):
> - **sub2api**(Vue3,账号中心/计费最全):user=dashboard/keys/usage/subscriptions/purchase/orders/monitor/redeem/affiliate/profile;admin=dashboard/ops/users/groups/channels(pricing+monitor)/subscriptions/accounts/announcements/proxies/risk-control/redeem/promo/affiliate(invites+rebates+transfers)/orders(dashboard+orders+plans)/usage/settings(多 tab)。
> - **new-api**(React19,网关/设置颗粒最细):user=dashboard/keys/profile/wallet(topup+transfer+redeem+sub)/usage-logs/subscriptions;admin=channels/models/redemption/users/**system-settings 8 大嵌套节**(Auth/Billing/Security/Operations/Content/Models/Site,每节几十旋钮)。
> - **CLIProxyAPI**:纯中继,**无任何前端面板(no-equivalent)** —— `~/refs/CLIProxyAPI/` 无 web/frontend/ui 目录,只有 management REST + OAuth 回执页。
> - **融合决策(既定,见记忆 sub2api-frontend-reuse-verdict):** 不 fork(Vue3/LGPL/数据层绑后端),用 HUAKAI 自有 Next.js15 + React18 + Tailwind v4 + shadcn 栈,只借 IA 重写。

---

## 1. 融合后的目标导航树(IA)

两端,沿用 HUAKAI 现有 Sidebar context-aware 切换(`/admin/*` 前缀自动换树)。来源标注:〔2〕=sub2api 借 IA,〔N〕=new-api 借 IA,〔H〕=HUAKAI 自有差异化。

### 用户端(customer portal)
```
概览 Dashboard            〔2〕〔N〕  余额/今日花费/Key数/quota窗/7日趋势/近期活动/签到
Playground 〔2〕 + 调试台 console 〔H〕  对话 + Embeddings/Images/Rerank
API Keys                 〔2〕〔N〕  ✅已对齐(搜索/筛选/排序/到期/按Key用量)
用量 Usage               〔2〕〔N〕  ✅已对齐(区间/粒度/模型Top/导出)
账户/安全/会话           〔2〕      profile 三拆(资料+2FA/IP+会话)
充值 Billing 〔2〕〔N〕 / 兑换 Redeem 〔2〕〔N〕 / 订阅 Subscriptions 〔2〕〔N〕
推荐 Affiliate           〔2〕      ❌缺页(后端已有 referrals/rewards/invitation-code)
通知 / 审计 / 定价        〔H 审计=信任链 Merkle,两参照都没有〕
```

### 管理端(admin portal)
```
运营总览 Ops             〔2 OpsDashboard〕  并发/切换率/吞吐/延迟直方/错误分布/告警/日志/全屏
用户管理 Users           〔2〕〔N〕  状态/分组/备注/余额史/2FA强关/动态属性筛选/批量
账号池 Accounts 〔2〕 + 凭证代理 Credentials 〔2〕  后端超深(OAuth获取流/paste/cli/csv/json导入/轮转/健康/批量)
渠道健康 Channels        〔2 pricing+monitor〕〔N weight/priority/test/balance〕
—— 以下管理页:后端 rich,前端缺/桩(最高 ROI)——
订阅计划管理 SubPlans    〔2〕〔N〕  ❌缺(后端 plans CRUD + assignments + extend/revoke)
支付/订单 Orders         〔2 orders+dashboard+plans〕  ❌缺(后端 admin payments 全套 + 退款审批)
兑换码/代金券 Vouchers   〔2 redeem〕〔N redemption〕  ❌缺(后端 vouchers CRUD)
公告 Announcements       〔2〕  ❌缺(后端 admin announcements CRUD)
告警规则 Alerts          〔H〕  ❌缺(后端 alert-rules/events/silences)
内容审核 Moderation      〔N sensitive-words〕  ❌缺(后端 keywords/hashes/config/logs/ban)
代理池 Proxies           〔2〕  ❌缺(后端 /admin/v1/proxies CRUD;运维开关 A-1 的 fallback_mode 也在这设)
平台设置 Settings(嵌套)  〔N system-settings 8 节〕  🟡浅(后端 platform-settings+email+billing+quota+pricing+tls-profiles 全有)
审核系统 System 〔H〕 / Hermes 助手 〔H 桩,后端已 rich〕
```

---

## 2. 每页颗粒度状态(current → target)

| 页 | 现状 | 后端 | 缺口 | 档 |
|---|---|---|---|---|
| API Keys / 用量 / Playground | ✅ 细对齐(7865d6ed) | rich | Keys 可加 per-key rate/quota controls UI(后端已有) | A |
| 概览 Dashboard | full ~398L | rich | 对照 sub2 补 token 分型(in/out/cache-read/creation)+ cache 命中率卡 | B |
| 充值/兑换/订阅(用户) | full | rich | 充值多网关 tab + 订单史;wallet 转账(new-api) | B |
| 账户/安全/会话 | full | rich | IP 白名单真接线(现占位);account-binding SSO | B |
| **推荐 Affiliate** | ❌ 无页 | rich | 整页新建(链接/邀请表/返佣/提现) | B |
| 运营总览 Ops | ~300L 浅 | rich | 对照 sub2 OpsDashboard 补并发/延迟直方/错误分布/全屏 | B |
| 用户/账号池/凭证/渠道 | full 但浅 | **超 rich** | 把后端已有深能力接上来(导入流/轮转/健康/批量/测试) | A→B |
| **订阅计划/订单/兑换码/公告/告警/审核/代理** | ❌ 缺或桩 | rich | **整页新建**,纯接后端 | A(高ROI) |
| **平台设置(嵌套)** | 🟡 1 页浅 | rich | 建 new-api 式嵌套设置树(Auth/计费/安全/运维/内容/模型/站点) | C(大) |
| Hermes / mimicry | 桩/mock | Hermes已rich;mimicry=TLS profiles已rich | 接线(mimicry → tls-fingerprint-profiles) | B |
| 审计 Audit | full | rich | HUAKAI 差异化,保持 | — |

档:**A=纯接后端已有(最快,前端为主)**;**B=前端补深 + 个别后端小补**;**C=大块结构(设置嵌套树),单独切**。

---

## 3. 缺口分级(决定先后)

- **T1 最高 ROI — 后端 rich、前端缺整页,纯接线就能上**:订单 Orders、兑换码 Vouchers、公告 Announcements、~~代理池 Proxies~~(✅ 已完成,见下)、订阅计划 SubPlans、告警 Alerts、审核 Moderation。→ 这些是"管理端一半的盘子",建一页就点亮一块,几乎不碰后端。

> **⚠️ 校正(2026-06-16,做代理页时发现):** 代理池**并非缺页** —— 它原本被**捆绑**在「凭证与代理」页的一个 tab 里(测绘 agent 没扒出来),已提升为独立 `/admin/proxies`(commit `7617f7ce`,凭证页 1190→729 行)。**教训:本清单的其余"❌缺页"项可能也被捆绑在别处**(如订单/告警可能藏在某管理页内),每页开工前先 grep 确认"是真缺、还是被捆绑",别凭这张表直接判缺。
>
> **进度:** Wave-T1 ① 代理池 ✅(`feat/frontend-admin-proxies` / `7617f7ce`,tsc 绿,后端 21 测试函数兜底,E2E 接线 + fallback_mode 待跟进)。
- **T2 接深 — 已有页但浅,后端深能力没surfaced**:账号池/凭证(导入流/轮转/健康/批量)、Ops 总览、用户管理、渠道。
- **T3 用户端补齐**:Affiliate 整页、Dashboard 分型、充值多网关、IP 白名单接线。
- **T4 大结构**:平台设置嵌套树(new-api 8 节);Hermes/mimicry 接线。

---

## 4. 执行序列(前后端一页一页一起收口)

原则:**一次一页,纵切到底**(页面 + API client + 接线测试断言 + 该页后端若有缺口一并补),做完即 commit+push,绝不并行铺开(这就是之前"东一榔头"的解药)。每页沿用 [api-keys/usage/chat] 的对齐范式 + `frontend_wiring_test.go` 扩断言。

1. **Wave-T1(管理端点亮,纯接线)** — Orders → Vouchers → Announcements → Proxies → SubPlans → Alerts → Moderation。每页:列表+筛选+CRUD+详情,接已有 `/admin/v1/*`。
2. **Wave-T2(接深)** — 账号池/凭证把后端导入/轮转/健康/批量接上;Ops 补图;Users 补动态筛选+批量。
3. **Wave-T3(用户端补齐)** — Affiliate 新页;Dashboard token 分型;充值多网关+订单史;安全 IP 白名单接线。
4. **Wave-T4(大块)** — 平台设置嵌套树(单独 spec);Hermes 聊天 UI 接线;mimicry → TLS profiles 接线。

**前后端"一起"在哪体现**:Wave-T1/T2 后端已就绪 → 前端接线 + 接线测试(后端基本不动);真正前后端同补的少数点(Keys 的 IP 白名单、用量的 per-platform 成本拆分、Playground 工具调用/多模态、mimicry CRUD)集中在 T3/T4,届时同一切片里前后端一起改。

---

## 5. 验收 / 纪律
- 每页切片:`go build`/`vet`/`codebudget` + `frontend_wiring_test.go` 新断言(真后端 E2E),前端 `tsc --noEmit`。
- clean-room:借 IA 不抄组件名/源码;参照按 specifier lane 已读,prose 已 paraphrase。
- 不并行多页;每页 commit+push 后再开下一页(单 PM 节流,治"晕")。
- money 路(订单退款/订阅计费)仍 Owner gate;schema 变更高危单列。

---

## 6. 决策点(待 Owner)
- **D1 起点**:先打 Wave-T1 哪一页?建议 **Orders(订单)** 或 **Proxies(代理池,顺带把运维开关 A-1 的 fallback_mode 暴露到 UI)**。
- **D2 设置树(T4)**:是否照 new-api 8 节铺?还是先合并成 3-4 节够用?(大块,影响工期。)
- **D3 节奏**:你要每页都过你一眼,还是 Wave-T1 整波我连做完一起给你看?
