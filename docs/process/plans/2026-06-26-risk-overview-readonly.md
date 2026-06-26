# 风控只读总览页(rank2)— 计划

状态:✅ 已实现待审查合并
日期:2026-06-26
作者:Claude(Owner 全权 + 按 scoping 排期推进)

## 背景与范围
scoping 真码核实:HUAKAI 风控**底座厚但信号散落**——限流/封禁(moderation 自动封 + 运维手动禁)/
告警(alerting 引擎)/用户封号/key IP 黑名单**均已接线**,但无统一视图。本切片=把这些**已有**信号
聚合成一张**只读**计数表,**零处置、零写入、零新引擎**。中央风控引擎(规则 DSL/自动升级/auto-ban)
是后续大 arc,不在本切片。

## 成功标准
- 后端新增只读端点 `GET /admin/v1/risk/overview`,admin 鉴权 + **强 tenant_id 隔离**(防 IDOR)。
- 仅 fan-out 已有表的 4 个 COUNT:已禁用 Key / 触发中告警 / 已封禁用户 / IP 黑名单 Key。
- 前端新增「风控总览」页(nav 安全审计组 + router),四卡片 + 「去处理」跳已有运维页。
- 三门绿 + 单测含 IDOR 守卫 + 集成测试证跨租户隔离(变异验证)+ openapi 一致。

## 实现(blast radius)
- 后端新包 `backend/internal/riskoverviewhttp/`(store.go 4 COUNT + handler.go admin 鉴权/tenant 隔离)
  + `cmd/gateway/routes_risk.go`(接线)+ routes.go 1 行 mount + openapi.yaml 声明。
- 前端新 feature `frontend/src/features/risk/`(types/risk 纯逻辑/api/页/test)+ nav.ts 1 行 + router.tsx 2 行。
- **零 schema 迁移、零 money、零 auth-core 改动**——纯新增只读端点 + 新页。

## 安全要点(IDOR 是本切片头号风险)
- 跨租户聚合**必须** admin 鉴权 + `CanIssueForTenant` 校验(参照 auditexporthttp 历史 IDOR S0 教训)。
  租户运营者请求他人 tenant_id → 403 且**不触达 store**(handler 单测 + 集成测试双证)。
- 响应**仅计数**,无明文 key、无完整 IP,零敏感字段泄露。
- 身份只信认证上下文(admin token),绝不信请求体。

## 验证结果
- 后端:`go build/vet` 净;riskoverviewhttp 单测 6 例绿(含 IDOR 守卫:越权 403 + store 不触达);
  integration_pg 跨租户隔离测试真 PG 绿(A 见 1、B 见 2,噪声行不计入);**变异验证**:去掉
  `tenant_id` 过滤 → 测试转红(确认判别性)。openapi 一致性绿。quality-gate PASS(baseline 零新增)。
- 前端:vitest 5 例绿(buildRiskCards tone/字段映射/跳转 + parseTenantInput);**变异**:tone 阈值
  改 `n>99` → 转红。tsc 净;vite build 成功(页进 bundle)。

## 后续(本切片不做,留 Owner / 大 arc)
- 中央风控引擎(规则 DSL / 异常用量 auto-ban / 告警→自动处置联动)= money/security 高敏,Owner-gated。
- 卡片内嵌处置动作(禁 key/封号/加黑名单)= 写路径,本切片只读跳转,后续可加。
