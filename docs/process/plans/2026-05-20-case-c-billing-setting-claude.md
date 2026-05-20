# Case-C 计费策略可配置面板设置 — Claude 计划

> 平行计划之一(CLAUDE.md #10)。Claude 独立起草,未参考 codex 版本。
> 配对文件: `2026-05-20-case-c-billing-setting-codex.md`。

## 背景

Owner 2026-05-20 决定: 流式计费的边缘情况 "case C" 做成操作面板可配置项。

**case C**: 流式请求打到上游,上游报了 input tokens(`prompt_tokens>0`)但 `completion_tokens=0`、
零 output,流在 `[DONE]` 前中断。当前 `gatewayhttp/chat_completions_stream.go` 结算门控
`settle := Chargeable() || DeliveredTokenCount>0 || EndClass==AmbiguousUsage` 把 case C
判为非计费 + 零交付 → abort(不计费、不记重放、可重试)。

Owner 给的候选取值: `no_bill`(当前默认) / `no_bill_record`(不计费但审计记账) / `bill_input`(照收 input)。

## 范围 (Scope)

**In**:
- 新建租户级 KV 设置表 `billing_settings`(对照已有 `0025_email_settings`)。
- 设置项 `zero_output_stream_billing` ∈ `{no_bill, no_bill_record, bill_input}`。
- 全局默认值(env config),租户无行时回退。
- 计费热路径(`forwardSSEAndSettle`)按设置分支处理 case C。
- `no_bill_record`: abort 路径把 observed input tokens 写进零成本审计记录。
- admin API endpoint 读/写该设置(租户边界严格)。

**Out / 分期**:
- `bill_input` 真正落地需改 `billing/state.go` 计费状态机(高风险)→ 见 D2,默认分期。
- 前端面板 UI → 前端疑似冻结期,后端先行,UI 待解冻(见 D3)。

## 设计

**存储**: 新表 `billing_settings`,结构对齐 `email_settings`(`tenant_id, setting_key,
setting_value, updated_at, updated_by`,`UNIQUE(tenant_id,setting_key)`)。理由: 复用已验证的
租户级 KV 模式;未来其它计费策略开关同表扩展,不必每个开关一张表。

**全局默认**: env `HUAKAI_BILLING_ZERO_OUTPUT_DEFAULT`(默认 `no_bill`)。租户无显式行 → 用默认。
默认保持 `no_bill` = 当前行为,确保上线零行为变化、只有运营者主动 opt-in 才改变。

**热路径读取**: `BillingSettingsResolver`,进程内缓存 + 短 TTL(30-60s)或写时失效。
结算发生在流结束后,多一次缓存命中读取可忽略;只有 cache miss 才查 DB。

**计费逻辑**: `forwardSSEAndSettle` 里,仅当门控将要 abort **且** `draft.TokensInput>0`
(= case C: 上游报了 input 但零 output)时,查设置:
- `no_bill` → abort(现状)。
- `no_bill_record` → abort,但把 observed input tokens 传给 Abort 审计记录。
- `bill_input` → settle 并按 input token 计费(依赖 D2 的 state.go 改动)。

真零交付(`TokensInput==0` 也为零)→ 永远 abort,不受设置影响。设置只管 case C。

**admin API**: `GET/PUT /admin/billing-settings`,operator 鉴权,**强制按 `tenant_id` 过滤**
(DR-001;Owner P1 清单已发现 invitation 的租户边界漏洞,本表从第一天就不能犯同样错)。

## 成功标准

- 迁移 `0042_billing_settings` 可正向 + 可回滚。
- 默认 `no_bill` 时,全量行为与当前一致(回归测试证明)。
- `no_bill_record` 时: case C abort 且零成本审计记录带 observed input tokens。
- 设置 per-tenant 生效;租户 A 改设置不影响租户 B。
- admin API 跨租户读/写被拒。
- `go test ./...` 0 FAIL。

## 影响面 (Blast radius)

- 计费热路径 `forwardSSEAndSettle` —— 但 case C 分支只命中"已经要 abort"的罕见流,默认值下完全不变。
- 新表 + 新 admin endpoint —— 加法变更,不动现有表。
- `bill_input`(若 D2 选现在做)会动 `billing/state.go`,影响所有计费路径 → 单独 slice + 单独 review。

## 可能出错的点

- 热路径 DB 读拖慢延迟 → 缓存兜底。
- admin API 租户越权 → 每个查询带 tenant_id,加 AT 覆盖跨租户拒绝。
- `bill_input` 改 state.go 误伤其它计费路径 → 分期、独立 review、不与本 slice 混。
- 缓存陈旧 → 运营者改设置后 TTL 内才生效;可接受,或写时失效。

## 需 Owner 拍板的决策点

- **D1 范围**: per-tenant(每商户自定)+ 全局默认 —— 推荐。或纯 global(整部署一个值,更简单但商户无法各自定)。
- **D2 `bill_input` 时机**: 推荐先上 `no_bill` + `no_bill_record`(都不向用户收费,无需改 state.go),
  `bill_input` 入 roadmap 单独 slice。或现在一起做(+1~2 天,动高风险计费状态机)。
- **D3 前端**: 推荐后端先行(API 即可配)+ 前端面板 UI 待前端解冻。或确认前端未冻、一起做。

## 估时 / 拆分

| Sub-phase | 内容 | 估时 |
|---|---|---|
| A | 迁移 `0042_billing_settings` + sqlc 查询 + store | 0.5 天 |
| B | resolver + 进程内缓存 + 全局默认 config | 0.5 天 |
| C | 热路径 case C 分支(`no_bill` / `no_bill_record`)+ 回归测试 | 1 天 |
| D | admin API endpoint(读/写,租户边界 + AT) | 0.5-1 天 |
| (E) | `bill_input`: state.go 改动 + 计费(D2 选做才进) | +1-2 天 |
| (F) | 前端面板 UI(D3,前端解冻后) | +0.5-1 天 |

核心 A-D ≈ 2.5-3 天 codex。E、F 按 Owner D2/D3 决定。
