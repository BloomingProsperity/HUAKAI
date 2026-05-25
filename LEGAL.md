# Legal Notice

**Languages:** [English](LEGAL.md) · [简体中文 (TBD)](LEGAL_CN.md)

---

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

The HUAKAI project authors and contributors **do not** claim that HUAKAI's use
is permitted by any specific upstream provider for any specific use case. The
operator must independently verify ToS compliance.

If an operator chooses to deploy HUAKAI as a SaaS or any third-party-facing
service, that operator is solely responsible for ensuring such deployment
complies with each upstream's ToS and applicable law.

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

- Email: `Huaxiaokai2@outlook.com`

Notice should include: identification of the copyrighted work, identification
of the allegedly infringing material, your contact information, a statement of
good faith belief, a statement of accuracy under penalty of perjury, and your
physical or electronic signature.

## Data handling

- HUAKAI itself collects no telemetry. The software does not phone home.
- The bundled `tools/fingerprint-collector` produces local files including raw
  packet captures. **These files must never leave the operator's local machine.**
  Specifically: do not attach pcap files or fingerprint output to issues, pull
  requests, or any public communication.
- Operator credentials, request bodies, and response bodies pass through HUAKAI
  in plain form by design (HUAKAI is a reverse proxy). Operators are responsible
  for ensuring their HUAKAI deployment's network and storage layers protect
  this data appropriately.

## Security disclosure

See [SECURITY.md](SECURITY.md) (TBD) for vulnerability disclosure procedure.

We do not accept reports framed as "how to use HUAKAI to bypass an upstream
provider's ToS". Such reports are out of scope of this project's security
program.

## Governing law

This notice is governed by the laws of the **Socialist Republic of Vietnam**,
without regard to conflict-of-law principles. Disputes shall be brought in the
courts of **Ho Chi Minh City, Vietnam**.

This choice of law does not override the operator's own legal obligations
under the laws of their own jurisdiction; operators outside Vietnam remain
responsible for their local compliance.

## Updates

This document may be updated. The version in effect for any release is the
version contained in that release's git tag. Material changes will be noted
in the project's release notes.

## Acknowledgements

HUAKAI is published under [MIT](LICENSE). Third-party libraries used by HUAKAI
remain subject to their own licenses; see project go.mod / package.json files
for the canonical dependency list.
