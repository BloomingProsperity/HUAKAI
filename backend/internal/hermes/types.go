// Package hermes 承担 Hermes service 层、runner client 与 audit 入口。
package hermes

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

const (
	AuditResultSuccess = "success"
	AuditResultFailure = "failure"
)

var (
	ErrInvalidInput      = errors.New("hermes: invalid input")
	ErrNotFound          = errors.New("hermes: not found")
	ErrGone              = errors.New("hermes: gone")
	ErrForbidden         = errors.New("hermes: forbidden")
	ErrMisconfigured     = errors.New("hermes: misconfigured")
	ErrProfileNotOwned   = errors.New("hermes: profile not owned")
	ErrProfileInUse      = errors.New("hermes: profile in use")
	ErrConflict          = errors.New("hermes: concurrent update conflict")
	ErrAuditRecordFailed = errors.New("hermes: audit record failed")
	ErrRunnerFailure     = errors.New("hermes: runner request failed")
)

// RunnerConfig 保存 gateway 调 runner 所需的最小 JWT 配置。
type RunnerConfig struct {
	RunnerURL     string
	JWTPrivateKey ed25519.PrivateKey
	JWTKID        string
	JWTIssuer     string
	JWTAudience   string
	HTTPClient    *http.Client
}

// ProfileSpec 是创建 Hermes API profile 的 service 输入。
type ProfileSpec struct {
	TenantID    int64
	OwnerUserID int64
	Name        string
	BaseURL     string
	APIKey      string
}

// AuditFields 是写 hermes_audit_events 的显式字段集合。
type AuditFields struct {
	TenantID      int64
	ActorSource   string
	ActorID       int64
	ActorRole     string
	Action        string
	SanitizedArgs map[string]any
	Result        string
	CorrelationID string
	RequestID     string
	LogCategory   string
}

type Settings struct {
	TenantID  int64     `json:"tenant_id"`
	UserID    int64     `json:"user_id"`
	Enabled   bool      `json:"enabled"`
	APISource string    `json:"api_source"`
	ProfileID *int64    `json:"profile_id,omitempty"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Profile struct {
	ID                int64     `json:"id"`
	TenantID          int64     `json:"tenant_id"`
	OwnerUserID       int64     `json:"owner_user_id"`
	Name              string    `json:"name"`
	Kind              string    `json:"kind"`
	BaseURL           string    `json:"base_url"`
	APIKeyMasked      string    `json:"api_key_masked"`
	CredentialVersion int32     `json:"credential_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Conversation struct {
	ID            int64      `json:"id"`
	TenantID      int64      `json:"tenant_id"`
	OwnerUserID   int64      `json:"owner_user_id"`
	ActorSource   string     `json:"actor_source"`
	ActorID       int64      `json:"actor_id"`
	ActorRole     *string    `json:"actor_role,omitempty"`
	Title         *string    `json:"title,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
}

type Message struct {
	ID             int64           `json:"id"`
	TenantID       int64           `json:"tenant_id"`
	ConversationID int64           `json:"conversation_id"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content"`
	TokenCount     *int32          `json:"token_count,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type AuditEvent struct {
	ID            int64     `json:"id"`
	TenantID      int64     `json:"tenant_id"`
	ActorSource   string    `json:"actor_source"`
	ActorID       int64     `json:"actor_id"`
	ActorRole     *string   `json:"actor_role,omitempty"`
	Action        string    `json:"action"`
	Result        string    `json:"result"`
	CorrelationID *string   `json:"correlation_id,omitempty"`
	RequestID     *string   `json:"request_id,omitempty"`
	LogCategory   string    `json:"log_category"`
	CreatedAt     time.Time `json:"created_at"`
}

type Store interface {
	AppendMessage(ctx context.Context, arg dbhermes.AppendMessageParams) (int64, error)
	CreateConversation(ctx context.Context, arg dbhermes.CreateConversationParams) (int64, error)
	CreateProfile(ctx context.Context, arg dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error)
	DeleteProfile(ctx context.Context, arg dbhermes.DeleteProfileParams) (int64, error)
	DisableHermes(ctx context.Context, arg dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error)
	GetConversation(ctx context.Context, arg dbhermes.GetConversationParams) (dbhermes.HermesConversation, error)
	GetProfile(ctx context.Context, arg dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error)
	GetSettings(ctx context.Context, arg dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error)
	InsertAuditEvent(ctx context.Context, arg dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error)
	ListConversationsByOwner(ctx context.Context, arg dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error)
	ListMessagesByConversation(ctx context.Context, arg dbhermes.ListMessagesByConversationParams) ([]dbhermes.ListMessagesByConversationRow, error)
	ListProfilesByOwner(ctx context.Context, arg dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error)
	ListProfilesByTenant(ctx context.Context, tenantID int64) ([]dbhermes.HermesApiProfile, error)
	ProfileInUse(ctx context.Context, arg dbhermes.ProfileInUseParams) (bool, error)
	SoftDeleteConversation(ctx context.Context, arg dbhermes.SoftDeleteConversationParams) (int64, error)
	UpdateConversationLastMessageAt(ctx context.Context, arg dbhermes.UpdateConversationLastMessageAtParams) (int64, error)
	UpsertSettings(ctx context.Context, arg dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error)
}

type txBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type transactor interface {
	withTx(context.Context, func(Store) error) error
}

type Service struct {
	store                 Store
	tx                    transactor
	messageContentKeys    credentialstore.KeyProvider
	profileCredentialKeys credentialstore.KeyProvider
}

type sqlcTransactor struct {
	queries  *dbhermes.Queries
	beginner txBeginner
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func NewServiceWithTx(queries *dbhermes.Queries, beginner txBeginner) *Service {
	service := NewService(queries)
	if queries != nil && beginner != nil {
		service.tx = sqlcTransactor{queries: queries, beginner: beginner}
	}
	return service
}

func (s *Service) WithMessageContentKeys(keys credentialstore.KeyProvider) *Service {
	if s != nil {
		s.messageContentKeys = keys
	}
	return s
}

func (s *Service) WithProfileCredentialKeys(keys credentialstore.KeyProvider) *Service {
	if s != nil {
		s.profileCredentialKeys = keys
	}
	return s
}

func (s *Service) withTx(ctx context.Context, fn func(Store) error) error {
	if s == nil || s.tx == nil {
		return ErrMisconfigured
	}
	return s.tx.withTx(ctx, fn)
}

func (s *Service) RunHermesTx(ctx context.Context, fn func(Store) error) error {
	return s.withTx(ctx, fn)
}

func (t sqlcTransactor) withTx(ctx context.Context, fn func(Store) error) error {
	if t.queries == nil || t.beginner == nil {
		return ErrMisconfigured
	}
	tx, err := t.beginner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin hermes tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := fn(t.queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit hermes tx: %w", err)
	}
	committed = true
	return nil
}
