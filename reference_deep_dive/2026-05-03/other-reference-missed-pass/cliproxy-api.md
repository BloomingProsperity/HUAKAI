# CLIProxyAPI missed pass

## Version

- Branch: `main`
- Commit: `56df36895a0e`
- Tag: `v6.10.1`
- Files: 524

## Source areas read

- Server bootstrap and login modes.
- Token store backend selection.
- Custom provider extension example.

## Behavior-confirmed capabilities

- Multi-login bootstrap covers Google, Codex OAuth/device, Claude, Antigravity, Kimi, and Vertex service-account import as first-class flows registered at startup.
- Token store backend selection is not a single local file path; startup prefers Postgres when configured and falls back through object store, git-backed store, and local-file style backends in order.
- Login handlers are registered as product flows, not one-off scripts.
- The provider extension example separates request preparation, token store, hooks, model registry, and request logger factory into distinct interfaces with no cross-cutting dependencies.

## HUAKAI gap

HUAKAI already has provider-account and credential-management specs, but CLIProxyAPI shows that operator bootstrap and provider extensibility must be first-class. If HUAKAI only models credentials after admin has manually inserted them, onboarding will be brittle.

## Upgrade design

- Add a provider onboarding state machine: `discovered -> login_started -> token_acquired -> verified -> schedulable`.
- Keep `CredentialInjector` separate from `ProviderOnboardingAdapter`; a login flow should not know gateway request internals.
- Make token store backends pluggable, but enforce server-side encryption and audit on every backend.
- Add provider SDK tests that prove new providers can add auth, model discovery, request logging, and redaction without touching gateway handler code.

## Suggested Feature IDs

- `F-PROVIDER-ONBOARD-001` L2: provider-account onboarding flow.
- `F-CRED-STORE-BACKEND-001` L2: credential store backend abstraction with KMS envelope requirement.
- `F-PROVIDER-SDK-001` L4: provider extension SDK with adapter contract tests.

## Acceptance test direction

- Onboard one OAuth provider and one static-key provider without editing gateway handlers.
- Simulate token store outage and verify gateway fails closed for new credential injection but preserves read-only admin diagnostics.
- Register a test provider plugin and verify model discovery, request logging, redaction, and credential injection contracts.

## Open questions

- Whether HUAKAI Personal Edition needs Git/object token stores or only Postgres/KMS.
- Whether CLI-style login belongs in admin UI, a CLI helper, or both.

---
Source files read: cliproxy-api cmd/server/main, examples/custom-provider/main
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
