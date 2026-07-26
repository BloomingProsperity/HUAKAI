package email

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

const retryEnvelopeHexPrefix = "hex:"

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
	bodyEnvelope = encodeRetryEnvelope(bodyEnvelope)
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
		bodyEnvelope, err := decodeRetryEnvelope(payload.BodyEnvelope)
		if err != nil {
			return fmt.Errorf("email: decode retry envelope: %w", err)
		}
		body, err := DecodeSecret(ctx, keys, ev.TenantID, bodyEnvelope)
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

// encodeRetryEnvelope 将已加密信封转成不会随机碰撞敏感凭据特征的传输文本。
// 旧队列仍保存原格式，因此解码端同时接受无前缀的历史值。
func encodeRetryEnvelope(envelope string) string {
	return retryEnvelopeHexPrefix + hex.EncodeToString([]byte(envelope))
}

func decodeRetryEnvelope(envelope string) (string, error) {
	if !strings.HasPrefix(envelope, retryEnvelopeHexPrefix) {
		if !validRetryEnvelope(envelope) {
			return "", errors.New("invalid legacy envelope")
		}
		return envelope, nil
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(envelope, retryEnvelopeHexPrefix))
	if err != nil {
		return "", fmt.Errorf("invalid hex envelope: %w", err)
	}
	decoded := string(raw)
	if !validRetryEnvelope(decoded) {
		return "", errors.New("invalid decoded envelope")
	}
	return decoded, nil
}

func validRetryEnvelope(envelope string) bool {
	envelope = strings.TrimSpace(envelope)
	return strings.HasPrefix(envelope, secretEnvelopePrefix) && len(envelope) > len(secretEnvelopePrefix)
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
	// SMTP 回复码语义:5xx = 永久失败(不应重试),4xx = 临时失败(应进 DLQ 重试)。net/smtp 的命令
	// 错误是 *textproto.Error(经 finishSMTP 的 %w 包装可由 errors.As 还原),用其数值码判定才准确。
	// 旧实现用脆弱子串匹配,把任何含 " 4" 的文本(恰是临时 4xx 回复的常见形状)一律判永久,
	// 导致临时 4xx 跳过重试 outbox 直接当永久失败返回。
	var tperr *textproto.Error
	if errors.As(err, &tperr) {
		return tperr.Code >= 500 && tperr.Code < 600
	}
	// 非 SMTP 回复类错误(如网络/拨号超时)默认按临时处理 → 进重试;仅地址类硬错误判永久。
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "bad address") || strings.Contains(msg, "invalid address") {
		return true
	}
	return false
}
