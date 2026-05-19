# Owner 本机 Setup 指引: PG 集成测试 + Provider 指纹抓取

本文档面向 Owner 本机执行的两类工作:

1. **PG 集成测试** — 跑 `-tags=integration_pg` 的真 DB 测试 (sandbox 跑不了, 因为没 docker socket)
2. **Provider 指纹抓取** — R-D 重抓 (codex / kiro / gemini / anthropic) 真上游 TLS/h2 指纹

两者都在 Owner 本机做, sandbox 拿不到 (前者需 docker, 后者需 Owner 自己账号 + 网络抓包)。

---

## A. PG 集成测试 (15 分钟)

### A.1 前置

需要本机有:

- Docker (Docker Desktop 或 Linux daemon)
- Go ≥ 1.22
- 端口 5432 空闲 (本机无其它 PG 在跑)

### A.2 命令序列

```bash
cd backend

# 1. 起 dev PG 容器 (postgres:16-alpine, 持久 volume)
make db-up

# 2. 应用全部 migrations (idempotent, golang-migrate)
make db-migrate

# 3. 跑 integration_pg-tag 测试 (全仓 -race)
make test-integration
```

通过判据:

- `make db-up` 末尾打 `PostgreSQL is ready`
- `make db-migrate` 末尾打 `migrated to <最新 version>` (当前 36 个 up.sql)
- `make test-integration` 全部 PASS, 无 `FAIL` / `--- FAIL:`

### A.3 出错处置

| 症状 | 原因 | 处置 |
|---|---|---|
| `db-up` 超时 30s | docker daemon 没起 / 端口被占 | `docker ps` 看冲突, kill 占用 5432 的进程或改 compose port |
| `db-migrate` 报 `dirty database version X` | 上一次 migration 失败留脏 | `make db-reset` 重来 (毁数据); 或 `docker exec ... psql -c "UPDATE schema_migrations SET dirty=false"` |
| `test-integration` 单包失败 | 测试 bug | 单独跑 `HUAKAI_DATABASE_URL=postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable go test -tags=integration_pg -race -run TestXxx ./internal/<pkg>/...` 复现, 把 log 贴回来 |

### A.4 收尾

完成后:

```bash
make db-down   # 停容器, 保 volume (下次还能用)
# 或
make db-reset  # 彻底毁数据
```

Owner 把通过的 test 输出 (尾 30 行就够) 贴回对话, Claude/Codex 把测试结果写进 release gate 记录。

---

## B. Provider 指纹抓取 (每 vendor ≈ 30 分钟, 共 4-5 vendor)

### B.1 不重复 RUNBOOK

完整流程在:

```text
exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md
```

那里有: 工具链、per vendor 最小请求、secret redaction checklist、artifact JSON schema、R-D gate (sample_count ≥ 3 + hash 全匹配 + h2 可复核)。

本节只列 **Owner 视角的最短执行路径** + 与现有 RUNBOOK 的对接点。

### B.2 准备 (一次, 跨 vendor 复用)

```bash
# 隔离工作目录 (退出 shell 后会留, 自己手动清)
export RECAPTURE_TMP="$(mktemp -d -t huakai-recapture.XXXXXX)"
cd "$RECAPTURE_TMP"

# 工具 (任选其一, mitmproxy 多一份 h2 证据)
sudo apt-get install -y tcpdump tshark   # 或 brew install ...
# 可选: python -m pip install ja3 mitmproxy
```

### B.3 抓哪些 vendor

| vendor | 触发客户端 (Owner 已装) | endpoint (默认起点) | 备注 |
|---|---|---|---|
| `codex` | Codex CLI | `chatgpt.com` 或实测 host | 必抓 |
| `kiro` | Kiro CLI / IDE | Owner 实测确认 | 必抓 (randomized profile, 3 样本) |
| `gemini` | Gemini CLI 或 `curl` | `generativelanguage.googleapis.com` | 必抓 |
| `anthropic` | Claude Code 或 `curl` | `api.anthropic.com` | 必抓 (h2 证据要补齐) |
| `openai_api` | `curl` | `api.openai.com` | 可选 (若与 codex 不分独立 vendor 可跳过) |

每 vendor 至少 **3 次独立样本** (同机同网同 client version 连抓), 这是 R-D gate 硬要求。

### B.4 每 vendor 单次抓包 (照 RUNBOOK §3-§5)

```bash
export VENDOR="anthropic"   # 改这一处
export ENDPOINT="api.anthropic.com"
export UTC_DATE="$(date -u +%Y%m%dT%H%M%SZ)"
export IFACE="$(tcpdump -D | head -1 | cut -d. -f1)"   # 或手选

# 1. 启 tcpdump (前台跑, 抓完 Ctrl-C)
sudo tcpdump -i "$IFACE" -s 0 -w "${VENDOR}-${UTC_DATE}.pcap" host "$ENDPOINT"

# 2. 另一终端触发最小请求 (照 RUNBOOK §5 对应 vendor 那段命令)
#    内容只用 "Return one short sentence: HUAKAI recapture ping." 避免业务上下文

# 3. tcpdump Ctrl-C 后, 抽 handshake-only
tshark -r "${VENDOR}-${UTC_DATE}.pcap" -Y 'tls.handshake' -F pcap \
       -w "${VENDOR}-${UTC_DATE}-handshake-only.pcap"
rm -f "${VENDOR}-${UTC_DATE}.pcap"   # 原 pcap 不进仓库
```

### B.5 Secret scan (每个 capture 必跑)

```bash
strings "${VENDOR}-${UTC_DATE}-handshake-only.pcap" \
  | grep -Ei 'sk-|ya29\.|Bearer|Authorization|Cookie|api[-_]?key|access[-_]?token|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+'
```

任何一行命中, **DROP 整个 capture 重抓**, 不要人工编辑。

### B.6 落 artifact (照 RUNBOOK §7 schema)

```text
tools/fingerprint-collector/templates/_pending-backfill/<vendor>-real-<utc-date>.json
```

JSON 字段照 RUNBOOK §7 全套, 关键:

- `ja3` + `ja3_hash` + `ja4` 三个必填
- `h2_settings` / `h2_settings_frame` / `h2_pseudo_header_order` 抓不到必须写 `available: false` + `limitation_note`
- `cipher_suites` / `extensions` / `supported_groups` / `signature_algorithms` 不能空着

3 个样本同 vendor stable hash 全匹配才能 promotion 为 builtin。

### B.7 通知信号

artifact 落盘后, 在对话里发:

```text
recapture done: tools/fingerprint-collector/templates/_pending-backfill/<vendor>-real-<utc-date>.json
```

Claude/Codex 接着做:

- 跑 mimicry profile loader 测试
- 字段级 diff vs 现 builtin
- 通过 R-D gate 后 promotion (走 R-C-A1 路径)

---

## C. 完成判据

| 工作 | 完成信号 |
|---|---|
| PG 集成测试 | `make test-integration` 末尾全绿 + Owner 把通过输出末 30 行贴回 |
| codex 指纹 | 3 artifact 落 `_pending-backfill/`, secret scan 通过, ja3_hash 三样本一致 |
| kiro 指纹 | 同上, 但 randomized → 用 `ja3_hash_samples` 数组, 不要求 hash 单值 |
| gemini 指纹 | 同 codex |
| anthropic 指纹 | 同 codex, **且** h2 SETTINGS / pseudo-header 有 mitmproxy 证据 (或写明 HTTP/1.1-only 限制) |

任一 vendor secret 残留或 hash 漂移, 立刻暂停该 vendor mimicry production dispatch (fail-closed)。

---

## D. 不在本文档范围

- Rust mimicry `cargo test` (另文; sandbox 也跑不了, 但 Owner 本机简单: `cd exploratory/rust-core-gateway/merged && cargo test --workspace`)
- 生产 image OpenSSL build flag 校验 (照 RUNBOOK §10)
- F-AUDIT-1-D dashboard / F-TRUST-1-C user verification 端点 (后续 wave)
