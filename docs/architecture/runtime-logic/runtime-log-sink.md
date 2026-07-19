# 全局日志采集与 30 天保留

## 运行链

```text
zap / slog
  -> 统一字段提取与脱敏
  -> 显式分类 Info / 全部 Warn、Error
  -> Info 队列 | 异常优先队列
  -> 有界批量写入
  -> ops_runtime_logs
  -> 分类检索 / 健康状态

全局普通日志表白名单
  -> 数据库可信 ingested_at
  -> PostgreSQL 当前时间 - 30 天
  -> PostgreSQL 事务租约
  -> 单批 5000、单表最多 20 批
  -> 自动补扫 / 每日清理 / 积压一分钟续跑
```

## 统一分类

所有进入统一运行日志面的事件只能属于六类：

- `operation`：管理和系统操作。
- `financial`：资金、配额、计费操作的运营轨迹；权威资金账本不在本表。
- `security`：认证、授权、凭据和内容安全事件。
- `error`：无法归入业务域的系统错误。
- `access`：HTTP 请求最小索引事件。
- `recovery`：重试、隔离、人工处理和恢复结果。

稳定字段包括事件类型、结果、错误分类、错误码、是否可重试、操作者、租户、目标、
入口请求、追踪、上游请求、幂等键和恢复状态。普通 Info 没有有效分类与事件类型时不入库；
Warn/Error 缺字段时归入明确的未分类错误，非法合同转成 `runtime.contract_invalid`，不能静默消失。
HTTP `4xx` 仍以结构化结果、错误分类和错误码完整记录，但日志级别保持 `Info`；只有 `5xx`
进入 `Error` 容量。客户端扫描、失效凭据或错误路由洪峰不能挤掉真正的服务端异常。

## 保留边界

固定保留期为 30 天，不读环境变量，不允许租户或管理员缩短。事件发生时间 `created_at`
只用于展示；保留器先从 PostgreSQL 读取一次固定截止线，删除唯一使用
`ingested_at < 数据库当前时间 - 30 天`，恰好位于截止线的记录保留。

白名单只包含已核实为运营记录的 14 张表：

```text
ops_runtime_logs
admin_audit_events
user_audit_events
channel_health_audit_events
credential_audit_events
hermes_audit_events
oauth_refresh_audit_events
pool_routing_audit_events
rate_limit_audit_events
quota_audit_events
payment_audit_events
subscription_plan_audit_events
moderation_log
referral_reward_audit_events
```

迁移 0195 在这 14 张表上统一建立 `log_category` 与 `ingested_at`。领域表使用稳定的
主分类默认值，`ops_runtime_logs` 按每条事件分类；原有事件字段和权限模型不变。存量行
无法证明原始入库时刻，因此迁移时由数据库统一重新起算 30 天，绝不拿可自报的事件时间
充当可信入库时间。

下列数据即使历史名称含 `audit/log/event` 也不是普通日志，严禁进入保留器：资金账本、
余额与退款事实、幂等和去重事实、定价签名哈希链、处罚实时状态、Outbox、DLQ、待恢复任务、
签名密钥和清理器状态。尤其是 `payment_audit_log` 仍承担旧余额调整幂等回放，
`subscription_audit_events` 直接参与订阅请求去重，`moderation_violation_events` 参与自动封禁；
三者都必须永久保留。`usage_record_reconciliation_events` 是资金校正事实，
`async_processor_events` 是恢复流程状态，`alert_events` 与 `channel_health_admin_alerts` 承载未关闭状态，
也均不进入普通日志清理。新增日志表必须显式进入白名单并提供可信 `ingested_at`，禁止按表名扫描。

## 运维合同

- `GET /v1/admin/ops/runtime-logs`：按分类、事件、结果、错误、租户和关联标识做键集分页。
- `POST /v1/admin/ops/runtime-logs/cleanup`：只接受 `{"confirm":true}`，触发同一固定策略；
  不接受时间点或全表清空参数，管理操作日志写失败则拒绝执行。
- `GET /v1/admin/ops/runtime-logs/health`：返回两条队列容量、丢弃/失败批次，以及保留器最近
  尝试、成功、积压、租约冲突和失败表。

当前运行日志端点只面向 `platform_admin`，也不会把其他权限域的领域日志汇总到该端点。
保留期全局一致不等于扩大读取权限。

## 生命周期

数据库迁移必须先于 sink 和保留器启动；否则启动日志会因表不存在而丢失。停机时先停止保留器
和其他业务 worker，再排空日志 sink，最后关闭数据库。采集始终有界且不反压业务链，但服务端
异常与普通访问使用独立容量，访问洪峰不能挤掉错误。
