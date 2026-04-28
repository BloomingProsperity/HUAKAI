This file is agent-facing and authoritative.

# Release Gates

## Purpose

Release gates prevent shipping an incomplete, unsafe, or license-contaminated platform.

Release gates do not authorize over-blocking. After the Owner starts a phase or task, agents should proceed proactively on low-risk and medium-risk work while preserving release checks for high-risk decisions.

## Required Gates

| Gate | Requirement | Owner |
| --- | --- | --- |
| Parity Gate | Every reference feature has a valid disposition. | Codex |
| Clean-Room Gate | No copied non-MIT implementation detail is present. | Codex |
| Scenario Gate | Material capabilities have real-world scenarios. | Claude |
| Acceptance Gate | Acceptance tests cover normal, failure, and operator recovery paths. | Codex |
| Deep Mining Gate | Every L1 MVP feature in [03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) cites at least one `E-X-DEEP-NNN` source-code-verified evidence row per [22_DEEP_MINING_MANDATE.md](22_DEEP_MINING_MANDATE.md); multi-source rows cover each cited reference. Required at Phase 1 → Phase 2 transition. | Codex |
| Reference Tracking Continuous Gate | Per [24_REFERENCE_TRACKING_POLICY.md](24_REFERENCE_TRACKING_POLICY.md), the tracking ledger under `docs/tracking/` is current within its cadence windows (per-release within 7 days; monthly sweep last business day; quarterly strategic at quarter end). Every HUAKAI release requires the tracking ledger to be current. **Continuous, never closes.** | Claude PM |
| Security Gate | Secrets, permissions, audit logs, and abuse controls are reviewed. | Claude |
| Billing Gate | Usage, quota, and billing behavior is testable and reconciled. | Codex |
| UI Ops Gate | Admin workflows are complete and operable. | Gemini |
| Release Decision Gate | Open mandatory roadmap items are explicitly accepted or blocked. | Claude |

## Release Rule

No release may claim full parity while any reference feature is unmapped, silently dropped, or hidden behind an undocumented gap.

## Owner Start Gate And Release Work

Agents must not begin implementation work until the Owner explicitly confirms the phase or task may start. Valid start signals include "Start Phase 1", "Start this task", "Begin implementation", "Proceed", "开始", "确认开始", "可以开始写", and "开始执行".

After a valid start signal, agents should not ask for repeated confirmation for every small step. They should make reasonable engineering decisions, record assumptions and risks, update required docs, run available checks when possible, and produce a final Chinese summary for the Owner.

## Proactive Execution Rule

After Owner confirmation, agents should read relevant rules, understand the assigned goal, execute to completion when safe, make reasonable engineering decisions, record assumptions, record risks, update required docs, run available checks when possible, and produce a final Chinese summary for the Owner.

## Risk-Based Confirmation Rule

Low-risk docs, tests, prompts, type fixes, UI copy, small refactors, and non-sensitive config examples may proceed after Owner start.

Medium-risk small implementation changes, helper utilities, UI structure changes, non-breaking API contract updates, mock data, and experimental logic may proceed when needed with recorded reason and risk.

High-risk changes must stop for Owner confirmation before action. High-risk changes include deleting files, changing `LICENSE`, changing database schema, changing auth core, changing billing ledger, changing quota enforcement, adding new runtime dependency, touching real secrets, destructive shell commands, production deployment, payment logic, production secrets, real credentials, deployment scripts, and destructive migration files.

## Feature Preservation Rule

License risk and security risk must not reduce functionality. If a feature is risky, convert it to `Safe Equivalent`, `Plugin`, `Feature Flag`, `Manual First`, `Experimental Module`, or `Mandatory Roadmap`. Do not remove the feature.
