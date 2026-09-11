package billing

import (
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestAppendSnapshotMarkerKeepsExistingAndJoinsNew(t *testing.T) {
	if got := AppendSnapshotMarker("", "mark=a"); got != "mark=a" {
		t.Fatalf("empty snapshot=%q", got)
	}
	if got := AppendSnapshotMarker("flat", "mark=a"); got != "flat;mark=a" {
		t.Fatalf("join snapshot=%q", got)
	}
	if got := AppendSnapshotMarker("flat;mark=a", "mark=a"); got != "flat;mark=a" {
		t.Fatalf("duplicate snapshot=%q", got)
	}
	if got := AppendSnapshotMarker("flat", ""); got != "flat" {
		t.Fatalf("empty marker mutated snapshot=%q", got)
	}
}

func TestMarkClientDeliveryInterruptedSetsPendingWithoutChangingNil(t *testing.T) {
	MarkClientDeliveryInterrupted(nil)

	draft := gateway.UsageRecordDraft{CostSnapshot: "flat", PendingReconciliation: false}
	MarkClientDeliveryInterrupted(&draft)
	if !draft.PendingReconciliation {
		t.Fatal("PendingReconciliation=false want true")
	}
	if !strings.Contains(draft.CostSnapshot, ClientDeliveryInterruptedMarker) {
		t.Fatalf("snapshot=%q missing delivery marker", draft.CostSnapshot)
	}
	before := draft.CostSnapshot
	MarkClientDeliveryInterrupted(&draft)
	if draft.CostSnapshot != before {
		t.Fatalf("second mark changed snapshot %q -> %q", before, draft.CostSnapshot)
	}
}
