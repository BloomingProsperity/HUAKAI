# R-D 真实上游指纹重抓 Runbook

本 runbook 只用于 Owner 在自己本机、自己账号、自己可授权网络里重抓真实上游指纹。
Claude/Codex 不能代跑真实账号请求，也不能接触 Owner 的 token、prompt、raw pcap。

依据：`docs/plans/2026-05-14-r3-on-merged-closure-codex.md` 第 3 节 Phase R-D 要求：
CI 只能做 local capture，真实上游验真必须由 Owner 本机执行；每个 vendor 至少 3 次样本；
codex/gemini/anthropic stable hash 必须全匹配，kiro 走 sample set；任一 hash 漂移必须 surface 并暂停 mimicry production dispatch。

## 1. 前置条件

账号条件：

- Owner 持有真实上游账号：codex、kiro、gemini、anthropic。
- 每次 capture 只使用 Owner 自己账号，不借用他人账号，不抓共享网络里的他人流量。
- 每个 vendor 至少抓 3 次独立样本；建议同一机器、同一网络、同一客户端版本下连续抓，降低变量。

工具条件：

- `tcpdump >= 4.99`，用于被动抓 TLS ClientHello。
- 或 `mitmproxy 11+`，用于 Owner 自愿 TLS terminate 并导出 raw ClientHello / h2 信息。
- `tshark`，用于从 pcap 中抽取 TLS handshake。
- JA3/JA4 计算工具二选一：
  - Python `ja3` library。
  - HUAKAI `tools/fingerprint-collector` 现有工具链。

安全边界：

- 不抓密钥。
- 不抓 prompt。
- 不提交 raw pcap。
- 只保留 TLS handshake、ALPN、ClientHello 字段、HTTP/2 preface / SETTINGS / pseudo-header order 这些 fingerprint artifact。
- 如果选择 mitmproxy，raw HTTP body、Authorization、cookie、account id、project id、prompt id 必须脱敏或丢弃。

建议隔离环境：

```bash
conda create -n huakai-recapture python=3.12 -y
conda activate huakai-recapture
python -m pip install ja3 scapy mitmproxy
export RECAPTURE_TMP="$(mktemp -d -t huakai-recapture.XXXXXX)"
cd "$RECAPTURE_TMP"
```

Docker 路径：

```bash
docker run --rm -it --network host -v "$PWD:/work" -w /work debian:stable-slim bash
apt-get update
apt-get install -y tcpdump tshark python3 python3-pip grep file
```

## 2. Endpoint 与 vendor 映射

Owner 以真实客户端实际访问的 host 为准。下表是本轮 R-D 的默认起点；如果客户端日志、
DNS、系统防火墙或 mitmproxy 显示真实模型请求用了不同官方 host，必须记录实际 host，
不要为了匹配表格而改写 artifact。

| vendor | 默认 endpoint | 触发客户端 | capture 备注 |
|---|---|---|---|
| codex | `codex.com` 或真实 Codex CLI 模型 host | Codex CLI | 现有内部模板曾记录 `chatgpt.com` 作为 Codex CLI 业务 endpoint；本轮以 Owner 实测为准。 |
| kiro | Kiro 实际模型 host | Kiro CLI / IDE | 先用日志、DNS 或防火墙确认 `<KIRO_HOST>`。 |
| gemini | `generativelanguage.googleapis.com` | Gemini CLI / API / Advanced | 如果实际走 `cloudcode-pa.googleapis.com` 或 regional endpoint，artifact 写实际 endpoint。 |
| anthropic | `api.anthropic.com` | Claude Code / Anthropic SDK | 需要单独补 HTTP/2 或说明只观察到 HTTP/1.1。 |
| openai_api | `api.openai.com` | OpenAI API 最小请求 | 如果本轮不作为独立 vendor，放入 notes 说明未执行。 |

## 3. tcpdump 抓包流程

每个 vendor 都按同一结构执行：准备隔离环境，启动 capture，触发一次最小请求，停止 capture，
只保留 handshake，再做 secret scan。

1. 找到出网接口。

   ```bash
   tcpdump -D
   ```

2. 设置变量。

   ```bash
   export IFACE="<iface>"
   export VENDOR="<vendor>"
   export ENDPOINT="<vendor_endpoint>"
   export UTC_DATE="$(date -u +%Y%m%dT%H%M%SZ)"
   export RECAPTURE_TMP="${RECAPTURE_TMP:-$(mktemp -d -t huakai-recapture.XXXXXX)}"
   cd "$RECAPTURE_TMP"
   ```

3. 启动 tcpdump。

   ```bash
   sudo tcpdump -i "$IFACE" -s 0 -w "${VENDOR}-${UTC_DATE}.pcap" host "$ENDPOINT"
   ```

4. 在另一个终端触发该 vendor 的最小请求。请求内容只用无业务含义的短句，不包含项目上下文。

5. tcpdump 收到样本后停止。原始 pcap 只在本机短暂保留，不能提交。

6. 只抽取 TLS handshake。

   ```bash
   tshark -r "${VENDOR}-${UTC_DATE}.pcap" -Y 'tls.handshake' -F pcap -w "${VENDOR}-${UTC_DATE}-handshake-only.pcap"
   ```

7. 对原始 pcap 做本地删除或加密归档；提交链路只使用 `*-handshake-only.pcap` 的路径。

   ```bash
   rm -f "${VENDOR}-${UTC_DATE}.pcap"
   ```

## 4. mitmproxy 抓包流程

mitmproxy 只由 Owner 自己选择使用。它能帮助抓 HTTP/2 preface、SETTINGS 和 pseudo-header order，
但也更容易接触 bearer token 和 body，因此 secret redaction 要更严格。

1. 启动 mitmproxy，并将输出限定到本地临时目录。

   ```bash
   export RECAPTURE_TMP="${RECAPTURE_TMP:-$(mktemp -d -t huakai-recapture.XXXXXX)}"
   mkdir -p "$RECAPTURE_TMP"
   mitmdump \
     --mode regular \
     --listen-host 127.0.0.1 \
     --listen-port 8080 \
     --set confdir="$RECAPTURE_TMP/mitm-conf" \
     --set stream_large_bodies=1 \
     --save-stream-file "$RECAPTURE_TMP/${VENDOR}-${UTC_DATE}.mitm" \
     -s "$RECAPTURE_TMP/dump-clienthello-only.py"
   ```

   `dump-clienthello-only.py` 必须只输出 ClientHello raw bytes 或解析后的 TLS metadata。
   它不能写 HTTP headers、body、Authorization、cookie、prompt。若当前 mitmproxy 版本不能可靠导出
   raw ClientHello bytes，则同时运行第 3 节 tcpdump，以 tcpdump 的 handshake-only pcap 作为
   `handshake_pcap_path`，mitmproxy 只用于 h2 SETTINGS / pseudo-header order。

2. 让目标 CLI / SDK 只在本次最小请求期间使用该代理。

   ```bash
   export HTTPS_PROXY="http://127.0.0.1:8080"
   export HTTP_PROXY="http://127.0.0.1:8080"
   ```

3. 触发一次最小请求后立即 unset。

   ```bash
   unset HTTPS_PROXY HTTP_PROXY
   ```

4. 从 mitmproxy UI 或导出的 flow 中只摘录：

   - TLS ClientHello 字段。
   - ALPN。
   - HTTP/2 SETTINGS frame 顺序和值。
   - HTTP/2 pseudo-header order。
   - HTTP/1 header order / case。

5. 不保留任何 Authorization、cookie、body、prompt、account id、project id。

## 5. per vendor 最小请求

### 5.1 codex

capture host 优先使用 Owner 实测的 Codex CLI 模型请求 host。若无法确认，先从
`codex.com` 开始；若样本为 0，再用 DNS / 客户端日志定位实际 host。

```bash
codex --version
codex exec "Return one short sentence: HUAKAI recapture ping."
```

artifact 里：

- `vendor`: `codex`
- `endpoint`: 实际 host + path；未知 path 时至少记录 host。
- `notes`: 记录客户端版本、OS、是否存在代理、是否观察到官方 host 变体。

### 5.2 kiro

Kiro 的模型 host 以 Owner 本机观测为准。先确认 CLI 或 IDE 已登录。

```bash
kiro --version
kiro "Return one short sentence: HUAKAI recapture ping."
```

如果只使用 IDE，在 chat / agent 面板发送同一句短请求。若没有 CLI，`kiro --version`
不可用不是失败，artifact notes 写明 `triggered_by=IDE`。

artifact 里：

- `vendor`: `kiro`
- `endpoint`: `<KIRO_HOST>`
- `notes`: 记录 CLI/IDE 路径和 endpoint 发现方法。

### 5.3 gemini

Gemini 可以用 Gemini CLI、Google AI Studio API、Vertex / gcloud 或网页路径。R-D artifact
必须写真实 endpoint，不要把 `generativelanguage.googleapis.com` 和其它 Google official host 混写。

Google AI Studio API 最小请求示例：

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent?key=${GEMINI_API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"contents":[{"parts":[{"text":"Return one short sentence: HUAKAI recapture ping."}]}]}'
```

Gemini CLI 示例：

```bash
gemini --version
gemini "Return one short sentence: HUAKAI recapture ping."
```

artifact 里：

- `vendor`: `gemini`
- `endpoint`: 实际模型 API endpoint。
- `notes`: 记录 API key / OAuth / Vertex 路径，但不要写 token、project id、prompt id。

### 5.4 anthropic

Anthropic SDK / curl 最小请求示例：

```bash
curl https://api.anthropic.com/v1/messages \
  -H "x-api-key: ${ANTHROPIC_API_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"claude-3-5-haiku-latest","max_tokens":16,"messages":[{"role":"user","content":"Return one short sentence: HUAKAI recapture ping."}]}'
```

Claude Code 路径示例：

```bash
claude --version
claude -p "Return one short sentence: HUAKAI recapture ping."
```

artifact 里：

- `vendor`: `anthropic`
- `endpoint`: `api.anthropic.com` 或 Claude Code 实测 endpoint。
- `notes`: 说明 SDK/curl/Claude Code 路径。

### 5.5 openai_api

如果 Owner 把 `api.openai.com` 作为独立 OpenAI API vendor 样本，使用最小 Chat Completions 请求。

```bash
curl https://api.openai.com/v1/chat/completions \
  -H "Authorization: Bearer ${OPENAI_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"Return one short sentence: HUAKAI recapture ping."}],"max_tokens":16}'
```

artifact 里：

- `vendor`: `openai_api`
- `endpoint`: `api.openai.com`
- `notes`: 说明是否与 codex 分开验收。

## 6. secret redaction checklist

每个 capture 必须逐项通过。任何一步失败都 DROP 该 capture，重新抓，不做人工修补。

- [ ] 原始 pcap 不提交。
- [ ] 已用 `tshark -Y 'tls.handshake'` 生成 handshake-only pcap。
- [ ] handshake-only pcap 不含 TLS application data。
- [ ] strings scan 没有 token、prompt、Authorization、cookie、account id、project id。
- [ ] JSON artifact 不含 raw bearer token、API key、refresh token、OAuth subject、真实账号 ID。
- [ ] JSON artifact 的 prompt 只保留固定 ping 句，不含业务上下文。
- [ ] mitmproxy flow 原始文件未进入 git。

推荐检查命令：

```bash
strings "${VENDOR}-${UTC_DATE}-handshake-only.pcap" \
  | grep -Ei 'sk-|ya29\.|Bearer|Authorization|Cookie|Set-Cookie|api[-_]?key|access[-_]?token|refresh[-_]?token|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+'
```

GitHub-style false positive 也要 surface。比如普通字符串碰巧匹配 `eyJ...` 形态，也先视为风险，
由 Owner 决定 DROP 或记录 false positive 证据。

检查 TLS record 类型：

```bash
tshark -r "${VENDOR}-${UTC_DATE}-handshake-only.pcap" -T fields -e tls.record.content_type | sort -u
```

允许值应只覆盖 handshake 相关记录。若出现 application data，DROP。

检查 git staging：

```bash
git status --short
find tools/fingerprint-collector exploratory/rust-core-gateway/merged/tools/recapture -name '*.pcap' -o -name '*.mitm'
```

任何 `.pcap` / `.mitm` 出现在可提交路径中，都先删除或移出仓库目录。

### anthropic-claude-code pending-backfill 教训

`tools/fingerprint-collector/templates/_pending-backfill/anthropic-claude-code.json`
当前留在 `_pending-backfill`，不是 builtin。内部文件显示它有 TLS ClientHello、JA3/JA4、
cipher suites、extensions 等字段，但 `h2_settings.available=false`，并注明 collector v1
无法解密 TLS record，HTTP/2 SETTINGS 为空是预期行为。这个样本还没有满足 R-D 对 h2 frame
可复核 artifact 的要求，所以本轮 anthropic 重抓必须补齐 h2 证据，或者在 artifact 里明确写出
为什么该客户端只观察到 HTTP/1.1 / 无 h2 SETTINGS。

## 7. artifact JSON 格式

文件名：

```text
<vendor>-<utc-date>-<seq>.json
```

示例：

```text
anthropic-20260515T040000Z-01.json
```

字段必须与 `tools/fingerprint-collector/templates/SCHEMA.md` 保持兼容。loader 可忽略未知字段，
但不能缺少 real template 的关键 TLS 字段。

模板：

```json
{
  "_comment": "Owner real-upstream R-D recapture artifact; no secrets; no raw prompt.",
  "_field_sources": {
    "tls": "capture: <handshake-only pcap path or fingerprint-collector output>",
    "h2_settings": "capture: <mitmproxy/tshark/wireshark artifact or limitation note>",
    "http_layer": "capture: <mitmproxy sanitized observation or limitation note>",
    "auth_layer": "manual redaction summary; no token values"
  },
  "vendor": "<codex|kiro|gemini|anthropic|openai_api>",
  "endpoint": "<actual upstream endpoint>",
  "capture_method": "<tcpdump|mitmproxy>",
  "capture_date": "<UTC RFC3339>",
  "handshake_pcap_path": "<post-redaction local/private path>",
  "mode_name": "<fingerprint profile name>",
  "collected_at": "<UTC RFC3339>",
  "target_host": "<actual host>",
  "capture_target_host": "<optional capture filter host>",
  "sample_count": 1,
  "tls_backend": "<observed or unknown>",
  "grease": false,
  "extension_order": "<stable|randomized|unknown>",
  "ja3": "<ja3 input string>",
  "ja3_hash": "<32 hex hash>",
  "ja3_hash_samples": [],
  "ja4": "<ja4 string>",
  "ja4_stable_prefix": "",
  "ja4_samples": [],
  "cipher_suites": [],
  "extensions": [],
  "supported_versions": [],
  "curves": [],
  "supported_groups": [],
  "sig_algos": [],
  "signature_algorithms": [],
  "alpn_protocols": [],
  "ec_point_formats": [],
  "key_share_groups": [],
  "psk_modes": [],
  "padding_len": 0,
  "early_data_enabled": false,
  "h2_settings": {
    "available": false,
    "settings": [],
    "limitation_note": "<required if unavailable>"
  },
  "h2_settings_frame": {
    "available": false,
    "raw_order": [],
    "values": {},
    "source": "<capture artifact or limitation note>"
  },
  "h2_pseudo_header_order": {
    "available": false,
    "order": [],
    "source": "<capture artifact or limitation note>"
  },
  "http_layer": {
    "protocol": "<h2|http1.1|h2_or_http1.1|unknown>",
    "endpoint": "<full endpoint if safely known>",
    "method": "POST",
    "user_agent": "<redacted/template value>",
    "header_order": [],
    "auth_mechanism": "<redacted summary>",
    "refresh_endpoint": "",
    "source_note": "<wire capture source or limitation>"
  },
  "auth_layer": {
    "mechanism": "<redacted category>",
    "authorization_header": "Authorization: Bearer <access_token>",
    "token_source": "<redacted source path/category only>"
  },
  "notes": [
    "No raw pcap committed.",
    "No bearer token, API key, cookie, account id, project id, prompt, or body retained."
  ]
}
```

字段填写规则：

- `supported_groups` 必须与 `curves` 保持一致。
- `signature_algorithms` 必须与 `sig_algos` 保持一致。
- `ja3` 保存 input string；`ja3_hash` 保存 MD5 hash。
- `ja4_samples` / `ja3_hash_samples` 用于多样本集合，尤其是 kiro 这类 randomized profile。
- `h2_settings_frame` 和 `h2_pseudo_header_order` 没抓到时不能留空解释，必须写 `available=false`
  和 `source` / `limitation_note`。
- `handshake_pcap_path` 指向 post-redaction artifact；不要指向 raw pcap。

## 8. 提交位置与信号

Owner 先把 artifact 放入 pending：

```text
tools/fingerprint-collector/templates/_pending-backfill/<vendor>-real-<utc-date>.json
```

示例：

```text
tools/fingerprint-collector/templates/_pending-backfill/anthropic-real-20260515T040000Z.json
```

然后给 Claude/Codex 明确信号：

```text
recapture done: tools/fingerprint-collector/templates/_pending-backfill/<vendor>-real-<utc-date>.json
```

Claude/Codex 后续只做：

- 读取 pending artifact。
- 跑 mimicry profile loader 测试。
- 做字段级 diff。
- 通过 R-D gate 后再提升为 builtin template，走 R-C-A1 路径。

Claude/Codex 不做：

- 不读取 Owner secret。
- 不读取 raw pcap。
- 不代跑真实上游。
- 不把 pending artifact 静默提升为 builtin。

## 9. R-D gate

每个 vendor 至少 3 次样本：

- `sample_count >= 3`，或同一 vendor 至少 3 个独立 artifact。
- codex、gemini、anthropic stable profile：JA3/JA4 hash 必须全匹配。
- kiro randomized profile：必须落在 Owner 真实样本集合内，使用 `ja3_hash_samples` /
  `ja4_samples`，不能由代码猜测扩展顺序。
- h2 artifact 必须可复核：SETTINGS order/value、pseudo-header order 有来源；不可用时必须有明确限制说明。

fail-closed 规则：

- 任一 stable hash 漂移，标记 vendor-side fingerprint upgrade。
- 任一 hash 漂移，暂停该 vendor mimicry production dispatch。
- 任一 capture 发现 secret 残留，DROP 整个 capture。
- 任一 artifact 字段由猜测填充，退回 pending，不能 promotion。

验收输出：

- 字段级 diff，而不是只给 PASS/FAIL。
- 对每个 mismatch 写明：observed value、expected builtin value、是否 GREASE/randomized、是否阻塞 production。
- R-D 不以 CI local capture 单独通过作为 Released gate；必须有 Owner 本机真实上游样本。

## 10. Source files read

- `docs/RULES.md`
- `docs/plans/2026-05-14-r3-on-merged-closure-codex.md`
- `tools/fingerprint-collector/templates/SCHEMA.md`
- `tools/fingerprint-collector/README.md`
- `tools/fingerprint-collector/PLAYBOOK.md`
- `tools/fingerprint-collector/NOTES.md`
- `tools/fingerprint-collector/verify-capture.sh`
- `tools/fingerprint-collector/templates/codex-cli.json`
- `tools/fingerprint-collector/templates/kiro-cli.json`
- `tools/fingerprint-collector/templates/gemini-advanced.json`
- `tools/fingerprint-collector/templates/_pending-backfill/anthropic-claude-code.json`

Lane: specifier

Agent: Codex GPT-5

UTC timestamp: 2026-05-15T04:03:57Z
