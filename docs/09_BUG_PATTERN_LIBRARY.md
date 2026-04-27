This file is agent-facing and authoritative.

# Bug Pattern Library

Bug patterns must improve parity and reliability without shrinking feature scope.

## Purpose

This library captures recurring failure modes in AI gateway, account hub, quota, billing, and admin operations systems.

## Patterns

| Pattern ID | Area | Failure Mode | Prevention | Test Direction |
| --- | --- | --- | --- | --- |
| BUG-GW-001 | Gateway | Retry causes duplicate billing or duplicate side effects. | Separate retry accounting from final charge policy. | Simulate timeout followed by successful retry. |
| BUG-GW-002 | Gateway | Streaming response fails after partial tokens. | Track partial usage and final state separately. | Interrupt stream and verify logs. |
| BUG-ACCT-001 | Account | Disabled provider account remains routable. | Route eligibility must check current account state. | Disable account and send request. |
| BUG-QUOTA-001 | Quota | Concurrent requests overspend quota. | Use atomic reservation or equivalent control. | Run concurrent quota exhaustion scenario. |
| BUG-BILL-001 | Billing | Provider cost and user charge drift. | Store normalized usage and pricing context. | Reprice historical request safely. |
| BUG-SEC-001 | Security | Secret appears in logs or UI. | Redact at capture and render boundaries. | Inject fake secrets and inspect outputs. |
| BUG-OPS-001 | Admin UI | Bulk action has no audit trail. | Require audit event for privileged changes. | Execute bulk disable and verify audit log. |

## Rule

When a new production bug is found, add a generalized pattern and a test direction. Do not only fix the local symptom.
