# New API missed pass

## Version

- Branch: `main`
- Commit: `dac55f0fdeb1`
- Tag: `v1.0.0-rc.2`
- Files: 1907

## Source areas read

- Ability/channel cache.
- Weighted and priority channel selection.
- Channel test all and recovery.
- Main scheduler bootstrap.
- I18n/error message keys.

## Behavior-confirmed capabilities

- Channel selection uses retry-aware priority, channel query filtering, ability records, and cache initialization.
- Channel cache excludes disabled channels, sorts by priority, preserves multi-key polling state, and supports weighted selection.
- Bulk channel tests are locked to avoid overlapping test-all runs and can disable or re-enable channels based on test results.
- Startup wires cache sync, options sync, quota data update, subscription quota reset, task polling, upstream model update checks, and batch updater jobs as distinct registered workers.
- User-facing payment, rate-limit, and channel error messages are first-class internationalization keys rather than inline strings.

## HUAKAI gap

HUAKAI's plan mentions routing, pricing, and payments, but New API shows the operational layer around them: cached eligibility, locked batch monitors, model refresh jobs, payment/user error messaging, and re-enable paths.

## Upgrade design

- Scheduler jobs need ownership, lock, timeout, and visible last-run status.
- Channel/account recovery must exist, not just disablement. A stable SaaS cannot only move accounts to dead state.
- Model refresh should be versioned and previewable before it changes routing.
- Payment state errors should be typed and localizable from day one, not bolted onto a generic payment row.

## Suggested Feature IDs

- `F-SCHED-LOCK-001` L2: locked recurring jobs with last-run status.
- `F-CHANNEL-RECOVERY-001` L2: auto re-enable after clean health window.
- `F-MODEL-CATALOG-SYNC-001` L2: versioned upstream model sync with preview/apply.
- `F-PAY-STATE-I18N-001` L3: typed payment/order error messages and operator recovery copy.

## Acceptance test direction

- Start two monitor workers and assert only one performs test-all.
- Disable a channel, pass recovery probes, and assert it becomes schedulable with audit.
- Refresh model catalog and assert in-flight requests keep old capability snapshot.

## Open questions

- Whether HUAKAI should expose auto-reenable in L1 or keep it manual until monitor confidence is strong.
- Whether model sync should be per provider account or per provider type.

---
Source files read: new-api model/ability, model/channel_cache, controller/channel-test, main, i18n/keys
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
