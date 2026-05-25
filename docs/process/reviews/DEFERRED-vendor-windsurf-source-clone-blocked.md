# DEFERRED vendor windsurf source clone blocked

Date: 2026-05-25

## Status

Implemented as a manual token safe equivalent for this slice; upstream source mining remains deferred.

## Reason

The requested clone target `/home/codex/refs-latest/windsurf-api` could not be created because the sandbox reports that path as read-only. The upstream license check against `https://raw.githubusercontent.com/dwgx/WindsurfAPI/master/LICENSE` identified MIT text at line 0, but Codex did not read WindsurfAPI source code in this turn because the clone step failed.

## Implemented Safe Equivalent

HUAKAI now accepts a pasted Windsurf / Codeium auth token as a `windsurf/oauth` credential candidate through `NewWindsurfCodeiumAuthTokenCandidate`. This preserves the user outcome of configuring Windsurf credentials after an operator retrieves the token manually.

## Known Operator Endpoint Notes

- Human token retrieval path to document in operator UI/docs: `https://windsurf.com/show-auth-token`
- Authentication host to verify in the next source-mining pass: `https://auth.windsurf.com`

## Follow-up

When a writable reference path is available, clone `dwgx/WindsurfAPI`, re-check license, and replace this safe equivalent with a source-observed clean-room decomposition if the source confirms additional behavior.
