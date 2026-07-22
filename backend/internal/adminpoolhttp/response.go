package adminpoolhttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
)

func writeAuditJSON(w http.ResponseWriter, status int, value any) {
	adminhttpcore.WriteJSON(w, status, value)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	adminhttpcore.WriteJSONError(w, status, code, message)
}
