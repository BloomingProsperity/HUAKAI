package announcement

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceValidationRejectsInvalidAnnouncementInput(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))

	tests := []struct {
		name string
		in   CreateInput
	}{
		{
			name: "empty title",
			in:   CreateInput{TenantID: 7, Title: " ", Body: "body", Severity: SeverityInfo},
		},
		{
			name: "empty body",
			in:   CreateInput{TenantID: 7, Title: "title", Body: "\t", Severity: SeverityInfo},
		},
		{
			name: "bad severity",
			in:   CreateInput{TenantID: 7, Title: "title", Body: "body", Severity: Severity("emergency")},
		},
		{
			name: "expires not after published",
			in: CreateInput{
				TenantID:    7,
				Title:       "title",
				Body:        "body",
				Severity:    SeverityInfo,
				PublishedAt: ptrTime(now),
				ExpiresAt:   ptrTime(now),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 变异: 移除 title/body/severity/expiry 的校验，此 Create 就会成功。
			if _, err := svc.Create(context.Background(), tt.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create err=%v want ErrInvalidInput", err)
			}
		})
	}
}

func TestServiceDefaultsCreateFields(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))

	created, err := svc.Create(context.Background(), CreateInput{
		TenantID: 7,
		Title:    "Upgrade window",
		Body:     "Short maintenance window.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Severity != SeverityInfo || !created.Active || !created.PublishedAt.Equal(now) {
		t.Fatalf("defaults severity/active/published=%q/%v/%s want info/true/%s",
			created.Severity, created.Active, created.PublishedAt, now)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
