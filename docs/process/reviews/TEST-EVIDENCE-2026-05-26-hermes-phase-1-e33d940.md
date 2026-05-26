# Test Evidence: `claude/hermes-phase-1` @ `e33d940` Docker Verify

| Field | Value |
| --- | --- |
| Verify date | 2026-05-26 (UTC+8) |
| Verifier | Owner (HUAKAI) — 本机 Docker / Linux |
| Branch | `claude/hermes-phase-1` |
| Verified commit | `e33d940` (cursor-vendor C1 partial revert) |
| Branch tip at verify time | `e33d940` (本 evidence commit 写入后会 fork 到 `fix/hermes-phase-1-e33d940`) |
| Recorded by | Claude PM (Opus 4.7) per Owner 报告 |
| Verify round | **2 of 2** (round 1 抓出本 commit 修的 P0/P1;round 2 验 fix 是否真治) |

## 1. 完整 Docker verify 循环 trace

```
Round 1 (63c7708)              Round 2 (e33d940, 本次)
======================         ============================
Claude PM 请求 Owner 验证      Claude PM 请求 Owner 验证
       │                              │
       ▼                              ▼
Owner Docker fresh checkout    Owner Docker fresh checkout
+ fresh PG migrations 1..59    + fresh PG migrations 1..59
+ go test ./...                + go test ./... + integration_pg + -race
       │                              │
       ▼                              ▼
🔴 抓出 2 个真问题:            ✅ 全 5 条 PASS
1. mode_refresh_test 红
   (count=19 want 20)
2. fail-closed test 是 helper
   层,真实入口走 fake exchanger
       │                              │
       ▼                              ▼
Claude PM 派 codex partial      Owner 确认 phase-1 可 merge
revert + 3 轮 review 落
e33d940
```

### Round 1 (`63c7708`) — 抓出本 commit 修的问题

Owner 第一轮 Docker fresh checkout 跑 `63c7708`(credentialstore SaveRefresh CAS race fix + hermes wiring gate fix 之后),抓到:
- `credentialworker/mode_refresh_test.go:28` 红 `mode adapter count=19 want 20`
- cursor 专属 `ValidateOAuthConfig` 测试是 helper 层,真实 admin 入口走通用 `credentialacq/oauth.go` + `cursor/oauth` fake exchanger,真实入口 fail-closed 未证明

### Round 2 (`e33d940`,本次) — 验 fix 是否真治

`e33d940` = cursor C1 partial revert(撤 cursor ModePlan + handlerSpec + 相关 tests,留 const + whitelist + cursor 自己包测试 + 决策档)。

预期效果:
- mode_refresh_test 自动绿(`wantCount = len(store.Names())`,撤 cursor 后 store 19,worker 19,自动匹配)
- cursor admin 入口完全消失(无 ModePlan),不存在 fake exchanger 被真实入口暴露问题

## 2. 测试命令清单 + 结果

Owner 2026-05-26 本机 Docker / Linux 环境执行:

| # | 测试 | 结果 |
| --- | --- | --- |
| 1 | `go test -count=1 ./...`(全量单元) | PASS |
| 2 | fresh Postgres migrations `1..59` 顺序 apply | PASS |
| 3 | `go test -tags integration_pg -count=1 ./...`(全量 PG 集成) | PASS |
| 4 | `go test -race -count=1 ./...`(全量 race detector) | PASS |
| 5 | fresh PG + race 核心包(`credentialstore` / `credentialworker` 等) | PASS |

mode_refresh_test 重点验证:
- 之前 `63c7708` 报 `mode adapter count=19 want 20` 已消失
- `internal/credentialworker` 退出 0,registry size 跟 store 自动对齐

## 3. 测试环境

- 平台:Docker on Linux(Owner 本机,非沙箱)
- Go:仓库 `go.mod` 锁定版本
- Postgres:fresh 容器(每次清空 volume),migrations 1..59 全跑过
- 网络:本地 PG socket,无外部依赖
- 沙箱 codex review 之前因 `/home/codex/.cache/go-build` 只读 + PG socket 拒入跑不全;Owner 本机 Docker 不受此限制,因此本 evidence 才是 ground truth

## 4. 残留风险 / 未覆盖项

`e33d940` 通过 Docker 全测试 ≠ 完全无风险。已知残留:

1. **Slice 2.7 hash-lock 仍未生成** — `backend/deploy/hermes-runner/requirements.txt` 仍是直接 pin 没 sha256 哈希,生产 Docker 构建用 `--require-hashes` 时会挂。Owner 本机跑 `cd backend/deploy/hermes-runner && ./scripts/regen-hashlock.sh` 后才完整。S2 不阻 merge。
2. **Slice 2.8 hermes runner key rotation 路径未通** — Gateway `/internal/keys` 等 endpoint 存在,Python runner 启动只一次性 `load_public_key_cache_from_env`,不调 gateway。S1 但不阻 phase-1 merge(无 rotation 业务依赖)。
3. **cursor C5 重开时必须补真实 admin 入口 fail-closed test** — 撤回保留了 `provider/cursor/bootstrap_test.go` helper test,但 cursor 真实复活时(C2+C5 一起开)必加一个走 `credentialacq/oauth.go` 通用 admin 入口的端到端 fail-closed test,**避免重蹈本次判别性假阳性覆辙**。
4. **5 fake OAuth exchanger 仍未修真**(主线 4 个:`anthropic/claude_ai_oauth`、`gemini/code_assist`、`gemini/google_one`、`gemini/antigravity` + 范围外 1 个 `openai/chatgpt_oauth`)。Owner 当前用 manual paste 真账号 token 给 Rust 线 capture,生产前需 admin UI 走真 OAuth。

## 5. Merge 建议

`claude/hermes-phase-1` 已可 merge 到 `main`:
- 外部 verify 报告 claim 1 (credentialworker 3 OAuth adapter) 在此前 Slice 2.6 已修
- 本 session 3 个 P0/P1 (`63c7708`)已修
- mode_refresh 红 + fail-closed 入口分歧(本 commit `e33d940`)已治
- Docker fresh 全套测试 PASS

Merge 方式:
```bash
git checkout main && git pull
git merge --ff-only origin/claude/hermes-phase-1
git push
```
fast-forward 可走(`claude/hermes-phase-1` 30 ahead / 0 behind `main`,无冲突)。

## 6. PM 永久教训(本次事件触发)

写进 `docs/process/reviews/DECISION-2026-05-26-cursor-c1-partial-revert.md` §5,但补本 evidence 文档:

1. **改任何 store registry / mode plan 后必跑 `go test ./...` 全量**,即使没改下游包,因为下游 test 可能反向引用 store registry size。
2. **fail-closed test 必明确"经过哪个真实入口"**,不能只测 helper 函数;spec 写 "通过 admin StartOAuthFlow 入口 fail-closed without operator config" 比 "ValidateOAuthConfig returns ErrCursorOAuthConfigRequired" 更可信。
3. **Docker fresh + fresh PG 是 ground truth**,沙箱内 codex 跑测试受 GOCACHE / 网络限制,不能替代 Owner 本机验证。任何"sandbox 全绿"必带"本机重跑"作为最终 merge 闸。

## 7. 后续 traceability

本 evidence commit + `e33d940` 在 `fix/hermes-phase-1-e33d940` 分支留档,作为 phase-1 merge 前最后一次 Docker 真验证证据;若日后 phase-1 merge 后 main 上跑出 regression,可对照此 evidence 的测试矩阵反查是否本次未覆盖。

---

**记录者**: Claude PM (Opus 4.7)
**Owner 验证报告**: 2026-05-26
**evidence commit 计划进入**: `fix/hermes-phase-1-e33d940`
