# 2026-06-23 backend quality renew round9

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮只审 HUAKAI 后端测试运行真实性与 CI 假绿风险: `.github/workflows`、`backend` Go 测试标签、integration env 读取、quality gate/codebudget/deadcode 相关入口。明确不碰另一个 backend-security-scan 目标文件,不做安全专项展开,不写 findings 报告 `.md`。 |
| Success criteria | 产出中文增量 findings,每条有真实 `file:line` 证据、问题、修法与测试方向;确认哪些测试只是存在但主 CI 不编译/不运行;如无证据则不硬凑。 |
| Time estimate | 约 45-75 分钟墙钟;本 agent 回合按当前上下文尽量完成一轮闭环。 |
| Blast radius | 只读审查 + 新增本计划文件;不改生产代码、不改 CI、不改测试。 |
| Failure modes | 误把 build-tag 测试存在当成 CI 覆盖;误读陈旧文档;把安全专项问题扩展过深;不小心读取/修改另一个目标计划。缓解:只以当前 `.go`/workflow 真码为证据,限定范围,不读取 backend-security-scan 计划文件。 |
| Decision points | 若发现需要改 CI 或统一 env 名,本轮只报告;是否直接 patch 由 Owner 后续确认。若发现纯安全问题,只指向 security 专项。 |
| Pre-execution checklist | 1. 读取 objective 与本计划;2. 检查 workflow 与 Go test tag/env;3. 核 `integration_pg` / `integration_redis` / benchmark fallback 覆盖;4. 核 quality gate/codebudget/deadcode 入口是否覆盖 `cmd`;5. 汇总增量 findings 并保持 goal active。 |
| Concrete execution order | 先扫 `.github/workflows` 和 `backend` 测试入口,再用 `rg` 定位 build tags/env 名,随后读关键脚本/测试文件,最后输出 round9 增量结论。 |
