package hermeshttp

import "testing"

// TestConfirmCacheBindsOperatorToken is the discriminating guard for the H4
// review S1: a mutating-tool confirmation must be bound to the EXACT operator
// admin token that issued the dry-run preview, not merely the tenant + tenant-
// user context. Without the TokenID check, operator B (a distinct admin token)
// acting in the same (tenant, as_user_id) context could consume operator A's
// preview and execute a privileged mutation.
//
// Regression caught: deleting `entry.TokenID != tokenID` from confirmCache.consume
// makes the wrong-operator-token consume succeed — this test goes RED.
func TestConfirmCacheBindsOperatorToken(t *testing.T) {
	const (
		tool      = "account_pause"
		tenantID  = int64(7)
		actorUser = int64(42)
		tokenA    = int64(100)
		tokenB    = int64(200)
		target    = int64(555)
	)
	c := newConfirmCache()

	// Operator A (token 100) issues a preview.
	id, err := c.issue(pendingConfirmation{
		ToolName: tool, TenantID: tenantID, ActorID: actorUser, TokenID: tokenA, TargetID: target,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Operator B (token 200) — same tool/tenant/tenant-user — must NOT be able to
	// consume A's correlation_id. (And the attempt still consumes it: single-use.)
	if _, ok := c.consume(id, tool, tenantID, actorUser, tokenB); ok {
		t.Fatal("operator B (different admin token) consumed operator A's confirmation — confirm is not bound to the operator token")
	}

	// The wrong-token attempt is single-use: even A can no longer consume it.
	if _, ok := c.consume(id, tool, tenantID, actorUser, tokenA); ok {
		t.Fatal("correlation_id survived a failed consume — single-use is broken")
	}

	// Sanity: a fresh preview consumed by the SAME operator token succeeds exactly once.
	id2, err := c.issue(pendingConfirmation{
		ToolName: tool, TenantID: tenantID, ActorID: actorUser, TokenID: tokenA, TargetID: target,
	})
	if err != nil {
		t.Fatalf("issue 2: %v", err)
	}
	entry, ok := c.consume(id2, tool, tenantID, actorUser, tokenA)
	if !ok {
		t.Fatal("the issuing operator could not consume its own confirmation")
	}
	if entry.TargetID != target {
		t.Fatalf("consumed entry target=%d want %d", entry.TargetID, target)
	}
	if _, ok := c.consume(id2, tool, tenantID, actorUser, tokenA); ok {
		t.Fatal("correlation_id was reusable after a successful consume — single-use is broken")
	}
}
