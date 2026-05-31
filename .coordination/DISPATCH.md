# 任务调度(总指挥 → 派活 → 干 → 审核 → 再派,自动闭环)

多 AI 跨机协作不能只靠"别撞文件"(那是 [`README.md`](README.md) 的锁层)。这一层解决**谁分活、按什么质量标准、怎么强制按文档做、完成后谁审、然后自动再派**——一个跑在协调服务器上的**任务账本 + 闭环状态机**。

## 闭环

```
backlog
  │ ① 总指挥(Claude PM)拆活 + 3 轮自反验证(verify_rounds≥3)→ 写账本(assigned)
  ▼
assigned ─② worker(机器X):task.sh start(claim 文件 + in_progress)→ 干+测 → task.sh review
  ▼
review ─③ dispatcher 独立审核(另一个脑子,绝不自审):完工门(DoD)逐条卡
  ├─ 过 → done ✓ → 机器X 空出 → 回 ① 派下一个 ───────┐
  ├─ 不过 → bounce 回 assigned(review_notes 写明哪条没达标)│
  └─ 高危(risk=money-path 等)→ park 成 needs_owner(停下等 Owner,绝不自动合)
  └──────────────────────────────────────────────────── 循环
```

## 状态机

`todo → assigned → in_progress → review → (done | 回 assigned | needs_owner | blocked)`

- **派活门**:任务带 `assignee` 或状态越过 `todo`,**必须 `verify_rounds≥3`**(服务器 422 拒绝),即"分配的人必须 review 三遍(自我反驳+自我验证)"。
- **完工门**:`review → done` 由 dispatcher 按下面的 DoD 逐条核,全过才 `done`。
- **高危永远 park**:money-path / billing / quota / schema / prod-migration → `needs_owner`,等 Owner 点头才合。

## 字段(每个任务)

`id, title, detail, wave, feature, spec_refs[], acceptance, scope_files[], assignee, status, priority, risk, verify_rounds, verify_notes, notes, review_notes, reviewed_by`

- `spec_refs` = 这活**必须遵守的文档/规则**(如 `CLAUDE.md#8`、`docs/specs/billing`、parity matrix F-ID)。
- `acceptance` = **完工定义**,可检验的一句话(不是"做完",是"满足 X 且测试注入缺陷会红")。
- `verify_notes` = 派活前 3 轮自反驳的证据。`review_notes` = dispatcher 审核结论/打回原因。

---

## 总指挥(Claude PM)协议

### A. 派活前——3 轮自反验证(硬性)
对每条要派的任务,**独立**跑 3 轮,每轮尝试**反驳**自己的分配,过不了就改:
1. **R1 冲突/重复**:这活的 `scope_files` 与同波其他在编任务是否撞热点文件?是否和已 `done`/`已修` 的重复(查 audit MASTER:refuted/already-fixed 的不要派)?
2. **R2 依赖/波次**:前置依赖是否先行(如 settlement 幂等先于 streaming)?是否属于本波、不是 Track B roadmap?是否前端(前端一律不碰)?
3. **R3 范围/文档/前提**:`acceptance` 可检验吗?`spec_refs` 齐吗?该 finding 的前提对真码仍成立吗(代码可能已被前一波改)?
只有 3 轮都过 → `task.sh assign <id> <agent> 3 "<三轮证据>"`。

### B. 审核(worker 标 review 后)——完工门 DoD
**dispatcher 是独立另一个脑子,绝不审自己干的**。逐条核,任一不过 → `task.sh bounce <id> "缺哪条"`:
- [ ] **先 fetch worker 的工作分支再审**(review 备注里的 `work/<id> @ <sha>`):`git fetch origin work/<id>` 后在该 SHA 上审。**取不到 / 没 push → 立即 bounce「push 提交到 origin,dispatcher 无法审核看不见的代码」**——跨脑审核成立的前提就是代码可见。
- [ ] `go build ./...` 绿;`make test`(`-race`)绿
- [ ] **判别性测试**(§14):注入该缺陷测试必须变红,否则 fixture 不合格 → 退回
- [ ] **codex §8 review 无 S0/S1**(见 [[codex-review-command-0134]] 的命令)
- [ ] **clean-room**(§11/§12):无逐行翻译/逐字标识符;断言带 `repo@sha:file:line`
- [ ] **parity-or-stronger**:对借鉴项目效果不缩水
- [ ] 按 `spec_refs` **逐条对文档核**:确实照要求文档做了
- [ ] **3 轮反驳"真的完了吗"**:每轮试图证明它没完/有回归,过不了就退回
过 → **dispatcher 把 worker 的 `work/<id>` ff-merge 到落地分支 `fix/hermes-phase-1-e33d940` + push**(未过审代码绝不进落地分支),再 `task.sh pass <id>`;高危 → `task.sh park <id> "理由"`。

### C. 高危必看借鉴项目(Owner 规则)
凡 `risk` 非空(money-path/auth/billing/quota/schema 等),其 `acceptance` 必须包含**三参考融合**:读 **sub2api + CLIProxyAPI + new-api** 对同一问题的处理 → **对比取优 → 融合**进 HUAKAI 设计(三者都引 `repo@sha:file:line` + 写明 fused delta 与维度,见 §12)。**分歧时以 sub2api 为基**,但**必须融合 CLIProxyAPI 与 new-api 的方法**(不是只取一家)。dispatcher 审核高危任务时必须确认这套融合在,否则 bounce。详见 [[consult-sub2api-cliproxy-for-features]]。

---

## Worker(每台机器的 AI)协议
**本地 codex 叫不动 claude?不影响**——干活与审核分开:本地 AI 只负责"干 + 自检 + 标 review",**跨脑独立审核由 dispatcher(Claude,在云端)做**。

每个 worker 循环:
1. `bash .coordination/task.sh mine` —— 看分给自己的活;没有就等下一轮。
2. 有活:先读它的 `spec_refs` 指向的文档 + `CLAUDE.md`/`AGENTS.md`;`bash .coordination/task.sh start <id>`(自动 claim 文件,撞锁会被拒→换别的)。
3. 干。每提交前**限时**自检:`timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`(本地 codex 自检一道,无 S0/S1 才 commit;clean-room、判别性测试照 §14)。**最佳努力**:若超时(exit 124)/报错,记一句"自检跳过:超时/错"然后**照常 commit+push+review** —— **绝不让自检卡死任务**(调度者会跑有约束力的跨脑 codex 审,所以自检挂了不该堵你)。
4. 满足 `acceptance` 后:**先把提交 push 到 origin 的每任务工作分支 `work/<id 小写>`**(如 `work/s1-005`),让 dispatcher 能 fetch 到代码做跨脑审核——**没 push = dispatcher 看不见代码 = 必被 bounce**(本地 codex 在 Windows 上 `commit` 后若不 push,云端 dispatcher 永远拉不到那个 SHA)。然后 `bash .coordination/task.sh review <id> "branch work/<id> @ <commit sha> + 自检结论"`(review 备注**必须**写明分支名 + SHA)。**不要自己标 done、也不要自己 ff-merge 到落地分支**——审过后由 pass 步骤合并。
5. 被 bounce 回来(`review_notes` 有原因)→ 按原因改,再回 review。卡住 → `task.sh block <id> "原因"`。

### 各机器 AI 设置
- **本地 Windows = codex**:用 codex 的 **goal 功能**挂一个长期 worker 目标(随时唤起续跑);
  模型 **gpt-5.5 + `model_reasoning_effort=xhigh`**;**禁止 fast 模式**。每提交自审用
  `codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`。
- 其它机器(Claude/Gemini)按各自 headless 调用填 `WORKER_AI_CMD`,模型同样走高推理档。

---

## 禁止停滞(不等 Owner,自动切下一个)

Owner 规则:**任何需要 Owner 的问题/高危确认,最多等 2 分钟;Owner 没回就把问题保留下来、自动切到下一个模块,绝不卡住不前。**

- worker 或 dispatcher 遇到需要 Owner 拍板的事(高危合并、设计分歧、缺信息)→ **`task.sh park <id> "问题原文"`**(状态 `needs_owner`,问题写进 `review_notes`,Owner 在 `/dispatch` 看板异步看到),然后**立刻去做下一个可做的任务**,不要同步等。
- 这是异步:Owner 随时回答被 park 的问题(直接改该任务状态/留言),循环不因此停。
- 同一时刻没有 `assigned`/`todo` 可做时才允许 sleep 轮询;只要还有非阻塞的活,就继续推进。
- 适用于所有角色,包括 PM 自己:把待 Owner 决定的事记下来 + 继续推进其余模块。

## Owner 决策 / 卡住检测 / 与停摆任务的冲突

**① AI 干到一半需要你拍板**:worker/dispatcher `task.sh park <id> "问题"` → 任务进 `needs_owner`,问题写进 `review_notes`,在 `/dispatch` 异步可见;AI **立刻去做下一个**(禁止停滞)。你回答有两条路:
- 你把决定告诉 PM(Claude),PM 把答案写回任务(`task.sh bounce <id> "按X做"` 让它带答案回到 assigned),或
- 高危放行只有你能做:`COORD_OWNER_TOKEN=<你的私钥> task.sh approve <id>`(普通 token 批不了,服务器 403)。
dispatcher 每轮还会**汇总所有 `needs_owner` 项**给你看,你不必盯着看板。

**② PM 怎么知道哪个 AI 卡住了**:dispatcher 每轮做一次"卡住扫描":
- `task.sh list status=in_progress` 里 `updated_at` 超过 N 分钟没动 = 疑似卡住 → 追问/收回重派;
- `status=blocked` = worker 自己报卡住(带原因);`status=needs_owner` = 等你;
- 文件锁心跳过期(worker 崩了)→ 锁自动 prune,任务回收。
worker 长编辑期由 `worker-loop.sh` 自动续心跳,不会误判。

**③ 新任务会不会和"之前停摆的事"冲突**:派活前**必查**:`task.sh conflicts "<新任务的文件>"` —— 它检查的不只是正在编的锁,而是**所有非终态任务(assigned/in_progress/review/needs_owner/blocked)的 scope_files**。所以即使某任务被 park/block 停在半路,它占的文件仍会被算进冲突,新任务不会撞上"停摆中、随时会续"的活。这是 3 轮自验 R1 的强制步骤。

## 触发机制(怎么让他们动起来)——全是 PULL,不是 PUSH

没人去"推"任务给谁。两边各跑一个**轮询常驻循环**,自己来拉:

- **Worker 触发**:每台机器常驻跑 `worker-loop.sh`,每 `WORKER_POLL_SECONDS`(默认 60s)`task.sh mine` 一次。dispatcher `assign` 把任务状态置 `assigned` → 那台机器**下一次 poll(≤60s)就自动唤起本机 AI** 走 worker 步骤 1–5。所以"派活"本身就是触发,由对面的轮询接住。前提:这循环得在那台机器上常驻(本地 Windows codex 用 codex 的 **goal** 功能保活;server-b 用 nohup/systemd 跑 `worker-loop.sh`)。任务自己 `assigned→in_progress→review` 往前走、worker 干完一个自动领下一个 = 轮询触发在生效的铁证。
- **Dispatcher 触发**:同理,`dispatcher-loop.sh` 常驻在装了 `claude` CLI 的那台机器上,每 `DISPATCH_POLL_SECONDS`(默认 90s)`task.sh list` 一次;一旦有 `review` 任务,就 `claude -p` headless 跑一轮(`dispatcher-round.md`),`flock` 保证同一时刻只跑一轮。worker 标 `review` 就是触发,由 dispatcher 的轮询接住。
  - 没有 `claude` CLI / 没起 `dispatcher-loop.sh` 的退路:在一个开着的 Claude 会话里挂定时任务(cron),空闲时自动跑一轮——只在会话存活时有效。
- **高危**:永远 `needs_owner`,停下等 Owner;其余全自动流转。
- **常驻命令**:`source ~/.config/huakai-coord/client.env && export PATH="$HOME/.local/bin:$PATH" && bash .coordination/dispatcher-loop.sh`(dispatcher 端)/ 各 worker 机器填好 `WORKER_AI_CMD` 后 `bash .coordination/worker-loop.sh`。`claude -p` 跑 headless 需 `--permission-mode bypassPermissions`(无人逐条点同意),Owner 须显式授权后启动。

## 命令速查

```bash
# worker
bash .coordination/task.sh mine
bash .coordination/task.sh start  <id>
bash .coordination/task.sh review <id> "notes"
bash .coordination/task.sh block  <id> "reason"
# dispatcher (Claude PM)
bash .coordination/task.sh assign <id> <agent> 3 "3轮证据"   # vr<3 → 服务器拒
bash .coordination/task.sh pass   <id> "notes"
bash .coordination/task.sh bounce <id> "缺哪条"
bash .coordination/task.sh park   <id> "高危理由"
bash .coordination/task.sh load   allocation.json            # 批量建/派
bash .coordination/task.sh list [status=review|wave=Wave 3]
```

看板(浏览器,不连服务器):`https://45.8.114.249:8443/dispatch` → 贴 token。
环境变量(每台):`COORD_URL` / `COORD_TOKEN` / `COORD_CACERT` / `COORD_AGENT`(每台不同,如 `local-codex` / `server2-claude` / `server3-gemini`)。
