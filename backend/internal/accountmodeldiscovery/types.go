// Package accountmodeldiscovery 负责按一条已落库账号的真实凭据发现上游模型，
// 并把经过规范化的结果原子同步回该账号的模型白名单。
package accountmodeldiscovery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const (
	maxResponseBytes = 8 << 20
	maxPages         = 16
	maxModels        = 1000
	defaultTimeout   = 30 * time.Second
)

type ErrorKind string

const (
	ErrorNotConfigured      ErrorKind = "not_configured"
	ErrorAccountUnavailable ErrorKind = "account_unavailable"
	ErrorUnsupported        ErrorKind = "unsupported"
	ErrorCredentialRejected ErrorKind = "credential_rejected"
	ErrorRateLimited        ErrorKind = "rate_limited"
	ErrorUpstream           ErrorKind = "upstream"
	ErrorResponseInvalid    ErrorKind = "response_invalid"
	ErrorEmptyCatalog       ErrorKind = "empty_catalog"
	ErrorCatalogTooLarge    ErrorKind = "catalog_too_large"
	ErrorCredentialChanged  ErrorKind = "credential_changed"
	ErrorPersistence        ErrorKind = "persistence"
)

type DiscoveryError struct {
	Kind       ErrorKind
	StatusCode int
	// Vendor/AuthMode 在凭据解析成功后回填,供失败日志按账号族辨识;
	// 解析前的失败(如账号不存在)拿不到,保持空串。
	Vendor   string
	AuthMode string
	Err      error
}

func (e *DiscoveryError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Err.Error()
}

func (e *DiscoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func KindOf(err error) ErrorKind {
	var discoveryErr *DiscoveryError
	if errors.As(err, &discoveryErr) {
		return discoveryErr.Kind
	}
	return ErrorUpstream
}

func annotate(err error, vendor, authMode string) {
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) {
		return
	}
	if discoveryErr.Vendor == "" {
		discoveryErr.Vendor = vendor
	}
	if discoveryErr.AuthMode == "" {
		discoveryErr.AuthMode = authMode
	}
}

type Model struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name"`
	ProtocolFamily string   `json:"protocol_family"`
	Capabilities   []string `json:"capabilities,omitempty"`
	ContextWindow  int      `json:"context_window,omitempty"`
}

type Result struct {
	AccountID           int64     `json:"account_id"`
	AccountCredentialID int64     `json:"account_credential_id,omitempty"`
	CredentialVersion   int       `json:"credential_version,omitempty"`
	Vendor              string    `json:"vendor"`
	AuthMode            string    `json:"auth_mode"`
	ProtocolFamily      string    `json:"protocol_family"`
	Models              []Model   `json:"models"`
	DiscoveredAt        time.Time `json:"discovered_at"`
}

func (r Result) ModelIDs() []string {
	out := make([]string, 0, len(r.Models))
	for _, model := range r.Models {
		out = append(out, model.ID)
	}
	return out
}

type SyncInput struct {
	TenantID  int64
	AccountID int64
	ActorID   string
	ActorRole string
	RequestID string
	Reason    string
}

type SyncResult struct {
	Result
	Changed       bool `json:"changed"`
	PreviousCount int  `json:"previous_count"`
}

type CredentialVault interface {
	Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error)
}

type Dispatcher interface {
	Dispatch(context.Context, gateway.DispatchInput) (*gateway.DispatchResult, error)
}

type Service struct {
	vault      CredentialVault
	dispatcher Dispatcher
	pool       *pgxpool.Pool
	timeout    time.Duration
}

func NewService(vault CredentialVault, dispatcher Dispatcher, pool *pgxpool.Pool) *Service {
	return &Service{vault: vault, dispatcher: dispatcher, pool: pool, timeout: defaultTimeout}
}
