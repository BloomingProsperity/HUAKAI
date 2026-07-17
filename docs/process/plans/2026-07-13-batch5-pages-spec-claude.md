# 第五批全站剩余页密度重构 Spec(38 页 · 11 个 codex 分派批)

审计已核(agent 逐页带部件计数):batch1-4 已完成 16 页;剩余 nav 可见页 38 个仍用旧原语
(裸 `<table>` / `hk-metric` 网格 / `hk-empty`)。**tenant_id 复核结论:本批无缺参风险**——
凡租户作用域的 admin 页 api.ts 已显式透传 tenantId(quotapolicies 的 `tenantQuery` 是范式);
backup/moduleregistry/routing/upstreammodels 四个端点为平台全局资源,天然无需 `?tenant_id`。

统一范式(与 batch2-4 相同):
- 裸 `<table>` → `src/ui/DataListTable`(列映射写纯函数 + vitest 变异证红);
- 指标网格 → `src/ui/StatCard`(状态→tone 同口径);
- `hk-empty` → `src/ui/EmptyState`;
- **展开行下钻表与游标"加载更多"分页保留页级自绘,不迁**;
- 财务/凭证/审计敏感页严格只动展示层,不改 api.ts、请求参数、业务逻辑、二次确认语义;
- 代码注释中文、报告中文、禁 commit;门=相关 vitest + `npm run build` 绿;浏览器真栈验收 PM 做。

## 分派批次(每批 2-4 页,工作量均衡)

| 批 | 页(路由 | 目录 | 待迁部件 | 工作量) | 约束 |
|---|---|---|
| B5-1 | /admin/route-rules routeadmin 1表+空态 低;/admin/groups groups 1表 低;/admin/tls-fingerprints tlsfp 1表+空态 低;/admin/channel-test-templates channeltesttemplates 1表+空态 低 | 热身批,全 1 表 |
| B5-2 | /admin/catalogs catalogs 2表+空态 中;/admin/channel-health channelhealth 1表+空态(状态徽标) 中;/models models 主表+RateVersionPanel(版本表可留下钻)+空态 中 | — |
| B5-3 | /admin/model-registry modelregistry 3表(策略/别名/覆盖)+空态+别名导入解析 高;/admin/moderation moderation 主表+Rules 2表+空态 中 | 重头批 |
| B5-4 | /admin/dlq dlq 主页1表+ObsDlqTab 1表+3空态 中;/admin/orphan-reconcile orphanreconcile 1表+空态 低(对账敏感只动展示);/admin/modules moduleregistry 1表+空态 低;/admin/cache cachemonitor 1表+空态 低 | — |
| B5-5 | /admin/billing-claims billingadmin Claims 2表+空态+RepriceCard 4指标格+1表 中;/admin/affiliates affiliateadmin 2表+3指标格+空态 中;/admin/disputes disputesadmin 1表+空态 低 | ⚠️money 敏感,仅迁展示 |
| B5-6 | /security audit 1表+空态;/admin/credential-renew credentialrenew 1表+空态;/admin/platform-credentials platformcredentials 2区共2表+5空态;/admin/backup backup 3指标格+1表 | ⚠️审计/凭证/备份敏感,不碰请求与逻辑 |
| B5-7 | /routing routing 1表+空态;/admin/model-sync upstreammodels 1表+空态;/admin/quota-policies quotapolicies 1表+空态;/admin/proxies proxies 1表 | routing/model-sync 全局端点无 tenant_id 属正常 |
| B5-8 | /wallet wallet 1表+6指标格+2空态 中;/affiliate affiliate 2表+4空态 中;/redeem redeem 1表+3空态 低 | ⚠️money 邻接,仅迁展示 |
| B5-9 | /orders orders 1表+空态;/subscriptions subscriptions 1表+空态;/my-groups megroups 1表+2空态;/trust trust 1表+2空态 | — |
| B5-10 | /available-channels availablechannels 1表+空态;/media-tasks mediatasks 1表+2空态;/notifications notifications 1空态;/profile profile PasskeyCard 2空态 | — |
| B5-11(可选尾批) | /playground playground 2表;/admin/version version 2空态;/admin/announcements announcements 1表+空态 | 低价值收尾 |

**跳过**:/rankings、/welcome(landing)——静态/公开壳外页。

验收基线:管理页用 huakai/huakai 登录 5176 真栈;有 seed 真数据的页(models/channel-health/
catalogs/groups 等)断言真值上屏;空资源页断言空态组件渲染而非白屏。
