--- Owner directive: "横向扩展完成后立即进行纵向闭环"; Lane: planner; Recommendation: 选择 A+Z，先做 Bedrock-on-Anthropic 可重跑 mock-server E2E，再由 Owner 用真 AWS 凭据补同路径烟测。 ---

# 2026-05-08 Vertical Closure Codex 独立计划

## 1. 结论

推荐闭环候选：**A: Bedrock-on-Anthropic 全 E2E**。

推荐执行路径：**Z: 双轨，Y 先 + X 后补**。

理由：

- Bedrock A1-A8、Track C、Track B、Track D、Track P 都刚落地，最大未知风险集中在这条新 client -> gateway -> adapter -> upstream -> response -> metric/audit 链路。
- OpenAI Chat Completions 是较稳路径，适合作为回归基线，但不能证明 Bedrock binary EventStream、Anthropic->Bedrock body translation、SigV4、SSE scanner、proto adapter 和 Track C wire 真正协同工作。
- 只跑真 AWS 会受凭据、区域、模型权限、费用和网络影响，无法作为长期 release gate；只跑 mock 又不能证明真实 provider contract。双轨能把“可重跑确定性”和“真实上游兼容性”分开处理。

## 2. Scope

In scope：

- 一条 Bedrock-on-Anthropic Messages 形请求从 client 进入 gateway，到 provider adapter，再到 upstream mock/真实 AWS Bedrock，再返回 canonical streaming/non-streaming response。
- Track B sticky routing：同 tenant/session/model 请求命中同一 provider account，并在缺失 binding 时执行 upsert。
- Track C cache_control 注入：Anthropic Messages 形 system prompt >= 4096 bytes 自动带 ephemeral marker，且短 prompt 不误注入。
- Bedrock A1-A8：endpoint URL、SigV4 request、binary EventStream decode、SSE scanner、proto adapter、forwarder 注册、chat handler、Anthropic->Bedrock translator 的同路径验证。
- Track D/P metrics：全局和 per-account cache token metrics 在闭环请求后可观测。
- audit/log trace：能以 request_id/tenant_id/provider_account_id 追踪一次成功和一次失败请求。

Out of scope：

- 不扩展 provider 覆盖面，不新增 OpenAI、Gemini、Azure、Vertex、Claude direct 的 E2E。
- 不做压测、长时间 soak、混沌测试或 quota/billing money-path 验证。
- 不改 auth core、billing ledger、quota enforcement、database schema 或 deployment scripts。
- 不把真实 AWS 凭据写入 repo、CI secret 或示例文件。
- 不读 CPA/sub2api/one-api 等外部参考项目源码。

## 3. Success Criteria

- CI 可重跑 mock-server E2E 能在无真实外部凭据情况下通过。
- Owner 本机真凭据烟测使用同一 client 请求形状，除 endpoint/credential 外不依赖另一套测试逻辑。
- 成功请求能验证 routing、credential selection、body translation、cache injection、signing envelope、EventStream decode、canonical response、metrics、sticky binding、audit trace。
- 至少一个 upstream failure path 能验证错误映射、audit 记录和 metrics 不虚增。
- plan 执行后能明确给出：Released-ready、Blocked by Owner credential、或 Blocked by implementation defect。

## 4. Execution Order

1. 建立 mock-server E2E harness。
   - 使用 `httptest` 模拟 Bedrock Runtime endpoint。
   - mock 接收 gateway 发出的 Bedrock 请求，记录 method/path/header/body。
   - mock 返回最小有效 Bedrock binary EventStream，覆盖 response delta 与 terminal event。

2. 构造最小租户和 provider account fixture。
   - tenant_id 固定为测试租户。
   - provider_account_id 使用测试内唯一值。
   - credential 使用测试专用 fake access key，mock 不做真实 AWS 校验。

3. 跑第一条 happy path。
   - client 发 Anthropic Messages 形请求。
   - system prompt 长度 >= 4096 bytes。
   - session_hash 固定，model 固定。
   - 期望 response 能还原为 gateway canonical event/SSE 输出。

4. 跑第二条 sticky hit path。
   - 同 tenant_id + session_hash + model 重发。
   - 期望不重新选择不同 account。
   - 期望 sticky binding 只创建一次，第二次读取已有 binding。

5. 跑短 prompt control path。
   - system prompt < 4096 bytes。
   - 期望 body translation 不带自动 ephemeral marker。

6. 跑 upstream failure path。
   - mock 返回 Bedrock-style auth/permission/rate-limit/server failure 之一。
   - 期望 gateway 返回稳定错误分类，audit 记录 provider/account/context，cache metrics 不把失败误记为成功命中。

7. 检查 metrics 和 audit。
   - 读取 `/debug/vars` 或内部 expvar snapshot。
   - 查询测试 audit/log sink 或 DB 记录。
   - 校验 global 与 per-account cache_token_count 维度一致。

8. Owner 真 AWS 补验。
   - 用环境变量注入 AWS region、model、access key、secret、可选 session token。
   - 跑同一请求形状的 smoke test，默认只发低成本短响应。
   - 输出 sanitized trace，不打印 secret，不提交任何凭据。

## 5. Closure Verification Matrix

| 层 | 必须验证项 | 通过标准 |
| --- | --- | --- |
| Client contract | Anthropic Messages 形 body 被 gateway 接收 | request 通过 handler validation，响应是 expected canonical/SSE 格式 |
| Tenant context | tenant_id 进入全链路 | routing、sticky binding、audit、metrics 记录同一 tenant_id |
| Routing | Bedrock provider 被选中 | selected provider/account 与 fixture 匹配；无 fallback 到 OpenAI/other |
| Credentials | provider account credential 被加载 | outbound request 使用该 account 的 credential source；日志不泄露 secret |
| Sticky upsert | 首次 tenant_id + session_hash + model 创建 binding | DB/test store 出现一条 binding，provider_account_id 等于选中 account |
| Sticky hit | 第二次同 key 复用 binding | 不创建重复 binding，不切换 account |
| Body translation | Anthropic Messages 转 Bedrock payload | mock 捕获 body 中 model/content/system/request options 符合 Bedrock adapter contract |
| Cache injection positive | system prompt >= 4096 bytes 自动 ephemeral | outbound body 中长 system prompt 位置出现 cache marker |
| Cache injection negative | system prompt < 4096 bytes 不注入 | outbound body 不出现自动 cache marker |
| Endpoint URL | Bedrock Runtime URL/path 正确 | mock 捕获 path 与 configured region/model operation 匹配 |
| Signing envelope | AWS SigV4 header 形态存在 | `Authorization`、date、content hash/security token 条件符合测试期望；不验证真实 secret |
| Binary EventStream decode | mock binary stream 被解码 | scanner/adapter 产出 expected token delta 与 terminal event |
| Canonical event | Bedrock event 转 gateway canonical event | client 看到稳定 chunk ordering、finish reason、usage/cache fields |
| Error mapping | upstream failure 被稳定映射 | HTTP status/error code 可预测，audit 有 failure reason |
| Global metrics | Track D counters 更新 | `cache_token_count.creation_total/read_total/request_count` 按请求预期变化 |
| Per-account metrics | Track P counters 更新 | `cache_token_count_by_account.<id>.*` 与 global 增量一致或有解释 |
| Audit/log trace | 单请求可追踪 | request_id 可关联 tenant、provider、account、model、status、duration、failure reason |
| Secret redaction | 日志和错误无 secret | access key secret、session token、Authorization 不出现在 output/log |

## 6. Non-Scope Detail

- OpenAI Chat Completions 全 E2E只作为后续稳定基线，不放进本 vertical 主线，避免把“老路径能跑”误当作“新 Bedrock 路径闭环”。
- 不在本 vertical 内证明全部 cache hit 经济性；只证明注入、upstream 回传字段、metrics 聚合链路可观测。
- 不做所有 Bedrock model family 兼容性，只选一个 Anthropic Messages-compatible Bedrock model 作为最小闭环。
- 不改生产 schema。若发现 sticky_bindings schema 或 audit schema 不足，记录 blocker，请 Owner 确认后另开 work unit。

## 7. Risks And Mitigations

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| Mock EventStream 与真实 Bedrock 有偏差 | CI 通过但真 AWS 失败 | mock 只覆盖 deterministic gate；Owner 真 AWS smoke 是独立通过条件 |
| 真 AWS 凭据/模型权限不可用 | X 轨无法当天完成 | Y 轨仍给出可重跑 defect signal；X 标记为 Owner credential blocker |
| SigV4 测试过度耦合实现细节 | 小重构导致 E2E 脆弱 | 只断言 AWS-required envelope，不断言内部 helper 或 exact canonical string |
| Metrics 异步刷新导致 flaky | E2E 偶发失败 | 使用 bounded polling，记录 timeout；不使用固定 sleep 作为唯一同步 |
| Sticky binding fixture 污染后续测试 | 路由测试互相影响 | 每个测试独立 tenant/session/model 或 transaction cleanup |
| 错误路径误打真实 provider | 产生费用或污染 audit | failure path 默认只走 mock；真 AWS smoke 只跑 happy path |
| Secret 泄露到 test log | 安全事故 | fake credential 用于 CI；真凭据只来自 env；日志扫描 Authorization/token/key 字样 |
| Scope 膨胀到 provider compatibility suite | 纵向闭环拖成横向回归 | 本 vertical 只选一条模型/一个 account/两类 prompt/一个 failure |

## 8. Blast Radius

- Mock-server E2E 和 fixtures 是低风险测试改动，不应影响 runtime behavior。
- 如果执行阶段需要改 adapter/handler/metrics 代码，属于 medium risk，必须记录原因和被修复的实际 defect。
- 任何 schema、auth、quota、billing、deployment 或 real secret 变更都是 high risk，必须停下请 Owner 确认。

## 9. Decision Points For Owner

- 真 AWS smoke 是否由 Owner 本机手动跑，还是 Owner 临时提供可用环境变量给 agent 运行。
- 真 AWS 选择的 region 和 Anthropic Bedrock model id。
- 若 mock E2E 发现 implementation defect，是否允许 Codex 做小安全修复，还是交给 Claude/Gemini 对应 owner。
- 若需要 schema 或 quota/billing/auth 相关修改，必须另行确认。

## 10. Estimate

- Codex planner artifact：已在本任务完成。
- Mock-server E2E harness：2-4 小时。
- Happy path + sticky + cache positive/negative：2-3 小时。
- Failure path + metrics/audit assertions：2-3 小时。
- True AWS smoke script/owner runbook：1-2 小时。
- Defect fix buffer：2-6 小时，取决于 Bedrock EventStream、translator 或 metrics 失败点。

总估时：**1 个工程日可拿到 Y 轨结论；X 轨取决于 Owner 真凭据和 Bedrock 模型权限，通常 0.5-1 小时补验**。

## 11. Clean-Room Boundary

本 vertical **不需要读 CPA/sub2api/one-api/new-api/All API Hub/Portkey 等外部参考项目源码**。

原因：

- 目标是验证 HUAKAI 已实现链路是否真实工作，不是提取参考行为。
- Bedrock upstream contract 应以 AWS/Anthropic 官方协议和 HUAKAI internal specs/code 为依据。
- Track B/C/D/P 和 A1-A8 都是 HUAKAI 当前实现闭环，planner lane 不应引入外部源码污染。

允许读取：

- HUAKAI internal docs/specs/plans except Claude 同名 plan。
- HUAKAI backend/frontend/test code when execution phase begins。
- Official AWS Bedrock and Anthropic/OpenAI protocol docs if exact upstream contract needs确认。

禁止读取：

- `docs/process/plans/2026-05-08-vertical-closure-claude.md`，直到双方独立草案完成并进入 compare/reconcile。
- 任何非 MIT 或 clean-room 管控 reference project source。
- 外部 reference README 中的 code blocks，如必须引用也需按 clean-room lane guard 另开 specifier/reviewer 工作。

如果后续确实需要回答“某 reference 如何处理 Bedrock/EventStream/cache sticky routing”，必须另开 clean-room specifier lane，并在 prompt 顶部粘贴完整 lane guard；当前 vertical closure 不做这件事。

## 12. Pre-Execution Checklist

- [ ] 确认 synthesized vertical closure plan 已由 Owner 批准。
- [ ] 确认不会读取 Claude 独立 plan，直到进入 compare/reconcile 阶段。
- [ ] 确认测试只使用 fake credentials，真 AWS 只从 env 读取。
- [ ] 确认 `/debug/vars` 或 expvar snapshot 在测试环境可访问。
- [ ] 确认 sticky binding test store/DB cleanup 方案存在。
- [ ] 确认 audit/log sink 可在测试中查询，且 secret redaction 可断言。
- [ ] 确认 failure path 默认指向 mock server。
- [ ] 确认任何 high-risk 文件变更会停下请 Owner 确认。

## 13. Final Output Shape After Execution

执行完成后应输出：

- Y 轨 mock E2E：PASS/FAIL、失败层级、可复现命令。
- X 轨 true AWS smoke：PASS/FAIL/SKIPPED_BY_OWNER_CREDENTIAL、sanitized request_id。
- 验证矩阵逐项状态：PASS / FAIL / NOT RUN / BLOCKED。
- 发现的实现缺陷和建议 owner。
- 是否阻塞下一 slice。

## 14. Owner Summary

本计划建议优先闭环 Bedrock-on-Anthropic，而不是先跑最稳的 OpenAI 路径；原因是当前最大真实风险在刚落地的 Bedrock A1-A8 与 Track B/C/D/P 组合链路。执行上采用 mock-server 先行、真 AWS 后补的双轨，既能进入 CI 成为 release gate，也能避免只靠 mock 带来的 provider contract 盲点。本任务不读取外部参考源码，不引入 clean-room 污染；如后续需要参考项目行为，必须另开 clean-room lane。
