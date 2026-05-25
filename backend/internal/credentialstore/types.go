// Package credentialstore owns F-AUTH-005 encrypted upstream credentials.
package credentialstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	VendorAnthropic = "anthropic"
	VendorOpenAI    = "openai"
	VendorGemini    = "gemini"
	VendorCopilot   = "copilot"

	AuthModeAPIKey          = "api_key"
	AuthModeClaudeAIOAuth   = "claude_ai_oauth"
	AuthModeClaudeCode      = "claude_code"
	AuthModeBedrock         = "bedrock"
	AuthModeVertexAnthropic = "vertex_anthropic"
	AuthModeChatGPTOAuth    = "chatgpt_oauth"
	AuthModeCodexCLIOAuth   = "codex_cli_oauth"
	AuthModeAzure           = "azure"
	AuthModeRefreshToken    = "refresh_token"
	AuthModeAIStudioAPIKey  = "aistudio_api_key"
	AuthModeVertexSA        = "vertex_sa"
	AuthModeCodeAssist      = "code_assist"
	AuthModeGoogleOne       = "google_one"
	AuthModeAntigravity     = "antigravity"
	AuthModeCopilotOAuth    = "copilot_oauth"

	StateActive              = "active"
	StateRefreshing          = "refreshing"
	StateRefreshingWithGrace = "refreshing_with_grace"
	StateExpired             = "expired"
	StateTempUnschedulable   = "temp_unschedulable"
	StateNeedsRotation       = "needs_rotation"
	StateRevoked             = "revoked"
	StateOperatorAttention   = "operator_attention"

	RuntimeAPIKey              = "api_key"
	RuntimeOAuthAccessToken    = "oauth_access_token"
	RuntimeSessionToken        = "session_token"
	RuntimeAWSSigV4            = "aws_sigv4"
	RuntimeUpstreamPassthrough = "upstream_passthrough"
)

var (
	ErrUnknownMode       = errors.New("credentialstore: unknown vendor/auth_mode")
	ErrInvalidPayload    = errors.New("credentialstore: invalid credential payload")
	ErrRuntimeMaterial   = errors.New("credentialstore: runtime material unavailable")
	ErrCredentialExpired = errors.New("credentialstore: credential expired")
)

type RuntimeMaterial struct {
	Kind  string
	Value string
	Extra map[string]string
}

type ModeHandler interface {
	Vendor() string
	AuthMode() string
	RuntimeKind() string
	Refreshable() bool
	AllowGrace() bool
	ValidatePayload(raw []byte) error
	RuntimeMaterial(raw []byte) (RuntimeMaterial, error)
}

type HandlerRegistry struct {
	handlers map[string]ModeHandler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]ModeHandler)}
}

func DefaultHandlerRegistry() *HandlerRegistry {
	r := NewHandlerRegistry()
	for _, h := range defaultHandlers() {
		_ = r.Register(h)
	}
	return r
}

func (r *HandlerRegistry) Register(h ModeHandler) error {
	if r == nil {
		return errors.New("credentialstore: handler registry is nil")
	}
	if h == nil {
		return errors.New("credentialstore: mode handler is nil")
	}
	key := ModeKey(h.Vendor(), h.AuthMode())
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrUnknownMode)
	}
	if _, exists := r.handlers[key]; exists {
		return fmt.Errorf("credentialstore: duplicate mode handler: %s", key)
	}
	r.handlers[key] = h
	return nil
}

func (r *HandlerRegistry) Lookup(vendor, authMode string) (ModeHandler, bool) {
	if r == nil {
		return nil, false
	}
	h, ok := r.handlers[ModeKey(vendor, authMode)]
	return h, ok
}

func (r *HandlerRegistry) MustLookup(vendor, authMode string) (ModeHandler, error) {
	h, ok := r.Lookup(vendor, authMode)
	if !ok {
		return nil, fmt.Errorf("%w: vendor=%s auth_mode=%s", ErrUnknownMode, Normalize(vendor), Normalize(authMode))
	}
	return h, nil
}

func (r *HandlerRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.handlers))
	for key := range r.handlers {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

func ModeKey(vendor, authMode string) string {
	vendor, authMode = Normalize(vendor), Normalize(authMode)
	if vendor == "" || authMode == "" {
		return ""
	}
	return vendor + "/" + authMode
}

func Normalize(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

type handlerSpec struct {
	vendor       string
	authMode     string
	runtimeKind  string
	required     []string
	anyOf        []string
	refreshable  bool
	allowGrace   bool
	sessionFirst bool
}

func (h handlerSpec) Vendor() string      { return h.vendor }
func (h handlerSpec) AuthMode() string    { return h.authMode }
func (h handlerSpec) RuntimeKind() string { return h.runtimeKind }
func (h handlerSpec) Refreshable() bool   { return h.refreshable }
func (h handlerSpec) AllowGrace() bool    { return h.allowGrace }

func (h handlerSpec) ValidatePayload(raw []byte) error {
	fields, err := parsePayloadFields(raw)
	if err != nil {
		return err
	}
	for _, key := range h.required {
		if fieldString(fields, key) == "" {
			return fmt.Errorf("%w: %s/%s requires %s", ErrInvalidPayload, h.vendor, h.authMode, key)
		}
	}
	if len(h.anyOf) > 0 {
		for _, key := range h.anyOf {
			if fieldString(fields, key) != "" {
				return nil
			}
		}
		return fmt.Errorf("%w: %s/%s requires one of %s", ErrInvalidPayload, h.vendor, h.authMode, strings.Join(h.anyOf, ","))
	}
	return nil
}

func (h handlerSpec) RuntimeMaterial(raw []byte) (RuntimeMaterial, error) {
	fields, err := parsePayloadFields(raw)
	if err != nil {
		return RuntimeMaterial{}, err
	}
	extra := stringMap(fields, "extra")
	for _, key := range []string{
		"org_id", "project_id", "base_url", "auth_header", "anthropic_version",
		"anthropic_beta", "openai_beta", "goog_user_project", "auth_in_query",
		"aws_access_key_id", "aws_region", "aws_session_token", "client_email",
		"token_uri", "scope", "tenant_id", "deployment", "endpoint_api",
		"copilot_endpoint_api", "auth_mode", "client_id_source",
		"oauth_token_endpoint", "expires_at",
	} {
		if value := fieldString(fields, key); value != "" {
			extra[key] = value
		}
	}
	kind := h.runtimeKind
	value := ""
	switch kind {
	case RuntimeAPIKey:
		value = firstField(fields, "api_key", "azure_api_key")
		if value == "" && fieldString(fields, "access_token") != "" {
			kind = RuntimeUpstreamPassthrough
			value = "Bearer " + fieldString(fields, "access_token")
			if extra["auth_header"] == "" {
				extra["auth_header"] = "Authorization"
			}
		}
	case RuntimeOAuthAccessToken:
		value = fieldString(fields, "access_token")
	case RuntimeSessionToken:
		value = firstField(fields, "session_token", "access_token")
	case RuntimeAWSSigV4:
		value = fieldString(fields, "aws_secret_access_key")
	case RuntimeUpstreamPassthrough:
		value = firstField(fields, "auth_header_value", "access_token", "api_key")
		if value != "" && fieldString(fields, "auth_header_value") == "" && fieldString(fields, "access_token") != "" {
			value = "Bearer " + value
			if extra["auth_header"] == "" {
				extra["auth_header"] = "Authorization"
			}
		}
	default:
		return RuntimeMaterial{}, fmt.Errorf("%w: unknown runtime kind %q", ErrRuntimeMaterial, kind)
	}
	if value == "" {
		return RuntimeMaterial{}, fmt.Errorf("%w: %s/%s missing runtime secret", ErrRuntimeMaterial, h.vendor, h.authMode)
	}
	if exp := expiresAt(fields); !exp.IsZero() && time.Now().After(exp) && !h.allowGrace {
		return RuntimeMaterial{}, fmt.Errorf("%w: %s/%s", ErrCredentialExpired, h.vendor, h.authMode)
	}
	return RuntimeMaterial{Kind: kind, Value: value, Extra: extra}, nil
}

func defaultHandlers() []ModeHandler {
	return []ModeHandler{
		handlerSpec{vendor: VendorAnthropic, authMode: AuthModeAPIKey, runtimeKind: RuntimeAPIKey, required: []string{"api_key"}},
		handlerSpec{vendor: VendorAnthropic, authMode: AuthModeClaudeAIOAuth, runtimeKind: RuntimeOAuthAccessToken, anyOf: []string{"access_token", "refresh_token"}, refreshable: true, allowGrace: true},
		handlerSpec{vendor: VendorAnthropic, authMode: AuthModeClaudeCode, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "refresh_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
		handlerSpec{vendor: VendorAnthropic, authMode: AuthModeBedrock, runtimeKind: RuntimeAWSSigV4, required: []string{"aws_access_key_id", "aws_secret_access_key", "aws_region"}},
		handlerSpec{vendor: VendorAnthropic, authMode: AuthModeVertexAnthropic, runtimeKind: RuntimeUpstreamPassthrough, anyOf: []string{"access_token", "metadata_token_endpoint", "client_email"}, refreshable: true, allowGrace: true},
		handlerSpec{vendor: VendorOpenAI, authMode: AuthModeAPIKey, runtimeKind: RuntimeAPIKey, required: []string{"api_key"}},
		handlerSpec{vendor: VendorOpenAI, authMode: AuthModeChatGPTOAuth, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "refresh_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
		handlerSpec{vendor: VendorOpenAI, authMode: AuthModeCodexCLIOAuth, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "refresh_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
		handlerSpec{vendor: VendorOpenAI, authMode: AuthModeAzure, runtimeKind: RuntimeAPIKey, anyOf: []string{"api_key", "azure_api_key", "access_token", "mock_token_endpoint"}, refreshable: true, allowGrace: true},
		handlerSpec{vendor: VendorOpenAI, authMode: AuthModeRefreshToken, runtimeKind: RuntimeUpstreamPassthrough, anyOf: []string{"access_token", "refresh_token"}, refreshable: true, allowGrace: true},
		handlerSpec{vendor: VendorGemini, authMode: AuthModeAIStudioAPIKey, runtimeKind: RuntimeAPIKey, required: []string{"api_key"}},
		handlerSpec{vendor: VendorGemini, authMode: AuthModeVertexSA, runtimeKind: RuntimeUpstreamPassthrough, anyOf: []string{"access_token", "metadata_token_endpoint", "client_email"}, refreshable: true, allowGrace: true},
		handlerSpec{vendor: VendorGemini, authMode: AuthModeCodeAssist, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "refresh_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
		handlerSpec{vendor: VendorGemini, authMode: AuthModeGoogleOne, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "refresh_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
		handlerSpec{vendor: VendorGemini, authMode: AuthModeAntigravity, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "refresh_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
		handlerSpec{vendor: VendorCopilot, authMode: AuthModeCopilotOAuth, runtimeKind: RuntimeSessionToken, anyOf: []string{"session_token", "access_token", "github_access_token"}, refreshable: true, allowGrace: true, sessionFirst: true},
	}
}

func parsePayloadFields(raw []byte) (map[string]json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("%w: empty json", ErrInvalidPayload)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("%w: expected object", ErrInvalidPayload)
	}
	return fields, nil
}

func fieldString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func firstField(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := fieldString(fields, key); value != "" {
			return value
		}
	}
	return ""
}

func stringMap(fields map[string]json.RawMessage, key string) map[string]string {
	out := map[string]string{}
	raw, ok := fields[key]
	if !ok {
		return out
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		return out
	}
	for k, v := range got {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func expiresAt(fields map[string]json.RawMessage) time.Time {
	for _, key := range []string{"access_expires_at", "expires_at"} {
		raw := fieldString(fields, key)
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
