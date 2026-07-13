# 第三批四页密度重构 Spec(/admin/subscriptions /vouchers /pricing /orders)

审计结论(agent 带 file:line 已核):四页 tenant_id 处理全合格(不会重蹈第二批验收红);
底座 StatCard(4tone)/DataListTable(actions+selectable)/EmptyState 已够用无需再扩;
四页现状零使用底座,迁移是纯替换。

**seed 真数据**:subscription_plans=23(enabled=14)/user_subscriptions=22(散在约11用户,如 user_id=6,179,180,185…)/
订单/兑换码/定价倍率全空(空态即合格,可页内自造)。

## 实施顺序与每页要点

### 1. /admin/subscriptions(先做,有真数据)
- 套餐主表(7列)迁 DataListTable:行内 详情/编辑/停用(danger);页头总数23量级用length可接受。
- **加统计头 StatCard×3**:套餐总数/启用/停用(当页统计,无需新端点)。
- 用户订阅分配子表(6列6动作)迁 DataListTable button-action-group。
- 订阅兑换券面板/各模态保留。tenant 硬编码1与seed匹配,不动。
- 验收:进页 23 套餐满表;输 user_id=6 见真订阅行。
- 缺口标注(二期后端):列表级 GET /subscriptions 总览(现只能逐 user 查)。

### 2. /admin/vouchers
- 主表(9列)迁 DataListTable:行内吊销 danger;批次下钻保留。
- 加 StatCard 统计头(总数/可用/已用尽/已吊销,当页口径注明)。
- 空态 EmptyState;页头 visible.length 充总数标注(接后端 count 二期)。
- 验收:空态合格;可"＋批量生成"自造真数据复验。
- 二期标注:?status 服务端筛选+count+offset 分页(现 limit=200 客户端过滤)。

### 3. /admin/pricing
- 3 表(分组倍率/缓存价覆盖/计费策略)迁 DataListTable;倍率行内编辑/删除(danger)。
- 工具附加费常量表直迁只读。无统计头需求(配置页)。空态 EmptyState。
- 验收:倍率/覆盖空态合格;计费策略应显示全局默认(source=global badge);工具附加费满表。

### 4. /admin/orders(收尾,区块最全)
- 4 卡仪表盘迁 StatCard;主表(8列)迁 DataListTable,行内加 确认/重试/取消(端点齐,现只在抽屉);
- 4 Tab/详情抽屉/退款(money敏感,二次确认+幂等key 逻辑不动)/CSV 导出保留。
- 验收:tenant=1 空态合格+dashboard 累计0;可用代客建单自造订单复验。

## 通用验收纪律(每页)
vitest 全绿+build 绿;映射纯函数+变异测试点;浏览器真栈(5176)验收由 PM 做;
codex 不动 money 语义(退款/建单流程逻辑保持);中文注释;不 commit。
