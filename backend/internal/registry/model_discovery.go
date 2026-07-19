package registry

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	ModelDiscoveryPending  = "pending"
	ModelDiscoveryPromoted = "promoted"
	ModelDiscoveryIgnored  = "ignored"
	ModelDiscoveryAbsent   = "absent"

	defaultModelDiscoveryPageSize = 50
	maxModelDiscoveryPageSize     = 200
	modelDiscoveryTxAttempts      = 4
)

var (
	ErrModelDiscoveryInvalid   = errors.New("registry: invalid model discovery input")
	ErrModelDiscoveryNotFound  = errors.New("registry: model discovery not found")
	ErrModelDiscoveryForbidden = errors.New("registry: model discovery forbidden")
	ErrModelDiscoveryConflict  = errors.New("registry: model discovery conflict")
)

// ModelDiscovery 是发现箱的公开元数据投影，不包含凭据、请求头或上游原始响应。
type ModelDiscovery struct {
	ID                int64            `json:"id"`
	Vendor            modelsync.Vendor `json:"vendor"`
	ModelIDNormalized string           `json:"model_id_normalized"`
	ProviderModelID   string           `json:"provider_model_id"`
	DisplayName       string           `json:"display_name"`
	OwnedBy           string           `json:"owned_by"`
	ProtocolFamily    string           `json:"protocol_family"`
	ContextWindow     int              `json:"context_window"`
	ModelCreatedAt    *time.Time       `json:"model_created_at,omitempty"`
	Capabilities      []string         `json:"capabilities"`
	Status            string           `json:"status"`
	FirstSeenAt       time.Time        `json:"first_seen_at"`
	LastSeenAt        time.Time        `json:"last_seen_at"`
	LastAbsentAt      *time.Time       `json:"last_absent_at,omitempty"`
	DecidedAt         *time.Time       `json:"decided_at,omitempty"`
	DecidedByActor    *string          `json:"decided_by_actor,omitempty"`
	DecisionReason    *string          `json:"decision_reason,omitempty"`
	PromotedModelID   *int64           `json:"promoted_model_id,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

// ModelDiscoveryAccess 只接收服务端认证结果，请求体不能指定角色或操作者。
type ModelDiscoveryAccess struct {
	Role      string
	Actor     string
	RequestID string
}

type ModelDiscoveryListParams struct {
	Access   ModelDiscoveryAccess
	Vendor   modelsync.Vendor
	Status   string
	Search   string
	BeforeID int64
	Limit    int
}

type ModelDiscoveryPage struct {
	Items        []ModelDiscovery `json:"items"`
	NextBeforeID *int64           `json:"next_before_id"`
}

type ModelDiscoveryDecision struct {
	Access ModelDiscoveryAccess
	ID     int64
	Reason string
}

type normalizedDiscoveredModel struct {
	ModelIDNormalized string
	ProviderModelID   string
	DisplayName       string
	OwnedBy           string
	ProtocolFamily    string
	ContextWindow     int
	ModelCreatedAt    *time.Time
	Capabilities      []string
}

func normalizeDiscoveredModel(vendor modelsync.Vendor, model modelsync.Model) (normalizedDiscoveredModel, error) {
	if !validModelDiscoveryVendor(vendor) {
		return normalizedDiscoveredModel{}, ErrModelDiscoveryInvalid
	}
	providerModelID := strings.TrimSpace(model.ID)
	normalizedID := AliasNormalize(providerModelID)
	if providerModelID == "" || normalizedID == "" || strings.ContainsRune(providerModelID, '\x00') ||
		utf8.RuneCountInString(providerModelID) > 512 || utf8.RuneCountInString(normalizedID) > 512 {
		return normalizedDiscoveredModel{}, ErrModelDiscoveryInvalid
	}
	displayName := strings.TrimSpace(model.DisplayName)
	if displayName == "" {
		displayName = providerModelID
	}
	ownedBy := strings.TrimSpace(model.OwnedBy)
	if ownedBy == "" {
		ownedBy = defaultOwnerForVendor(vendor)
	}
	if strings.ContainsRune(displayName, '\x00') || strings.ContainsRune(ownedBy, '\x00') ||
		utf8.RuneCountInString(displayName) > 512 || utf8.RuneCountInString(ownedBy) > 128 {
		return normalizedDiscoveredModel{}, ErrModelDiscoveryInvalid
	}
	protocol := normalizeSyncedProtocolFamily(strings.TrimSpace(model.ProtocolFamily))
	if protocol == "" {
		protocol = defaultProtocolForVendor(vendor)
	}
	if !registrydefault.IsSupportedProtocolFamily(protocol) || model.ContextWindow < 0 || model.ContextWindow > math.MaxInt32 {
		return normalizedDiscoveredModel{}, ErrModelDiscoveryInvalid
	}
	capabilities, err := normalizeDiscoveryCapabilities(model.Capabilities)
	if err != nil {
		return normalizedDiscoveredModel{}, err
	}
	var createdAt *time.Time
	if !model.CreatedAt.IsZero() {
		value := model.CreatedAt.UTC()
		createdAt = &value
	}
	return normalizedDiscoveredModel{
		ModelIDNormalized: normalizedID,
		ProviderModelID:   providerModelID,
		DisplayName:       displayName,
		OwnedBy:           ownedBy,
		ProtocolFamily:    protocol,
		ContextWindow:     model.ContextWindow,
		ModelCreatedAt:    createdAt,
		Capabilities:      capabilities,
	}, nil
}

func normalizeDiscoveryCapabilities(items []string) ([]string, error) {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		capability, err := normalizeKnownModelCapability(item)
		if err != nil {
			return nil, ErrModelDiscoveryInvalid
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	if len(out) > 64 {
		return nil, ErrModelDiscoveryInvalid
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func normalizeModelDiscoveryList(params *ModelDiscoveryListParams) error {
	if params == nil || params.Access.Role != modelAdminRolePlatform {
		return ErrModelDiscoveryForbidden
	}
	if params.Vendor != "" && !validModelDiscoveryVendor(params.Vendor) {
		return ErrModelDiscoveryInvalid
	}
	params.Status = strings.TrimSpace(params.Status)
	if params.Status != "" && !validModelDiscoveryStatus(params.Status) {
		return ErrModelDiscoveryInvalid
	}
	params.Search = strings.TrimSpace(params.Search)
	if utf8.RuneCountInString(params.Search) > 200 || params.BeforeID < 0 || params.Limit < 0 {
		return ErrModelDiscoveryInvalid
	}
	if params.Limit == 0 {
		params.Limit = defaultModelDiscoveryPageSize
	}
	if params.Limit > maxModelDiscoveryPageSize {
		params.Limit = maxModelDiscoveryPageSize
	}
	return nil
}

func normalizeModelDiscoveryDecision(in *ModelDiscoveryDecision) error {
	if in == nil || in.Access.Role != modelAdminRolePlatform {
		return ErrModelDiscoveryForbidden
	}
	in.Access.Actor = strings.TrimSpace(in.Access.Actor)
	in.Access.RequestID = strings.TrimSpace(in.Access.RequestID)
	in.Reason = strings.TrimSpace(in.Reason)
	if in.ID <= 0 || in.Access.Actor == "" || strings.ContainsRune(in.Access.Actor, '\x00') ||
		utf8.RuneCountInString(in.Access.Actor) > 128 || utf8.RuneCountInString(in.Access.RequestID) > 256 ||
		in.Reason == "" || strings.ContainsRune(in.Reason, '\x00') || utf8.RuneCountInString(in.Reason) > 200 {
		return ErrModelDiscoveryInvalid
	}
	return nil
}

func validModelDiscoveryVendor(vendor modelsync.Vendor) bool {
	switch vendor {
	case modelsync.VendorAnthropic, modelsync.VendorOpenAI, modelsync.VendorGemini:
		return true
	default:
		return false
	}
}

func validModelDiscoveryStatus(status string) bool {
	switch status {
	case ModelDiscoveryPending, ModelDiscoveryPromoted, ModelDiscoveryIgnored, ModelDiscoveryAbsent:
		return true
	default:
		return false
	}
}

func retryModelDiscoveryTx[T any](ctx context.Context, run func() (T, error)) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var zero T
	for attempt := 0; attempt < modelDiscoveryTxAttempts; attempt++ {
		result, err := run()
		if err == nil {
			return result, nil
		}
		if !isRetryableModelDiscoveryTx(err) || attempt == modelDiscoveryTxAttempts-1 {
			return zero, err
		}
		delay := time.Duration(1<<attempt) * 2 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, ErrRegistryBackend
}

func isRetryableModelDiscoveryTx(err error) bool {
	if errors.Is(err, ErrModelDiscoveryConflict) {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "40001", "40P01", "23505":
		return true
	default:
		return false
	}
}
