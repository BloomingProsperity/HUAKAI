# Reference Project Deep Mining — 通用 Brief（T1 dir skeleton）

## 触发

Owner 2026-05-13 quote: "sub2api和那些借鉴的项目拆分的不明显，还有很多遗漏。我想让你指挥codex去每个文件夹，每个项目，每个代码一一拆解，按照他们的项目目录写明拆解后发现了什么，逻辑，以及实现功能等，包括提升的办法。"

Owner 选 **option 3：T1 + T2 一并铺，max parallel，codex 和 sonnet 一起**。

T1 = 顶层目录骨架（本 brief 范围）。T2 = 跨 ref 功能切片深挖（下一波）。T3 = 文件级精读（按 T2 发现触发）。

## 范围

本 brief 适用于 6 个 ref project：

| ref | 路径 | git URL |
|---|---|---|
| sub2api | `~/refs/sub2api/` | https://github.com/Wei-Shaw/sub2api.git |
| new-api | `~/refs/new-api/` | https://github.com/Calcium-Ion/new-api.git |
| litellm | `~/refs/litellm/` | https://github.com/BerriAI/litellm.git |
| portkey | `~/refs/portkey/` | https://github.com/Portkey-AI/gateway.git |
| helicone | `~/refs/helicone/` | https://github.com/Helicone/helicone.git |
| all-api-hub | `~/refs/all-api-hub/` | https://github.com/qixing-jk/all-api-hub.git |

每个 ref **同时由 2 个 agent 拆解**（codex lane + sonnet Explore lane），各自独立写到不同输出文件，最后由 Claude PM 做 synthesis。

## 工作流（每 agent 必须按此走）

### 第 1 步：stub + SHA + 目录树

```bash
# 验 ref 健在 + 取最新 SHA + 顶层目录树
cd ~/refs/<ref-name>
git rev-parse --short=12 HEAD
git log -1 --format='%cd' --date=short
ls -la
tree -L 2 -d --noreport 2>/dev/null || find . -maxdepth 2 -type d | head -40
```

写 stub 报告到 `/tmp/codex-deep-mining-<ref>-<agent>.txt`（agent ∈ codex/sonnet），含启动时间 + SHA。

### 第 2 步：每个一级目录详细拆解

按 ref 自己的目录结构，**每个一级目录**（顶层下的子目录）写一节，含 6 个子项：

1. **用途**（1-2 句话）：这个目录在 ref project 里是干什么的
2. **关键文件**（3-7 个最重要的）：每个含 `<file>:<approx LoC>` + 一句说明
3. **入口 / 调用关系**：从哪里被调用，调用什么
4. **核心 logic / 算法**：用 HUAKAI 词汇描述，不抄上游 identifier
5. **暴露功能**：从用户 / operator 视角能看到什么
6. **HUAKAI 升级点**（3 个维度任选 1-3）：
   - 架构升级（module boundary / data flow / storage / contract surface）
   - 算法升级（scoring / selection / failure detection / retry）
   - 生态升级（ops / observability / lifecycle / admin / audit）

### 第 3 步：跨目录 logic 流图（如适用）

如果某个核心 workflow 横跨多个目录（如 sub2api 的 OAuth 引导 + Provider Account refresh），单独一节做"workflow trace"，列每跳的目录 + 关键 file:line。

### 第 4 步：HUAKAI 整体升级 punch list

文末单列一张表：

| ref 项 | HUAKAI 现状 | HUAKAI 升级建议 | 升级维度 | 优先级 |
|---|---|---|---|---|
| 比如 sub2api `frontend/src/views/admin/ops/` 的 OpsConcurrencyCard | HUAKAI Round 10 只有 Top 5 账号表 | 加 platform/group/account/user 4 维度 toggle + 实时 QPS panel | 生态升级 | P0 |

## 输出

- Codex lane: `docs/research/2026-05-13-<ref>-dir-skeleton-codex.md`
- Sonnet lane: `docs/research/2026-05-13-<ref>-dir-skeleton-sonnet.md`
- 文件大小目标：每份 500-1000 行；不要凑字数，但不允许少于 300 行（说明拆得太浅）。

## Clean-room 约束（必守 — CLAUDE.md #11 / #12）

- ✅ 引 `<repo>@<sha>:<file>:<line>` 做证据 cite
- ✅ 引 verbatim file:line 在 prose 里 OK，但**周围 prose 不要复用 identifier 本身**
- ❌ 严禁 verbatim 复制 ref 的 function name / struct field / config constant 到产出 doc
- ❌ 严禁线性翻译 ref 算法 → HUAKAI 术语
- ❌ 严禁 copy file structure 到 HUAKAI（HUAKAI 自架构）
- ✅ 描述算法时用 HUAKAI vocabulary（如 "request hop" 而不是 ref 的 "proxy step"）

## 不在范围

- 不读 `docs/research/2026-05-12-sub2api-frontend-decomposition.md`（sonnet 已写的 sub2api frontend 旧 decomp）— 本次不要污染
- 不读 HUAKAI 自己代码（这次只对 ref 单边拆，不做 cross-ref；HUAKAI 升级点用一般工程经验判断）
- 不做文件级 verbatim diff（那是 T3）
- 不做 cross-ref 功能对比（那是 T2）
- 不实施代码 / 不起 dev server / 不 npm install

## 防死提示（最重要）

历史教训：trust-chain codex 在 clone portkey 时挂了 7 min；frontend round 9 codex 死 7 min 没出报告。本次必做：

1. **第一件事**就 `echo "deep-mining <ref> <agent> started $(date)" > /tmp/codex-deep-mining-<ref>-<agent>.txt`
2. **每完成一节**追加 `echo "[$(date)] section <name> done" >> /tmp/codex-deep-mining-<ref>-<agent>.txt`
3. **不要先 read 大文件再决定写哪**；用 `find` / `ls` / `head` 先扫，看到值得读的再 read
4. **不要 fetch / clone**（ref 都已 local），有些过期可接受，不要试图 refresh
5. **不要 read ~/refs 之外的目录**

## 时间预算

- Codex lane：30-45 min per ref，xhigh + fast_mode
- Sonnet lane：20-30 min per ref，Explore agent type，read-only

## 输出报告末尾必带

```
---
Agent: <codex|sonnet>
Ref: <ref-name>
SHA: <sha12>
Pushed: <last commit date>
Mining started: <UTC>
Mining done: <UTC>
Output LoC: <count>
Source files read (per CLAUDE.md #11 closing): <list>
```
