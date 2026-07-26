package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const providerAccountTestRecordTimeout = 3 * time.Second

type ProviderAccountTestDeps struct {
	Auth     providerAccountTestAuth
	Accounts providerAccountTestAccountStore
	Tester   ProviderAccountTester
	Now      func() time.Time
}

type providerAccountTestAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type providerAccountTestAccountStore interface {
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	RecordProviderAccountTest(context.Context, providerAccountTestRecordInput) error
}

type ProviderAccountTester interface {
	Probe(context.Context, accountprobe.Input) (accountprobe.Result, error)
}

func MountProviderAccountTestRoutes(r chi.Router, d ProviderAccountTestDeps) {
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).
		Post("/{id}/test", newProviderAccountTestHandler(d))
}

type providerAccountTestResponseBody struct {
	OK                   bool     `json:"ok"`
	Attempted            bool     `json:"attempted"`
	Model                string   `json:"model,omitempty"`
	ProtocolFamily       string   `json:"protocol_family,omitempty"`
	StatusCode           int      `json:"upstream_status,omitempty"`
	ErrorClass           *string  `json:"error_class"`
	Message              string   `json:"message"`
	LatencyMS            *int64   `json:"latency_ms"`
	TestedAt             *string  `json:"tested_at"`
	HealthSignal         string   `json:"health_signal,omitempty"`
	HealthSignalRecorded bool     `json:"health_signal_recorded"`
	Warnings             []string `json:"warnings"`
}

func newProviderAccountTestHandler(d ProviderAccountTestDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountTestTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := parseProviderAccountTestID(w, r)
		if !ok {
			return
		}
		account, err := d.Accounts.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{
			ID: id, TenantID: tenantID,
		})
		if err != nil {
			writeProviderAccountTestReadError(w, err, "provider_account_get_failed")
			return
		}
		probeModel := ""
		if account.ProbeModel != nil {
			probeModel = strings.TrimSpace(*account.ProbeModel)
		}
		result, err := d.Tester.Probe(r.Context(), accountprobe.Input{
			TenantID: tenantID, AccountID: id, ProbeModel: probeModel,
			ModelAllowList: account.ModelAllowList, RequestID: middleware.GetReqID(r.Context()),
		})
		if result.Attempted && result.TestedAt.IsZero() {
			now := time.Now
			if d.Now != nil {
				now = d.Now
			}
			result.TestedAt = now().UTC()
		}
		recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(r.Context()), providerAccountTestRecordTimeout)
		defer recordCancel()
		if recordErr := d.Accounts.RecordProviderAccountTest(recordCtx, providerAccountTestRecordInput{
			Identity: ident, TenantID: tenantID, AccountID: id,
			RequestID: middleware.GetReqID(r.Context()), Result: result, TestError: err,
		}); recordErr != nil {
			writeError(w, http.StatusServiceUnavailable, "provider_account_probe_record_failed", "provider account probe result could not be recorded")
			return
		}
		logProviderAccountProbeWarnings(recordCtx, tenantID, id, result)
		if err != nil {
			writeProviderAccountTestReadError(w, err, "provider_account_test_failed")
			return
		}
		var errorClass *string
		if result.ErrorClass != "" {
			errorClass = &result.ErrorClass
		}
		var latencyMS *int64
		var testedAt *string
		if result.Attempted {
			latency := result.LatencyMS
			latencyMS = &latency
			value := result.TestedAt.UTC().Format(time.RFC3339Nano)
			testedAt = &value
		}
		writeProviderAccountTestJSON(w, http.StatusOK, providerAccountTestResponseBody{
			OK: result.OK, Attempted: result.Attempted, Model: result.Model,
			ProtocolFamily: result.ProtocolFamily, StatusCode: result.StatusCode,
			ErrorClass: errorClass, Message: result.Message, LatencyMS: latencyMS, TestedAt: testedAt,
			HealthSignal: string(result.HealthSignal), HealthSignalRecorded: result.HealthSignalRecorded,
			Warnings: nonNilProbeWarnings(result.Warnings),
		})
	}
}

func resolveProviderAccountTestTenant(w http.ResponseWriter, r *http.Request, d ProviderAccountTestDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Accounts == nil || d.Tester == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account test dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		tenantID, ok := resolvePlatformAdminQueryTenant(w, r, ident)
		if !ok {
			return admin.AdminIdentity{}, 0, false
		}
		return ident, tenantID, true
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func parseProviderAccountTestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeProviderAccountTestReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, credentialstore.ErrCredentialNotFound) ||
		errors.Is(err, provider.ErrAccountNotFound) {
		writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, code, "provider account model probe is unavailable")
}

func logProviderAccountProbeWarnings(ctx context.Context, tenantID, accountID int64, result accountprobe.Result) {
	if len(result.Warnings) == 0 {
		return
	}
	_ = privacy.LogSystem(ctx, privacy.SystemEvent{
		Severity: privacy.SeverityError, Component: "adminhttp.provider_account_probe",
		RequestID: middleware.GetReqID(ctx), ErrorClass: "probe_state_feedback_incomplete",
		Attrs: map[string]any{
			"event_class": "provider_account_probe_feedback_failed", "outcome": "partial",
			"tenant_id": tenantID, "provider_account_id": accountID, "warnings": result.Warnings,
		},
	})
}

func nonNilProbeWarnings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return values
}

func writeProviderAccountTestJSON(w http.ResponseWriter, status int, body providerAccountTestResponseBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type providerAccountTestRecordInput struct {
	Identity  admin.AdminIdentity
	TenantID  int64
	AccountID int64
	RequestID string
	Result    accountprobe.Result
	TestError error
}

type providerAccountTestStoreAdapter struct {
	pool *pgxpool.Pool
}

func NewProviderAccountTestStoreAdapter(pool *pgxpool.Pool) providerAccountTestAccountStore {
	return &providerAccountTestStoreAdapter{pool: pool}
}

func (s *providerAccountTestStoreAdapter) GetAdminProviderAccount(ctx context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	if s == nil || s.pool == nil {
		return admindb.AdminProviderAccountRow{}, errors.New("provider account test store not configured")
	}
	return admindb.New(s.pool).GetAdminProviderAccount(ctx, arg)
}

func (s *providerAccountTestStoreAdapter) RecordProviderAccountTest(ctx context.Context, in providerAccountTestRecordInput) error {
	if s == nil || s.pool == nil {
		return errors.New("provider account test store not configured")
	}
	payload, err := providerAccountTestAuditPayload(in)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := admindb.New(tx)
		if in.Result.Attempted {
			affected, err := queries.RecordProviderAccountProbe(ctx, admindb.RecordProviderAccountProbeParams{
				ProbeAt:   pgtype.Timestamptz{Time: in.Result.TestedAt.UTC(), Valid: true},
				LatencyMs: providerAccountProbeLatency(in.Result.LatencyMS),
				ID:        in.AccountID, TenantID: in.TenantID,
			})
			if err != nil {
				return err
			}
			if affected != 1 {
				return pgx.ErrNoRows
			}
		}

		actorID := in.Identity.AuditActor()
		targetID := in.AccountID
		reason := "测试上游账号模型链路"
		requestID := in.RequestID
		_, err := queries.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID: &in.TenantID, ActorID: actorID, ActorRole: in.Identity.Role,
			Action: "test_provider_account", TargetType: "provider_account", TargetID: &targetID,
			RequestID: &requestID, Reason: &reason, Payload: payload,
		})
		return err
	})
}

func providerAccountProbeLatency(value int64) int32 {
	if value <= 0 {
		return 0
	}
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(value)
}

func providerAccountTestAuditPayload(in providerAccountTestRecordInput) ([]byte, error) {
	errorClass := in.Result.ErrorClass
	if in.TestError != nil && errorClass == "" {
		errorClass = "temporary"
	}
	outcome := "completed"
	if in.TestError != nil {
		outcome = "unavailable"
	} else if !in.Result.OK {
		outcome = "failed"
	}
	payload := map[string]any{
		"tenant_id":              in.TenantID,
		"operation":              "provider_account_model_probe",
		"controlled_probe":       true,
		"billed_to_user":         false,
		"attempted":              in.Result.Attempted,
		"ok":                     in.Result.OK && in.TestError == nil,
		"result":                 outcome,
		"model":                  in.Result.Model,
		"protocol_family":        in.Result.ProtocolFamily,
		"upstream_status":        in.Result.StatusCode,
		"latency_ms":             in.Result.LatencyMS,
		"health_signal":          in.Result.HealthSignal,
		"health_signal_recorded": in.Result.HealthSignalRecorded,
	}
	if errorClass != "" {
		payload["error_class"] = errorClass
	}
	if len(in.Result.Warnings) > 0 {
		payload["warnings"] = in.Result.Warnings
	}
	return json.Marshal(payload)
}
