package notify

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type Type string

const (
	TypeNone    Type = "none"
	TypeEmail   Type = "email"
	TypeWebhook Type = "webhook"
	TypeBark    Type = "bark"
	TypeGotify  Type = "gotify"

	EventLowBalance = "low_balance"
)

var (
	DefaultLowBalanceThreshold = decimal.RequireFromString("5.00000000")
	ErrInvalidSettings         = errors.New("notify: invalid settings")
	ErrUnsafeEndpoint          = errors.New("notify: unsafe endpoint")
	ErrHeaderInjection         = errors.New("notify: header injection")
	ErrDeliveryFailed          = errors.New("notify: delivery failed")
	ErrStoreUnavailable        = errors.New("notify: store unavailable")
)

type Settings struct {
	TenantID          int64
	UserID            int64
	NotifyType        Type
	WebhookURL        string
	WebhookSecret     string
	NotificationEmail string
	BarkURL           string
	GotifyURL         string
	GotifyToken       string
	GotifyPriority    int
	BalanceThreshold  decimal.Decimal
	UpdatedAt         time.Time
	UpdatedBy         string
}

type Event struct {
	EventType      string          `json:"event_type"`
	TenantID       int64           `json:"tenant_id"`
	UserID         int64           `json:"user_id"`
	Balance        decimal.Decimal `json:"balance"`
	Threshold      decimal.Decimal `json:"threshold"`
	BillingEventID int64           `json:"billing_event_id,omitempty"`
	OccurredAt     time.Time       `json:"occurred_at"`
}

type Store interface {
	GetSettings(ctx context.Context, tenantID, userID int64) (Settings, error)
	UpsertSettings(ctx context.Context, settings Settings) (Settings, error)
}

func DefaultSettings(tenantID, userID int64) Settings {
	return Settings{
		TenantID:         tenantID,
		UserID:           userID,
		NotifyType:       TypeNone,
		GotifyPriority:   5,
		BalanceThreshold: DefaultLowBalanceThreshold,
		UpdatedBy:        "system",
	}
}

func (s Settings) normalized() Settings {
	s.NotifyType = Type(strings.TrimSpace(strings.ToLower(string(s.NotifyType))))
	if s.NotifyType == "" {
		s.NotifyType = TypeNone
	}
	s.WebhookURL = strings.TrimSpace(s.WebhookURL)
	s.WebhookSecret = strings.TrimSpace(s.WebhookSecret)
	s.NotificationEmail = strings.TrimSpace(s.NotificationEmail)
	s.BarkURL = strings.TrimSpace(s.BarkURL)
	s.GotifyURL = strings.TrimSpace(s.GotifyURL)
	s.GotifyToken = strings.TrimSpace(s.GotifyToken)
	s.UpdatedBy = strings.TrimSpace(s.UpdatedBy)
	if s.UpdatedBy == "" {
		s.UpdatedBy = "system"
	}
	if s.BalanceThreshold.IsZero() {
		s.BalanceThreshold = DefaultLowBalanceThreshold
	}
	if s.GotifyPriority == 0 {
		s.GotifyPriority = 5
	}
	return s
}

func ValidateSettings(settings Settings) (Settings, error) {
	s := settings.normalized()
	if s.TenantID <= 0 {
		return s, fmt.Errorf("%w: tenant_id", ErrInvalidSettings)
	}
	if s.UserID <= 0 {
		return s, fmt.Errorf("%w: user_id", ErrInvalidSettings)
	}
	if s.BalanceThreshold.IsNegative() {
		return s, fmt.Errorf("%w: balance_threshold", ErrInvalidSettings)
	}
	switch s.NotifyType {
	case TypeNone:
		return s, nil
	case TypeEmail:
		if err := rejectHeaderInjection(s.NotificationEmail); err != nil {
			return s, err
		}
		if _, err := mail.ParseAddress(s.NotificationEmail); err != nil {
			return s, fmt.Errorf("%w: notification_email", ErrInvalidSettings)
		}
	case TypeWebhook:
		if s.WebhookSecret == "" {
			return s, fmt.Errorf("%w: webhook_secret", ErrInvalidSettings)
		}
		if err := validateOutboundURL("webhook_url", s.WebhookURL); err != nil {
			return s, err
		}
	case TypeBark:
		if err := validateOutboundURL("bark_url", s.BarkURL); err != nil {
			return s, err
		}
	case TypeGotify:
		if s.GotifyToken == "" {
			return s, fmt.Errorf("%w: gotify_token", ErrInvalidSettings)
		}
		if s.GotifyPriority < 1 || s.GotifyPriority > 10 {
			return s, fmt.Errorf("%w: gotify_priority", ErrInvalidSettings)
		}
		if err := rejectHeaderInjection(s.GotifyToken); err != nil {
			return s, err
		}
		if err := validateOutboundURL("gotify_url", s.GotifyURL); err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("%w: notify_type", ErrInvalidSettings)
	}
	return s, nil
}

func rejectHeaderInjection(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: CRLF", ErrHeaderInjection)
	}
	return nil
}
