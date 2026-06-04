package imageshttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
)

type imageRequest struct {
	Model    string          `json:"model"`
	Prompt   *string         `json:"prompt,omitempty"`
	N        *int            `json:"n,omitempty"`
	Size     string          `json:"size,omitempty"`
	Quality  string          `json:"quality,omitempty"`
	Image    json.RawMessage `json:"image,omitempty"`
	Images   json.RawMessage `json:"images,omitempty"`
	ImageURL string          `json:"image_url,omitempty"`
}

type tokenImageUsage struct {
	InputTokens  int
	OutputTokens int
	ImageTokens  int
}

func validateRequest(w http.ResponseWriter, r *http.Request, endpoint imageEndpoint) ([]byte, imageRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, imageRequest{}, false
	}
	var req imageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidJSON, clienterr.MessageFor(clienterr.CodeInvalidJSON))
		return nil, imageRequest{}, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, imageRequest{}, false
	}
	switch endpoint {
	case imageEndpointGenerations, imageEndpointEdits:
		if req.Prompt == nil || strings.TrimSpace(*req.Prompt) == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_prompt", "prompt field required")
			return nil, imageRequest{}, false
		}
	case imageEndpointVariations:
		if req.Prompt != nil && strings.TrimSpace(*req.Prompt) != "" {
			writeJSONError(w, http.StatusBadRequest, "unexpected_prompt", "variations endpoint does not accept prompt")
			return nil, imageRequest{}, false
		}
	}
	if endpoint == imageEndpointEdits || endpoint == imageEndpointVariations {
		if !req.hasImageReference() {
			writeJSONError(w, http.StatusBadRequest, "missing_image_reference", "image, images, or image_url reference required")
			return nil, imageRequest{}, false
		}
	}
	if req.N != nil && *req.N <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_n", "n must be positive")
		return nil, imageRequest{}, false
	}
	return body, req, true
}

func (r imageRequest) Amount() int {
	if r.N == nil {
		return 1
	}
	return *r.N
}

func (r imageRequest) NormalizedQuality() string {
	if strings.TrimSpace(r.Quality) == "" {
		return "standard"
	}
	return strings.TrimSpace(r.Quality)
}

func (r imageRequest) PromptText() string {
	if r.Prompt == nil {
		return ""
	}
	return *r.Prompt
}

func (r imageRequest) hasImageReference() bool {
	if strings.TrimSpace(r.ImageURL) != "" {
		return true
	}
	return len(bytesTrimSpace(r.Image)) > 0 || len(bytesTrimSpace(r.Images)) > 0
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func promptCharCount(prompt string) int {
	return utf8.RuneCountInString(prompt)
}

func estimatePromptTokens(prompt string) int {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return 1
	}
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: prompt}}
	if n := (tokencheck.HeuristicEstimator{}).Estimate(blocks); n > 0 {
		return n
	}
	return 1
}

func parseTokenImageUsage(raw []byte) (tokenImageUsage, bool) {
	var body struct {
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				ImageTokens int `json:"image_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return tokenImageUsage{}, false
	}
	if body.Usage.InputTokens <= 0 && body.Usage.OutputTokens <= 0 {
		return tokenImageUsage{}, false
	}
	return tokenImageUsage{
		InputTokens:  body.Usage.InputTokens,
		OutputTokens: body.Usage.OutputTokens,
		ImageTokens:  body.Usage.InputTokensDetails.ImageTokens,
	}, true
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("images upstream response exceeds %d bytes", maxUpstreamBodyBytes)
	}
	return raw, nil
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
