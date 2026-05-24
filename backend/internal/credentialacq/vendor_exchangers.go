package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type Exchanger interface {
	StartOAuthFlow(context.Context, *PostgresSessionStore, StartInput, OAuthClientConfig) (OAuthStartResult, error)
	ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error)
}

type StoreAwareExchanger interface {
	ExchangeOAuthCodeWithStore(context.Context, *PostgresSessionStore, Session, string, string) (CredentialCandidate, error)
}

type ExchangerRegistry struct {
	mu         sync.RWMutex
	exchangers map[string]Exchanger
}

func NewExchangerRegistry() *ExchangerRegistry {
	return &ExchangerRegistry{exchangers: map[string]Exchanger{}}
}

func DefaultExchangerRegistry() *ExchangerRegistry {
	r := NewExchangerRegistry()
	register := func(name string, exc Exchanger) {
		_ = r.RegisterExchanger(name, exc)
	}
	register(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), NewPKCEFakeExchanger(TokenShapeAccessRefresh))
	register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess))
	register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne), NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess))
	register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity), NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess))
	register(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), NewPKCEFakeExchanger(TokenShapeAnySessionOrAccess))
	register(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth), NewDeviceCodeExchanger())
	register(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeBedrock), NewSSOExchanger())
	register("copilot/device_code", NewDeviceCodeExchanger())
	register("kiro/sso", NewSSOExchanger())
	register("cursor/oauth", NewPKCEFakeExchanger(TokenShapeSession))
	register("windsurf/oauth", NewPKCEFakeExchanger(TokenShapeSession))
	return r
}

var defaultExchangers = DefaultExchangerRegistry()

func RegisterExchanger(name string, exc Exchanger) error {
	return defaultExchangers.RegisterExchanger(name, exc)
}

func RegisterOrReplaceExchanger(name string, exc Exchanger) error {
	return defaultExchangers.RegisterOrReplaceExchanger(name, exc)
}

func (r *ExchangerRegistry) RegisterExchanger(name string, exc Exchanger) error {
	if r == nil {
		return errors.New("credentialacq: exchanger registry is nil")
	}
	if exc == nil {
		return errors.New("credentialacq: exchanger is nil")
	}
	key := normalizeExchangerName(name)
	if key == "" {
		return errors.New("credentialacq: exchanger name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exchangers == nil {
		r.exchangers = map[string]Exchanger{}
	}
	if _, exists := r.exchangers[key]; exists {
		return fmt.Errorf("credentialacq: exchanger already registered: %s", key)
	}
	r.exchangers[key] = exc
	return nil
}

func (r *ExchangerRegistry) RegisterOrReplaceExchanger(name string, exc Exchanger) error {
	if r == nil {
		return errors.New("credentialacq: exchanger registry is nil")
	}
	if exc == nil {
		return errors.New("credentialacq: exchanger is nil")
	}
	key := normalizeExchangerName(name)
	if key == "" {
		return errors.New("credentialacq: exchanger name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exchangers == nil {
		r.exchangers = map[string]Exchanger{}
	}
	r.exchangers[key] = exc
	return nil
}

func (r *ExchangerRegistry) Lookup(name string) (Exchanger, bool) {
	if r == nil {
		return nil, false
	}
	key := normalizeExchangerName(name)
	r.mu.RLock()
	defer r.mu.RUnlock()
	exc, ok := r.exchangers[key]
	return exc, ok
}

func (r *ExchangerRegistry) Exchange(ctx context.Context, session Session, code string) (CredentialCandidate, error) {
	exc, ok := r.Lookup(exchangerKey(session.Vendor, session.AuthMode))
	if !ok {
		exc, ok = r.Lookup(session.Vendor)
	}
	if !ok {
		return CredentialCandidate{}, fmt.Errorf("%w: %s", ErrOAuthExchangerMissing, exchangerKey(session.Vendor, session.AuthMode))
	}
	return exc.ExchangeOAuthCode(ctx, session, code)
}

func (r *ExchangerRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.exchangers))
	for name := range r.exchangers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func exchangerKey(vendor, authMode string) string {
	return credentialstore.ModeKey(vendor, authMode)
}

func normalizeExchangerName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type TokenShape string

const (
	TokenShapeAccessRefresh      TokenShape = "access_refresh"
	TokenShapeSession            TokenShape = "session"
	TokenShapeAnySessionOrAccess TokenShape = "any_session_or_access"
)

type pkceFakeExchanger struct {
	shape TokenShape
}

func NewPKCEFakeExchanger(shape TokenShape) Exchanger {
	if shape == "" {
		shape = TokenShapeAnySessionOrAccess
	}
	return pkceFakeExchanger{shape: shape}
}

func (e pkceFakeExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	return startPKCEOAuthFlow(ctx, store, in, cfg)
}

func (e pkceFakeExchanger) ExchangeOAuthCode(_ context.Context, session Session, code string) (CredentialCandidate, error) {
	fields, raw, err := parseFakeTokenPayload(code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	if err := validateTokenShape(fields, e.shape); err != nil {
		return CredentialCandidate{}, err
	}
	return CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
	}, nil
}

func parseFakeTokenPayload(code string) (map[string]any, []byte, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil, fmt.Errorf("%w: empty fake token payload", ErrInvalidTokenShape)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(code), &fields); err != nil {
		return nil, nil, fmt.Errorf("%w: fake mode expects JSON token payload", ErrInvalidTokenShape)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	return fields, raw, nil
}

func validateTokenShape(fields map[string]any, shape TokenShape) error {
	hasAccess := stringField(fields, "access_token") != ""
	hasRefresh := stringField(fields, "refresh_token") != ""
	hasSession := stringField(fields, "session_token") != ""
	switch shape {
	case TokenShapeAccessRefresh:
		if hasAccess || hasRefresh {
			return nil
		}
	case TokenShapeSession:
		if hasSession {
			return nil
		}
	default:
		if hasSession || hasAccess || hasRefresh {
			return nil
		}
	}
	return fmt.Errorf("%w: token payload does not match %s", ErrInvalidTokenShape, shape)
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
