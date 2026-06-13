package hermeshttp

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/headerfirewall"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
)

func (h handler) startChat(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
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
	// WAVE H3b: thread the operator actor (role + admin token id) so the bridge
	// can bind it to the session and the runner's mid-conversation READ-ONLY tool
	// callbacks resolve to THIS operator's scope. An end-user-path request (no
	// admin actor) leaves Operator zero-valued, so no tool loop is bound.
	chatReq := hermeschat.Request{
		TenantID: ident.TenantID, UserID: ident.UserID,
		RequestID: requestID(r), CorrelationID: correlationID(r), Body: body,
	}
	if actor, ok := adminActorFromContext(r.Context()); ok {
		chatReq.Operator = hermeschat.SessionOperator{
			AdminActorTokenID: actor.TokenID,
			Role:              actor.Role,
		}
	}
	prepared, err := h.chatBridge.PrepareRequest(r.Context(), chatReq)
	if err != nil {
		h.auditFailureThenError(w, r, ident, hermes.ActionChatStart, args, err)
		return
	}
	if prepared.BoundOperator {
		defer h.chatBridge.ReleaseSession(prepared.RequestID)
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
