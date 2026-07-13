# 第二批三页密度重构 — 部件级 Spec(/accounts /users /keys)

接 operator 大屏样板(2026-07-13-operator-overview-1to1-spec-claude.md)之后的第二批。
审计依据:三页现状+端点全量核查(agent 带 file:line 证据)。**页面实施等 Owner 认可大屏样板后再动**;
本 spec 先拍在纸上,底座扩展(纯组件能力)可先行。

**真数据验收基线(huakai_seed)**:196 用户 / 51 账号(5活跃46软删) / 259 Key / 14 活跃池。

---

## 0. 共性前置:底座扩展(先行,codex 已派)

| 组件 | 现状缺口 | 扩展 |
|---|---|---|
| DataListTable | action 槽只支持单 Link;无勾选列 | ①行内 action 按钮组(onClick+Link 混排,危险动作 danger tone)②可选 selection 列+表头全选+受控 selectedIds ③向后兼容现用法 |
| StatCard | tone 仅 default/danger | 扩 warn/ok(承接 HealthSummaryCard 三态语义) |
| EmptyState/StatusBadge | 无缺口 | 三页即插即用 |

**共性缺陷(三页必修)**:UI 写死 limit=100 且页头用 items.length 充总数 → 196/259 真数据下显示"共 100"。
修法:接后端 count/total 字段 + 补分页控件(账号页游标分页已是范式,用户/Key 页照搬 offset 分页)。

---

## 1. /accounts 上游账号池页(工作量:大)

现状 10 列表+统计卡+双筛选+按标签批量,区块最全但零底座。

| 部件 | 重构后 | 数据源(已核) | 空态/验收 |
|---|---|---|---|
| 统计头 4 卡 | StatCard(扩tone):总数/已启用/已停用/需关注,needs_attention>0 用 warn | GET health-summary(现成) | 真数据:总数按口径 51 或 5(软删过滤需核 SQL 口径后钉死) |
| 健康态 pill 行 | 保留,各 health_state 计数,点击=筛选联动 | health-summary.states[] | 非空 |
| 筛选栏 | 状态/池组/标签(服务端)+名称(客户端,标注口径);重置 | 现有 query 参数 | — |
| 主表 | DataListTable:10列保留+新增**行内操作列**(启停 PATCH enabled/测试 POST test/清限流/删除 danger,全部端点现成) | GET provider-accounts(游标分页保留) | 51 行可翻页;软删是否显示核 SQL |
| 勾选批量 | selection 列+批量启停/删除(复用 bulk 语义;bulk-by-tag 卡保留) | POST bulk-by-tag(按标签);勾选批量走逐 id 循环或后端补 bulk-by-ids(二期) | — |
| 详情下钻 | 名称链接保留;行展开预览(health/recent-requests 端点现成,可做展开行) | GET /{id}/health,/recent-requests | 展开有真请求行 |

需新建后端:按 name 服务端搜索参数(现客户端过滤只覆盖当页)。

## 2. /users 用户管理页(工作量:中)

| 部件 | 重构后 | 数据源(已核) | 空态/验收 |
|---|---|---|---|
| 统计头 | StatCard×4:总用户(2fa-stats.total_users,不受分页限)/2FA 普及率/活跃/锁定 | 2fa-adoption-stats 现成;**活跃/锁定计数无聚合端点→一期不显或客户端当页统计并标注口径** | 总用户=196 |
| 搜索+筛选 | 搜索保留;**按 role/status 筛选需新建后端参数(二期)**,一期不做 | GET users?q= | — |
| 主表 | DataListTable:现 6 列+**补 user_group/备注列(后端已返未展示,零后端改动)**+行内操作(启停/解锁现成,加"调额"入口→详情) | GET users(items 已含 user_group,remark) | — |
| 分页 | offset/limit 控件(端点参数现成,后端 maxPageLimit=100) | — | 修后能翻到 196 |
| 详情下钻 | balance-history/usage 端点现成,详情页已有;列表行加余额历史快捷入口 | — | 真余额分布 |

## 3. /keys Key 管理页(工作量:大)

| 部件 | 重构后 | 数据源(已核) | 空态/验收 |
|---|---|---|---|
| 统计头 | StatCard×3:总数(用后端 count 字段,现成但 UI 未用!)/活跃/已撤销(一期客户端当页统计标注口径;列表级聚合端点二期) | GET /v1/api-keys 的 count | 总数=259(修截断后) |
| 主表 | DataListTable(selection+action):7列保留;批量撤销条保留(本页勾选范式反哺另两页) | 现成 | 259 可翻页 |
| 筛选 | **按 status 筛选需新建后端参数(二期)**,一期不做 | — | — |
| 每 Key 控制 | EditKeyModal+KeyControlsSection 保留(quota/group/ip/model 端点齐) | 现成 | — |
| admin 跨租户 Key 台 | **端点齐全无页面**(admin/v1/api-keys 列/签发/吊销)→ 独立新页排第三批,不塞本页 | 现成 | — |

---

## 实施顺序(等 Owner 认可样板后)
1. 底座扩展(已先行派 codex)→ 2. /users(最简,验分页范式)→ 3. /keys(selection 范式)→ 4. /accounts(区块最全收尾)。
每页交付含:vitest 映射函数变异测试 + 浏览器真数据截图验收(196/259/51 真值上屏)。
