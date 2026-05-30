# 多 AI 并行编辑协调 — 给每台机器 AI 的提示词 + 说明

> 用法:在**任意一台机器**(本地 / 服务器)上,把下面【提示词】整段贴给那台的 AI(Claude / Codex / Gemini 都行)。它就会接入协调服务并遵守协议。下面的【说明】是给你(和 AI)理解这套东西用的。

---

## 一、提示词(整段复制给 AI)

```
我们这个 HUAKAI 仓库由多个 AI 在 3 台机器(1 本地 + 2 服务器)上并行编辑,会出现两个 AI 同时改同一个文件、互相覆盖的问题。已经搭好一个【跨机器编辑协调服务】,你必须先接入、并在每次改文件前遵守协议。

【第 0 步:配置(每次会话开始做一次)】
在 shell 里设这几个环境变量(token 和证书路径找 Owner 要,或按下面取):
  export COORD_URL="https://45.8.114.249:8443"
  export COORD_TOKEN="<Owner 给你,或在协调服务器上 grep COORD_TOKEN /etc/huakai-coord.env>"
  export COORD_CACERT="<本机仓库路径>/.coordination/server/coord-server.crt"
  export COORD_AGENT="<本机唯一名字,例如 local-claude / server1-codex / server2-gemini>"
然后自检:  bash .coordination/check.sh
  能打印"看板/(no live edits)"就说明接通了;报错就把错误发给 Owner。

【铁律:改任何共享文件之前,必须按顺序做】
1) 先查冲突:           bash .coordination/check.sh <要改的文件路径>
2) 如果该文件正被【别的】活跃 agent 锁着 → 绝对不要改、不要覆盖它。改去做别的、
   或等它 release、或让 Owner 协调。不要硬改别人正在编的文件。
3) 认领(广播你在编什么/动哪个核心功能/目的):
     bash .coordination/claim.sh "$COORD_AGENT" "文件1,文件2" "核心功能" "目的"
   —— 如果和别人冲突,这条会以 exit code 2 拒绝你,并打印是谁在编。被拒就回第 2 条。
4) 长时间编辑:每隔几分钟重跑一次上面的 claim.sh 续心跳(超过 1800 秒没心跳的锁会被
   当成"死会话"自动过期,别人就能接管)。
5) 改完立刻释放:      bash .coordination/release.sh "$COORD_AGENT"

【任务调度(领活/干活/审核/再派)】
- 这套不止"别撞文件",还有一层【任务账本】:总指挥派活、你领活干、完成标 review、
  调度者独立审核后再派。完整协议见 .coordination/DISPATCH.md,**务必先读它**。
- 你的循环:bash .coordination/task.sh mine → start <id>(自动 claim 文件)→ 干 + 每提交
  codex 自审 → review <id>。不要自己标 done(等调度者审)。被打回按 review_notes 改。
- 需要 Owner 拍板的事:task.sh park <id> "问题",然后**立刻做下一个**;最多等 2 分钟,绝不停滞。

【其它】
- COORD_URL 没设时,这套会退回"本地文件模式"(只在单机有效);跨机器务必设 COORD_URL。
- 这是"广播+约定",不是操作系统强制锁——靠你自觉遵守。别绕过。
- 你看板上能看到所有机器当前在编哪些文件、动的哪个功能、目的;善用它避开别人。
```

---

## 二、说明(这套东西是什么 / 怎么运作)

**解决什么**:3 台机器上的多个 AI 并行改同一个仓库,容易同时编辑同一文件→互相覆盖、丢改动。这套让每个 AI 在动手前**广播**"我在编哪些文件、动哪个核心功能、目的",并能看到别人在编什么、**检测冲突**。

**架构**:
- 一台服务器(`45.8.114.249`)上跑一个极小的协调服务(systemd 常驻、开机自启、崩溃自拉起),数据存 SQLite。
- 对外只开 `https://45.8.114.249:8443` 一个端口:**TLS 加密 + token 认证 + 自签证书客户端固定(pinning)**。没碰该服务器上任何现有服务(nginx / api.hkai.shop / Postgres / 容器都没动)。
- 三台机器(含本地)都是**客户端**,用同一套 `check.sh / claim.sh / release.sh` 命令访问它;命令在 `COORD_URL` 设了时自动走这台服务器。

**为什么不会"协调状态本身也撞车"**:每个 AI 只写自己的锁(服务端按 agent 名分别存),`claim` 走 SQLite `BEGIN IMMEDIATE` **原子串行**——两个 AI 同抢一个文件,必然有一个被拒(HTTP 409),不可能两个都拿到。

**接口**(一般不用直接调,用上面的脚本即可):
- `GET /healthz` 健康检查(无需 token)
- `GET /board` 当前所有活跃锁
- `GET /check?file=<路径>` 查某文件是否被占
- `POST /claim` `{agent,files[],core_feature,purpose}` 认领(冲突返 409)
- `POST /heartbeat` `{agent}` 续心跳
- `POST /release` `{agent}` 释放

**安全模型**:token 是高熵随机串(只在服务器 `/etc/huakai-coord.env`,权限 600,从不进聊天/仓库);传输走 TLS;客户端用固定的自签证书校验服务端(防中间人)。万一 token 泄露,影响面也很小——这只是个"编辑登记板",没有代码执行、碰不到仓库/数据库/凭证;最坏是别人能看你在编什么、或乱占锁干扰。可随时在服务器换 token + 重启服务来吊销。

**运维**(在协调服务器上):
- 看状态:`systemctl status coord-server`
- 重启:`systemctl restart coord-server`
- 看 token:`grep COORD_TOKEN /etc/huakai-coord.env`
- 换 token:改 `/etc/huakai-coord.env` 里的 `COORD_TOKEN` → `systemctl restart coord-server` → 三台客户端同步更新 `COORD_TOKEN`
- 证书在 `/opt/huakai-coord/coord.crt`(公开);仓库副本 `.coordination/server/coord-server.crt`
- 服务代码 `/opt/huakai-coord/coord_server.py`;部署细节见 `.coordination/server/README.md`

**协议出处**:跨 AI 公约同时写在仓库 `AGENTS.md`(Codex 读)和 `CLAUDE.md`(Claude 读)里,所以三家 AI 都认这套。
