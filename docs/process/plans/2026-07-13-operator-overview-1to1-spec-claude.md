# Operator 总览大屏 1:1 复刻 — 部件级 Spec

Owner 给定目标图(HUAKAI AI Gateway 控制台/概览),拍板「先做这一页当全站颗粒度样板」。
本 spec 把大屏拆到**部件级**:每格的布局、字段、空态文案、下钻目标、数据源端点。
数据源列由端点审计(agent a3dd471f)回填;标 `【待填】` 的等审计结论。

**铁律**:1:1 复刻的是**密度/布局**,不是图里的假 demo 数(2,842,731 这类)。有真数填真数,
无数走**诚实空态**(带出口的"接入后显示 X",不是灰字"暂无")。数字必须带上下文(较昨日/占比/时间戳)。

页面归属:operator `/overview`(operatorEnabled 用户);现有薄版 OperatorOverview 整体替换。

---

## 布局骨架(自上而下)

```
┌ 顶栏:Logo · 面包屑 · 全局搜索⌘K · 快捷操作▾ · 安全中心 · 审计日志 · 通知🔔6 · admin▾ ┐
├ 5 指标卡横排(icon + label + 大数 + 较昨日Δ + sparkline)                              ┤
├ [网关资源概览 4 子卡]  ────────────  [今日待处理事项 表]                              ┤
├ [请求趋势与成功率 双轴折线]  ──────  [模型调用分布 donut]  ──  [快捷入口 6 格]        ┤
├ [告警与维护建议]  ─────────────────  [最近变更与审计事件 表]                          ┤
└──────────────────────────────────────────────────────────────────────────────────────┘
```

栅格:主内容 12 列。指标卡行 = 5×等宽。第二行 = 8:4(资源概览:待办)。
第三行 = 5:4:3(趋势:分布:快捷)。第四行 = 5:7(告警建议:审计流)。

---

## 部件级明细

### A. 顶栏(全站共用外壳,本页首现)
| # | 部件 | 布局/交互 | 空态 | 下钻 | 数据源 |
|---|---|---|---|---|---|
| A1 | Logo+面包屑 | 控制台/概览 | — | — | 静态/路由 |
| A2 | 全局搜索 ⌘K | 输入弹下拉,分组模型/账号/用户 | 无匹配"未找到 X" | 命中项跳详情 | 【待填】 |
| A3 | 快捷操作▾ | 下拉:新建账号/建号池/配限流… | — | 跳对应页 | 已有页面路由 |
| A4 | 安全中心 · 审计日志 | 两枚跳转钮 | — | /security · /activity | 已有 |
| A5 | 通知🔔badge | 未读数角标,点开抽屉 | 无未读不显角标 | /notifications | 【待填】未读数端点 |
| A6 | admin▾ | 头像+超级管理员+菜单 | — | /profile · 登出 | me(已有) |

### B. 顶部 5 指标卡
每卡结构:`圆形icon | label(带ⓘ) | 大数 | 较昨日 ↑/↓Δ(色) | 底部 sparkline`
| # | 卡 | 主数 | 副信息 | 空态 | 下钻 | 数据源(审计 a3dd471f 判定) |
|---|---|---|---|---|---|---|
| B1 | 网关可用率 | 成功率% | **无 delta** | 无数据"接入监控后显示" | /health | **部分**:`GET /v1/admin/usage/overview` totals.success_rate(十进制串,是请求成功率非严格可用率);system/health 只有枚举态。**delta 需新建**。一期先用 success_rate 当近似,delta 留空不编 |
| B2 | 今日请求量 | 计数 | sparkline;**delta 需新建** | 0 请求"今日暂无调用" | /usage | **部分**:`GET /v1/admin/usage/overview?window=24h` totals.requests(现成);窗口是滚动24h非自然日;环比 delta 无端点 |
| B3 | 活跃上游账号 | enabled/total | — | — | /accounts | **现成**:`GET /admin/v1/provider-accounts/health-summary` → total/enabled/disabled/needs_attention;tenant scope 齐 |
| B4 | 在线模型 | data.length | — | — | /models | **前端聚合**:`GET /v1/models` 取 data.length(无 count 无健康态);注意此端点是 API-key 鉴权非 admin |
| B5 | 异常告警 | firing 数 | **delta 需新建** | 0 告警"当前无异常"(positive) | /admin/alerting | **部分**:`GET /v1/admin/alert-events?state=firing` 前端数条(AlertingFiringCount 内部有但未接线恒0);delta 无 |

> sparkline:近 24h 时序,取自趋势端点同源(避免各卡各拉一次)。
> ⚠️**delta(较昨日)全线无端点**——一期决策:有 delta 端点前**不显 delta 徽标**(不编环比数),只显主数+副信息;新建聚合端点后再补 delta。

### C. 网关资源概览 4 子卡
每卡:`icon | 标题 | 大数 | 两枚分解徽标(健康/异常式) | 底部主操作按钮`
| # | 卡 | 大数 | 分解 | 按钮→ | 数据源(判定) |
|---|---|---|---|---|---|
| C1 | 模型服务 | 计数 | 健康 · 异常 | 管理模型服务 /models | **需新建**:无模型健康聚合端点;一期退化为"在线模型数"(/v1/models 长度),不显健康/异常分解 |
| C2 | 上游账号 | enabled/total | 可用 · 不可用 | 管理上游账号 /accounts | **现成**:`provider-accounts/health-summary`(同 B3) |
| C3 | 账号池 | total | 可用 · 异常 | 管理账号池 /accounts?tab=pool | **前端聚合**:`GET /v1/admin/channel-health/summary` → by_state(cooling_down+disabled+degraded 求和=异常) |
| C4 | 流量控制 | 计数 | 生效中 · 已停用 | 配置限流策略 /routing | **前端聚合**:`GET /v1/admin/quota-policies` 列表按 enabled 分组数(注意分页) |

### D. 今日待处理事项(表)
列:`优先级徽标 | 事项 | 详情 | 操作按钮`。按优先级高→低排。
聚合来源:异常告警数、上游账号不可用数、账号池异常数、限流优化建议数。
| 行(条件出现) | 优先级 | 详情文案 | 操作→ |
|---|---|---|---|
| 异常告警处理 | 高 | "N 条告警待处理" | 查看 /admin/alerting |
| 上游账号不可用 | 高 | "N 账号不可用" | 处理 /accounts?filter=down |
| 账号池异常 | 中 | "N 个账号池存在异常" | 处理 /accounts?tab=pool |
| 限流策略优化建议 | 低 | "N 条限流策略待优化" | 查看 /routing |

空态:全部为 0 → "今日无待处理事项"(positive)。底部"查看全部任务(N)"。
数据源:**需新建后端聚合**(4 原子端点都在:告警=alert-events?state=firing;账号不可用=health-summary.needs_attention/disabled;账号池异常=channel-health/summary.by_state;**限流优化建议=后端完全缺,一期不出这行**)。一期先前端并 3 个已有端点凑表,limit行数用 log 说明。

### E. 请求趋势与成功率(双轴折线,近24h)
左轴请求量 + 右轴成功率(85–100%)。窗口切换下拉(近24h/近7天)。刷新钮。
空态:无数据"窗口内暂无调用"。数据源:**部分/需新建**。`usage/overview` 的 trend[] 是**按天**且每点**只有 requests+cost,无 success_rate**(现有 OperatorOverview 只画了按日单序列)。近24h **小时级 + 成功率第二序列无端点**(perf-metrics/by-bucket 有小时桶但字段是 ttft/tps/error_rate)。一期先画**按日请求量单序列**(现成),成功率第二序列待新建 hourly 端点。

### F. 模型调用分布(donut)
环形 + 图例(模型名+占比%)+ 中心总调用量。取前 N 模型,其余归"其他"。
空态:"窗口内暂无调用"。下钻:图例项 → /usage?model=X。数据源:**现成** `GET /v1/admin/usage/leaderboard?by=model` → entries[].{key,request_count,total_cost,total_tokens},用 request_count 占比画环。

### G. 快捷入口(6 格 icon 磁贴)
新建上游账号 · 创建账号池 · 配置限流 · 查看审计日志 · 处理告警(带角标N) · 更多操作。
纯跳转,无数据依赖(告警角标复用 B5)。

### H. 告警与维护建议(左右两栏)
- H1 当前告警(N):紧急/重要/次要 三级计数行。→ 查看全部告警 /admin/alerting。
  数据源:**需新建或前端 join**。`alert-events` 无 severity 字段(severity 只在 alert-rules 上)→ 一期前端拉 rules + events join 数级别,或先只显 firing 总数不分级。空态:"当前无告警"(positive)。
- H2 维护建议:系统给出的 N 条建议 bullet。→ 查看全部建议。
  数据源:**无端点**。一期**隐藏该栏**(不编建议),留位到有推荐引擎再上。

### I. 最近变更与审计事件(表)
列:`时间 | 类型徽标 | 对象 | 操作人 | 详情`。最近 N 条。→ 查看全部审计事件 /activity。
空态:"暂无审计事件"。数据源:**现成** `GET /admin/v1/audit-events` → items[].{created_at,event_class,event_type,actor_id,actor_role,provider_account_id/pool_group_id,reason,severity,payload},字段齐。

---

## 组件底座(本页产出,全站复用)
- `StatCard`(已有,需扩)→ 加 `delta`(值+方向+色)、`sparkline`(number[])、`icon` 槽。
- `ResourceCard`(新)→ 大数 + 两枚分解徽标 + 主操作按钮。
- `PriorityTable` / `DataTable`(新)→ 徽标列 + 行操作;待办表与审计表共用。
- `Donut`(新)→ 纯 SVG 环形 + 图例,无外部图表库依赖(CSP/包体)。
- `DualAxisTrend`(新)→ 复用现有 sparkline/趋势渲染,加右轴。
- `Sparkline`(已有 Skeleton 同目录?确认)→ 卡内迷你折线。
- `TopBar`(新,全站外壳)→ 搜索/快捷操作/通知/账号菜单。

## 验收标准(全站每页照此)
1. 每张聚合卡可下钻,且卡内自带分解/明细预览,不是孤零零一个数。
2. 数字带上下文:较昨日 Δ / 占比 / 时间戳,禁裸数字。
3. 空态是兜底不是主角;设计以"有数据时的密度"为先。
4. 无端点的部件走诚实占位,绝不编数(禁 2,842,731 式假数)。
5. 无外部前端图表库(SVG 自绘,守 CSP + 包体)。

## 数据源判定汇总(端点审计 a3dd471f 已回填)

**现成直接能用(4)**:上游账号 health-summary(B3/C2)、模型分布 leaderboard?by=model(F)、审计流 audit-events(I)、请求量 usage/overview.totals(B2 主数)。
**前端聚合已有端点(3)**:在线模型 /v1/models 长度(B4/C1)、账号池 channel-health/summary(C3)、流控 quota-policies(C4)。
**需新建后端(5)**:①可用率环比+请求量/告警的 delta ②模型服务健康计数 ③待办事项聚合 ④小时级双序列(requests+success_rate)⑤告警 severity 分级 + 全局搜索。

## 一期落地边界(现成+前端聚合先做透,新建端点二期)
本页**一期只用现成+前端聚合的部件**做到 1:1 密度,新建端点的部件走**退化但不编数**:
- **不显 delta 徽标**(全线无环比端点)——主数+副信息即可,密度靠明细行撑。
- C1 退化为"在线模型数"(不显健康/异常分解)。
- E 一期画**按日请求量单序列**(成功率第二序列二期)。
- H1 一期显 firing 总数(分级二期);**H2 维护建议整栏隐藏**(无端点不编)。
- D 待办表前端并 3 个已有端点(限流建议行二期)。
这样一期**零编造**即可 1:1 铺出密度;二期补 5 个聚合端点点亮 delta/分级/双序列/待办/搜索。

## 二期新建端点清单(后端小活,可派 codex)
| 端点 | 包 | 供给部件 | 鉴权/租户 |
|---|---|---|---|
| `GET /v1/admin/ops/overview-summary`(当前值+环比delta+模型健康+待办聚合) | 新 `internal/opsoverviewhttp/` | B1/B2/B5 delta·C1·D | adminGate + tenant scope |
| `GET /v1/admin/usage/overview/hourly?window=24h` → {hour,requests,success_rate} | 扩 `usageanalyticshttp`(加 ByHour+success 聚合) | E 双序列 | adminGate |
| `GET /v1/admin/alert-events/severity-counts?tenant_id=` | 扩 `alertinghttp`(join rule.severity) | H1 分级 | adminAuth |
| `GET /v1/admin/search?q=&type=model\|account\|user` | 新 `internal/adminsearchhttp/` | A2 全局搜索 | adminGate |

⚠️租户注意(审计发现):`usage/overview` 的按天趋势聚合**不带 tenant 参数=全局跨租户**;新建端点必须显式带 tenant scope,别沿用这个漏。

## 真数据燃料(并行,codex 跑中)
sub2 真数据正由 codex 写映射 SQL 种进 huakai_seed 测试库([[sub2-migration-survey-2026-07-13]])。种好后**把 gateway 指向 huakai_seed**,overview 及全站页面即渲染真行(195用户/51账号/259Key/23池)——密度用真数据验,不靠空态臆想。
