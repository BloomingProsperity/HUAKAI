# 并行编辑协调（多 AI / 多线程）

多个 AI（Claude / Codex / Gemini …）和多线程会**同时编辑同一个仓库**。本目录提供一个轻量协议:每个 AI 在动手改文件前**广播自己正在编的文件、动的哪个核心功能、目的**,并能看到别人正在编什么,避免对撞、互相覆盖。

## 为什么是「每个 agent 一个文件」

如果大家都往同一个 `ACTIVE-EDITS.md` 里写,这个状态文件本身就会被并发编辑——正是要解决的问题。所以**每个 agent 只写自己的那一个锁文件** `locks/<agent>.json`,读取时把整个 `locks/` 目录 glob 出来合并看。互不写同一个文件 = 元数据层不会再对撞。

## 目录

```
.coordination/
  README.md          ← 本协议(唯一权威)
  claim.sh           ← 认领/续约:写自己的锁 + 广播意图 + 检测冲突
  release.sh         ← 改完:释放自己的锁
  check.sh           ← 看板:谁在编什么(冲突检测)
  locks/<agent>.json ← 每 agent 一个锁(ephemeral,gitignored)
  activity.log       ← append-only 意图广播(谁/何时/动了哪个功能/目的)
```

## 锁文件 schema（`locks/<agent>.json`）

```json
{
  "agent": "claude-opus-4.8",
  "session": "c316ba40",
  "status": "editing",                 // editing | done
  "files": ["backend/internal/auth/storm_controller.go"],
  "core_feature": "S2-045 storm scope",
  "purpose": "wire endpoint/global token buckets",
  "started_at": "2026-05-30T02:40:00Z",
  "heartbeat_at": "2026-05-30T02:55:00Z",
  "ttl_seconds": 1800                  // 心跳超过 ttl = 视为死会话,锁过期可忽略/清理
}
```

## 协议（每个 AI 改任何共享文件前必须做）

1. **看板**:`bash .coordination/check.sh`（无参=列全部活锁;带文件=查该文件冲突)。忽略 `heartbeat_at` 超过 `ttl_seconds` 的死锁。
2. **冲突**:若有**别人**的活锁列了你要改的文件 → **不要改**。改去做别的、等它 `done`、或与 Owner/对方协调交接。**不要覆盖别人正在编的文件。**
3. **认领**:`bash .coordination/claim.sh "<agent>" "<file1,file2>" "<core_feature>" "<purpose>"`。它会先做冲突检测,再写你自己的锁 + 往 `activity.log` 追加一条意图。
4. **续约**:长编辑期间周期性重跑 `claim.sh`（刷新 `heartbeat_at`),否则锁会被当成 stale。
5. **释放**:改完 `bash .coordination/release.sh "<agent>"`（标记 done + 追加 log + 删锁)。

手写 `locks/<agent>.json` **仅在本地模式有效**。⚠️ **跨机器模式(设了 `COORD_URL`)下严禁手写本地锁**——它只落在本机 checkout、永远不会到服务器,别的机器看板/check 看不到,你会以为占了锁、别人却仍能改同一文件。跨机器模式**必须**用 `claim.sh/check.sh/release.sh`(它们走服务器)。

## 两种模式

- **本地模式(默认)**:不设 `COORD_URL` 时,锁就是 `locks/<agent>.json` 本地文件,适合**同一工作树多会话并行**(同一台机器)。建议性广播,无 OS 强锁。
- **跨机器模式(推荐用于 1 本地 + 2 服务器)**:设 `COORD_URL`+`COORD_TOKEN` 后,**同一套命令**(check/claim/release)自动改为访问一台服务器上的协调服务 `server/coord_server.py`——三台机器共享**一个实时看板**,SQLite `BEGIN IMMEDIATE` 提供**真原子锁**(同抢一文件必拒一个)。部署见 **[`server/README.md`](server/README.md)**。

> 物理隔离的多台机器(不同工作树)只能走跨机器模式——本地 `locks/` 互不可见。

## Claude Code 侧可选自动化

Claude Code 可在 `~/.claude/settings.json` 加 PreToolUse hook(匹配 Edit|Write):改文件前自动跑 `check.sh` 检测冲突 + `claim.sh` 自动认领。其它 AI(Codex 等)不跑 Claude hook,故仍以本协议(写进 AGENTS.md)为跨 AI 公约。
