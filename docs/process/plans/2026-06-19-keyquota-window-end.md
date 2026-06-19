# Plan — per-key 配额视图补 window_end 重置边界 (生态 parity 完整性切片)

- 日期: 2026-06-19
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」+「别反复问」; 收尾挖矿队列候选B, 最后一个)
- 基线: origin/feat/frontend-portal @ 4ee77175
- 分支: feat/keyquota-window-end

## 背景 (禁止凭记忆 — 真码已核)

收尾挖矿队列候选B: per-key 自助配额视图 KeyQuotaView 缺窗口重置边界。
- producer 真实: GetKeyQuota(key_control_service.go:127)调 ListCurrentWindowsForScope → 它**仅返回 cost_usd 窗口**(pg_store_window_reads.go:21 `[]string{string(MetricCostUSD)}`), 循环(:138)读 SettledValue+ReservedValue 算 UsedUSD, 但 **w.Window.End 被读后丢弃**。quota.Window 有 Start/End(quota/types.go:54-55), CurrentWindowRead.Window(:162)。
- gap: KeyQuotaView(types.go:40-58)只有 Used/Remaining USD, 无 window_end → 用户看不到配额何时重置/释放。
- **包内/repo 先例**: 广义自助 /quota 端点(mequotahttp/handler.go:55/118)已暴露 `window_end: w.Window.End`(每窗口一条)→ per-key 视图不对称缺这维。

disjoint(仅 userkeycontrols + userkeycontrolshttp 直出无映射[quota_group_handlers.go:86-91]+ openapi, **不碰 userkey/userkeyhttp 碰撞包, 不碰 mequotahttp**[仅作先例读]), 无迁移/money/auth/avoidance; 与 proxies 0 碰撞(userkeycontrols/userkeycontrolshttp 不在碰撞面)。

## 设计决策(自决, 非高风险叉)
- **多窗口取最早 End**: 防御性循环可能多窗口(同为 cost_usd), 取 soonest End = 最近一次重置释放, order-independent, nil 当无窗口。
- **绝对时间戳形式(window_end), 不加 seconds-to-reset**: 对齐 HUAKAI **既有** /quota 端点形式(mequotahttp 只给绝对 window_end 无相对秒)→ repo 内一致优先于抄 sub2api 更富形式。seconds-to-reset 可后续。
- **顺带同步 schema 陈旧**: UserAPIKeyQuotaResponse(openapi.yaml:12878)additionalProperties:false 但**漏了 used_usd/remaining_usd**(KEY-007 加了 Go 字段没加 schema)→ 响应实际返回它们却不在 schema = 既有 doc bug。本切片把它们 + window_end 一并补全(同响应对象, 不补会留 schema 撒谎)。

## #16 三镜像 (specifier lane, 本轮新探针 #16-keyquota-window-end 完成)
「per-key/per-token 配额状态视图是否暴露当前窗口重置时间」:
- **sub2api@e34ad2b(最强先例)**: subscription progress 视图(subscription_service.go:961-1027, subscription_handler.go:89-113)每窗口给 limit/used/remaining + **绝对 resets_at + 相对 resets_in_seconds(floored 0)**;per-key DTO 给滚动限速档(5h/1d/7d)的算出窗口结束戳(dto/mappers.go:108-119)。**但 sub2api 的 per-KEY 美元预算是 lifetime 计数(无重置)**(service/api_key.go:87-95), 重置边界在 subscription 窗口 + 限速档。
- **new-api@1ac0f58**: per-token 配额是 **lifetime 余额**(model/token.go:23-28)只减不自动重置, 仅有固定 expired_time(到期≠重置); 窗口重置只在单独 subscription 子系统(model/subscription.go:259 next_reset_time)非 token 级。
- **CLIProxyAPI@2a050dc**: **no-equivalent**——纯中继, inbound key 是裸字符串列表无 per-key 配额(config/sdk_config.go:43); 唯一重置概念是上游账号冷却(非本地配额)。

### HUAKAI delta — 生态/completeness(+小架构点)
| 维度 | sub2api | new-api | CLIProxy | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| per-key 配额视图 | ✓(但美元预算 lifetime 无重置) | ✓(token lifetime 余额) | ✗(裸字符串) | ✓ windowed 美元配额 | — |
| per-key 配额视图给窗口重置时间 | 仅 subscription/限速档 | ✗(仅 expiry, 重置在 subscription) | ✗ | **✓ per-key 美元窗口的 window_end** | 生态(completeness)+小架构(per-key 美元配额是 windowed 带重置, 两镜像都在 key 级是 lifetime) |
- **delta**: 给 per-key 美元配额视图加窗口重置边界。诚实定性: 主要是 生态/completeness(对齐 HUAKAI 自有 /quota 的 window_end 形式)+ 一个小架构点(HUAKAI per-key 美元配额本身就是 windowed 带真重置, sub2api/new-api 在 key/token 级都是 lifetime 无重置)。形式取绝对戳(同 /quota), 不抄 sub2api 的 +seconds-to-reset。

## 实现范围 (success criteria)
- types.go: KeyQuotaView + `WindowEnd *time.Time json:"window_end,omitempty"`。
- key_control_service.go: 循环里追踪 soonest End → view.WindowEnd(else 分支内, 同 UsedUSD, 进度读失败/nil 则 WindowEnd nil)。
- openapi.yaml: UserAPIKeyQuotaResponse 补 window_end + 同步陈旧 used_usd(required)/remaining_usd。
- 测试(变异验证): 扩 TestKeyQuotaUsed —— 两窗口(较早 End 放第二个窗口防 windows[0] 取巧)断 view.WindowEnd==最早; 无窗口 case 断 nil。删 view.WindowEnd 赋值→nil→红(已证, tab 无关变异验真)。

## blast radius
- 仅 userkeycontrols/{types.go,key_control_service.go}(+test)+ openapi.yaml。无 db/sqlc/迁移/依赖/money/auth/schema-迁移。codebudget: +~10 行远 < 600。manualWindowEnd 哨兵(9999, rate_window.go:7)会原样透出, 同 /quota 既有行为, 不特判保持一致。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI) → squash → ff。**收尾挖矿 3 候选(PR#49/#50/本)全清后该真见底 → 下轮给 Owner 合并菜单**。

## Clean-room 出处 (#11(d))
- Source files read: sub2api@e34ad2b {handler/dto/types.go, handler/dto/mappers.go, handler/subscription_handler.go, service/api_key.go, service/api_key_service.go, service/subscription_service.go, server/routes/user.go};
  new-api@1ac0f58 {model/token.go, controller/token.go, model/subscription.go, service/subscription_reset_task.go, middleware/model-rate-limit.go};
  CLIProxyAPI@2a050dc {internal/config/sdk_config.go, internal/api/handlers/management/config_lists.go, internal/api/handlers/management/api_key_usage.go, sdk/cliproxy/auth/types.go, sdk/cliproxy/auth/selector.go}
- 首引 recency#12: 三 SHA 同 [[parity-audit-2026-06-18]] 已核 active@2026-06-18(GitHub API 沙箱不可达, 复用并记 SHA)。
- Lane: specifier(独立 agent #16-keyquota-window-end). Agent: Claude PM. UTC: 2026-06-19
