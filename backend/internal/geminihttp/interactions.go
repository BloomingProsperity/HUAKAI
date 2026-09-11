package geminihttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	protogemini "github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
)

const (
	ActionCreateInteraction      = "createInteraction"
	ActionGetInteraction         = "getInteraction"
	endpointFamilyInteractions   = "gemini_interactions"
	officialInteractionsPath     = "/v1beta/interactions"
	officialInteractionsRevision = "2026-05-20"
)

func serveGeminiInteractions(w http.ResponseWriter, r *http.Request, d Deps) {
	if d.Gateway == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Gemini native gateway dependency unset")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if r.URL.Path != officialInteractionsPath {
			writeJSONError(w, http.StatusNotFound, "unknown_route", "Gemini interactions create must be POST /v1beta/interactions")
			return
		}
		serveGeminiInteractionCreate(w, r, d)
	case http.MethodGet:
		serveGeminiInteractionGet(w, r, d)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Gemini interactions only accepts POST or GET")
	}
}

func serveGeminiInteractionCreate(w http.ResponseWriter, r *http.Request, d Deps) {
	body, ok := readRequestBody(w, r)
	if !ok {
		return
	}
	model, agent, stream, err := parseInteractionsCreate(body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return
	}
	routingModel := model
	if routingModel == "" {
		routingModel = agent
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	d.Gateway.ServeNativeClient(w, r, gatewayhttp.NativeClientRequest{
		Model:          routingModel,
		Action:         ActionCreateInteraction,
		Stream:         stream,
		ClientProtocol: proto.ClientProtocolGemini,
		ClientAdapter:  &protogemini.GeminiClient{},
		EndpointFamily: endpointFamilyInteractions,
		EndpointPath:   officialInteractionsPath,
		HTTPMethod:     http.MethodPost,
	})
}

func serveGeminiInteractionGet(w http.ResponseWriter, r *http.Request, d Deps) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, officialInteractionsPath+"/"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONError(w, http.StatusNotFound, "unknown_route", "Gemini interactions retrieve must be GET /v1beta/interactions/{id}")
		return
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "retrieve requires model query to select an authorized Gemini account")
		return
	}
	stream := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("stream")), "true")
	d.Gateway.ServeNativeClient(w, r, gatewayhttp.NativeClientRequest{
		Model:          model,
		Action:         ActionGetInteraction,
		Stream:         stream,
		ClientProtocol: proto.ClientProtocolGemini,
		ClientAdapter:  &protogemini.GeminiClient{},
		EndpointFamily: endpointFamilyInteractions,
		EndpointPath:   officialInteractionsPath + "/" + url.PathEscape(id),
		HTTPMethod:     http.MethodGet,
	})
}

func parseInteractionsCreate(body []byte) (model, agent string, stream bool, err error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return "", "", false, errInvalidInteractionsJSON
	}
	model = jsonRawString(root["model"])
	agent = jsonRawString(root["agent"])
	if model == "" && agent == "" {
		return "", "", false, errInteractionsNeedModelOrAgent
	}
	if raw, ok := root["stream"]; ok {
		if json.Unmarshal(raw, &stream) != nil {
			return "", "", false, errInvalidInteractionsStream
		}
	}
	return model, agent, stream, nil
}

var (
	errInvalidInteractionsJSON      = interactionsError("request body must be a JSON object")
	errInteractionsNeedModelOrAgent = interactionsError("exactly one of model or agent is required")
	errInvalidInteractionsStream    = interactionsError("stream must be a boolean")
)

type interactionsError string

func (e interactionsError) Error() string { return string(e) }

func jsonRawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
