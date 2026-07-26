package adminpoolhttp

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

const adminPoolsCursorPrefix = "pool:"

func parseAdminPoolsCursor(w http.ResponseWriter, r *http.Request) (int64, *string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		return 0, nil, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return 0, nil, false
	}
	text := string(decoded)
	if !strings.HasPrefix(text, adminPoolsCursorPrefix) {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return 0, nil, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(text, adminPoolsCursorPrefix), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return 0, nil, false
	}
	return id, &raw, true
}

func encodeAdminPoolsCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(adminPoolsCursorPrefix + strconv.FormatInt(id, 10)))
}
