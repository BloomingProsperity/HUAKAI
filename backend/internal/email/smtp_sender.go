package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

const (
	defaultSMTPDialTimeout = 10 * time.Second
	defaultSMTPIOTimeout   = 20 * time.Second
)

type SMTPSender struct {
	Settings    SMTPSettings
	DialTimeout time.Duration
	IOTimeout   time.Duration
}

func NewSMTPSender(settings SMTPSettings) *SMTPSender {
	return &SMTPSender{Settings: settings}
}

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if s == nil {
		return ErrEmailBackendUnconfigured
	}
	settings := s.Settings
	if len(settings.MissingRequired()) > 0 {
		return ErrEmailBackendUnconfigured
	}
	payload, from, to := buildSMTPPayload(settings, msg)
	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	auth := smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)
	dialTimeout := s.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultSMTPDialTimeout
	}
	ioTimeout := s.IOTimeout
	if ioTimeout <= 0 {
		ioTimeout = defaultSMTPIOTimeout
	}
	if settings.UseTLS {
		return sendSMTPOverTLS(ctx, addr, settings.Host, auth, from, to, payload, dialTimeout, ioTimeout)
	}
	return sendSMTPPlain(ctx, addr, settings.Host, auth, from, to, payload, dialTimeout, ioTimeout)
}

func buildSMTPPayload(settings SMTPSettings, msg Message) ([]byte, string, string) {
	fromAddress := SanitizeHeaderValue(settings.From)
	fromHeader := fromAddress
	if name := SanitizeHeaderValue(settings.FromName); name != "" {
		fromHeader = (&mail.Address{Name: name, Address: fromAddress}).String()
	}
	to := SanitizeHeaderValue(msg.To)
	subject := SanitizeHeaderValue(msg.Subject)
	body := msg.HTMLBody
	headers := []string{
		"From: " + fromHeader,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body), fromAddress, to
}

func SanitizeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return strings.TrimSpace(value)
}

func sendSMTPPlain(ctx context.Context, addr, host string, auth smtp.Auth, from, to string, payload []byte, dialTimeout, ioTimeout time.Duration) error {
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	defer func() { _ = conn.Close() }()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	return finishSMTP(client, auth, from, to, payload)
}

func sendSMTPOverTLS(ctx context.Context, addr, host string, auth smtp.Auth, from, to string, payload []byte, dialTimeout, ioTimeout time.Duration) error {
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	defer func() { _ = conn.Close() }()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()
	return finishSMTP(client, auth, from, to, payload)
}

func finishSMTP(client *smtp.Client, auth smtp.Auth, from, to string, payload []byte) error {
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	_ = client.Quit()
	return nil
}
