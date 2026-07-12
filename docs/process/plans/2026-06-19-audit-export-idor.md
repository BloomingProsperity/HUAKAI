# audit 导出未认证跨租户泄露(IDOR)修复(wave-2 审计 wy94u3tn9 S0)

## 背景 / 来源
审计确认 **S0**:`/v1/audit/export`(+ `/proof/{request_id}.json`)整组在 cmd/gateway/routes.go 无 auth 中间件,
唯一"授权"是查询参数 `tenant_scope_ref`,而它 = `"tenant:"+hex(sha256("huakai-ledger-tenant-scope-v1:"+tenantID)[:8])`
(auditledger/canonical.go:58),tenant_id 是小整数 → 攻击者离线枚举任意 tenant 的 scope_ref → 未认证拉取任意租户
**整条审计链**(request_id / hop_chain / model_chain / 时间戳 / 签名 / merkle 根)。range 路径还无需 request_id,可分页全量收割。

## 真码摸透(已读)
- 路由:cmd/gateway/routes.go:141 `r.Route("/v1/audit", ...)` 无 `r.Use`;内挂 pubkey/pubkeys/verify/merkle(**故意公开**,
  trust-chain 单负载验证)+ `auditexporthttp.MountRoutes`(export/proof)。对比 /v1/receipts:166 每路由 `.With(auth.SessionMiddleware(d.userSessions, d.clientIPResolver))`。
- 处理器:auditexporthttp/handler.go 的 NewExportHandler/NewProofDownloadHandler 直接 `r.URL.Query().Get("tenant_scope_ref")`
  作为**唯一**授权范围,喂给 ledger.ListByRange/ListByRequestIDs/GetByRequestIDAndTenantScope。身份取自请求、从不取自认证上下文。
- 认证读取先例:gatewayhttp/cost_receipt_handler.go:107 `ident, ok := sessionauth.SessionFromContext(r.Context())`(import
  `sessionauth "internal/auth"`);SessionIdentity{TenantID, UserID, ...}。`auditledger.TenantScopeRef(tenantID)` 派生 scope。
- frontend_wiring_test.go:443 只断言 /v1/audit/pubkey 公开,**未**断言 export 公开 → 加认证不破坏该测试。

## #16 三镜像(审计链导出鉴权)
- sub2api / new-api:无 HUAKAI 这种"租户可验证审计链 + 跨租户 scope 导出"对应物(其审计日志是 admin 后台内嵌、单租户/会话内,
  不暴露独立的按 scope_ref 导出端点)。CLIProxyAPI 纯 relay 无审计链。**no-equivalent**:本修复遵循 HUAKAI 自有 /v1/receipts
  的 SessionMiddleware 先例 + trust-chain 规范(docs/specs trust-chain §3:stored owner-bound 查询须会话/owner 范围、拒跨租户探测)。

## 修复(additive 鉴权,不破坏公开验证,身份取自认证上下文)
1. **handler 失败闭合(主防线)**:NewExportHandler/NewProofDownloadHandler 一进来就
   `ident, ok := auth.SessionFromContext(r.Context())`;`!ok || ident.TenantID==0` → 401。这条**直接堵原 bug**:即便将来
   路由挂载漏了中间件,处理器自身也拒绝无会话请求(fail-closed)。
2. **授权范围取自认证身份**:`authScopeRef := auditledger.TenantScopeRef(ident.TenantID)`;若请求显式带了 `tenant_scope_ref`
   且 != authScopeRef → 403(拒跨租户探测);否则一律用 authScopeRef 做所有 ledger 查询。**绝不信请求里的 scope_ref 决定范围**。
3. **路由挂认证中间件**:cmd/gateway/routes.go 把 `auditexporthttp.MountRoutes` 包进
   `r.Group(func(r){ r.Use(auth.SessionMiddleware(d.userSessions, d.clientIPResolver)); auditexporthttp.MountRoutes(r, deps) })`,
   只给 export/proof 加认证,pubkey/verify/merkle 保持公开。

不动 schema、不动 money;默认行为变化=未认证导出从"放行"变"401",这是**修 S0 的必需**、不是降级,无需 env 开关。

## 成功标准 / 测试(变异可证)
- 未认证(context 无 session)调 export/proof → 401(变异:删 handler 的 SessionFromContext 401 守卫 → 又能未认证导出 → RED)。
- 认证 tenant=7、请求 tenant_scope_ref=tenant(9) → 403 跨租户拒(变异:删 scope 不匹配 403 守卫 → 能拉别租户 → RED)。
- 认证 tenant=7、scope_ref 匹配(或不传)→ 200 且只返回 tenant 7 的链;授权范围用的是认证身份派生值(不是请求里的)。
- 路由层:wrap 后未认证请求被 SessionMiddleware 401(集成式);frontend_wiring_test/OpenAPI 干净基线绿。
- handler_test 既有用例改为注入匹配会话(auth.ContextWithSession)。

## blast radius
auditexporthttp/handler.go + cmd/gateway/routes.go(+ handler_test、可能 wiring_test)。碰 cmd/gateway(谨慎 additive)。
对抗审查零 S0/S1 后合并。CRL(S1)是独立下一切片。
