# 2026-07-14 P0-b 代理组全链修复(Claude 独立草案 + 交叉综合)

## Claude 独立草案(先于阅读 codex 稿写成,原文=派工单)

原文见会话派工单(codex-p0b-dispatch.txt),要点:
1. proxyadmin 三 struct 加 `GroupID *string`;SQL 读写全补 group_id;格式校验 `[A-Za-z0-9_-]{0,64}`,空串规格化 NULL。
2. HTTP DTO create/update 收、list/get 吐 group_id;审计机制随现状不新增。
3. OpenAPI /admin/v1/proxies 请求/响应补字段;`go test ./cmd/gateway/` 一致性门必绿。
4. resolver 空组 **fail-closed 语义不变**(IP 隔离设计),错误 wrap 带 account/group 上下文 + slog 警告。
5. 前端代理页组输入+分组列;账号侧绑组预警复用 proxies list 聚合(datalist+active 计数+0 成员醒目警告),不加新端点。
6. #14 判别性测试全链(删 INSERT 列红/删校验红/去 wrap 红/警告条件翻转红)+ 真 PG 集成。

## 交叉讨论结论(阅读 codex 稿后)

两稿**方向、范围、安全语义完全一致**,零冲突。采纳 codex 稿全部细化,其中三点是有价值的踩点增量:
- service 层走 `internal/db/admin` sqlc 查询而非手写 SQL → 手工同步 CreateProxy/UpdateProxy/GetProxy/ListProxiesByTenant 四查询的源 SQL + 生成码(不跑 sqlc generate)。
- 账号编辑弹窗已请求租户代理列表 → 预警零新增请求,纯前端聚合。
- 代理 create/update 路由现无审计机制 → 本轮不另造,保持现状。
- 另采纳:日志只含 tenant_id/account_id/group_id 严禁凭据;PATCH 清组显式 `group_id: null`;active 计数只认 `status === 'active'`;未知组按 0 成员同警告。

## 综合裁定

**批准执行**:以 codex 稿(2026-07-14-p0b-proxy-group-chain-codex.md)的执行顺序 1-9 为准,语义边界以两稿共同约束为准(不改 schema、不改账号侧校验、不改 fail-closed、不动 Sidebar.tsx、不跑 sqlc generate、不 git 写操作)。验收后由 Claude 亲检 + 变异复核 + 提交。
