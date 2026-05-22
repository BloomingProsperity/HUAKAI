# Rust 补救波 独立并行线 —— 启动简报

> 这份文件是给「Rust 并行线」那个独立 Claude 对话看的。
> Owner 2026-05-22 决定:补救波分两条并行线 —— **Go 线(W4…)在主工作树
> `/home/codex/HUAKAI` 由另一个 Claude 对话推进;Rust 线(本线,W11+W12)在
> 独立工作树推进**。两条线文件零重叠(`backend/` vs `exploratory/`)。

## 你是谁、你的范围

你负责 HUAKAI 的 **Rust 预生产硬化**,即 12 波审计补救计划里的 **W11 + W12**。

- **工作树**:`/home/codex/HUAKAI-rust`(独立 git worktree)。
- **分支**:`claude/rust-hardening`。**只在这个分支提交。**
- **只动** `exploratory/rust-core-gateway/`。**绝不碰** `backend/`(那是 Go 线的)。
- 完工后这个分支合并回 `claude/phase-1`(文件不重叠,合并干净)。

## 要修什么

来源:`docs/process/research/2026-05-22-deep-audit-rust.md`(Zone D 深度审计,
10 个发现 D-1..D-10)。波次划分见
`docs/process/plans/2026-05-22-audit-remediation-wave.md` W11/W12 两行:

- **W11 rust 安全边界硬化**(先做):D-1 路由用可伪造 header / D-2
  `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 生产绕过 / D-3 planned endpoint 允许明文 HTTP /
  D-6 客户端 org/project header 透传 / D-10 `mimicry-boring` feature 绕过
  fail-closed 阻断。(审计文里编号是 1/2/3/6/10。)
- **W12 rust 账务遥测硬化**(后做):D-4 terminal report 队列满丢账 / D-5
  非流式响应不解析 usage / D-7 heartbeat 硬编码健康数据 / D-8 429/408 误归
  不可重试 / D-9 chunked/H2 body 字节数记 0 / O-2。(审计文里 4/5/7/8/9。)
- W11 先于 W12(硬依赖:请求规划仍可伪造/mock-bypass 时,遥测正确性无意义)。

## 必须遵守的纪律(和 Go 线同一套)

工作树里有 `CLAUDE.md` + `AGENTS.md`,**先读**。要点:

1. **小切片闭合**:每个切片开工前把完整 spec 一次写全(含已知难点);≤2 轮
   review;闭合再开下一个。别 big-bang。
2. **plan 先行 + codex 交叉评审**:W11 spec 写完,先派 codex 评审 spec
   (REJECT 就改到过),再实现。见 `AGENTS.md` §Plan-Before-Execute、
   §Parallel Plans。
3. **测试质量**:`AGENTS.md` §Test Quality Discipline —— 每个测试必须能在它
   该抓的缺陷出现时变红;写完做 mutation 自检。
4. **包/文件结构**:`AGENTS.md` §Package & File Structure Discipline ——
   按职责分 module,不堆 god-file。
5. **per-commit codex review**:每次 commit 前 `codex exec review --uncommitted`,
   修 HIGH。
6. **commit 命名**:`<英文模块> <中文说明>`,无 type/无阶段号/无 PASS 字样;
   结尾 `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`。
7. **clean-room**:全程只改 HUAKAI 内部 Rust 代码,不读参照项目源码。

## 环境注意(踩过的坑)

- **Rust 构建别用 /tmp**:`export CARGO_TARGET_DIR=$HOME/huakai-rust-target`
  (`/tmp` 配额会被撑爆,有案底)。
- **codex 调用**:`codex exec -c model_reasoning_effort=xhigh --enable fast_mode
  --sandbox workspace-write "<prompt>" < /dev/null`(末尾 `< /dev/null` 必加,
  否则 codex 等 EOF 永久挂死)。codex review 用
  `codex exec review --uncommitted -c model_reasoning_effort=xhigh --enable
  fast_mode -c sandbox_mode='"read-only"'`。
- codex 并行 ≤ 3。
- 真实上游 / 真账号 / 真 fingerprint 的测试在 sandbox 里做不准;W11/W12 的
  硬化(去 mock 绕过、强制 https、修分类)绝大多数是单元级,sandbox 可测。

## 第一步

1. 读 `CLAUDE.md` + `AGENTS.md` + `docs/process/research/2026-05-22-deep-audit-rust.md`。
2. 读 `exploratory/rust-core-gateway/merged/src/` 把 W11 那 5 个发现的现状代码摸清。
3. 写 W11 完整 spec → `docs/process/plans/2026-05-22-w11-rust-security.md`。
4. 派 codex 交叉评审 spec;过了再实现。
5. 实现 → 自测(`cargo test`)→ per-commit codex review → 提交到
   `claude/rust-hardening`。
6. W11 闭合后做收尾对照,再开 W12。

## 与 Go 线的协调

- 两条线**不共享 git 操作**(各自独立 worktree + 分支),互不踩。
- 不需要实时同步;各自跑完各自合并。
- 若发现需要改 `backend/` 的东西 —— 停,记下来,别动,交给 Go 线。
