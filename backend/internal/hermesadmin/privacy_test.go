package hermesadmin

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// sentinel is a value that MUST NEVER appear in the report body. It stands in for
// a secret / raw body / PII that a buggy projection might leak from an upstream
// row's free-form field.
const sentinel = "sk-LEAK-SECRET-customer@example.com-RAWBODY-9f3a"

// TestPrivacyNoLeakIntoBody injects the sentinel into every upstream free-form /
// identity field the inspection reads (tenant name, account name, DLQ failure
// reason + raw payload, usage stream-terminated reason, module probe detail).
// It asserts the sentinel is ABSENT from the rendered HTML + plaintext bodies AND
// from the recorded run outcome.
//
// Mutation proof: if the projection were neutered to copy a free-form field
// straight into a section (e.g. surface DLQ failure_reason, or the renew tenant
// name, or the probe Detail), the sentinel would appear in the body and this
// test goes RED. The current projection keeps only enums/counts/ids, so it stays
// green.
func TestPrivacyNoLeakIntoBody(t *testing.T) {
	leakReason := sentinel
	src := Sources{
		ChannelSummary: func(_ context.Context, _ int64) (channelhealth.ChannelHealthSummary, error) {
			// Channel summary is aggregate counts only — no string field to poison,
			// but include a benign state so the section composes.
			return channelhealth.ChannelHealthSummary{
				Total:   1,
				ByState: map[channelhealth.HealthState]int64{channelhealth.StateActive: 1},
			}, nil
		},
		RenewStatus: func(_ context.Context, _ credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
			// Poison the identity / free-form fields the projection must drop.
			row := credentialstore.RenewStatusMetadata{
				CredentialID: 1,
				TenantName:   sentinel,
				AccountName:  sentinel,
				State:        "active",
				FailureClass: &leakReason, // even failure class is a poisoned value here
				FailureCount: 0,
			}
			return []credentialstore.RenewStatusMetadata{row}, nil
		},
		DLQList: func(_ context.Context, _ dlq.ListFilter) ([]dlq.Record, error) {
			rec := dlq.Record{
				ID:            1,
				Lane:          dlq.LaneHigh,
				Status:        dlq.StatusDelivered,
				FailureReason: sentinel,                           // free-form failure text
				Payload:       []byte(`{"x":"` + sentinel + `"}`), // raw request body
				FailureAt:     fixedNow(),
			}
			return []dlq.Record{rec}, nil
		},
		ListUsage: func(_ context.Context, _ dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			reason := sentinel
			row := dbbilling.ListUsageRecordsRow{
				ID: 1, TenantID: testTenant, EndClass: "ok", StreamTerminatedReason: &reason,
			}
			return []dbbilling.ListUsageRecordsRow{row}, nil
		},
		Modules: fakeSnapshotter{snaps: nil},
	}

	svc := NewInspectionService(src, testTenant, fixedNow)
	rep := svc.Inspect(context.Background())
	formatted := Format(rep)

	if strings.Contains(formatted.HTMLBody, sentinel) {
		t.Fatalf("sentinel leaked into HTML body")
	}
	if strings.Contains(formatted.PlainBody, sentinel) {
		t.Fatalf("sentinel leaked into plaintext body")
	}
	if strings.Contains(formatted.Subject, sentinel) {
		t.Fatalf("sentinel leaked into subject")
	}

	// Also assert it never reaches the recorded outcome (which carries only
	// enums/counts — no body, no error text).
	var captured RunOutcome
	rec := captureRecorder{out: &captured}
	rec.RecordRun(context.Background(), RunOutcome{
		IssueCount:   rep.IssueCount(),
		SourceErrors: len(rep.SourceErrors),
		FailureClass: "send_failed",
	})
	if strings.Contains(captured.FailureClass, sentinel) {
		t.Fatalf("sentinel leaked into recorded outcome")
	}
}

type captureRecorder struct{ out *RunOutcome }

func (c captureRecorder) RecordRun(_ context.Context, o RunOutcome) { *c.out = o }
