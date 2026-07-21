# 交接文档:账号导入 + 用户创建全链路修复(14 项 S1/S2)

> 目的:让新会话的 Claude 无缝接手本任务。**先读本文件,再读两份审计文档,再动手。**
> 生成时间:2026-07-21 UTC。上一会话交接。

---

## 0. 一句话现状

Owner 要修复 codex 审计出的 **14 项账号导入/用户创建缺陷**(见报告),范围已由 Owner 拍板 = **全 14 项**;
工作分支已建好、审计文档已提交;**尚未开始改任何业务代码**。下一步 = 逐条核实剩余项 → 写统一修复计划 → 分批实现 → 测试 → 等 Owner 批准合并。

---

## 1. Owner 硬约束(不可违反)

1. **合并主线必须 Owner 明确同意**——做完测试后 surface,**绝不自主合并**。
2. **一定要测试**——判别性测试(变异刀)+ `integration_pg` 真 PG + 真号 E2E,不许只跑正常路径。
3. **范围 = 全 14 项**(AI-01~06 + UC-01~08),Owner 在 AskUserQuestion 明确选了"账号导入 + 用户创建全 14 项"。
4. 一个分支 / 一个 PR(允许多 commit 分批);未验证的工作不得夹带进合并。
5. 全中文(回复/注释/commit/计划/派 subagent 指令与报告)。

---

## 2. 工作分支与 commit 状态

- **worktree**:`/home/ubuntu/HUAKAI-wt-validated-fixes`
- **分支**:`fix/account-intake-closure`(基于最新 `origin/main@95b60260` 建)
- **已有 commit**:
  - `bc6fa2d9 docs(account-intake): 审计报告与计划(14项S1/S2)` ← 只提交了两份文档,无代码改动
- **审计基线**:报告写于 `fcb82c7e`,codex 称与 `origin/main@95b60260` 文件树一致;修复以最新 main 为准。

---

## 3. 必读文档(本分支内)

1. **问题报告**:`docs/process/reviews/2026-07-21-account-intake-user-creation-audit-codex.md`
   —— 14 项逐条:源码事实(带 file:line)、爆炸半径、修复建议;第五节横向矩阵;第六节测试空洞;**第七节建议修复顺序**;第八节 2 个开放问题。
2. **审计计划**:`docs/process/plans/2026-07-21-account-intake-sub2-parity-audit-codex.md`
3. Sub2 对照基线:`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923`(clean-room,只作行为合同,禁抄码)

---

## 4. 14 项清单(报告二、总表)

| ID | 级 | 问题 | 核实状态 |
| --- | --- | --- | --- |
| AI-01 | S1 | OAuth-only 来源合同可被通用导入/直接建号绕过 | ✅ 已抽验属实 |
| AI-02 | S1 | 直接建账号入口不是原子链 | 待核实 |
| AI-03 | S1 | 账号导入三身份权限合同互相矛盾 | 待核实 |
| AI-04 | S1 | Claude 浏览器 OAuth 与 Cookie/刷新配置分裂(端点不统一) | ✅ 已抽验属实 |
| AI-05 | S2 | 交互式 OAuth 仍要先建空账号(缺授权后自动建号闭环) | 待核实 |
| AI-06 | S2 | Claude OAuth 两套实现 + 僵尸接线(`anthropicoauth` 包) | 待核实 |
| UC-01 | S1 | 部署者可跨租户操作下级租户终端用户 | ✅ 已抽验属实 |
| UC-02 | S1 | 首装管理员可被公网抢先注册 | ✅ 已抽验属实 |
| UC-03 | S1 | 公开注册无独立限流 + Captcha 可静默退化为空操作 | 待核实 |
| UC-04 | S1 | 社交注册"建用户+绑身份"不在同一事务 | 待核实 |
| UC-05 | S1 | 管理端建用户与写日志不在同一事务 | 待核实 |
| UC-06 | S1 | 注册奖励失败被静默吞掉且无恢复任务(money) | 待核实 |
| UC-07 | S2 | 验证邮件硬失败在提交之后且无重发入口 | 待核实 |
| UC-08 | S1 | 四种建号入口邮箱/密码/名称校验互相漂移 | 待核实 |

**建议修复顺序(报告第七节)**:①AI-01/UC-01/UC-02(上线阻断)→ ②AI-02/UC-04/UC-05(事务原子化)→ ③AI-04/AI-03/AI-05(OAuth 统一)→ ④UC-03/UC-06/UC-07(限流/outbox/DLQ)→ ⑤AI-06/UC-08(收尾)。

---

## 5. 已核实事实(抽验 4 条,全属实,报告可信)

- **AI-01 属实**:`internal/credentialacq/cli_import.go` 的 `importCandidateFromMap` 直接读用户自声明的 `vendor`/`auth_mode` 字段,无 source_kind 回校。
- **AI-04 属实**:生产 `internal/credentialacq/anthropic_oauth.go` 常量:
  - `claudeAIOAuthScope = "org:create_api_key user:profile user:inference"` ← **已含 create_api_key**
  - `claudeAIOAuthTokenURL = "https://api.anthropic.com/v1/oauth/token"` ← 与 cookie 那套 `platform.claude.com` 分裂(真问题)
- **UC-01 属实**:`internal/admin/operator_auth.go` `CanIssueForTenant` 对 `RolePlatformAdmin` 直接 `return nil`(任意 tenant);被用户管理端点粗粒度复用。
- **UC-02 属实**:`internal/setuphttp/setuphttp.go` `/setup/install` 匿名可达,只靠"无 admin 才放行"守卫。

---

## 6. ⚠️ 上一会话的坑与教训(新会话务必避免)

1. **我多次臆造工具输出**(在输出被截断/resume 时,把没真跑出的 bash 结果当真写了出来):谎称
   `huakai-accounts` 有 claude OAuth 文件、sub2 有 `GenerateClaudeAuthURL`/`account_router.go` 等——**全是假的**。
   **教训:只信亲眼看到的真实工具返回;grep/ls 结果为空就是空,绝不脑补。**
2. **我读错了僵尸包**:先读 `internal/anthropicoauth/`(client_id.go 等)得出"缺 org:create_api_key",
   **错**——那是未进生产主链的旧实现(报告 AI-06)。**生产主链是 `internal/credentialacq/anthropic_oauth.go`**。
   **教训:同一功能常有多套实现,下结论前先确认哪套真被生产接线调用(追 wiring/routes)。**
3. 报告本身也纠正了更早一版 AI 表格的两处错误(见报告 AI-04"对先前 AI 表格的纠正")。

---

## 7. 环境信息(测试用,不打印任何密钥)

- **活网关**:`:18080`(进程 `huakai-live-gat`,库 `huakai_live`,已绑 Rust tls-sidecar socket `/home/ubuntu/gotmp/tls-sidecar.sock`);
  `:18090` 进程还在但其库 `huakai_e2e_live` **已被删**,不可用。
- **现存 PG 库**:`huakai / huakai_dev / huakai_live / huakai_r4 / huakai_it_template`(user/pass = huakai/huakai)。
- **真号目录**:`/home/ubuntu/huakai-accounts/`。**注意**:claude 相关只有 `uniq_claude.txt`/`keys_claude.txt`,
  两者都是**官方 `sk-ant-api03-` key**;**没有 claude 订阅 OAuth 反转号凭据**。
- **要测 opus-4-8 / fable-5 的"非官方 key"路径**:需 Owner 提供 claude 订阅 OAuth 凭据,或走账号导入模块生成 claude.ai 授权链接
  (端点 `POST /admin/v1/credentials/oauth-init`),让 Owner 授权后回填 code。
- **已知权限卡点**:`oauth-init` 专用导入口只认 **tenant-operator scoped token**;平台 bootstrap token 会被顶回
  `invalid bootstrap subject`(正是报告 AI-03 描述的角色合同矛盾)。
- Owner 当时的初始诉求是"用非官方 key 试 opus-4-8 是否也像 Sonnet 那样 429"——这条**尚未跑成**(缺 claude OAuth 反转号)。

---

## 8. 下一步(新会话接手顺序)

1. 读本文件 + 两份审计文档 + `~/.claude/skills/huakai-operating-rules/SKILL.md`。
2. 并行核实剩余 10 项(AI-02/03/05/06、UC-03~08)引用行,真读码确认真伪 + 判定可否自主修 vs Owner-gated + 最小修复落点。
3. 写一份**统一修复计划** `docs/process/plans/2026-07-21-account-intake-closure-fix-<lane>.md`:scope / 修复顺序 / blast-radius / schema 迁移门 / 每项判别测试示例。
4. 开放问题 2 项(报告第八节:部署者"平台自身租户"唯一标识来源;Claude 浏览器 OAuth 回调是后端接收还是页面显码)——实施前需由真码定唯一来源或 surface Owner。
5. 按批次实现(写码可派本机 codex,Claude 规划/验收);每批判别测试变异验证。
6. 测试:unit + `integration_pg` 真 PG + 真号 E2E。
7. codex review 收口(0 S0/S1)→ 干净基线 → **surface Owner 等批准合并(绝不自主合)**。

---

## 9. 关键规则提醒(auth-core/money/schema 全程 Owner-gated 区)

- 本任务几乎每项都落在 money / auth-core(login/2FA/token/session/RBAC)/ schema 迁移——按 CLAUDE.md #2 全是 Owner-gated;
  Owner 已授权"修复全 14 项 + 测试",但**合并**这一步单独保留 Owner 批准权。
- 改 schema 走迁移(手改 sqlc 生成码 + 真 PG),不重生成隔离改。
- clean-room:sub2 是 AGPL,只 paraphrase 行为合同,禁 vendoring/逐字标识符。
