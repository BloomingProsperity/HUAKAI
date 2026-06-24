package embeddingshttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
)

type embeddingRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

func validateRequest(w http.ResponseWriter, r *http.Request) ([]byte, embeddingRequest, []string, bool) {
	body, err := relaybody.ReadLimitedRequestBody(w, r, relaybody.RequestBodyLimit())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, embeddingRequest{}, nil, false
	}
	var req embeddingRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidJSON, clienterr.MessageFor(clienterr.CodeInvalidJSON))
		return nil, embeddingRequest{}, nil, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, embeddingRequest{}, nil, false
	}
	texts, ok := parseInput(req.Input)
	if !ok || len(texts) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_input", "input field must be a non-empty string or string array")
		return nil, embeddingRequest{}, nil, false
	}
	return body, req, texts, true
}

func parseInput(raw json.RawMessage) ([]string, bool) {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if strings.TrimSpace(one) == "" {
			return nil, false
		}
		return []string{one}, true
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil || len(many) == 0 {
		return nil, false
	}
	for _, text := range many {
		if strings.TrimSpace(text) == "" {
			return nil, false
		}
	}
	return many, true
}

func estimateInputTokens(texts []string) int {
	blocks := make([]proto.CanonicalContentBlock, 0, len(texts))
	for _, text := range texts {
		blocks = append(blocks, proto.CanonicalContentBlock{Type: "text", Text: text})
	}
	if n := (tokencheck.HeuristicEstimator{}).Estimate(blocks); n > 0 {
		return n
	}
	return 1
}

func promptTokens(raw []byte) (int, bool) {
	var body struct {
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Usage.PromptTokens <= 0 {
		return 0, false
	}
	return body.Usage.PromptTokens, true
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("embeddings upstream response exceeds %d bytes", maxUpstreamBodyBytes)
	}
	return raw, nil
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
