package email

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	SettingMailHost          = "smtp_host"
	SettingMailPort          = "smtp_port"
	SettingMailUsername      = "smtp_username"
	SettingMailPassword      = "smtp_password"
	SettingMailFrom          = "smtp_from"
	SettingMailFromName      = "smtp_from_name"
	SettingMailTLS           = "smtp_use_tls"
	SettingVerifyRequirement = "email_verify_enabled"

	DefaultSMTPPort                  = 587
	DefaultVerificationTTL           = 15 * time.Minute
	DefaultVerificationEmailCooldown = time.Minute
	DefaultPasswordResetCooldown     = 30 * time.Second
)

var (
	ErrEmailBackendUnconfigured = errors.New("email: backend unconfigured")
	ErrEmailSettingsInvalid     = errors.New("email: settings invalid")
)

var RequiredProductionSettings = []string{
	SettingMailHost,
	SettingMailPort,
	SettingMailUsername,
	SettingMailPassword,
	SettingMailFrom,
}

type SMTPSettings struct {
	TenantID           int64
	Host               string
	Port               int
	PortConfigured     bool
	Username           string
	Password           string
	From               string
	FromName           string
	UseTLS             bool
	VerifyEmailEnabled bool
}

type Message struct {
	TenantID int64
	To       string
	Subject  string
	HTMLBody string
}

type EmailSender interface {
	Send(context.Context, Message) error
}

func SettingsFromStored(ctx context.Context, raw StoredSettings, keys SecretKeyProvider, tenantID int64) (SMTPSettings, error) {
	settings := SMTPSettings{
		TenantID: tenantID,
		Port:     DefaultSMTPPort,
	}
	if raw == nil {
		return settings, ErrEmailBackendUnconfigured
	}
	settings.Host = strings.TrimSpace(raw[SettingMailHost])
	settings.Username = strings.TrimSpace(raw[SettingMailUsername])
	settings.From = strings.TrimSpace(raw[SettingMailFrom])
	settings.FromName = strings.TrimSpace(raw[SettingMailFromName])
	settings.UseTLS = parseBool(raw[SettingMailTLS])
	settings.VerifyEmailEnabled = parseBool(raw[SettingVerifyRequirement])
	if portRaw := strings.TrimSpace(raw[SettingMailPort]); portRaw != "" {
		port, err := strconv.Atoi(portRaw)
		if err != nil || port <= 0 || port > 65535 {
			return settings, fmt.Errorf("%w: smtp port", ErrEmailSettingsInvalid)
		}
		settings.Port = port
		settings.PortConfigured = true
	}
	if secret := strings.TrimSpace(raw[SettingMailPassword]); secret != "" {
		plain, err := DecodeSecret(ctx, keys, tenantID, secret)
		if err != nil {
			return settings, err
		}
		settings.Password = plain
	}
	if len(settings.MissingRequired()) > 0 {
		return settings, ErrEmailBackendUnconfigured
	}
	return settings, nil
}

func (s SMTPSettings) MissingRequired() []string {
	var missing []string
	if strings.TrimSpace(s.Host) == "" {
		missing = append(missing, SettingMailHost)
	}
	if !s.PortConfigured || s.Port <= 0 || s.Port > 65535 {
		missing = append(missing, SettingMailPort)
	}
	if strings.TrimSpace(s.Username) == "" {
		missing = append(missing, SettingMailUsername)
	}
	if strings.TrimSpace(s.Password) == "" {
		missing = append(missing, SettingMailPassword)
	}
	if strings.TrimSpace(s.From) == "" {
		missing = append(missing, SettingMailFrom)
	}
	return missing
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
