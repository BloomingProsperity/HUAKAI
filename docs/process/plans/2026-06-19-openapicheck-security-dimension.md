# openapicheck 补 security 维度校验(防 IDOR 类契约漂移复发)

## 背景与动机
审计 IDOR(S0,PR #74)修复时发现:`internal/openapicheck` 的契约一致性校验只比 **path + method**,
不比 **security**。结果"spec 标公开但实现要求认证(或反之)"这类漂移逃过 CI——IDOR 修复本身就在
spec 留下 `security: []` 与新认证实现矛盾,靠人工 review 才发现。本切片把 security 维度纳入自动校验。

## 范围(additive,test-infra + 一处 spec 修正)
1. `internal/openapicheck/openapicheck.go` 加三个函数(纯新增,不改既有):
   - `ParseSpecPublicOperations` —— 行解析抽出 spec 里显式 `security: []`(公开)的 method+path 操作。
   - `OperationsGatedByMiddleware(r, marker)` —— chi.Walk + runtime 函数名内省,抽出中间件链含 marker
     (如 "SessionMiddleware")的操作。chi 中间件是闭包无法按类型识别,故用函数名(形如 `...SessionMiddleware.1`)。
   - `SecurityContractDrift(specPublic, implGated)` —— 取交集:spec 标公开但 impl 挂认证 = 漂移。
2. `docs/openapi/openapi.yaml` 修正本检查抓出的真实漂移:`POST /v1/receipts/{request_id}/verify`
   实现挂了 `SessionMiddleware`(handler 用会话身份守退款队列跨租户,见 cost_receipt_handler.go),
   spec 却标 `security: []`。改为 `sessionBearerAuth` + 补 401 响应,与兄弟 `/disputes` 一致。
3. 测试:openapicheck 单测(合成三类操作判别)+ cmd/gateway 集成测(真 spec×真 router,断言零漂移,
   两处 len==0 守卫防"空集假绿")。

## 检查方向取舍
只覆盖 **"spec 公开但 impl 认证"** 方向。反向("spec 认证但 impl 公开")不纳入:admin 等路由在 handler
内鉴权(Resolve 取角色)、不挂中间件,纯中间件内省无法判定,纳入会对这些路由误报。IDOR 漂移正是
本方向,价值最高、误报最低。

## 成功标准
- 三函数 build/vet 通过;openapicheck.go ≤600 行;codebudget 绿。
- 单测:三类操作(公开+认证→漂移 / 公开+无认证→不报 / 认证+非公开→不报)断言精确。
- 集成测:真 spec 修正后零漂移;变异(还原 spec 的 `security: []`)→ 集成 RED 且点名该 path。
- 4 处变异全部 RED(漂移检测器 / 中间件内省 / spec 解析 / spec 还原)。

## blast radius / 可能出错
- 中间件函数名内省依赖 runtime 名含 "SessionMiddleware";若未来认证中间件改名,marker 需同步(集成测
  的 len(gated)==0 守卫会立即 RED 提醒,不会静默失效)。
- spec 修正是**纯文档对齐**(impl 行为不变,本就要求认证),无运行时风险;只是让契约如实反映认证要求。

## 决策点(Owner)
无 money/schema/auth 行为变更;spec 修正方向已由真码确认(impl 强制认证是 money-safety 设计,非 bug)。
属低风险 test-infra + 文档对齐,自主落地。
