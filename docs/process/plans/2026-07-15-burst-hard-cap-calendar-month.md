# burst 硬上限接 enforce + calendar_month 白名单放开 · 计划

日期:2026-07-15。Owner 已拍板:burst 语义=**硬上限**(「跟 sub2 一样就行 同意你的建议」),即窗口内真正的顶=limit_value+burst_value;burst=0(默认)行为与今天完全一致,零翻转。语义决策已由 Owner 定,故本片单计划直接实现。

## 三镜结论(已源码核实)
- new-api 无 burst 概念(固定窗口计数+余额硬扣);sub2api 有 burst_size 字段但未接判定(与我们现状同为死字段),真实生效的是滚动窗口 USD 硬上限;CLIProxyAPI 无用户配额。无任何镜像实现令牌桶——硬上限与生态对齐。

## 改动面
1. **判定**(internal/quota/service_assess.go:33/:58/:78):三处 metric 的 `exceeded` 比较由 `> LimitValue` 改为 `> LimitValue+BurstValue`(封装一个 `effectiveLimit(policy)` 复用,注释写明语义出处);assessment 里暴露的 limit 字段保持原 LimitValue 不变还是改有效上限,以审计/错误体口径一致为准(倾向:决策 payload 同时带 limit 与 burst,避免运营看数对不上)。concurrency metric 走 DB 函数路径,本片不动。
2. **schema 注释**:补 `COMMENT ON COLUMN quota_policies.burst_value`(硬上限语义,limit+burst=窗口真顶)——纯注释迁移,不改结构。
3. **calendar_month 白名单放开 4 处**:adminquotahttp/quota_policy_crud.go:29 validWindowKinds、validate.go:62 错误文案、前端 types.ts:28 WindowKind、quotapolicies.ts:36 WINDOW_KINDS 下拉。引擎与 DB(0072)本就支持,只是 admin 写口挡着;订阅子系统内部写月窗已在生产用。
4. **前端**:配额策略表单 burst 输入框已存在(QuotaPolicyForm.tsx:113),补一行帮助文案「窗口内实际上限=上限+突发」。

## 门
- 判别单测:同窗口用量在 (limit, limit+burst] 区间必须放行、>limit+burst 必须拒;变异(把 burst 加法删掉)→ 红。burst=0 回归 golden:现有全部配额测试不动全绿。
- calendar_month:admin 建月窗策略走真 PG 落库+enforce 判定生效的集成测试;变异(白名单收回)→ 红。
- CI 四 job 绿+对抗审查零 S0/S1;OpenAPI 若 DTO 文案变动同步。

## 风险
- 语义歧义点:是「limit+burst」还是「burst 单独作为更高顶」——已定前者(加法),文案与注释统一写死,避免运营理解分叉。
- 历史已存的 burst_value>0 行:激活后即刻生效(这正是 Owner 要的「配了就得管用」);上线说明里提一句。
