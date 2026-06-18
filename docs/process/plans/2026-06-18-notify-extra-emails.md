# Plan — 通知设置 extra_emails 双向接线 (inert-gap 切片)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner 已授全权自主实现+合并)
- 基线: origin/feat/frontend-portal @ 141b956a
- 分支: feat/frontend-admin-notify-extra-emails

## 背景 / 动机 + 范围修正 (禁止凭记忆 + 禁止假绿)

inert-gap 猎取(wa0iey8lu)把 notify 的 `extra_emails`(额外抄送邮箱) 与 `threshold_type`(fixed/percentage)
并列报为「建了无写路径」。读真码核实后**修正范围 —— 仅接线 extra_emails**:

- **ExtraEmails(`internal/notify/types.go:50` `[]string`)**: store 持久化✓(store.go:204/217/235 INSERT+UPSERT
  + 204/138 SELECT/decode)、ValidateSettings 校验✓(types.go:157-167: ≤10 + rejectHeaderInjection + mail.ParseAddress)、
  notifier **消费✓**(notifier.go:555 sendExtraEmailCopies 逐个独立发送, :259/:294 + provider:119 调用; scrubInactiveFields
  不抹它, 所有 notify 类型都保留)。唯一缺口: `notifySettingsRequest`(notify_handler.go:38-48) / `notifySettingsResponse`
  / `notifyRequestToSettings` / `notifyResponseFromSettings` 都不带它 → **真双向 inert(不能设也不能读)**, 接线即真功能。
- **ThresholdType(types.go:49 `string`)**: store 持久化✓ + ValidateSettings 校验✓(∈{fixed,percentage}), 但
  **零消费者** —— grep 全 backend, notifier.go:112-122 只读 `BalanceThreshold` 金额, **从不读 ThresholdType**;
  `percentage` 仅出现在 ValidateSettings 检查处, 无任何代码解释百分比。**接线它 = 暴露静默无效的死开关, 伤 UX**
  (正是 Owner 「别 bolt-on 没验证/伤 UX 的东西」警告)。→ **不接线, 记 Feature Preservation roadmap**:
  需先在 notifier 低余额 crossing(notifier.go:112) 建百分比解释消费者(threshold = 总充值×pct/100, 需总充值数据源),
  再连同 read/write 一并暴露 —— 那是独立的更大切片, 非本「接线」切片。

## #16 三镜像研究 (clean-room specifier, 已读真源)

调研「额外抄送邮箱 + 阈值类型」:
- **sub2api@e34ad2b (默认 tiebreaker)**: extra_emails 是一等公民, 列表(每条 {email,disabled,verified}), 用户行 JSON 列,
  上限 3, 大小写去重, gin email 校验, **投递逐个独立发送非 CC**(防收件人互见+单失败不连坐), 跳过停用/未验证;
  专用验证子端点(发码→验证)。threshold_type = fixed|percentage 枚举, 但**类型本身无用户写路径**(上游缺口),
  且经独立证据确认**非 money**(扣费在前同步、通知判定在后 fire-and-forget 只读)。
- **new-api@1ac0f58**: 无 extra_emails(单邮箱+webhook/bark/gotify); 阈值仅固定额无枚举; 走整体 PUT /api/user/setting;
  阈值同确认**非 money**(PostConsumeQuota 扣费不读阈值, 预警在扣费后只读)。
- **CLIProxyAPI@2a050dc**: 纯 relay, 两项均 **no equivalent**(无通知设置/用户余额/邮件子系统, source-cited)。

### 取舍 (sub2api 默认 tiebreaker)
- extra_emails 形态: HUAKAI 已是**纯 `[]string`**(非 sub2api 的结构体三态), 上限**10**(HUAKAI 自有, 非 sub2api 的 3),
  投递已是**逐个独立发送非 CC**(sendExtraEmailCopies, 与 sub2api 市场验证做法一致)。本切片**不改这些既有决策**,
  只把字段接进 request/response —— 最小 delta。
- 写面: 字段进**既有整体 PUT**(notifySettingsRequest), 非专用子端点 —— 因 store/校验/投递全在, 缺口只在请求体。
  sub2api 的逐邮箱验证流是后续增强(专用子端点才需要), 本切片先做对称的整体-PUT 列表(最低 delta)。
- delta vs 镜像: HUAKAI 把 extra_emails 暴露进**用户自助 + 管理员代设**两条既有 PUT(notify_handler.go 两 handler 共用
  notifyRequestToSettings), 一次改动覆盖双路径; 校验复用既有 ValidateSettings(写路径 UpsertSettings→ValidateSettings:174)。

## 实现范围 (success criteria)

后端(controlhttp/notify_handler.go, 无新文件):
1. `notifySettingsRequest` 加 `ExtraEmails []string \`json:"extra_emails,omitempty"\``; `notifyRequestToSettings`
   映射 `out.ExtraEmails = req.ExtraEmails`(原值透传, 校验交既有 ValidateSettings)。
2. `notifySettingsResponse` 加 `ExtraEmails []string \`json:"extra_emails,omitempty"\``; `notifyResponseFromSettings`
   映射 `resp.ExtraEmails = settings.ExtraEmails`(GET 读回, 支持 read-modify-write)。
   (DisallowUnknownFields 已在 decodeNotifySettingsRequest:219 → 未知字段仍 400, 不受影响。)

前端(只接线测功能):
3. notifications.ts `NotifySettingsRequest` + `NotifySettings`(响应) 各加 `extra_emails?: string[]`。
4. notify-settings-form.ts `buildNotifySettingsBody` 非空时输出 extra_emails(返回类型加 `string[]`);
   `validateNotifySettings` 加 extra_emails fail-fast(≤10 + 每条 email 形态, 镜像 ValidateSettings)。

强测试(变异验证): 后端 handler 交付 3 测 —— ① 往返(PUT extra_emails → service 收到 ExtraEmails 的请求映射; +
PUT 响应回带 extra_emails 的响应映射, 与 GET 共用 notifyResponseFromSettings 故同时锤住 read-back); ② 非法 email → 400;
③ >10 → 400。后两测同时证明「映射的非空列表确到达 ValidateSettings」(漏映射则校验见空列表放行→200)。
header 注入(CRLF) 由 notify 包单测(notify_extras_test.go) + handler 的 ErrHeaderInjection→400 路由(notify_handler.go writeNotifyError)
**传递性覆盖**, 不另加 handler 级专测 —— CRLF payload 同被 rejectHeaderInjection 与 mail.ParseAddress 拦截, 单独的 handler CRLF
测试无法判别 rejectHeaderInjection 守卫(移除它 ParseAddress 仍 400), 故按测试质量纪律(#14)不加冗余非判别测试。
前端 builder/validate 各有判别测试。每条变异转红再还原。

## blast radius
- notify 写/读 handler + 一映射 + 两类型 + 一 builder。notifier 投递逻辑**不改**(已消费 ExtraEmails)。store/schema
  **不改**(列已存在)。无 money(纯通知抄送)。无避让面。无新文件/新包。

## 可能出错 & 缓解
- extra_emails 校验绕过 → 复用既有 ValidateSettings(写路径已调), 不在 handler 另做弱校验。
- 走私其它字段 → DisallowUnknownFields 已在。
- 收件人互见泄露 → 投递端已是逐个独立发送(非 CC), 本切片不改。
- threshold_type 死开关 → 明确不接线 + roadmap(见上)。

## 门禁
codex 401 → ultracode 对抗审查(refute-by-default) = #8 替代门禁; 提交门 = 无未结 S0/S1。squash 合并入
feat/frontend-portal → 清理 worktree/锁 → ff main。
