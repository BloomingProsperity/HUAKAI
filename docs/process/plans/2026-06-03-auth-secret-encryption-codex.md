# 2026-06-03 auth-secret encryption codex plan

| Owner directive | "Fix the proxy auth_secret credential-governance gap... Implement + verify only." |
| Scope | In: backend proxy auth_secret write/read encryption, discriminating tests, build/test verification. Out: commits, schema migrations, new files in frozen packages, logging secrets, changing LICENSE. |
| Success criteria | Proxy auth_secret writes through the admin wrapper store an encrypted serialized envelope, not the plaintext input; the PostgreSQL proxy resolver decrypts that stored value before constructing the upstream proxy URL; tests fail if encryption is removed or decrypt is skipped; requested build/tests pass or blockers are recorded. |
| Time estimate | 45-75 minutes wall time; one Codex session. |
| Blast radius | Medium/high security surface: proxy credentials and outbound proxy routing. Changes are scoped to a new non-frozen admin wrapper package and the existing provider resolver file. |
| Failure modes | Generated sqlc code may be tempting to edit directly: avoid constructor/signature churn and keep encryption at the caller wrapper. Existing `proxies.auth_secret` has only one text column: serialize the existing credentialstore AES-GCM envelope instead of inventing crypto. Existing plaintext rows may fail decrypt: preserve a fallback only if necessary for compatibility and clearly mark as PM deep-review risk. |
| Decision points | PM deep-review required before land because this touches credential handling. If PM wants fail-closed on legacy plaintext instead of compatibility fallback, adjust resolver behavior in a follow-up. No commit by Codex. |
| Pre-execution checklist | 1. Cite write/read flow with file lines. 2. Locate existing credentialstore key provider/cipher pattern. 3. Write failing tests for encrypted write and decrypted read. 4. Implement minimal code. 5. Run gofmt and requested verification. 6. Report mutation evidence and blockers. |

## Concrete Execution Order

1. Verify current flow:
   - `backend/sql/queries/admin_proxies.sql` and generated `backend/internal/db/admin/admin_proxies.sql.go` define create/update with caller-encrypted `auth_secret`.
   - `backend/internal/provider/postgres_proxy_resolver.go` reads `p.auth_secret` and builds `url.UserPassword`.
   - `backend/internal/gateway/upstream_dispatcher.go` calls `ProxyResolver.Resolve` before wrapping transport.
2. Add TDD tests:
   - New non-frozen package `backend/internal/proxyadmin` tests prove `Create` and `Update` pass encrypted text to `admindb.CreateProxy`/`UpdateProxy` and never plaintext.
   - Provider resolver unit test proves an encrypted stored secret decrypts to the original proxy password before URL construction.
3. Implement:
   - Create `internal/proxyadmin` service using existing `credentialstore.Cipher` and `credentialstore.KeyProvider`.
   - Serialize `credentialstore.Envelope` into a versioned text format stored in `proxies.auth_secret`.
   - Edit existing `backend/internal/provider/postgres_proxy_resolver.go` to accept an optional `credentialstore.KeyProvider`, deserialize/decrypt encrypted values before `buildProxyURL`, and keep existing constructor behavior for callers not yet wired.
   - Wire gateway production resolver with the existing credential key provider in `backend/cmd/gateway/wiring.go` if the provider is already available there.
4. Verify:
   - Run focused RED before production edits.
   - Run focused GREEN after implementation.
   - Temporarily remove/bypass encryption to confirm the write test goes RED, then restore.
   - Run `cd backend && go build ./...`.
   - Run `cd backend && go test ./internal/credentialstore/... ./internal/proxyadmin/... ./internal/provider/... ./internal/gateway/...`.
