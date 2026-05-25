# F-CAPACITY-GRAPH-001: Cross-Vendor Capacity Graph — Forecast, Restock, and Fault-Domain Spillover

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-CAPACITY-GRAPH-001 |
| Specifier | Claude (Executor, 2026-05-06) |
| Specifier date | 2026-05-06 |
| Reviewer | — |
| Review date | — |
| Released date | — |
| Lane mode | Option B (default) |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Internal synthesis plan (2026-05-02-huakai-algo-upgrade-synthesis.md) — internal artifact; algorithms paraphrased, no verbatim copy
- DR-009-algorithm-upgrade-policy.md — Owner decisions Q6 (capacity graph scope), §6.6 hard floors (A19)
- Internal algo upgrade plan (Claude and Codex variants, 2026-05-02) — specifier-lane backing artifacts only; implementer MUST NOT open
- Evidence rows: synthesis §1 (A17/A18/A19/A20), synthesis §6 (Q6 decision), synthesis §6.6 (A19 hard floor), DR-009 §Owner Decision

## Capability

This spec satisfies F-CAPACITY-GRAPH-001 (new row, to be added to [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md)): "Cross-vendor provider-account capacity graph that continuously forecasts depletion timelines, recommends optimal restock allocations, schedules demand across vendor accounts via a min-residual graph model, and enforces fault-domain-aware spillover to prevent correlated failure propagation — with mandatory per-tenant isolation in SaaS Edition (DR-009 Q6)."

Related specs: [F-ROUTE-001 pool-routing.md](pool-routing.md), [F-ROUTER-HEALTH-001 rate-limiting.md](rate-limiting.md).

## Actor

- **System** (Capacity Planner daemon, Routing Engine): executes forecast, graph traversal, and spillover decisions continuously.
- **Operator**: observes capacity signals, receives restock recommendations, configures fault-domain labels and spillover policy.
- **External Provider**: receives dispatched demand; capacity residuals are tracked per provider account.
- **User** (SaaS Edition): request is served within tenant-isolated capacity slice; never observes another tenant's residual.

## Preconditions

1. Provider accounts exist in the pool with capability flags, vendor labels, region labels, and account-group labels resolved.
2. Fault-domain label records (`account_fault_domain_labels`) are present for every active provider account.
3. Capacity graph edge materialized view (`capacity_graph_edges`) is populated and refreshed on a sub-minute schedule.
4. Forecast state tables (`capacity_forecasts`) are writable.
5. SaaS Edition: tenant context (`tenant_id`) is present on every demand unit entering the graph; cross-tenant leakage is structurally impossible (enforced by row-level tenant filter before graph traversal — DR-009 Q6, synthesis §6 Q6 硬底线).
6. Operator has configured spillover policy weights and fault-domain distance thresholds.

## Normal Path

### Phase 1 — Capacity Depletion Forecast (A17)

The system runs an EWMA-based depletion forecast for each (account, pool) pair on every tick (configurable interval, default 60 s).

**Algorithm sketch (A17 — paraphrased from synthesis §1):**

1. Maintain a per-account exponentially weighted moving average of the demand consumption rate. At each tick, blend the observed rate `r_obs` with the prior smoothed rate `r_ewma` using decay factor `α`:

   ```
   r_ewma ← α · r_obs + (1 − α) · r_ewma
   ```

   Default `α = 0.2` (operator-tunable per pool).

2. Compute a seasonal adjustment factor `s_t` derived from historical consumption at the same time-of-day / day-of-week slot (rolling 4-week window, P95 of observed seasonal multipliers). Adjusted forecast rate:

   ```
   r_forecast ← r_ewma · s_t
   ```

3. Given current residual capacity `C_remaining` and `r_forecast`, compute the estimated time-to-depletion (ETA in minutes):

   ```
   eta_minutes ← C_remaining / r_forecast        (when r_forecast > 0)
   eta_minutes ← ∞                               (when r_forecast = 0)
   ```

4. Derive confidence bounds from the distribution of residuals between `r_ewma` and `r_obs` over the rolling window:

   - `p10_minutes` — optimistic ETA (high consumption rate scenario)
   - `p50_minutes` — median ETA
   - `p90_minutes` — conservative ETA (low consumption rate scenario)

5. Write one row to `capacity_forecasts` per account per tick (upsert on `(account_id, forecast_at)`).

6. If `eta_minutes < alert_threshold_minutes` (operator-configured per pool), emit `capacity_eta_minutes` signal to the observability pipeline.

### Phase 2 — Restock Recommendation (A18)

When `eta_minutes` falls below the restock planning horizon (operator-configured, default 2× `alert_threshold_minutes`), the system computes a restock recommendation via a bounded knapsack formulation.

**Algorithm sketch (A18 — paraphrased from synthesis §1):**

1. Enumerate candidate restock units (provider account "top-up" options), each described by:
   - `capacity_gain` — units of capacity added per restock unit
   - `cost` — operator-assigned cost weight (financial or operational)
   - `max_units` — upper bound on how many of this option can be applied (bounded knapsack)

2. Solve the bounded knapsack over candidate options subject to:
   - Total cost ≤ `budget_cap` (operator-configured)
   - Maximise total `capacity_gain`

   For small item counts (≤ 20 distinct types), use dynamic-programming bounded knapsack (O(n · W) where W = budget steps). For larger item counts, fall back to a greedy fractional approximation as a lower bound, flagged in the recommendation payload as `approximate = true`.

3. Emit the winning allocation as a `recommendations jsonb` blob appended to the `capacity_forecasts` row for this tick.

4. Recommendation payload structure (stored in `capacity_forecasts.recommendations`):

   ```
   {
     "generated_at": "<iso8601>",
     "horizon_minutes": <int>,
     "approximate": <bool>,
     "items": [
       { "option_id": "<str>", "units": <int>, "capacity_gain": <num>, "cost": <num> }
     ],
     "total_capacity_gain": <num>,
     "total_cost": <num>
   }
   ```

### Phase 3 — Cross-Vendor Min-Residual Graph Scheduling (A19)

**This algorithm is a seller-interest hard floor (DR-009 §6.6, synthesis §6.6): implementer MUST NOT weaken or remove the min-residual constraint.**

The routing engine models provider accounts as nodes in a directed capacity graph. Each edge connects a demand source (logical demand unit) to a candidate provider account node, weighted by the account's current residual capacity.

**Algorithm sketch (A19 — paraphrased from synthesis §1 and §6.6):**

1. On each scheduling decision, read the materialized view `capacity_graph_edges` to obtain the current set of eligible (demand, account) pairs with their capability check results.

2. For each candidate account node `v`, compute the residual capacity after hypothetically assigning this demand unit:

   ```
   residual_after(v) ← C_remaining(v) − demand_size
   ```

3. Apply the min-residual selection rule: among all eligible accounts that pass capability check, select the account that **maximises** `residual_after(v)`. This is equivalent to routing to the account with the most headroom, which preserves the overall capacity of the graph (a min-cut preservation property — ensures no single vendor drains before others, maintaining maximum combinatorial availability).

4. **SaaS Edition mandatory constraint (DR-009 Q6):** Before graph traversal, apply a hard row-level tenant filter. Only accounts whose `tenant_id` matches the request `tenant_id` may appear in the graph for this scheduling decision. No cross-tenant residual sharing, no cross-tenant spillover. This filter is non-bypassable and must be applied before, not after, the min-residual computation.

5. On assignment, atomically decrement `C_remaining(v)` in the same transaction that reserves the demand slot (integrates with A09 two-phase quota reserve — Tx1 reserve phase).

6. Refresh `capacity_graph_edges` materialized view at sub-minute cadence (default 30 s). Stale reads during a refresh window are acceptable; the Phase C atomic admission gate (from F-POOL-001 pool-routing spec) provides authoritative revalidation.

### Phase 4 — Fault-Domain Spillover Guard (A20)

When a provider account's residual falls below a configured low-water mark, or when a fault event is detected in a fault domain, the spillover guard reroutes demand to an account in a different fault domain.

**Algorithm sketch (A20 — paraphrased from synthesis §1 and §2):**

1. Each provider account is annotated with a fault-domain label tuple `(vendor, region, project_id, account_group)` from the `account_fault_domain_labels` table.

2. Define the fault-domain distance between two accounts `u` and `v` as the number of label dimensions that differ (Hamming distance over the 4-tuple), normalised to [0, 1] by dividing by 4.

3. When the current best-candidate account `u` is in a fault domain with residual below `low_water_fraction` (operator-configured, default 0.15 of capacity), compute a spillover score for each alternative candidate account `v`:

   ```
   spillover_score(v) ← 0.7 · distance(u, v) + 0.3 · (residual_after(v) / C_max(v))
   ```

   Select the candidate `v` maximising `spillover_score(v)`. The 0.7 / 0.3 weight split (operator-tunable) prioritises domain separation while still accounting for residual headroom.

4. Emit the spillover event to the observability pipeline:
   - `capacity_residual_min_by_domain` — minimum residual fraction per fault domain
   - `fallback_domain_distance_histogram` — histogram of domain distances at which spillover was triggered

5. **Fault-domain isolation invariant**: spillover MUST NOT select a candidate in the same fault domain as the triggering account when at least one cross-domain candidate is available (even if it has lower residual). This protects against correlated failure modes (e.g., a cloud provider AZ outage draining all accounts sharing that AZ label).

6. **SaaS Edition**: spillover candidates are subject to the same hard tenant-filter as Phase 3 (A19). Cross-tenant spillover is prohibited.

## Failure Path

### Failure: No Eligible Account After Min-Residual Filter

- **Trigger**: all candidate accounts have `residual_after(v) ≤ 0` or fail capability check.
- **Observable outcome**: scheduling returns `NO_CAPACITY`; request falls through to pool-exhausted path in F-POOL-001.
- **Operator-visible signal**: `capacity_residual_min_by_domain` drops to 0 for affected domain; `pool_exhausted` annotation on the routing reason payload.

### Failure: SaaS Cross-Tenant Attempt Detected

- **Trigger**: a demand unit arrives with a `tenant_id` that does not match any account in the graph after the mandatory tenant filter — caused by misconfiguration, not by algorithm bypass (the filter is structural).
- **Observable outcome**: zero eligible candidates returned; request receives 503 with `Retry-After`.
- **Operator-visible signal**: alert `capacity_graph_tenant_isolation_zero_candidates`; log includes `tenant_id` (masked) and the pool ID.

### Failure: Forecast Rate Zero (Division Guard)

- **Trigger**: `r_forecast = 0` because no demand has been observed in the EWMA window (cold start or idle account).
- **Observable outcome**: `eta_minutes` set to sentinel value `∞` (stored as `NULL` in the forecasts table); no alert emitted.
- **Operator-visible signal**: `eta_minutes IS NULL` visible in operator dashboard; no false depletion alarm.

### Failure: Restock Knapsack Budget Infeasible

- **Trigger**: no restock option fits within `budget_cap` (all options cost more than budget).
- **Observable outcome**: recommendation payload emitted with `items = []`, `total_capacity_gain = 0`, `approximate = false`.
- **Operator-visible signal**: `restock_infeasible` flag set in the recommendation payload; operator alerted to revise budget cap or add lower-cost options.

### Failure: Spillover — No Cross-Domain Candidate Available

- **Trigger**: all accounts across all candidates are in the same fault domain (e.g., single-vendor pool).
- **Observable outcome**: spillover guard logs the constraint violation but selects the best intra-domain candidate by residual (fallback to min-residual ordering). Emits `spillover_intra_domain_forced` signal.
- **Operator-visible signal**: `spillover_intra_domain_forced` counter in observability; operator prompted to add cross-domain accounts.

### Failure: Graph Edge View Stale Beyond Threshold

- **Trigger**: `capacity_graph_edges` has not been refreshed within `max_stale_seconds` (operator-configured, default 120 s).
- **Observable outcome**: scheduling continues using the stale view but appends `edge_view_stale = true` to the routing reason; Phase C atomic admission gate in F-POOL-001 remains authoritative.
- **Operator-visible signal**: `capacity_graph_edge_view_stale_seconds` metric; alert if staleness exceeds 2× refresh interval.

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| `NO_CAPACITY` / pool exhausted | `capacity_residual_min_by_domain` at 0; `pool_exhausted` annotations | Add provider accounts; trigger restock per recommendation payload; or raise `budget_cap` |
| SaaS tenant isolation zero candidates | Alert `capacity_graph_tenant_isolation_zero_candidates` | Verify tenant-to-account assignment; check that accounts are tagged with correct `tenant_id` in pool config |
| Cold start / forecast NULL | Dashboard shows `eta_minutes IS NULL` | Normal on first tick; clears after first observation window (1 EWMA period); no action if transient |
| Restock infeasible | `restock_infeasible` flag in recommendation | Increase `budget_cap` or add lower-cost restock options to the candidate table |
| Spillover intra-domain forced | `spillover_intra_domain_forced` counter | Add provider accounts in different vendor / region / project to the pool |
| Edge view stale | `capacity_graph_edge_view_stale_seconds` alert | Check materialized view refresh job; restart if stopped; no data loss — Phase C gate is authoritative |

## Data Structures

### `capacity_forecasts`

Stores one forecast row per account per tick; upserted on `(account_id, forecast_at)`.

```
capacity_forecasts (
  forecast_at         timestamptz   -- tick timestamp (PK component)
  account_id          uuid          -- provider account (PK component); NULL for pool-level aggregate
  pool_id             uuid          -- pool this account belongs to (for aggregate rows)
  eta_minutes         numeric       -- NULL when r_forecast = 0 (cold / idle)
  p10_minutes         numeric       -- optimistic ETA bound
  p50_minutes         numeric       -- median ETA bound
  p90_minutes         numeric       -- conservative ETA bound
  r_ewma              numeric       -- smoothed consumption rate at this tick
  s_t                 numeric       -- seasonal adjustment factor applied
  recommendations     jsonb         -- restock recommendation payload (NULL if not in planning horizon)
  restock_infeasible  boolean       -- true when knapsack returned empty set
  approximate         boolean       -- true when greedy fallback used instead of exact DP
)
```

### `capacity_graph_edges`

Materialized view (not a base table). Refreshed on sub-minute cadence. Stores the live (demand type, account) eligibility pairs for the min-residual scheduler.

```
capacity_graph_edges (  -- materialized view
  demand_id            uuid          -- logical demand-type identifier (model + capability tuple)
  capacity_id          uuid          -- provider account node
  capability_check     boolean       -- true = account satisfies demand capability requirements
  residual             numeric       -- C_remaining at last refresh
  tenant_id            uuid          -- tenant filter key (SaaS Edition; NULL = single-tenant Personal)
  refreshed_at         timestamptz
)
```

### `account_fault_domain_labels`

One row per provider account. Used by A20 spillover guard to compute domain distance.

```
account_fault_domain_labels (
  account_id      uuid   PK FK → provider_accounts
  vendor          text   -- e.g. "vendor-a", "vendor-b" (no real names in spec)
  region          text   -- cloud region label
  project_id      text   -- vendor project / organisation label
  account_group   text   -- operator-assigned grouping label (e.g. "prod-tier-1")
)
```

## Audit / Usage / Log Evidence

Every scheduling decision that invokes A19 or A20 writes a structured annotation into the routing reason payload (per F-POOL-001 `routing_reason` schema, extended here):

```
routing_reason.capacity_graph {
  algorithm_version     semver        -- version of A17/A18/A19/A20 rule set in effect
  selected_account_id   uuid
  residual_after        numeric       -- C_remaining − demand_size after selection
  spillover_triggered   boolean
  spillover_reason      enum          -- null | low_water | fault_event
  spillover_distance    numeric       -- normalised Hamming distance [0,1] (null if no spillover)
  intra_domain_forced   boolean       -- true if no cross-domain candidate was available
  tenant_filter_applied boolean       -- always true in SaaS Edition
  edge_view_stale       boolean
  forecast_eta_minutes  numeric       -- eta_minutes at decision time (null = cold start)
}
```

**Forbidden contents**: raw provider credentials, prompt body content, raw response bodies, plaintext tenant identifiers in non-masked fields.

Forecast rows in `capacity_forecasts` are retained per the standard operational data retention policy. Restock recommendations are retained for operator audit for a minimum of 30 days.

## Signals

| Signal name | Type | Description |
|---|---|---|
| `capacity_eta_minutes` | gauge | Estimated minutes to depletion per account; emitted when below alert threshold |
| `forecast_mape` | gauge | Mean absolute percentage error of rolling forecast vs. actual consumption (retrospective, computed over prior 24 h window) |
| `capacity_residual_min_by_domain` | gauge | Minimum residual fraction across accounts per fault-domain label group |
| `fallback_domain_distance_histogram` | histogram | Distribution of normalised domain distances at which spillover was triggered |
| `capacity_graph_edge_view_stale_seconds` | gauge | Age of `capacity_graph_edges` materialized view at query time |
| `spillover_intra_domain_forced` | counter | Number of spillover decisions where no cross-domain candidate was available |
| `restock_recommendation_generated` | counter | Number of restock recommendations generated per pool per tick |

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md).

| AT ID | Scenario | Pass Criterion |
|---|---|---|
| AT-CAPGRAPH-001 | Forecast accuracy: inject synthetic consumption trace with known seasonal pattern; run A17 for 10 ticks | `forecast_mape` ≤ 15% over the synthetic window |
| AT-CAPGRAPH-002 | Cold-start guard: account with no prior consumption; call forecast | `eta_minutes IS NULL`; no alert emitted; no division error |
| AT-CAPGRAPH-003 | EWMA decay: spike consumption then drop; verify EWMA tracks decay | Smoothed rate converges toward new lower rate within 5 ticks at `α = 0.2` |
| AT-CAPGRAPH-004 | Knapsack optimality: 5-option bounded knapsack with known optimal solution | Recommendation matches analytic optimum; `approximate = false` |
| AT-CAPGRAPH-005 | Knapsack infeasible: all options exceed `budget_cap` | `items = []`; `restock_infeasible = true`; no panic |
| AT-CAPGRAPH-006 | Min-residual selection: three accounts with different residuals; single demand unit | Scheduler selects account with highest `residual_after`; others not selected |
| AT-CAPGRAPH-007 | SaaS tenant isolation — cross-tenant attempt: two tenants A and B each with separate accounts; demand from tenant A must never be assigned to tenant B accounts | After mandatory tenant filter, zero tenant-B accounts appear in tenant-A graph traversal; verified via graph edge view contents |
| AT-CAPGRAPH-008 | SaaS tenant isolation — no cross-tenant spillover: tenant A's accounts at low-water; tenant B's accounts have high residual | Spillover for tenant A only considers tenant A accounts; tenant B accounts never appear as spillover candidates |
| AT-CAPGRAPH-009 | Fault-domain spillover: account U (vendor-a, region-1) at low-water; candidates V1 (vendor-a, region-1) and V2 (vendor-b, region-2) available | V2 selected (higher domain distance = 0.5); `spillover_triggered = true`; `spillover_distance ≈ 0.5` |
| AT-CAPGRAPH-010 | Spillover intra-domain forced: all candidates in same fault domain as triggering account | `intra_domain_forced = true`; best-residual intra-domain candidate selected; `spillover_intra_domain_forced` counter increments |

## Effort Estimate

| Algorithm | A-ID | Effort |
|---|---|---|
| Capacity Depletion Forecast (EWMA + seasonal P95 + ETA) | A17 | 12 h |
| Restock Recommendation (bounded knapsack) | A18 | 10 h |
| Cross-Vendor Min-Residual Graph + SaaS tenant isolation | A19 | 16 h |
| Fault-Domain Spillover Guard (weighted score + domain distance) | A20 | 8 h |
| **Total** | | **46 h** |

A19 carries the highest effort because it includes the mandatory SaaS tenant isolation structural enforcement (DR-009 Q6), materialized view management, and integration with the A09 two-phase quota Tx1 reserve phase.

## Open Questions

1. **Materialized view refresh mechanism**: should `capacity_graph_edges` use PostgreSQL native `REFRESH MATERIALIZED VIEW CONCURRENTLY` (zero read-lock downtime) or a custom incremental cache layer? Implementer lane should flag if concurrent refresh is unavailable on target Postgres version.
2. **EWMA decay parameter `α` per-pool vs. per-account**: synthesis recommends per-pool default with per-account override; implementer should verify whether per-account config storage is in scope for the first slice or deferred.
3. **Knapsack DP budget granularity `W`**: the number of budget steps determines memory usage; implementer should pick a sensible default (e.g., W = 1000 quantized steps) and expose as operator config.
4. **Fault-domain label completeness**: if an account has no row in `account_fault_domain_labels`, should it be treated as a singleton domain (never shares a domain with any other account) or excluded from spillover entirely? Recommended: treat as singleton (safest for isolation), implementer to confirm.

## Related Decisions

- [DR-009](../process/decisions/DR-009-algorithm-upgrade-policy.md) — Owner decisions Q6 (SaaS capacity graph scope, tenant isolation mandate) and §6.6 (A19 hard floor)
- [F-POOL-001 pool-routing.md](pool-routing.md) — Phase C atomic admission gate (authoritative revalidation integrates with A19 Tx1 reserve)
- A09 two-phase quota spec (observability-billing.md extension, DR-009 Phase B) — Tx1/Tx2 reservation integrates with A19 atomic decrement

## Implementer Notes (added by implementer lane)

> This section is filled by the implementer after consuming the spec, NOT by the specifier. Notes here record local design choices, dependencies, and deviations.

(empty until implementer-lane work begins)
