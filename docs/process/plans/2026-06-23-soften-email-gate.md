# 计划:production 邮箱门惰性化(软化为选项,默认不拦启动)

- 日期:2026-06-23
- 切片:soften/email-gate-lazy(四条软化之③)
- 基线:origin/feat/frontend-portal @9284f465
- Owner 绿灯:已(AskUserQuestion 全选四条软化)

## 背景

现状:`HUAKAI_RELEASE_MODE=production` 下,`ValidateProductionReleaseGate`(`backend/internal/email/sender_factory.go:213-234`)在启动期遍历每个 active 租户,任一租户未配齐 SMTP 或未开邮箱验证即 `return err`,wiring(`backend/cmd/gateway/wiring.go:839`)把它包成 fatal 拒启。这是单运营者首次上线的最大隐性硬门(见 [[go-live-readiness-2026-06-19]] 的 email 门)。

## 三镜对照(§16,planning workflow 已核 file:line)

| 项目 | 邮箱门做法 |
|---|---|
| new-api @1ac0f58 | 无启动门;发信入口 `common/email.go` 调用瞬间未配才返回错误;邮箱验证是运行时 admin 开关默认 false |
| sub2api @e34ad2b | 无启动门;`internal/service/email_service.go` 发信前 host 空即返回 `EMAIL_NOT_CONFIGURED` 请求时错误 |
| CLIProxyAPI @2a050dc | 纯 relay,无用户注册/无 SMTP 子系统(no equivalent) |

结论:三镜无一在启动期强制 SMTP,验证邮件一律 default-off + 请求时惰性。HUAKAI 当前比三镜都硬。**默认 tiebreaker=sub2api 的"请求时惰性"。**

## 关键发现(亲核真码,简化了设计)

**请求时降级已正确就位,无需改 userauth/VerificationPolicy:**
- `VerificationPolicy.EmailVerificationEnabled`(sender_factory.go:258-267):租户没配 SMTP 时 `store.Load` 返回空设置(无 err)→ `parseBool("")`=false → 返回 `(false, nil)`。
- `requireEmailVerification`(`backend/internal/userauth/service.go:481-489`):`err==nil` 时返回 `enabled`(此处=false)→ **注册不要求验证 → 用户直接 active**(三镜惰性行为)。
- 仅当 `store.Load` 真错(DB down)才返回 err → 落到 `RequireVerified=true` → **fail-safe 要求验证,不绕过**。即"空设置无 err vs 真 err"天然区分,**计划担心的"DB 故障误判成未配=放行"漏洞不存在**。

**所以本切片只需软化那个启动硬门。**

## 设计(最小改动)

1. 新增 `HUAKAI_REQUIRE_EMAIL_GATE`(默认 false)。新文件 `backend/cmd/gateway/email_gate.go`:
   - `requireEmailReleaseGate() bool`:读该 env(仅 "true" 大小写不敏感开启,与 `HUAKAI_DEV_AUTH_RETURN_TOKEN` 同款读法)。
   - `emailGateStartupError(gateErr error, required bool) error`:纯决策——门未过(gateErr!=nil)时,仅当 required 才返回该错误拒启;默认 nil(软化放行)。
2. 改 `backend/cmd/gateway/wiring.go:838-842` 调用点:production 下仍跑 `ValidateProductionReleaseGate`,但失败时经 `emailGateStartupError` 决定 fatal 还是 warn;默认 warn-log 继续启动,设 `HUAKAI_REQUIRE_EMAIL_GATE=true` 恢复旧 fail-loud。
3. `ValidateProductionReleaseGate` 函数本体不动(保留为可复用诊断)。
4. `.env.prod.example` / `.env.direct.example` / `docs/deploy/production-bootstrap.md` 增 `HUAKAI_REQUIRE_EMAIL_GATE` 说明 + 更新 email 门描述(production 默认不再因缺 SMTP 拒启)。

## 默认行为变化(default-flip,Owner 已绿灯)

- 改前:production 缺 SMTP/邮箱验证 → 启动 fatal 拒启。
- 改后(默认):production 缺 SMTP → 启动只 warn;注册走"验证关闭"分支用户直接 active;显式触发验证邮件/密码重置的接口在 SMTP 未配时请求期返回 503 `EMAIL_BACKEND_UNCONFIGURED`(已存在)。
- **逃生舱**:已配齐 SMTP 的部署行为完全不变;想恢复旧严格行为设 `HUAKAI_REQUIRE_EMAIL_GATE=true`,等价零行为变更。

## 测试策略(判别式)

- `requireEmailReleaseGate()`:unset→false / "true"→true / "TRUE"→true / "false"→false。变异:把默认翻成 true,unset→false 即 RED。
- `emailGateStartupError`:(someErr,false)→nil(软化)/(someErr,true)→someErr(拒启)/(nil,*)→nil。变异:把 `&& required` 改成无条件 return err,则 (someErr,false)→nil 即 RED——精确守护"默认不拦启动"。

## blast radius / 风险

- 改动限 cmd/gateway(wiring 调用点 + 新小文件)+ 文档;不碰 userauth、不碰 email 包本体、无 schema、无新依赖。
- 风险:default-flip(production 安全姿态变化)。已用逃生舱(REQUIRE_EMAIL_GATE=true)保留旧行为;请求时降级真码已 fail-safe(DB 错不绕过验证)。多轮对抗审查盯死"降级不反成漏洞"。
