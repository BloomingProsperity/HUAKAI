package credentialacq

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSessionStoreCancelFinalizeRaceGuards keeps the in-package fake aligned with the real SQL
// contract. The SQL-discriminating proof is TestCancelFinalizeRaceGuardsPG; this local test gives a
// no-Postgres red/green signal for the same behavior.
func TestSessionStoreCancelFinalizeRaceGuards(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now })
	ctx := context.Background()
	mk := func(id string) string {
		if _, err := store.Create(ctx, Session{
			ID: id, TenantID: 1, ProviderAccountID: 2, Vendor: "openai", AuthMode: "api_key",
			Kind: FlowKindPaste, Status: StatusStarted, ActorID: "admin-1", ActorRole: "platform_admin",
			ClientIdentitySource: ClientSourceNone,
			RequestedScopes:      []string{},
			RedactedContext:      map[string]any{"id": id},
			ExpiresAt:            now.Add(10 * time.Minute),
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		return id
	}

	consumedID := mk("race-consumed")
	if _, err := store.BeginFinalize(ctx, consumedID); err != nil {
		t.Fatalf("BeginFinalize consumed: %v", err)
	}
	if _, err := store.Cancel(ctx, consumedID); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("Cancel after BeginFinalize: err=%v want ErrFlowReplay", err)
	}
	finalized, err := store.MarkFinalized(ctx, consumedID, 8101)
	if err != nil {
		t.Fatalf("MarkFinalized after rejected Cancel: %v", err)
	}
	if finalized.Status != StatusFinalized || finalized.ResultAccountCredentialID != 8101 {
		t.Fatalf("finalized consumed flow=(status=%q credential=%d), want finalized/8101", finalized.Status, finalized.ResultAccountCredentialID)
	}

	cancelledID := mk("race-cancelled")
	if _, err := store.Cancel(ctx, cancelledID); err != nil {
		t.Fatalf("Cancel before finalize: %v", err)
	}
	if _, err := store.MarkFinalized(ctx, cancelledID, 8102); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("MarkFinalized on cancelled flow: err=%v want ErrFlowReplay", err)
	}
	reloaded, err := store.Get(ctx, cancelledID)
	if err != nil {
		t.Fatalf("Get cancelled flow: %v", err)
	}
	if reloaded.Status != StatusCancelled || reloaded.ResultAccountCredentialID != 0 {
		t.Fatalf("cancelled flow after MarkFinalized=(status=%q credential=%d), want cancelled/0", reloaded.Status, reloaded.ResultAccountCredentialID)
	}

	normalID := mk("race-normal")
	if _, err := store.BeginFinalize(ctx, normalID); err != nil {
		t.Fatalf("BeginFinalize normal: %v", err)
	}
	normal, err := store.MarkFinalized(ctx, normalID, 8103)
	if err != nil {
		t.Fatalf("MarkFinalized normal: %v", err)
	}
	if normal.Status != StatusFinalized || normal.ResultAccountCredentialID != 8103 {
		t.Fatalf("normal finalized=(status=%q credential=%d), want finalized/8103", normal.Status, normal.ResultAccountCredentialID)
	}
	retry, err := store.MarkFinalized(ctx, normalID, 8103)
	if err != nil {
		t.Fatalf("MarkFinalized idempotent retry: %v", err)
	}
	if retry.Status != StatusFinalized || retry.ResultAccountCredentialID != 8103 {
		t.Fatalf("retry finalized=(status=%q credential=%d), want finalized/8103", retry.Status, retry.ResultAccountCredentialID)
	}
}
