# R0 serving capability 延后 review 事项

## 路由侧 pre-existing 缺口

- [S2] disabled provider 仍可能进入 pool 选号 — source: Codex review round 1 F3；证据：`backend/sql/queries/pool_accounts.sql` 的 `ListEligibleAccountsByPoolGroup` 会过滤 `channels.enabled`、`provider_accounts.enabled` 与删除状态，但 join `providers` 时只过滤 `providers.deleted_at`，未消费 `providers.enabled`；rationale：这是 R0 之前已存在的路由语义缺口，直接修改 pool SQL 会扩大本轮薄闭合闸范围，并需要并发选号与路由回归验证；follow-up：后续路由切片应把 provider 生命周期纳入候选资格，补 disabled→不再入选、重新 enabled→恢复入选及并发场景测试；Owner decision：本 R0 不改 pool SQL，只把新增增量收窄。

## R0 当前收窄措施

R0 只允许 `registrydefault` canonical 支持集中的 family 在 disabled 写入时沿用既有旁路。仅存在于 serving contract 登记表、但不在 canonical 支持集中的 family，即使写入 disabled，也必须通过当前进程闭合判定；未 ready 时返回 HTTP 422 与结构化 reason。该措施不宣称修复上述路由缺口，只避免 R0 扩大可落库但可能被路由选中的 family 集合。
