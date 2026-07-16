# 2026-07-16 三层分发与委托管理调研（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “去查一下，像这种分发的模式是什么样的，类似于发卡网那种出现下级代理的模式；再看我们实际代码里面有没有这一块的东西，记得当时写这一块的时候调研过。” |
| Scope | 调研“部署者/平台方 → 多租户使用者/下级代理 → 最终用户”的成熟权限与分发模型；核对 HUAKAI 的用户会话、面板角色、admin token、tenant scope、邀请、订阅、分组、代理、佣金/返利、配额、凭据和账号池源码。只做研究与问题记录，不修改鉴权、schema、资金或生产行为。 |
| Success criteria | 明确三类主体各自拥有、可委托和不可越过的权限；区分 reseller、tenant admin、staff、end user；找出 HUAKAI 已有基础、冲突接线和完全缺失能力；给出至少两种可落地模型及推荐方案，说明迁移、审计和安全代价。 |
| Time estimate | 约 2-4 小时源码与官方资料核实；本轮先形成第一版结论。 |
| Blast radius | 本轮只读源码并更新调研文档。后续若采用方案，可能触及鉴权核心、用户角色、管理会话、授权表、收益/账务和 OpenAPI，必须另行确认。 |
| Failure modes | 把“租户管理员”误当“代理商”；把面板角色当真实授权；只看角色名不看调用链；把部署者跨租户能力继续留给 session admin；把普通用户升级路径和代理授权混在一起；照搬电商分销的资金模型却忽略 API 网关账号池秘密边界。 |
| Decision points | 本轮不做高风险修改。需要 Owner 后续决定：是否存在可转售/下级代理层；代理是否能创建子租户；收益是否来自加价、佣金或额度批发；部署者是否只治理不接触租户数据；授权绑定用户、组织职位还是管理令牌。 |
| Pre-execution checklist | 1. 只用当前独立工作树；2. 先读 HUAKAI 角色与租户真码；3. 官方资料只采用原始/权威来源；4. 参考项目源码若需要，另走 clean-room specifier；5. 不修改鉴权/schema/资金；6. 记录观察、推断和 open questions。 |

## 调研顺序

1. 还原 HUAKAI 当前身份图：部署凭据、用户 session、`users.role`、`admin_tokens.role`、tenant scope。
2. 追踪管理入口：谁能管理 provider account、credential、用户、配额、订阅和账务。
3. 搜索历史设计与现存业务能力：邀请、返利、订阅分组、代理/渠道、组织层级和委托授权。
4. 调研成熟模型：
   - 平台运营方与客户组织分离；
   - reseller/partner 的委托管理；
   - 租户内 owner/admin/staff/end-user；
   - 资源所有权、账单所有权和秘密可见性分离。
5. 输出 HUAKAI 适配方案、现状差距和分阶段迁移建议。

## 执行状态

- 已完成 216 个本地/远端跟踪 ref 的主题定位和全历史相关文档路径枚举。
- 已亲读主线身份、租户、平台设置、邀请返利、订阅和历史 reseller branch 的 migration、查询与运行时鉴权源码。
- 已核对 AWS、Microsoft CSP/GDAP、Stripe Connect 官方模型。
- 结果记录在 `docs/process/research/2026-07-16-three-tier-distribution-auth-research-codex.md`。
- 本轮未修改鉴权、schema、资金、配额、部署或生产路由。
- 等待 Owner 回答“下级代理是否需要管理下级租户业务，以及该委托由谁确认”后，才能进入实现计划。
