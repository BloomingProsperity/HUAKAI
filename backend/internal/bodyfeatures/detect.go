// Package bodyfeatures extracts request-level capability signals from a raw
// client request body so the Router can demand those capabilities of pool
// accounts (capability_flags @> required_capabilities).
//
// The single entry point Detect is protocol-agnostic: one request handler
// backs OpenAI Chat Completions, Anthropic Messages, and OpenAI Responses,
// so the scan recognises all three body shapes. It is deliberately
// defensive — any malformed, null, empty, or wrong-typed body yields all
// false (no capability constraint, never a panic) so a parse hiccup can
// never shrink the eligible account set or crash the dispatch path.
package bodyfeatures

import (
	"encoding/json"
	"strings"
)

// Detect scans a raw request body and reports which routable capabilities
// the request actually needs. The booleans align with the capability tokens
// the Router emits and accounts are stamped with:
//
//	vision -> "vision", tools -> "tools", json -> "json".
//
// Detection is conservative: only a genuinely-present signal flips a flag
// (a non-empty tools/functions array, a real image part with a usable URL,
// a json_object/json_schema response format). Unknown or empty signals stay
// false so the request is not over-constrained.
func Detect(body []byte) (vision, tools, json bool) {
	if len(body) == 0 {
		return false, false, false
	}
	var doc looseRequest
	if err := unmarshal(body, &doc); err != nil {
		return false, false, false
	}
	vision = detectVision(doc)
	tools = detectTools(doc)
	json = detectJSON(doc)
	return vision, tools, json
}

// unmarshal is a thin seam over the stdlib decoder so the package keeps a
// single tolerant parse of the (<=1MB) body; callers run Detect once before
// the attempt loop because capabilities are stable across retries.
func unmarshal(body []byte, v *looseRequest) error {
	return json.Unmarshal(body, v)
}

// looseRequest holds only the fields that carry capability signals across
// the three protocols, leaving content as RawMessage so the per-part walk
// is deferred until a flag is actually in question.
type looseRequest struct {
	// OpenAI Chat / Anthropic Messages
	Messages []looseMessage `json:"messages"`
	// OpenAI Responses (input may be a string or an array of parts)
	Input json.RawMessage `json:"input"`

	// tools / function-call signals across all three shapes
	Tools     json.RawMessage `json:"tools"`
	Functions json.RawMessage `json:"functions"`

	// structured-output signals
	ResponseFormat json.RawMessage `json:"response_format"`
	Text           json.RawMessage `json:"text"`
}

type looseMessage struct {
	// Content is string (OpenAI Chat simple) or an array of typed parts
	// (OpenAI Chat multimodal / Anthropic content blocks).
	Content json.RawMessage `json:"content"`
}

// --- vision -----------------------------------------------------------------

// detectVision reports whether any message or Responses-input part carries a
// media payload that requires a vision-class account. It covers OpenAI Chat
// content parts (image_url / input_audio routed elsewhere / file / video_url),
// Anthropic image blocks (type "image" with a source), and OpenAI Responses
// input_image parts (data-URI image_url).
func detectVision(doc looseRequest) bool {
	for _, msg := range doc.Messages {
		if contentHasVisionPart(msg.Content) {
			return true
		}
	}
	return contentHasVisionPart(doc.Input)
}

// contentHasVisionPart walks a content value that may be a bare string, an
// array of typed parts, or junk. Only an array can hold media parts; every
// lookup is ok-guarded so adversarial shapes fall through to false.
func contentHasVisionPart(raw json.RawMessage) bool {
	parts, ok := asArray(raw)
	if !ok {
		return false
	}
	for _, part := range parts {
		obj, ok := asObject(part)
		if !ok {
			continue
		}
		if partIsVision(obj) {
			return true
		}
	}
	return false
}

// partIsVision classifies a single content part. The part "type" token spans
// protocols: OpenAI Chat uses image_url/file/video_url; Anthropic uses image
// (with a non-empty source); OpenAI Responses uses input_image. An image part
// with no usable URL is skipped to avoid the empty-image false positive.
func partIsVision(obj map[string]json.RawMessage) bool {
	partType := stringField(obj, "type")
	switch partType {
	case "image_url":
		return imageURLPresent(obj["image_url"])
	case "input_image":
		// Responses input_image carries a data-URI/url at the part level.
		return nonEmptyString(obj["image_url"]) && !isEmptyDataURI(stringValue(obj["image_url"]))
	case "image":
		// Anthropic image block requires a source object/value to be real.
		return present(obj["source"])
	case "file", "input_file", "video_url":
		// Document/video media is vision-class media content; only count it
		// when the carrying field is actually present, not an empty stub.
		return present(obj["file"]) || present(obj["file_url"]) ||
			present(obj["video_url"]) || present(obj["image_url"]) ||
			nonEmptyString(obj["file_id"])
	default:
		return false
	}
}

// imageURLPresent handles the OpenAI Chat image_url part whose payload is an
// object {url:...} (or, tolerantly, a bare string), skipping empty or empty
// base64 data URIs so a placeholder part does not over-constrain routing.
func imageURLPresent(raw json.RawMessage) bool {
	if !present(raw) {
		return false
	}
	if s, ok := tryString(raw); ok {
		return s != "" && !isEmptyDataURI(s)
	}
	obj, ok := asObject(raw)
	if !ok {
		return false
	}
	url := stringField(obj, "url")
	return url != "" && !isEmptyDataURI(url)
}

// --- tools -------------------------------------------------------------------

// detectTools reports whether the request supplies callable tools. It treats
// a non-empty top-level tools[] (OpenAI Chat / Anthropic / Responses) or the
// legacy functions[] (OpenAI) as the signal; an empty array is not a signal.
func detectTools(doc looseRequest) bool {
	return nonEmptyArray(doc.Tools) || nonEmptyArray(doc.Functions)
}

// --- json --------------------------------------------------------------------

// detectJSON reports whether the request asks for structured output. OpenAI
// Chat/Responses use response_format.type in {json_object, json_schema};
// OpenAI Responses also nests the format under text.format.type. A "text"
// format type (or any other value) is not a structured-output request.
func detectJSON(doc looseRequest) bool {
	if formatTypeIsJSON(doc.ResponseFormat) {
		return true
	}
	// Responses text.format.{type|json_schema}.
	if obj, ok := asObject(doc.Text); ok {
		if formatTypeIsJSON(obj["format"]) {
			return true
		}
	}
	return false
}

// formatTypeIsJSON inspects a response_format / text.format object and reports
// whether its "type" selects JSON output. Tolerant of non-object shapes.
func formatTypeIsJSON(raw json.RawMessage) bool {
	obj, ok := asObject(raw)
	if !ok {
		return false
	}
	switch stringField(obj, "type") {
	case "json_object", "json_schema":
		return true
	default:
		return false
	}
}

// --- low-level tolerant helpers ---------------------------------------------

// asArray decodes raw into a JSON array of raw elements, returning ok=false
// for null, strings, numbers, objects, or malformed input.
func asArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if !present(raw) {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	return arr, true
}

// asObject decodes raw into a JSON object map, returning ok=false for any
// non-object shape so callers can comma-ok guard every field access.
func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if !present(raw) {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	if obj == nil {
		return nil, false
	}
	return obj, true
}

// nonEmptyArray reports whether raw decodes to an array with at least one
// element. Empty arrays and non-arrays are not signals.
func nonEmptyArray(raw json.RawMessage) bool {
	arr, ok := asArray(raw)
	return ok && len(arr) > 0
}

// stringField reads obj[key] as a string, returning "" when absent or not a
// string.
func stringField(obj map[string]json.RawMessage, key string) string {
	return stringValue(obj[key])
}

// stringValue reads raw as a string, returning "" for any non-string shape.
func stringValue(raw json.RawMessage) string {
	s, _ := tryString(raw)
	return s
}

// tryString reports whether raw is a JSON string and returns its value.
func tryString(raw json.RawMessage) (string, bool) {
	if !present(raw) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// nonEmptyString reports whether raw is a non-empty JSON string.
func nonEmptyString(raw json.RawMessage) bool {
	s, ok := tryString(raw)
	return ok && s != ""
}

// present reports whether raw carries a value distinguishable from JSON null
// or absence.
func present(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// isEmptyDataURI reports whether s is a base64 data URI whose payload is
// empty, so a placeholder image part is not mistaken for a real one.
func isEmptyDataURI(s string) bool {
	if !strings.HasPrefix(s, "data:") {
		return false
	}
	rest := strings.TrimPrefix(s, "data:")
	idx := strings.Index(rest, ";")
	if idx < 0 {
		return false
	}
	rest = rest[idx+1:]
	if !strings.HasPrefix(rest, "base64,") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, "base64,")) == ""
}
