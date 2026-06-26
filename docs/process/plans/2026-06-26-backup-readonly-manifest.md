# 备份只读 manifest(rank6)— 计划

状态:✅ 已实现待审查合并
日期:2026-06-26
作者:Claude(Owner 全权 + 按 scoping 排期推进)

## 范围
scoping 真码核实:HUAKAI **无**备份/恢复端点(exporthttp 等只是窄范围业务 CSV 导出,非备份)。
本切片=最小、纯只读的备份**元数据** manifest,点亮"备份能力的地基与边界",**不导出任何业务数据**。
真正的数据导出 bundle 是后续切片;恢复(写入)是最高危的 Owner-gated 后续切片。

## 实现(blast radius)
- 后端新包 `backuphttp`(store.go 只读 pg_catalog + schema_migrations;handler.go 组装 + 静态脱敏策略声明)
  + `cmd/gateway/routes_backup.go`(adminGate **platform_admin** 包裹)+ routes.go 1 行 mount + openapi 声明。
- 前端新 feature `frontend/src/features/backup/`(types/纯逻辑/api/页/test)+ nav.ts 系统组 1 行 + router.tsx 2 行。
- **零 schema 迁移、零 money、零写入、零凭据外泄**——只读元数据。

## 安全要点
- **RBAC**:全库元数据属平台级 → adminGate 强制 **platform_admin only**(tenant_operator 已认证但 403)。
  已核 adminGate(middleware.go:151)对 `Role != RolePlatformAdmin` 返 403,并注入身份到 context。
- **零数据泄露**:响应只有表名 + 行数估算(pg_class.reltuples)+ schema 版本 + **静态**脱敏策略声明
  (一组真实敏感列名,非实际数据)。绝无业务行数据、绝无凭据/密码/令牌原文。
- **fail-closed**:store=nil 或查询失败 → 503,不出半成品 manifest。
- **零昂贵查询**:行数用 reltuples 估算,不对大表跑 COUNT(*)。

## 验证结果
- 后端:build/vet 净;backuphttp 单测(happy/nil→503/error→503 不泄原始错误);integration_pg 真 PG 绿
  (pg_catalog 查询返回 tenants/users/api_keys 等核心表);**变异** schema 'public'→'pg_catalog' → 集成测试转红。
- 前端:vitest 5 例(totalEstimatedRows/topTablesByRows + 纯函数不改原数组);**变异** 降序→升序 → 转红;
  tsc 净;vite build 成功(页进 bundle)。
- openapi 一致 + quality-gate PASS(baseline 零新增)。

## 后续(本切片不做)
- 数据导出 bundle(真导出脱敏后的业务数据)= 第二切片,需逐表核脱敏。
- 恢复(import 写入)= 最高危,可覆盖余额/配额/账本 → Owner-gated 独立切片。
- 凭据原文导出 = 独立 Owner-gated 高危开关,绝不默认开。
