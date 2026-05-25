# DEFERRED vendor kiro license blocked

Date: 2026-05-25

## Status

Blocked for this Lane B implementation.

## Reason

The requested local clone target `/home/codex/refs-latest/kiro-gateway` could not be created because the sandbox reports that path as read-only. A primary license check against the upstream license file showed AGPL-3.0 text at `https://raw.githubusercontent.com/jwadow/kiro-gateway/main/LICENSE` line 0. Because the Owner directive allowed only MIT/Apache-2.0 reference implementation use for this vendor, Codex did not read or paraphrase kiro-gateway source code.

## Disposition

Mandatory Roadmap / Safe Equivalent:

- Keep existing HUAKAI Kiro refresher and session framework untouched.
- Do not add new Kiro acquisition behavior from the AGPL reference.
- Next safe path is either an MIT/Apache-2.0 Kiro reference, official AWS documentation for a clean independent SigV4 credential acquisition spec, or explicit Owner decision to design Kiro behavior without reading AGPL source.

## Clean-room note

No AGPL implementation source was read or copied. Only the license file was checked.
