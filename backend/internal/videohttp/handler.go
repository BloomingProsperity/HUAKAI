package videohttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
	"github.com/BloomingProsperity/HUAKAI/internal/modality"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

const (
	providerGrokVideo       = "grok_video"
	providerGeminiVideo     = "gemini_video"
	maxRequestBodyBytes     = 32 << 20
	maxIdempotencyBytes     = 128
	selectionReleaseTimeout = 5 * time.Second
)

type authResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type mediaService interface {
	Submit(context.Context, int64, int64, mediatask.SubmitInput) (mediatask.Task, error)
	StatusForAPIKey(context.Context, int64, int64, int64, string) (mediatask.Task, error)
	ContentForAPIKey(context.Context, int64, int64, int64, string) (mediatask.ContentResult, error)
}

type Deps struct {
	Auth            authResolver
	Registry        registry.Registry
	Router          router.Router
	Selector        pool.Selector
	CredentialVault provider.CredentialVault
	Service         mediaService
}

type videoRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type endpoint struct {
	path     string
	taskType string
}

func MountRoutes(r chi.Router, deps Deps) {
	r.Post("/v1/videos/generations", newSubmitHandler(deps, endpoint{path: "/v1/videos/generations", taskType: "video_generate"}))
	r.Post("/v1/videos/edits", newSubmitHandler(deps, endpoint{path: "/v1/videos/edits", taskType: "video_edit"}))
	r.Post("/v1/videos/extensions", newSubmitHandler(deps, endpoint{path: "/v1/videos/extensions", taskType: "video_extend"}))
	r.Get("/v1/videos/{request_id}/content", newContentHandler(deps))
	r.Get("/v1/videos/{request_id}", newStatusHandler(deps))
}

func newContentHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Auth == nil || deps.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "video handler dependency unset")
			return
		}
		identity, ok := resolveIdentity(w, r, deps.Auth)
		if !ok {
			return
		}
		requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
		if requestID == "" || len(requestID) > 96 {
			writeError(w, http.StatusNotFound, "video_not_found", "video request not found")
			return
		}
		content, err := deps.Service.ContentForAPIKey(r.Context(), identity.TenantID, identity.UserID, identity.APIKeyID, requestID)
		if err != nil {
			if errors.Is(err, mediatask.ErrNotFound) || errors.Is(err, mediatask.ErrContentUnavailable) {
				writeError(w, http.StatusNotFound, "video_content_not_found", "video content not available")
				return
			}
			writeError(w, http.StatusBadGateway, "video_content_unavailable", "video content is temporarily unavailable")
			return
		}
		if content.Close != nil {
			defer content.Close()
		}
		if content.Body == nil {
			writeError(w, http.StatusBadGateway, "video_content_unavailable", "video content is temporarily unavailable")
			return
		}
		contentType := strings.TrimSpace(content.Headers.Get("Content-Type"))
		if contentType == "" {
			contentType = "video/mp4"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `inline; filename="`+requestID+`.mp4"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, content.Body)
	}
}

func newSubmitHandler(deps Deps, target endpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !configured(deps) {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "video handler dependency unset")
			return
		}
		identity, ok := resolveIdentity(w, r, deps.Auth)
		if !ok {
			return
		}
		body, request, ok := readVideoRequest(w, r)
		if !ok {
			return
		}
		if !apikeymodelallow.AllowsCSV(identity.AllowedModels, request.Model) {
			writeError(w, http.StatusForbidden, "model_not_allowed", "api key is not allowed to use this model")
			return
		}
		requestID, ok := publicRequestID(w, r, identity)
		if !ok {
			return
		}
		resolved, err := deps.Registry.ResolveModel(r.Context(), request.Model, identity.TenantID)
		if err != nil {
			writeRegistryError(w, err)
			return
		}
		if !modality.Supports(resolved.Capabilities, modality.Video) {
			writeError(w, http.StatusBadRequest, "model_not_video_capable", "model is not enabled for video output")
			return
		}
		mediaProvider, ok := videoProviderForProtocol(resolved.ProtocolFamily)
		if !ok {
			writeError(w, http.StatusBadRequest, "video_protocol_not_supported", "model protocol does not support the video endpoint")
			return
		}
		if err := mediatask.ValidateProviderTaskType(mediaProvider, target.taskType); err != nil {
			writeError(w, http.StatusBadRequest, "video_operation_not_supported", "model provider does not support this video operation")
			return
		}
		input := mediatask.SubmitInput{
			RequestID: requestID, TaskType: target.taskType, Provider: mediaProvider,
			InputParams: body, APIKeyID: identity.APIKeyID, RequestedModel: request.Model,
		}
		if existing, err := deps.Service.StatusForAPIKey(r.Context(), identity.TenantID, identity.UserID, identity.APIKeyID, requestID); err == nil {
			if !mediatask.MatchesSubmission(existing, input) {
				writeError(w, http.StatusConflict, "video_request_conflict", "idempotency key belongs to a different video request")
				return
			}
			writeSubmitResponse(w, existing)
			return
		} else if !errors.Is(err, mediatask.ErrNotFound) {
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		plan, err := deps.Router.Plan(r.Context(), router.PlanInput{
			Context: router.RequestContext{TenantID: identity.TenantID, UserID: identity.UserID, APIKeyID: identity.APIKeyID, RequestID: requestID},
			Model:   resolvedForRouter(resolved),
		})
		if err != nil || len(plan.Attempts) == 0 {
			writeError(w, http.StatusServiceUnavailable, "video_route_unavailable", "video route is unavailable")
			return
		}
		selected, attempt, account, ok := selectBoundAccount(r.Context(), deps, identity, resolved, plan, requestID)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "video_account_unavailable", "no eligible video account is available")
			return
		}
		if selected.Release != nil {
			defer releaseSelection(r.Context(), selected.Release)
		}
		providerModel := strings.TrimSpace(attempt.UpstreamModelID)
		if providerModel == "" {
			providerModel = strings.TrimSpace(resolved.ProviderModelID)
		}
		input.ProviderAccountID = account.AccountID
		input.PoolGroupID = attempt.PoolGroupID
		input.ProtocolFamily = resolved.ProtocolFamily
		input.ProviderModelID = providerModel
		input.RouteID = router.TraceRouteID(plan, attempt)
		input.BindingID = attempt.BindingID
		input.BindingRPMLimit = attempt.BindingRPMLimit
		input.BindingTPMLimit = attempt.BindingTPMLimit
		input.BindingMaxParallelRequests = attempt.MaxParallelRequests
		task, err := deps.Service.Submit(r.Context(), identity.TenantID, identity.UserID, input)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeSubmitResponse(w, task)
	}
}

func videoProviderForProtocol(protocolFamily string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(protocolFamily)) {
	case "grok_chat":
		return providerGrokVideo, true
	case "gemini_messages":
		return providerGeminiVideo, true
	default:
		return "", false
	}
}

func newStatusHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Auth == nil || deps.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "video handler dependency unset")
			return
		}
		identity, ok := resolveIdentity(w, r, deps.Auth)
		if !ok {
			return
		}
		requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
		if requestID == "" || len(requestID) > 96 {
			writeError(w, http.StatusNotFound, "video_not_found", "video request not found")
			return
		}
		task, err := deps.Service.StatusForAPIKey(r.Context(), identity.TenantID, identity.UserID, identity.APIKeyID, requestID)
		if err != nil {
			if errors.Is(err, mediatask.ErrNotFound) {
				writeError(w, http.StatusNotFound, "video_not_found", "video request not found")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		writeTaskResponse(w, task)
	}
}

func configured(deps Deps) bool {
	return deps.Auth != nil && deps.Registry != nil && deps.Router != nil && deps.Selector != nil &&
		deps.CredentialVault != nil && deps.Service != nil
}

func resolveIdentity(w http.ResponseWriter, r *http.Request, resolver authResolver) (auth.Identity, bool) {
	identity, err := resolver.Resolve(r.Context(), r)
	switch {
	case errors.Is(err, auth.ErrAuthMisconfigured), errors.Is(err, auth.ErrAuthBackend):
		writeError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend unavailable")
		return auth.Identity{}, false
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
		return auth.Identity{}, false
	case err != nil:
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
		return auth.Identity{}, false
	default:
		return identity, true
	}
}

func readVideoRequest(w http.ResponseWriter, r *http.Request) ([]byte, videoRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 || !json.Valid(body) {
		writeError(w, http.StatusBadRequest, "invalid_video_request", "request body must be valid JSON")
		return nil, videoRequest{}, false
	}
	var request videoRequest
	if err := json.Unmarshal(body, &request); err != nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "invalid_video_request", "model and prompt are required")
		return nil, videoRequest{}, false
	}
	request.Model = strings.TrimSpace(request.Model)
	return body, request, true
}

func publicRequestID(w http.ResponseWriter, r *http.Request, identity auth.Identity) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > maxIdempotencyBytes {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "idempotency key is too long")
		return "", false
	}
	if key == "" {
		if requestID := strings.TrimSpace(middleware.GetReqID(r.Context())); requestID != "" {
			key = requestID
		} else {
			key = uuid.NewString()
		}
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"huakai-video-v1", identityKey(identity), key,
	}, ":")))
	return "video_" + hex.EncodeToString(digest[:16]), true
}

func identityKey(identity auth.Identity) string {
	return strings.Join([]string{
		jsonNumber(identity.TenantID), jsonNumber(identity.UserID), jsonNumber(identity.APIKeyID),
	}, ":")
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func selectBoundAccount(ctx context.Context, deps Deps, identity auth.Identity, resolved registry.Resolved, plan router.RoutePlan, requestID string) (*pool.SelectionResult, router.AttemptPlan, provider.AccountInfo, bool) {
	budget := plan.AttemptBudget
	if budget <= 0 || budget > len(plan.Attempts) {
		budget = len(plan.Attempts)
	}
	excluded := make(map[int64]struct{})
	for index := 0; index < budget; index++ {
		attempt := plan.Attempts[index]
		providerModel := strings.TrimSpace(attempt.UpstreamModelID)
		if providerModel == "" {
			providerModel = resolved.ProviderModelID
		}
		selection, err := deps.Selector.Select(ctx, pool.SelectionRequest{
			TenantID: identity.TenantID, UserID: identity.UserID, APIKeyID: identity.APIKeyID,
			PoolGroupID: attempt.PoolGroupID, RequestedModel: resolved.PublicAlias,
			ProviderModelID:  providerModel,
			ModelCooldownKey: providerModel, ProtocolFamily: resolved.ProtocolFamily,
			EndpointFamily: "videos", CapabilityFlags: attempt.RequiredCapabilities,
			RequestID: requestID, Vendor: pool.VendorFromProtocolFamily(resolved.ProtocolFamily),
			UserGroup: identity.UserGroup, SelectionMode: attempt.SelectionMode,
			BindingID: attempt.BindingID, BindingRPMLimit: attempt.BindingRPMLimit,
			BindingTPMLimit: attempt.BindingTPMLimit, MaxParallelRequests: attempt.MaxParallelRequests,
			RateAccountingScope: pool.RateAccountingLogicalOnly,
			ExcludedAccounts:    excluded, AttemptSeq: index + 1,
		})
		if err != nil || selection == nil || selection.AccountID <= 0 || selection.WaitPlan != nil {
			continue
		}
		credential, account, err := deps.CredentialVault.Resolve(ctx, identity.TenantID, selection.AccountID)
		if account.AccountID == 0 {
			account.AccountID = selection.AccountID
		}
		if err == nil {
			err = servingcapability.ValidateRuntimeAccountCompatibility(resolved.ProtocolFamily, credential, account)
		}
		if err == nil {
			return selection, attempt, account, true
		}
		excluded[selection.AccountID] = struct{}{}
		if selection.Release != nil {
			releaseSelection(ctx, selection.Release)
		}
	}
	return nil, router.AttemptPlan{}, provider.AccountInfo{}, false
}

func releaseSelection(parent context.Context, release func(context.Context) error) {
	if release == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), selectionReleaseTimeout)
	defer cancel()
	_ = release(ctx)
}

func resolvedForRouter(resolved registry.Resolved) router.ResolvedModel {
	out := router.ResolvedModel{
		PublicAlias: resolved.PublicAlias, InternalModelID: resolved.CanonicalModelID,
		ProviderModelID: resolved.ProviderModelID, ContextWindow: resolved.ContextWindow,
		Capabilities: resolved.Capabilities, PricingClass: resolved.PricingClass,
		ProtocolFamily: resolved.ProtocolFamily, PoolCandidates: resolved.PoolCandidates,
		SnapshotVersion: resolved.SnapshotVersion,
	}
	for _, binding := range resolved.BindingMetadata {
		providerModel := resolved.DefaultProviderModelID
		if providerModel == "" {
			providerModel = resolved.ProviderModelID
		}
		if binding.ProviderModelIDOverride != nil && *binding.ProviderModelIDOverride != "" {
			providerModel = *binding.ProviderModelIDOverride
		}
		out.PoolMetadata = append(out.PoolMetadata, router.PoolCandidateMeta{
			PoolGroupID: binding.PoolGroupID, ProviderModelID: providerModel,
			BindingID: binding.BindingID, BindingRPMLimit: int64Value(binding.RPMLimit),
			BindingTPMLimit: int64Value(binding.TPMLimit), MaxParallelRequests: int64Value(binding.MaxParallelRequests),
			Priority: binding.Priority, Weight: binding.Weight, SelectionMode: binding.SelectionMode,
			FallbackClass: bindingfallback.NormalizeClass(binding.FallbackClass),
		})
	}
	return out
}

func int64Value(value *int32) int64 {
	if value == nil {
		return 0
	}
	return int64(*value)
}

func writeRegistryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrUnknownModel), errors.Is(err, registry.ErrModelDisabled), errors.Is(err, registry.ErrTenantNoAccess):
		writeError(w, http.StatusNotFound, "model_not_available", "model not available")
	case errors.Is(err, registry.ErrRegistryBackend):
		writeError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry backend unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "registry_error", "model registry failed")
	}
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrInsufficientBalance):
		writeError(w, http.StatusPaymentRequired, "insufficient_balance", "insufficient balance")
	case errors.Is(err, billing.ErrTenantInactive):
		writeError(w, http.StatusForbidden, clienterr.CodeTenantInactive, clienterr.MessageFor(clienterr.CodeTenantInactive))
	case errors.Is(err, mediatask.ErrQuotaDenied):
		writeError(w, http.StatusTooManyRequests, "quota_exceeded", "quota exceeded")
	case errors.Is(err, mediatask.ErrRequestIDConflict):
		writeError(w, http.StatusConflict, "video_request_conflict", "request id belongs to a different video request")
	case errors.Is(err, mediatask.ErrDisabled), errors.Is(err, mediatask.ErrProviderUnavailable):
		writeError(w, http.StatusServiceUnavailable, "video_service_unavailable", "video service is unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
	}
}

func writeSubmitResponse(w http.ResponseWriter, task mediatask.Task) {
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": task.RequestID})
}

func writeTaskResponse(w http.ResponseWriter, task mediatask.Task) {
	if task.Status == mediatask.StatusSucceeded && len(task.Result) > 0 && json.Valid(task.Result) {
		if mediatask.RequiresContentProxy(task) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(mediatask.ContentProxyResult(task))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(task.Result)
		return
	}
	status := "pending"
	switch task.Status {
	case mediatask.StatusFailed:
		status = "failed"
	case mediatask.StatusExpired:
		status = "expired"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "progress": task.Progress})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
