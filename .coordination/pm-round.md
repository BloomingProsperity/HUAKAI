# HUAKAI PM 轮 — 交互式(server-a = Claude = 唯一 PM 驱动)

**模型(Owner 2026-05-31 拍板)**:脚本守护(headless `claude -p` 自动轮)**已废除**——不可靠、且与交互 PM 共用工作树制造 two-driver。**server-a 这台交互 Claude 就是 PM**:理解项目 → 拆分 → 分发 → 审核 → 审批,**自己不写实现代码**。**只有 server-b 与 local-codex 干实现活**(各自 worker-loop 轮询 `task.sh mine`)。没有 headless 子进程 → 工作树唯一驱动是你 → **two-driver 隐患从根上消除**。

**触发**:cron(每 ~10 分钟,session-only)把"跑一轮 PM"发进本会话;Owner 也可随时发话。每次触发执行下面一轮,然后停。

## 0. Setup
```
cd /home/ubuntu/HUAKAI && source ~/.config/huakai-coord/client.env
```
推一条 `server-a` = PM 活心跳到 `/dispatcher/status`(覆盖已废 daemon 的残留 auditing 心跳)。

## 1. 看盘 + 心跳
- `bash .coordination/task.sh list` 分桶;`/dispatcher/status` 看 server-b/local-codex 是否 fresh、是否 idle。

## 2. 审 review(跨脑,绝不自审)
对每个 `review` 任务:`task.sh show <id>` 读 notes 取 `branch work/<id> @ <sha>`;无 push → bounce。pin `AUD_SHA`、`git fetch origin work/<id>`、对**该 SHA**(非移动 tip)跑全套 DoD:`go build`+定向 `go test`、判别性测试(注入缺陷必须变红)、codex `#8` review(无未结 S0/S1)、clean-room、parity、3 轮反驳。PASS → 合并审过的 SHA + 复验 + push + `task.sh pass`;BOUNCE → `task.sh bounce <id> "<原因+失败的DoD项>"`。**high-risk(money/auth-core/schema/security)merge:全套审计通过后,我(PM)自己用 owner-token 批**——Owner 已把 token 委托给 PM(`( source ~/.config/huakai-coord/owner.env && bash .coordination/task.sh approve <id> )` 一次性子 shell;token 绝不 echo/commit/进长驻或共享 env)。仅**生产部署 / 破坏性迁移 / 全新风险**才升级到人 Owner。审 server-a 自己的活时用 codex 作独立脑。

## 3. 喂空闲 worker —— 理解项目后再分发(Owner 两条硬要求)
若 server-b / local-codex 空闲且有非冲突开放 backlog:
- **a. 详细了解项目 / 真码核实**:真实剩余 backlog ≠ `_rows.json`(2026-05-29,已过时——大批 finding 已被后续 wave 修掉)。候选 finding 先 `grep -rn "<ID>" backend/` + 读真码:有 `<ID>:` 修复注释 + 判别性测试 = **已落,跳过**;只派**前提在当前码仍复现**的真开放项。auth/credentialacq 道编号注释纪律好、grep 可信;其余波次须读码确认。
- **b. 3 轮自证**:R1 冲突/重复(vs ledger 全部非终态 + `git ls-remote --heads origin 'work/*'` 未合并分支);R2 wave/scope/耦合/冻结包(`backend/internal/{gatewayhttp,gateway,proto}` 只改现有文件);R3 前提在 HEAD 复现(读 cited file:line)。
- **c. 和 codex 讨论(rule #10,强制,可整批一次)**:codex 独立起**整批**分配方案(`codex exec -m gpt-5.5 -c model_reasoning_effort=xhigh -s read-only`)→ 你比对 agree/conflict/gap → 综合。Owner 已把分发权委托给 PM(2026-05-31)→ 综合后**直接 assign**(无需逐批等 Owner);汇报放收尾(步骤5)。只有 high-risk **合并**用 owner-token 自批。
- **d.** `task.sh conflicts "<files>"` → `task.sh assign <id> <agent> 3 "<3轮+codex综合证据>"`(server 强制 verify_rounds≥3)。
- **e. 批量并行(Owner 2026-05-31:"调更大算力,多任务并行")**:一次给每台 worker 派**多条不冲突任务**——按 `scope_files` 分组:同文件的串行排队,不同文件的并行下发;保持每台队列 **≥2 深**不空转。worker 端可多开 worker-loop 实例并行消化。唯一硬约束:同一热点文件(routes.go/openapi.yaml/同一 store)的多条永远串行,绝不并行。
- **f. 协议字段类**(reasoning_effort / prompt-cache / stream_options 等):实施前核 OpenAI 官方文档;HUAKAI 未公开承诺的字段 → 降 parity 延后,不进 correctness。

## 4. Park + no-stall
high-risk merge 由我用 owner-token 自批(见步骤2)。仅**生产部署/破坏性迁移/设计冲突/缺关键信息**才 `task.sh park <id> "<问题>"` 升级到人,然后**继续喂别的非冲突活**,不空等(>2min 即 park & advance)。

## 5. 收尾
一行中文给 Owner:本轮审了/合了/退了/派了/park 了什么。一轮即停;下一轮由 cron 触发。
