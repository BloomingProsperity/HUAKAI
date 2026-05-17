package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

type PermanentFailure struct {
	Err error
}

func (e PermanentFailure) Error() string {
	if e.Err == nil {
		return "email: permanent failure"
	}
	return e.Err.Error()
}

func (e PermanentFailure) Unwrap() error {
	return e.Err
}

func Permanent(err error) error {
	if err == nil {
		err = errors.New("email: permanent failure")
	}
	return PermanentFailure{Err: err}
}

type retryPayload struct {
	To           string `json:"to"`
	Subject      string `json:"subject"`
	BodyEnvelope string `json:"body_envelope"`
	CreatedAt    string `json:"created_at"`
}

func enqueueEmailRetry(ctx context.Context, outbox obsdlq.Outbox, keys SecretKeyProvider, settings SMTPSettings, msg Message, cause error) error {
	if outbox == nil {
		return obsdlq.ErrOutboxNotConfigured
	}
	bodyEnvelope, err := EncodeSecret(ctx, keys, settings.TenantID, msg.HTMLBody)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(retryPayload{
		To:           SanitizeHeaderValue(msg.To),
		Subject:      SanitizeHeaderValue(msg.Subject),
		BodyEnvelope: bodyEnvelope,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	_, err = outbox.Enqueue(ctx, obsdlq.OutboxEvent{
		TenantID:      settings.TenantID,
		EventType:     obsdlq.EventTypeEmailRetry,
		Priority:      obsdlq.PriorityCritical,
		Payload:       payload,
		FailureReason: cause.Error(),
	})
	return err
}

func NewDLQHandler(store SettingsStore, keys SecretKeyProvider, dispatch SMTPDispatch) obsdlq.Handler {
	if dispatch == nil {
		dispatch = defaultSMTPDispatch
	}
	return func(ctx context.Context, ev obsdlq.OutboxEvent) error {
		if store == nil || keys == nil {
			return ErrEmailBackendUnconfigured
		}
		var payload retryPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return fmt.Errorf("email: decode retry payload: %w", err)
		}
		body, err := DecodeSecret(ctx, keys, ev.TenantID, payload.BodyEnvelope)
		if err != nil {
			return fmt.Errorf("email: decrypt retry body: %w", err)
		}
		settings, err := LoadSMTPSettings(ctx, store, keys, ev.TenantID)
		if err != nil {
			return err
		}
		msg := Message{TenantID: ev.TenantID, To: payload.To, Subject: payload.Subject, HTMLBody: body}
		if err := validateMessage(settings, msg); err != nil {
			return err
		}
		return dispatch(ctx, settings, msg)
	}
}

func validateMessage(settings SMTPSettings, msg Message) error {
	if _, err := mail.ParseAddress(SanitizeHeaderValue(settings.From)); err != nil {
		return fmt.Errorf("%w: smtp from", ErrEmailSettingsInvalid)
	}
	if _, err := mail.ParseAddress(SanitizeHeaderValue(msg.To)); err != nil {
		return fmt.Errorf("%w: recipient", ErrEmailSettingsInvalid)
	}
	return nil
}

func isPermanentEmailFailure(err error) bool {
	if err == nil {
		return false
	}
	var permanent PermanentFailure
	if errors.As(err, &permanent) {
		return true
	}
	if errors.Is(err, ErrEmailSettingsInvalid) || errors.Is(err, ErrEmailBackendUnconfigured) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "bad address") || strings.Contains(msg, "invalid address") {
		return true
	}
	if strings.Contains(msg, ": 4") || strings.Contains(msg, " 4") {
		return true
	}
	return false
}
