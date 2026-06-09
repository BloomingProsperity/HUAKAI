package userkeycontrolshttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
)

type setIPBlacklistRequest struct {
	IPBlacklist []string `json:"ip_blacklist"`
}

func newSetIPBlacklistHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Service != nil)
		if !ok {
			return
		}
		apiKeyID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req setIPBlacklistRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		out, err := d.Service.SetKeyIPBlacklist(r.Context(), userkeycontrols.SetKeyIPBlacklistRequest{
			TenantID:    ident.TenantID,
			UserID:      ident.UserID,
			APIKeyID:    apiKeyID,
			IPBlacklist: req.IPBlacklist,
			RequestID:   requestIDFromReq(r),
		})
		if err != nil {
			writeControlsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func newGetIPBlacklistHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Service != nil)
		if !ok {
			return
		}
		apiKeyID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		out, err := d.Service.GetKeyIPBlacklist(r.Context(), ident.TenantID, ident.UserID, apiKeyID)
		if err != nil {
			writeControlsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
