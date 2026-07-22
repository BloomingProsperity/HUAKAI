本文件面向 Claude，仅约束 Claude 特有行为；全局规则以 `AGENTS.md` 为准。

# Claude 运行适配层

## 0. 先读什么

Claude 进入 HUAKAI 后按顺序读取：

1. `AGENTS.md`：全局唯一完整规则；
2. `docs/RULES.md`：规则 ID 与启动门清单；
3. 当前唯一执行计划；
4. 任务相关 `.agents/skills/<name>/SKILL.md`；
5. 当前分支真实源码与测试。

本文件不复制全局规则，不得覆盖 `AGENTS.md`。遇到旧 memory、历史计划、旧报告或本文件旧 commit 与当前规则冲突时，执行 Owner 最新指令和 `AGENTS.md`。

## 1. 当前角色与所有权

- Claude 不再天然拥有 PM 或主架构师身份；谁被 Owner 指派，谁负责当前工作单元。
- Owner 已关闭强制 Claude/Codex 并行双计划。不得自动生成 `*-claude.md`、`*-codex.md` 两份计划。
- 若 Owner 指派 Claude 执行，Claude 对调研、设计、实现、测试、review 收口和中文汇报负责；可以调用独立 reviewer，但不能把工作推回固定模型角色。
- 所有判断、实现、测试和提交先同步并核对最新 `origin/main`，再在当前唯一功能分支处理；禁止直接在 `main` 分支或主线工作树修改、提交或推送，主线只接受经验证且获 Owner 批准的 PR。
- 当前目标沿用一个计划、一个功能分支、一个 PR；不得为了同步主线新开 worktree/branch，未经 Owner 同意不合主线、不碰另一个目标。
- 当前工作未闭环时，Owner 中途提出的新需求排到当前目标之后；只有 Owner 明确要求暂停、替换或调整优先级时才切换。禁止一套未完成就并行开另一套。
- 已经独立验证、CI 全绿并获 Owner 明确批准的闭环切片可以先合主线；未验证工作必须留在工作树，禁止夹带进该次合并。前一 PR 合并后，剩余目标只允许再开一个活动 PR。

Claude 还必须从 `AGENTS.md` 读取并执行以下项目级合同，不得因本文件较短而漏掉：三身份不得混用；官方 API Key 走 Go standard transport，需要客户端指纹的会话/OAuth 账号走 Rust mimicry 唯一出口；分组策略真相不可用时返回明确 503，禁止越级用池；普通分类日志保留 30 天，永久资金事实不参与清理。

## 2. 一屏执行顺序

```text
Owner start
  -> 读规则与当前计划
  -> 判定领域
  -> 许可证/活跃度核实
  -> clean-room specifier 读领域源码并形成行为合同
  -> 另一个实现上下文读 HUAKAI 真码
  -> 由点到面追完整链与细节
  -> 更新唯一计划
  -> 独立实现
  -> 判别性测试/并发/故障/恢复
  -> stage 精确 diff
  -> 独立 Codex review
  -> commit/push 到唯一 PR
  -> 等 Owner 批准 merge
```

外部项目行为只是一项设计输入。HUAKAI 是中转站，必须结合本地租户、账号池、账务、协议、worker 和运维合同做 `直接适配 / 融合改造 / Safe Equivalent / 不适用` 判断，不能照搬电商、发卡或账本对象模型。

## 3. Claude 工具与文件约束

- `.agents/skills/` 是 Skill canonical；`.claude/skills/` 是只读机械镜像。
- `.claude/agents/` 仅是可选角色模板，不能覆盖当前 Owner 指派或重新启动并行双计划。
- `/cross-review` 只用于完整 slice、money/auth/schema 或跨功能收口；普通提交仍按 `AGENTS.md` 的 per-commit review。
- 修改共享文件前仅在 Owner 明确恢复多 agent 并行时使用 `.coordination/`；单 agent 不制造协调噪音。
- 大型临时产物不放 `/tmp`，放当前磁盘的项目缓存目录。
- 不修改 `LICENSE`、真实凭据、生产数据，不部署或合并主线，除非 Owner 明确批准。

## 4. Clean-room 派发要求

任何会读取外部源码的 Claude 自执行或 subagent prompt，必须完整使用 `AGENTS.md` 的 clean-room guard，并显式要求：

- 全中文报告；
- `specifier` 不读取 HUAKAI 当前实现、diff、schema、内部标识符或实现文档；
- 只输出行为合同，不给贴合本地代码的补丁；
- 每条行为有 `repo@sha:file:line`；
- 不复制源码、名称、结构、注释、schema、UI 或测试；
- 尾部列 Source files read、Lane、Agent、UTC。

中转站核心默认核实 sub2api、CLIProxyAPI、new-api；专业模块必须额外选择该领域头部项目并读生产源码。支付/退款不能只看中转站项目；需要按问题补发卡/电商、支付编排、订阅计费和账本证据。

## 5. Claude 输出要求

计划和决策必须先写 HUAKAI 真码事实，再写官方规范与领域源码证据，最后给适配结论、完整链影响、细节风险、测试和运维恢复。

需要 Owner 决策时按 `AGENTS.md` 三条件门执行，并提供成熟项目对照；能够通过源码消歧的不得反问。

最终向 Owner 用中文说明：做了什么、改了什么、全链路如何收敛、测试和未验证项、功能是否缩水、clean-room/许可证/安全风险、待批准事项和下一步。
