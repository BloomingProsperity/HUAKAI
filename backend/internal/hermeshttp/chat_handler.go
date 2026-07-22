package hermeshttp

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/headerfirewall"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

func (h handler) startChat(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	actor, ok := requireConversationActor(w, r)
	if !ok {
		return
	}
	settings, err := h.svc.GetSettings(r.Context(), ident.TenantID, ident.UserID)
	if errors.Is(err, hermes.ErrNotFound) || (err == nil && !settings.Enabled) {
		writeHermesDisabled(w)
		return
	}
	if err != nil {
		writeHermesError(w, err)
		return
	}
	model := strings.TrimSpace(settings.Model)
	if model == "" {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, nil, hermes.ErrMisconfigured)
		return
	}
	if settings.APISource != hermes.APISourceExternal || settings.ProfileID == nil || *settings.ProfileID <= 0 {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, nil, hermes.ErrMisconfigured)
		return
	}
	profile, profileErr := h.svc.GetProfile(r.Context(), *settings.ProfileID, ident.TenantID)
	if profileErr != nil || profile.OwnerUserID != ident.UserID || profile.Kind != hermes.APISourceExternal {
		if profileErr == nil {
			profileErr = hermes.ErrMisconfigured
		}
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, nil, profileErr)
		return
	}
	credential, credentialErr := h.svc.ResolveProfileCredential(r.Context(), profile.ID, ident.TenantID)
	if credentialErr != nil {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, nil, credentialErr)
		return
	}
	defer privacy.Zeroize(credential.APIKey)
	if !h.requireRunner(w) {
		return
	}
	if !h.requireChatBridge(w) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	args := map[string]any{"content_length": len(body)}
	if err != nil {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, args, err)
		return
	}
	// 将真实管理员身份签入短时内部令牌，使任意网关副本都能验证会话式工具回调。
	chatReq := hermeschat.Request{
		TenantID: ident.TenantID, UserID: ident.UserID,
		RequestID: requestID(r), CorrelationID: correlationID(r), Body: body,
		Model: model, ModelBaseURL: credential.BaseURL, ModelAPIKey: credential.APIKey,
	}
	chatReq.Operator = hermeschat.SessionOperator{
		ActorSource: actor.Source,
		ActorID:     actor.ID,
		Role:        actor.Role,
	}
	prepared, err := h.chatBridge.PrepareRequest(r.Context(), chatReq)
	if err != nil {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, args, err)
		return
	}
	args["conversation_id"] = prepared.ConversationID
	resp, err := h.runner.Chat(r.Context(), ident.TenantID, ident.UserID, prepared.Body)
	if err != nil {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, args, err)
		return
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if !h.audit(w, r, ident, hermes.ActionChatStart, args, hermes.AuditResultFailure) {
			resp.Body.Close()
			return
		}
		policy := headerfirewall.PolicyFromPlatformSettings(r.Context(), h.headerSettings)
		copyProxyResponseWithPolicy(w, resp, policy)
		return
	}
	if !h.audit(w, r, ident, hermes.ActionChatStart, args, hermes.AuditResultSuccess) {
		resp.Body.Close()
		return
	}
	if err := h.chatBridge.Stream(r.Context(), w, resp, prepared); err != nil {
		log.Printf("hermes chat bridge stream failed: %v", err)
	}
}

func writeHermesDisabled(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprint(w, `{"error":"hermes_disabled"}`)
}

func copyProxyResponse(w http.ResponseWriter, resp *http.Response) error {
	return copyProxyResponseWithPolicy(w, resp, headerfirewall.Policy{})
}

func copyProxyResponseWithPolicy(w http.ResponseWriter, resp *http.Response, policy headerfirewall.Policy) error {
	defer resp.Body.Close()
	filtered := headerfirewall.FilterResponseHeaders(resp.Header, policy.ExtraDeny, policy.AllowOverride)
	for k, values := range filtered {
		if hopByHopHeader(k) {
			continue
		}
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
		flusher.Flush()
	}
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func hopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
