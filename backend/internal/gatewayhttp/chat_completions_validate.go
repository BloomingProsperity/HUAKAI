package gatewayhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	Stream              bool          `json:"stream"`
	MaxTokens           *int          `json:"max_tokens"`
	MaxCompletionTokens *int          `json:"max_completion_tokens"`
	MaxOutputTokens     *int          `json:"max_output_tokens"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content 保留 raw JSON：OpenAI Chat 使用 string，Anthropic Messages API
	// 允许 string 或 content block 数组，不能在入口校验时静默丢失。
	Content json.RawMessage `json:"content"`
}

type chatValidatedRequest struct {
	Body           []byte
	Request        chatRequest
	ClientProtocol proto.ClientProtocol
	ClientAdapter  proto.ClientAdapter
	RequestID      string
}

func validateChatCompletionsRequest(w http.ResponseWriter, r *http.Request, ctx context.Context) (chatValidatedRequest, bool) {
	body, ok := readChatRequestBody(w, r, ctx)
	if !ok {
		return chatValidatedRequest{}, false
	}
	if !rejectRemovedBodyFields(w, body) {
		return chatValidatedRequest{}, false
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeLoggedJSONError(ctx, middleware.GetReqID(ctx), w, http.StatusBadRequest, clienterr.CodeInvalidJSON, err)
		return chatValidatedRequest{}, false
	}
	clientProtocol, clientAdapter, ok := validateClientProtocol(w, r, req)
	if !ok {
		return chatValidatedRequest{}, false
	}
	if req.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return chatValidatedRequest{}, false
	}
	requestID := middleware.GetReqID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return chatValidatedRequest{
		Body:           body,
		Request:        req,
		ClientProtocol: clientProtocol,
		ClientAdapter:  clientAdapter,
		RequestID:      requestID,
	}, true
}

func readChatRequestBody(w http.ResponseWriter, r *http.Request, ctx context.Context) ([]byte, bool) {
	// 保留客户端原始 body，后续 dispatcher 直接交给 provider adapter。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeLoggedJSONError(ctx, middleware.GetReqID(ctx), w, http.StatusBadRequest, clienterr.CodeBodyReadError, err)
		return nil, false
	}
	return body, true
}

func rejectRemovedBodyFields(w http.ResponseWriter, body []byte) bool {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err == nil {
		if _, found := keys["pool_group_id"]; found {
			writeJSONError(w, http.StatusBadRequest, "body_field_disallowed",
				"pool_group_id field removed in N+5b; the gateway resolves the pool from the model alias")
			return false
		}
	}
	return true
}

func validateClientProtocol(w http.ResponseWriter, r *http.Request, req chatRequest) (proto.ClientProtocol, proto.ClientAdapter, bool) {
	var clientProtocol proto.ClientProtocol
	var clientAdapter proto.ClientAdapter
	if inferred, ok := proto.ClientProtocolByIngressPath(r.URL.Path); ok {
		clientProtocol = inferred
	} else if !req.Stream {
		writeJSONError(w, http.StatusNotFound, "unknown_route",
			fmt.Sprintf("no client protocol registered for ingress path %q", r.URL.Path))
		return "", nil, false
	}
	if !req.Stream {
		var adapterOK bool
		clientAdapter, adapterOK = proto.DefaultClientAdapterRegistry().Lookup(clientProtocol)
		if !adapterOK {
			writeJSONError(w, http.StatusServiceUnavailable, "adapter_unregistered",
				fmt.Sprintf("client adapter not registered for protocol %q", clientProtocol))
			return "", nil, false
		}
	}
	return clientProtocol, clientAdapter, true
}
