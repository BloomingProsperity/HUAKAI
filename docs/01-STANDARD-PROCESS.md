# HUAKAI 标准操作流程 (STANDARD OPERATING PROCESS)
> Owner 2026-06-07:「将这套写进我的标准!按照这个流程来!」—— 本文件 = 以后所有工作的**默认流程 + 唯一真相源约定**。配 `00-MASTER-PLAN.md`(规则/冻结/战略)一起读。

## 0. 三件套「活产物」= 唯一真相源
| 产物 | 文件 | 管什么 | 怎么再生 |
|---|---|---|---|
| **项目总纲** | `docs/00-MASTER-PLAN.md` | 定位 / 架构 / 规则 / **冻结区** / 战略 / 路线 / 现状 | 6-agent 挖掘 → opus 合成;季度或大变更刷 |
| **态势看板** | `HUAKAI-gateway-status-<date>.html` | 9 轴总览 + 六级环 + 5 波路线 + Rust | 手编 HTML(轻量,改 KPI/卡片)|
| **功能树网页** | `huakai-feature-tree.html` | 2322 行**逐行**(搜索/模块/优先级/六级/三家对标/证据/推进动作)| `python3 /tmp/gen_tree.py`(解析 `/home/ubuntu/benchmark/` A-H .md → 自包含 HTML)|
| **底层真相** | `/home/ubuntu/benchmark/`(A-H + master + INDEX + 审计)| 六级状态**禁虚标**逐行账本 | 审计回写 |
> 改动同步规约:**功能状态变 → 刷树+看板;规则变 → 同步 `AGENTS.md`/`docs/RULES.md`+总纲;会话末 → 更新记忆。** 三件套一处看全「全貌→路线→逐行」,不再东一块西一块。

## 1. 标准闭环流程(每个任务波次照走)
1. **证据级融合审计** —— 对准 **landing 分支**(`origin/fix/hermes-phase-1-e33d940`,**非 origin/main**)、范围**含 Rust 出口层**、三镜(sub2/new-api/cliproxy)带 `file:line`、对抗去误报(0 假阳)→ 产出真缺口。
2. **折进标杆树** —— 六级状态回写 `/home/ubuntu/benchmark/`,排进 **5 波路线**(W1 协议广度 / W2 运行时接线 / W3 schema 加性 / W4 定价细分 / W5 尾巴)。
3. **逐缺口闭环流水线**:
   `opus 设计 spec(clean-room,codex 盲实现,理解-step + 判别测试示例)`
   → `codex 多并行实现(≤2-3 并发,共享 token)`
   → `PM 全门:build + vet + full-unit(HUAKAI_SKIP_PERF_LATENCY_GATE=1)+ 真库 integration_pg(fresh huakai_gate + migrate + -p1)`
   → `commit-FIRST(先提交再变异,别毁未提交补丁)`
   → `mutation 验红(注入缺陷必红,否则测试无效)`
   → `rebase onto landing → 再门 → FF push → reap worktree`
4. **每 3 合** 跑累积健康(`migrate` + `go build ./... && go test ./...`)。
5. **刷新三件套**(树 + 看板 + 总纲)→ 实时进度。

## 2. 纪律铁律(本会话踩坑固化,防再犯)
- **对准 landing**(非 main,落后 500+);喂 agent 前 prompt 写死 + `git rev-list` 核实。
- **冻结包 = 反 god-package 模块化,非协议冻结**:新协议/功能**落新包 + 既有文件加性 edit**,需内部能力就**导出**;**禁 shim/body-rewrite hack 绕冻结**。
- **commit-FIRST 再 mutation**(切勿 `git checkout` 毁未提交补丁 → 提交构建失败)。
- **全网关含 Rust 出口**(`exploratory/rust-core-gateway` + `transport/mimicry`);判 parked 先 git/wiring 核实,别凭目录名。
- **workflow 可靠配方**:schema(逼内容产出)+ 聚焦小 agent(避免超时)+ 显式「StructuredOutput 即交付,勿写文件勿返状态串」。
- **高危 money/auth/schema → park 待 Owner**;真 PSP/前端/主动反检测 = 冻结/Park。
- 每提交 codex 审 + S0/S1 门;每任务出中文 8 点报告;`git add` 只暂存预期 diff;新路由同步 `openapi.yaml`(串行热点)。

## 3. 角色 & 算力(Owner 2026-06-04)
- **opus(我)= 研究 + 设计 + 门控 + 验证 + wiring + 决策**;**codex = 多并行实现**;sonnet 退役。
- 算力**拉满**;所有活在 **kaifa(server)**上跑(`source ~/.cargo/env` 用 Rust);本地 Win 路径只放产物副本。

## 4. 一句话
**审计(对准landing+含Rust+三镜)→ 折进树 → 逐缺口闭环(spec→盲实现→全门+mutation+真库→commit-first→FF→reap)→ 每3合累积健康 → 刷三件套。** 高危 park,纪律不破。
