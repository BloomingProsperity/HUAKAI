# 第四批八页密度重构 Spec(/ops /usage-records /usage /health /admin/logs /activity /admin/alerting /system)

审计已核(带 file:line):八页零使用底座;tenant_id 无新栽点(alerting 已合格;/ops health-score 刻意省略有注释);
底座两处不覆盖须页级保留:**展开行下钻**(/usage-records 成本详情)与**游标/offset"加载更多"**——别硬迁。

实施顺序(真数据料优先):
1. **/ops**(★★★ 平台级 9552 条全聚合):6 KPI 迁 StatCard(成功率 ok/warn/danger tone)+5 裸表迁 DataListTable(纯只读)+Empty→EmptyState;SVG 趋势/Segmented 保留。工作量中。
2. **/usage-records**(★★★ 用户逐笔):主表下钻**不可迁**保留自绘;仅"我的争议"子表迁 DataListTable+空态+页头 StatCard;money 争议语义不动。工作量中高。
3. **/usage**(★★):汇总表迁 DataListTable+统计头 StatCard×3(当页聚合)+EmptyState;配额方格条特色保留。工作量低。
4. **/health**(★★★ 运行时恒真值):两处 hk-metric 网格迁 StatCard(status→tone)+EmptyState。工作量低。
5. **/admin/logs**(★ warn+稀疏):运行日志表迁 DataListTable(request_id 做回填过滤 link)+sink 健康迁 StatCard×3(dropped>0 danger)+EmptyState;级别热调卡保留。
6. **/activity**(○):单表迁 DataListTable+EmptyState;加载更多页级保留。工作量低。
7. **/admin/alerting**(○ 三资源空):3 Tab 表迁 DataListTable(manual-resolve button/改删 danger)+本地 ui.tsx 原语替换+window.confirm→confirmDanger+StatCard 计数;tenant_id 现处理合格别动。工作量中。
8. **/system**(轻触):键值配置面不迁表,仅 hk-empty→EmptyState;secret-mask 语义严守不动。工作量极低。

通用:vitest 全绿+build 绿;映射纯函数+变异测试点;浏览器真栈验收 PM 做;中文注释;不 commit。
用户侧两页(usage/usage-records)验收需登录持记录 seed 用户(user_id=6/179/180 等,bcrypt 密码登不了→PM 用 API 造 huakai 的用量或改由 /ops 验平台级)。
