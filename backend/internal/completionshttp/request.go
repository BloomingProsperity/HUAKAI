package completionshttp

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
)

type completionRequest struct {
	Model     string          `json:"model"`
	Prompt    json.RawMessage `json:"prompt"`
	Stream    bool            `json:"stream"`
	MaxTokens *int            `json:"max_tokens"`
}

type countTokensRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
}

type completionUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func validateCompletionRequest(w http.ResponseWriter, r *http.Request) ([]byte, completionRequest, []string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, completionRequest{}, nil, false
	}
	var req completionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidJSON, clienterr.MessageFor(clienterr.CodeInvalidJSON))
		return nil, completionRequest{}, nil, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, completionRequest{}, nil, false
	}
	prompts, ok := parsePrompt(req.Prompt)
	if !ok || len(prompts) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_prompt", "prompt field must be a non-empty string or string array")
		return nil, completionRequest{}, nil, false
	}
	return body, req, prompts, true
}

func validateCountTokensRequest(w http.ResponseWriter, r *http.Request) ([]byte, countTokensRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, countTokensRequest{}, false
	}
	var req countTokensRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidJSON, clienterr.MessageFor(clienterr.CodeInvalidJSON))
		return nil, countTokensRequest{}, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, countTokensRequest{}, false
	}
	if !messagesPresent(req.Messages) {
		writeJSONError(w, http.StatusBadRequest, "invalid_messages", "messages field must be a non-empty array")
		return nil, countTokensRequest{}, false
	}
	return body, req, true
}

func parsePrompt(raw json.RawMessage) ([]string, bool) {
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

func messagesPresent(raw json.RawMessage) bool {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) == 0 {
		return false
	}
	for _, msg := range messages {
		if len(strings.TrimSpace(string(msg))) == 0 || string(msg) == "null" {
			return false
		}
	}
	return true
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

func usageFromJSON(raw []byte) (completionUsage, bool) {
	var body struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return completionUsage{}, false
	}
	return normalizeUsage(body.Usage.PromptTokens, body.Usage.CompletionTokens, body.Usage.TotalTokens)
}

func usageFromSSE(raw []byte) (completionUsage, bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 64*1024), maxUpstreamBodyBytes)
	var last completionUsage
	found := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if usage, ok := usageFromJSON([]byte(payload)); ok {
			last = usage
			found = true
		}
	}
	return last, found
}

func normalizeUsage(prompt, completion, total int) (completionUsage, bool) {
	if prompt < 0 || completion < 0 || total < 0 {
		return completionUsage{}, false
	}
	if total == 0 && (prompt > 0 || completion > 0) {
		total = prompt + completion
	}
	if prompt <= 0 && completion <= 0 && total <= 0 {
		return completionUsage{}, false
	}
	return completionUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total}, true
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("completions upstream response exceeds %d bytes", maxUpstreamBodyBytes)
	}
	return raw, nil
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
