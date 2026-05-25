# README + LEGAL.md 边界草案（R3 启动前置）

**Date:** 2026-05-06
**Status:** Draft（待 Owner 评 + sign-off 后落到对应根目录文件）
**Source:** Owner 2026-05-06 directive "这一步很很危险。记得 readme 写好界限"

---

## 用法

下面 3 份文档是 R3 transport mimicry 启动前必须落地的边界文档草案。
Owner 评审后若同意：

- `README.md` 草案 → 落到仓库根的 `README.md`（覆盖现有 README ack 草稿）
- `LEGAL.md` 草案 → 落到仓库根的 `LEGAL.md`（新增）
- `tools-fingerprint-collector-README.md` 草案 → 落到 `tools/fingerprint-collector/README.md`（在工具实施时一并 commit）

每份文档都明确"使用边界 + 不背书 + 责任自负"。

---

## 草案 1：仓库根 README.md

```markdown
# HUAKAI

A self-hosted reverse-proxy and account router for personal / small-team use of LLM API
provider accounts.

## What HUAKAI is

HUAKAI is an **operator-side** tool. It runs in front of one or more upstream LLM
provider accounts (Anthropic, OpenAI, Google Vertex, AWS Bedrock, OpenRouter) **that
the operator already lawfully holds**, and provides:

- A unified protocol surface for downstream clients
- Health-aware account dispatch
- Rate-limit / cooldown handling
- Usage / billing accounting
- Optional impersonation of the upstream's first-party CLI client (where required by
  the operator's lawful use case — see "Limits" below)

HUAKAI is intended for **personal use, internal team self-hosting, or security
research environments**. The repository is published as open source so operators can
audit what runs on their own infrastructure.

## What HUAKAI is NOT

- HUAKAI is **not affiliated with, endorsed by, or partnered with** any upstream LLM
  provider. Names like "Claude Code", "Claude", "Anthropic", "OpenAI", "ChatGPT",
  "Cursor", "Vertex AI", "Bedrock" and similar are the property of their respective
  owners. HUAKAI uses these names only for interoperability description.
- HUAKAI is **not a SaaS resale platform**. The project does not encourage,
  facilitate, or accept responsibility for operators reselling upstream account
  capacity to third parties for commercial gain.
- HUAKAI does **not** ship with any pre-loaded credentials, captured fingerprints,
  or other operational artifacts. All configuration is the operator's own.

## Intended use cases (whitelist)

- An operator runs HUAKAI on their own machine / server with their own legitimately
  obtained upstream accounts.
- A small team self-hosts HUAKAI internally to share usage of accounts that team
  members already lawfully hold.
- Security researchers, students, or developers study reverse-proxy / multi-account
  routing patterns in a controlled environment.

## Prohibited use cases (explicitly out of scope)

- Reselling upstream account capacity to third parties as a commercial service.
- Operating HUAKAI as a public-facing SaaS that pools accounts of unrelated
  third parties.
- Bypassing any specific upstream provider's Terms of Service for paid services
  rendered to the public.
- Phishing, man-in-the-middle attacks, or any unauthorized observation of network
  traffic.
- Use against accounts the operator does not lawfully hold.

## Compliance and responsibility

**The operator is solely responsible for ensuring their use of HUAKAI complies
with the Terms of Service of every upstream provider, with the laws of their
jurisdiction, and with the rights of any third parties involved.** The HUAKAI
project authors and contributors:

- Provide HUAKAI on an "AS IS" basis with no warranty.
- Make no claim that HUAKAI's use is permitted by any specific upstream provider.
- Accept no liability for the operator's use, misuse, account suspension, financial
  loss, legal exposure, or any other consequence.

If you are unsure whether your intended use is compliant with a given upstream's
ToS, **consult the upstream provider's documentation directly and / or seek
independent legal advice before deploying HUAKAI**.

## Transport-level impersonation (advanced, gated)

HUAKAI ships an optional transport-mimicry module (`R3`) that can adjust outbound
TLS / HTTP/2 fingerprints to match a first-party CLI client. This module:

- Is **off by default**. Operators must explicitly enable it per upstream provider.
- Requires the operator to capture their own first-party client fingerprint using
  the bundled `tools/fingerprint-collector` tool, **on their own machine, against
  their own client**, in their own legitimate network environment. **No fingerprint
  templates are shipped with the project.**
- Is intended only for cases where the operator's lawful use case requires the
  outbound to be indistinguishable from the first-party client at the transport
  layer.

By using R3, the operator confirms that:

1. They have the right to capture and use the source client fingerprint they collected.
2. Their use of impersonation does not violate the upstream provider's ToS or any
   applicable law in their jurisdiction.

## Components

[High-level component diagram TBD — list public packages, CLI entry, admin UI etc.]

## Quickstart

[TBD — link to setup guide]

## Building

[TBD — go build instructions, prerequisites]

## License

HUAKAI itself is licensed under the MIT License — see [LICENSE](LICENSE).
Third-party libraries (utls, gopacket, etc.) are subject to their own licenses;
see [docs/licenses/](docs/licenses/) for details.

## Legal

See [LEGAL.md](LEGAL.md) for full legal terms, trademark notices, and DMCA
contact.

## No warranty

```
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```
```

---

## 草案 2：仓库根 LEGAL.md

```markdown
# Legal Notice

## Trademarks

Names referenced in this repository — including but not limited to:

- "Anthropic", "Claude", "Claude Code"
- "OpenAI", "ChatGPT", "Codex"
- "Google", "Vertex AI", "Gemini"
- "Amazon", "AWS", "Bedrock"
- "OpenRouter", "Cursor", "Aider"

— are trademarks or registered trademarks of their respective owners. HUAKAI is
not affiliated with, endorsed by, or sponsored by any of these entities.
References to these names in HUAKAI source code, documentation, configuration,
or interface are made solely for the purpose of describing interoperability
behavior and do not imply any commercial relationship.

## Compliance

HUAKAI's design assumes an operator who:

1. Lawfully holds the upstream provider account(s) being routed.
2. Has reviewed and complies with each upstream provider's Terms of Service for
   the specific use case in question.
3. Operates HUAKAI on infrastructure they control.
4. Does not pool or resell upstream account capacity to unrelated third parties
   in violation of the upstream's ToS.

The HUAKAI project authors and contributors **do not** claim that HUAKAI's use
is permitted by any specific upstream provider for any specific use case. The
operator must independently verify ToS compliance.

## No legal advice

Nothing in this repository — including code, documentation, comments, plan
documents, decision records, or commit messages — constitutes legal advice.
Operators should consult independent legal counsel for any compliance questions
specific to their jurisdiction and use case.

## Liability disclaimer

In no event shall the HUAKAI authors or contributors be liable for any direct,
indirect, incidental, special, exemplary, or consequential damages (including
but not limited to procurement of substitute goods or services; loss of use,
data, or profits; or business interruption) however caused and on any theory of
liability, whether in contract, strict liability, or tort (including negligence
or otherwise) arising in any way out of the use of this software, even if
advised of the possibility of such damage.

## DMCA / takedown contact

If you believe your intellectual property rights have been infringed by content
in this repository, please contact:

[Contact email TBD by Owner]

Notice should include: identification of the copyrighted work, identification
of the allegedly infringing material, your contact information, a statement of
good faith belief, a statement of accuracy under penalty of perjury, and your
physical or electronic signature.

## Data handling

- HUAKAI itself collects no telemetry.
- The bundled `tools/fingerprint-collector` produces local files including raw
  packet captures. **These files must never leave the operator's local machine.**
  Specifically: do not attach pcap files or fingerprint output to issues, pull
  requests, or any public communication.
- Operator credentials, request bodies, and response bodies pass through HUAKAI
  in plain form by design (HUAKAI is a reverse proxy). Operators are responsible
  for ensuring their HUAKAI deployment's network and storage layers protect
  this data appropriately.

## Security disclosure

See [SECURITY.md](SECURITY.md) for vulnerability disclosure procedure.

We do not accept reports framed as "how to use HUAKAI to bypass an upstream
provider's ToS". Such reports are out of scope.

## Governing law

[TBD — Owner to specify, e.g. "This notice is governed by the laws of <jurisdiction>".]

## Updates

This document may be updated. The version in effect for any release is the
version contained in that release's git tag.
```

---

## 草案 3：tools/fingerprint-collector/README.md

```markdown
# claude-code-fingerprint-collector

A diagnostic tool for capturing the operator's own first-party CLI client's
TLS / HTTP-2 transport-layer fingerprint, for use by HUAKAI's R3 transport
mimicry module.

## What it does

The tool listens on a network interface using libpcap (Linux) or npcap
(Windows), filters for HTTPS traffic to the configured host, and dumps:

- TLS ClientHello bytes (cipher_suites order, extensions order, GREASE values,
  key_share groups, supported_versions, signature_algorithms list)
- HTTP/2 SETTINGS frames (parameter order and values)
- ALPN, SNI, EC point formats, and other publicly-visible TLS metadata

It produces:

- `output/clienthello-template.json` — a structured fingerprint template
- `output/ja3-hashes.txt`, `ja4-hashes.txt` — derived fingerprint hashes
- `output/raw-pcap-snippet.pcap` — local-only raw packet dump (do NOT commit)
- `output/metadata.json` — capture timestamp and tool version

The tool **does not decrypt** any TLS payload. It only inspects the unencrypted
ClientHello and HTTP/2 SETTINGS frames, which are visible to any on-path
observer of the connection.

## What it must NOT be used for

- Capturing other people's traffic on a shared or corporate network without
  authorization.
- Inspecting traffic on networks the operator does not own or have explicit
  authorization to monitor.
- Capturing traffic to upstream providers from clients the operator does not
  lawfully control.
- Any form of network reconnaissance or unauthorized observation.

If your network environment includes a TLS-inspecting proxy (corporate antivirus,
zscaler, etc.), the tool will detect the certificate chain mismatch and refuse
to proceed — because what would otherwise be captured is the **proxy's** TLS
fingerprint, not the first-party client's.

## Pre-flight checklist

Before running this tool, the operator confirms:

- [ ] I am running it on a machine I own or fully control.
- [ ] The traffic I am about to capture is generated by my own legitimate
      first-party client, on my own legitimately-held upstream account.
- [ ] My network environment does not include a TLS-inspecting MITM proxy I
      am unaware of.
- [ ] I will not commit, upload, or share the raw pcap file. Only the
      sanitized fingerprint template (with no IP / MAC addresses) may be
      committed to a private template store.
- [ ] My use of the captured fingerprint complies with the upstream provider's
      Terms of Service and applicable law in my jurisdiction.

## Usage

```
fingerprint-collector \
  -iface <NIC name> \
  -host api.anthropic.com \
  -duration 600 \
  -min-samples 5 \
  -out ./output/
```

Then in another terminal, run the first-party CLI client and issue 5–10 typical
requests (varied: short text, long context, tool_use, streaming, non-streaming).

## Platform notes

- **Windows**: requires npcap. Install with "WinPcap API-compatible Mode" enabled.
  Run the tool as administrator.
- **Linux**: requires libpcap-dev. Run with `sudo` or grant `cap_net_raw` to
  the binary.
- **macOS**: untested. Adapt at your own risk.

## Output handling

| File | Commit to git? | Why |
|------|---------------|-----|
| `clienthello-template.json` | OK to commit to a **private** template store inside HUAKAI | Sanitized; no IP / MAC; only TLS metadata |
| `ja3-hashes.txt`, `ja4-hashes.txt` | OK to commit | Just derived hashes |
| `http2-settings.json` | OK to commit | Just parameter values |
| `raw-pcap-snippet.pcap` | **NEVER commit** | Contains raw packets; may include private IP / MAC / SNI |
| `metadata.json` | Review before committing | May contain hostname / IP info |

A pre-commit hook (`.githooks/no-pcap-commit`) is provided to block accidental
pcap commits.

## Source

This tool is part of the HUAKAI project. See the project root for license and
legal notice.
```

---

## 评审清单（Owner sign-off 用）

| 项 | OK? | 备注 |
|---|---|---|
| README "What HUAKAI is" 段是否准确表达 Owner 意图（个人 / 小团队，不是商业 SaaS） | | |
| README "Prohibited use cases" 是否覆盖 Owner 想要禁止的场景 | | |
| README "Transport-level impersonation" 段是否过于鼓励 | | |
| LEGAL "Trademarks" 段是否漏了上游名 | | |
| LEGAL "DMCA contact" 邮箱填谁 | | |
| LEGAL "Governing law" 司法辖区填哪里 | | |
| collector README "must NOT be used for" 段是否充分 | | |
| 三份文档中文版本是否需要双语并行（ZH + EN）以 README_CN.md 形式发布 | | |
| 是否需要先在仓库加 LICENSE 文件本身（MIT 文本），还是先 README/LEGAL | | |

---

## 与 R3 plan 的衔接

R3 transport mimicry 的 §0 已固化"启动前置：本草案三份文档落地 + Owner
sign-off"作为硬 gate。未完成本前置物时：

- 不写 R3 任何代码
- 不写 fingerprint-collector 任何代码
- 不接受 Sonnet sub-agent dispatch 来实施 R3

完成本前置物后才进入 R3 实施序列。
