# 2026-07-15 保号 Slice 1：Gemini 真实 UA 与 Provider 出站身份头普查（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “保号 Slice 1: Gemini 换真 UA + 全 provider 出站身份头假货普查 —— 中文注释、中文报告、禁 commit” |
| Scope | **包含**：核对 `backend/internal/provider/**` 及其实际委托的出站构造路径；以 `tools/fingerprint-collector/templates/**` 的自有抓包模板为证据，修正可由模板证实的假编身份头；为 Gemini Code Assist UA 增加精确判别测试并亲做红/绿变异；写根目录中文报告；运行指定门禁。**不包含**：R7、请求体伪装、默认 feature flag、计费、auth 核心、schema、依赖、commit、push、无抓包依据的猜值。
| Success criteria | Gemini Code Assist 默认 `User-Agent` 与自有抓包模板一致；`X-Goog-Api-Client` 等相关头逐项核对；Gemini Advanced 网页会话路径按其浏览器形态单独判定；所有真实 provider 出站构造均进入证据表；假编值在有模板真值时修正，无真值时只列待采集；变异恢复假 UA 时目标断言稳定转红、还原后转绿；六项门禁全部通过或如实记录环境阻塞；工作树无 commit/push。
| Time estimate | 约 90–150 分钟墙钟时间；主要成本是逐文件核对出站构造、模板证据与全量 Go 门禁。
| Blast radius | 生产行为改动限定为有抓包真值支持的静态出站身份头；测试与报告为低风险。错误值可能造成上游兼容性或账户风控变化，因此禁止把“看起来合理”的值当真值。
| Failure modes | ① 把 grep 命中误当行为证据：必须读完整构造路径和调用条件。② 把 CLI 模板套到 Gemini 网页 session：按 endpoint/凭据形态分开。③ 为“单一真相源”强行增加运行时文件依赖：先核对既有加载/注入路径，无安全现成路径则保留常量并标明模板与采集出处。④ 模板缺失仍猜值：归类“缺抓”并进入待采集清单。⑤ 变异后忘记还原：变异前后检查 diff，并在最终门禁前再次核对目标常量。⑥ 全量测试受本机服务或既存失败影响：保留完整命令、首个可行动错误和复跑结果，不伪报通过。
| Decision points | 当前 Owner 指令已明确允许仅以自有抓包模板换真身份头，并明确排除 R7；这作为对 `docs/RULES.md` CB-001 旧 park 状态的本 Slice 特定执行授权。若发现必须触碰 auth/计费/schema/依赖、必须翻 flag、或抓包证据互相矛盾，立即停下请 Owner 决定。

## 独立性与交叉计划说明

- 本计划由 Codex 仅依据 Owner 当前 brief、HUAKAI 内部代码和自有抓包模板独立起草；起草前未读取任何同主题 Claude 计划。
- 当前工作树未发现同主题 `2026-07-15-*-claude.md` 或 synthesis 文件。Owner brief 已把范围、禁区、真值、测试和门禁固定得足够具体，本次按它作为已授权执行基线；Claude 后续亲验时应将其独立计划与本计划比较，任何实质冲突以 Owner 决定为准。
- 本工作不读取或引用 sub2api、new-api、CLIProxyAPI 等非本仓参考项目源码；身份真值仅来自 HUAKAI 自有抓包模板，避免 clean-room 污染。

## Pre-execution checklist

1. 确认分支为 `feat/provider-real-ua`、HEAD 为 `0f7d6b69`，记录初始工作树状态，绝不覆盖既有用户改动。
2. 读取 `docs/RULES.md`、适用技能说明、Gemini 两条出站构造、相关测试以及全部模板的 HTTP 身份层字段。
3. 建立实际 provider 出站入口清单：不以目录名推断，沿 registry、adapter 和委托路径确认哪些代码真正创建/修改请求。
4. 对每条身份头记录“代码位置、触发条件、当前值来源、模板证据”；无模板证据的厂商明确标记缺抓。
5. 先写/加强 Gemini UA 与 `X-Goog-Api-Client` 的判别测试，运行目标测试确认绿色基线或预期失败。
6. 只修改有自有抓包真值支持的身份头；全部 `.go` 注释使用中文且不提任何借鉴项目名。
7. 临时将 Gemini UA 改回 `HUAKAI-GeminiCLI/1.0`，运行唯一目标测试并保存转红输出；立即还原真实值并复跑转绿。
8. 完成全 provider 人工核对表和待采集清单，写入 `mimicry_slice1_report.md`；区分“无身份头需求”和“该抓没抓”，避免把模板缺失直接说成代码缺陷。
9. 使用指定 `GOCACHE`、`GOTMPDIR`、`GOFLAGS`，在 `backend/` 依次运行 `go build ./...`、`go vet ./...`、provider 测试、全量测试、`make quality-gate`、codebudget 测试；保存每项真实结果。
10. 检查 `git diff --check`、`git status --short`、目标假 UA 不再存在；不 stage、不 commit、不 push，等待 Claude 亲验。

## Concrete execution order

1. 盘点模板：抽取每个模板的采集状态、endpoint、UA 及 vendor 特有身份头。
2. 盘点代码：逐 provider 阅读请求构造和 header 合并优先级，形成临时证据账本。
3. 修 Gemini Code Assist 测试与身份常量；核对 Gemini Advanced 浏览器路径，不跨形态套值。
4. 修正其余“模板有真值且代码是假编”的小范围值；对缺抓项不编值。
5. 做指定变异刀与局部回归。
6. 写报告表格、待采集清单、变异证据和风险分歧。
7. 跑从窄到宽的全部门禁；如修复仅限本 Slice 低/中风险范围则处理，否则如实记录并停下。
8. 最终核查 diff 与禁 commit/push 条件，提交中文 Owner 汇报。

