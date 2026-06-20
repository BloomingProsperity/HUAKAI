# 配额拒绝透出窗口种类(quota_window)— parity build_now

## 背景与真码核实(workflow wf4qjytks)
HUAKAI 已建完整多窗口配额引擎(quota/types.go:39-48 WindowKind:none/fixed/calendar_day/week/month/manual;
rate_window.go 各自算边界),一个租户可挂多条 policy(各带 Window.Kind)。拒绝(429)时引擎**内部已知**
是哪条 policy/哪个窗口触发(service.go evaluatePolicies → exceededDecision 时 policy 在手),但:
- Decision(types.go:79)**无 WindowKind 字段**,exceededDecision 构造时丢掉了 policy.Window.Kind。
- 429 body(chat_completions_error.go writeInsufficientQuotaBody)只回 type/code/message + 可选 window_resets_at,
  **不含窗口种类**。客户端无法区分"今天日额满(明天恢复)"还是"本月月额满(月底恢复)"。
- 反差:window_kind 在 admin/自助 quota VIEW 接口(userkeycontrols)已是一等 JSON 字段,唯独运行时 DENY 没有。
→ 纯 additive 透传缺口(内部知道、只差透出),gap 真实存在。

## #16 三镜像(specifier 已读源码)
- **sub2api(成熟范本,做得最全)**:多窗口(日/周/月+速率窗口)各有独立命名错误哨兵 + 窗口专属 message +
  per-window reset(billing_service.go / subscription_service.go / gateway_handler.go billingErrorDetails),
  窗口种类经 message+metadata 透给客户端。这是默认 tiebreaker 范本。
- **new-api**:配额是单一累计 amount 计数器,无多窗口概念 → no-equivalent。
- **CLIProxyAPI**:纯 relay 无用户侧 windowed-quota → no-equivalent。
- HUAKAI delta(生态升级):已有多窗口引擎,本切片把"哪个窗口"以结构化 quota_window 字段透出(不抄 sub2api
  的 per-window 哨兵实现,复用自有 WindowKind),并顺带写进审计 payload。

## 范围(纯 additive,不改任何配额判定/限额语义)
1. quota/types.go:Decision 加 `WindowKind WindowKind` 字段(零值=未知/无窗口)。
2. quota/service.go:exceededDecision 填 `WindowKind: policy.Window.Kind`;assessmentPayload 加 `window_kind`(审计也受益)。
3. quotaenforce/settler.go:加 `DenyWindowKind(result, err) string`(与 DenyRetryAfter 同源:DenyError.Decision
   或 fail-soft result.Decision);windowKindLabel 把 none/空抑制为空串,其余(日历/固定/manual)原样透出。
4. gatewayhttp/chat_completions_error.go:writeInsufficientQuotaBody 加 windowKind 参数,非空才写 body 的
   `quota_window`;dispatch.go 拒绝处传 DenyWindowKind。两个非窗口调用方传 ""。

## 成功标准
- 三层判别测试 + 变异证(已验):exceededDecision 带 kind(删→quota 单测红);DenyWindowKind 全分支
  (删 none 抑制→红);e2e 429 body 含 quota_window=calendar_month(删 body 写出→红);manual 窗口
  quota_window 透出但与 window_resets_at 解耦。
- 对未配多窗口/budget(rpm/tpm)拒绝:WindowKind 空→不写 quota_window,**零行为变化**(已验 budgetenforce 绿)。
- quota+quotaenforce+gatewayhttp+budgetenforce 全量 + cmd/gateway 构建 + codebudget 全绿(已验)。

## blast radius / 风险
- 纯加字段 + 加可选 body 键,不改限额/判定/拒绝与否,不动 schema、不动 money 结算。default-behavior 不翻转
  (仅多窗口策略租户多一个信息字段)。collision:核心在 off-collision 的 quota/quotaenforce;gatewayhttp 仅
  就地改既有函数(不新增文件,codebudget 绿)。
- 审计 payload 加 window_kind 是 additive 键;下游审计消费者按 key 取值,新增键不破坏既有解析。

## 决策点(Owner)
属配额-enforcement **输出面**(非判定/限额变更),纯只读透传 + 不改默认行为,按规则不触发硬 gate;
本切片自主落地,PR 中显式说明,Owner 若不认可可回退(纯减字段)。
