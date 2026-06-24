package rerankhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
)

type rerankRequest struct {
	Model           string            `json:"model"`
	Query           string            `json:"query"`
	Documents       []json.RawMessage `json:"documents"`
	TopN            *int              `json:"top_n,omitempty"`
	ReturnDocuments *bool             `json:"return_documents,omitempty"`
}

func validateRequest(w http.ResponseWriter, r *http.Request) ([]byte, rerankRequest, bool) {
	body, err := relaybody.ReadLimitedRequestBody(w, r, relaybody.RequestBodyLimit())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "body_read_error", "request body could not be read")
		return nil, rerankRequest{}, false
	}
	var req rerankRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return nil, rerankRequest{}, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, rerankRequest{}, false
	}
	if strings.TrimSpace(req.Query) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_query", "query field required")
		return nil, rerankRequest{}, false
	}
	if len(req.Documents) == 0 || len(req.Documents) > maxRerankDocuments {
		writeJSONError(w, http.StatusBadRequest, "invalid_documents", "documents must contain between 1 and 1000 items")
		return nil, rerankRequest{}, false
	}
	return body, req, true
}

func searchUnitsForDocuments(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 99) / 100
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("rerank upstream response exceeds %d bytes", maxUpstreamBodyBytes)
	}
	return raw, nil
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
