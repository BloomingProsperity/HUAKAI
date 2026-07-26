package signupreward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

type Kind string

const (
	KindSignupBonus   Kind = "signup_bonus"
	KindInviteeReward Kind = "invitee_reward"
)

type Payload struct {
	TenantID    int64 `json:"tenant_id"`
	UserID      int64 `json:"user_id"`
	Kind        Kind  `json:"reward_kind"`
	AmountCents int64 `json:"amount_cents"`
}

func ValidKind(kind Kind) bool {
	return kind == KindSignupBonus || kind == KindInviteeReward
}

func NewEvent(tenantID, userID int64, kind Kind, amountCents int64) (obsdlq.OutboxEvent, error) {
	if tenantID <= 0 || userID <= 0 || amountCents <= 0 || !ValidKind(kind) {
		return obsdlq.OutboxEvent{}, errors.New("signupreward: invalid event input")
	}
	payload, err := json.Marshal(Payload{
		TenantID:    tenantID,
		UserID:      userID,
		Kind:        kind,
		AmountCents: amountCents,
	})
	if err != nil {
		return obsdlq.OutboxEvent{}, err
	}
	return obsdlq.OutboxEvent{
		ID:        fmt.Sprintf("signup-reward-%d-%d-%s", tenantID, userID, kind),
		TenantID:  tenantID,
		EventType: obsdlq.EventTypeSignupReward,
		Priority:  obsdlq.PriorityHigh,
		Payload:   payload,
	}, nil
}

func ParseEvent(event obsdlq.OutboxEvent) (Payload, error) {
	var payload Payload
	if event.EventType != obsdlq.EventTypeSignupReward {
		return Payload{}, errors.New("signupreward: unexpected event type")
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return Payload{}, fmt.Errorf("signupreward: decode event: %w", err)
	}
	if payload.TenantID <= 0 ||
		payload.UserID <= 0 ||
		payload.AmountCents <= 0 ||
		payload.TenantID != event.TenantID ||
		!ValidKind(payload.Kind) {
		return Payload{}, errors.New("signupreward: invalid event payload")
	}
	expected, err := NewEvent(payload.TenantID, payload.UserID, payload.Kind, payload.AmountCents)
	if err != nil || expected.ID != event.ID {
		return Payload{}, errors.New("signupreward: event identity mismatch")
	}
	return payload, nil
}

func Ensure(ctx context.Context, database db.DBTX, tenantID, userID int64, kind Kind, amountCents int64) error {
	event, err := NewEvent(tenantID, userID, kind, amountCents)
	if err != nil {
		return err
	}
	_, err = obsdlq.EnqueueWithDB(ctx, database, event)
	return err
}
