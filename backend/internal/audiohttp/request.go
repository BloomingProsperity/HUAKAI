package audiohttp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
)

type audioRequest struct {
	Model          string
	Input          string
	Voice          string
	ResponseFormat string
	File           audioFile
	Fields         map[string]string
}

type audioFile struct {
	Name        string
	ContentType string
	Data        []byte
}

func validateAudioRequest(w http.ResponseWriter, r *http.Request, endpoint audioEndpoint) ([]byte, string, audioRequest, bool) {
	if endpoint == audioEndpointSpeech {
		body, req, ok := validateSpeechRequest(w, r)
		return body, "application/json", req, ok
	}
	return validateMultipartRequest(w, r)
}

func validateSpeechRequest(w http.ResponseWriter, r *http.Request) ([]byte, audioRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, audioRequest{}, false
	}
	var raw struct {
		Model          string `json:"model"`
		Input          string `json:"input"`
		Voice          string `json:"voice"`
		ResponseFormat string `json:"response_format"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidJSON, clienterr.MessageFor(clienterr.CodeInvalidJSON))
		return nil, audioRequest{}, false
	}
	if strings.TrimSpace(raw.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, audioRequest{}, false
	}
	if strings.TrimSpace(raw.Input) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_input", "input field required")
		return nil, audioRequest{}, false
	}
	if utf8.RuneCountInString(raw.Input) > maxSpeechInputRunes {
		writeJSONError(w, http.StatusBadRequest, "input_too_long", "input exceeds audio speech maximum")
		return nil, audioRequest{}, false
	}
	return body, audioRequest{
		Model:          strings.TrimSpace(raw.Model),
		Input:          raw.Input,
		Voice:          strings.TrimSpace(raw.Voice),
		ResponseFormat: strings.TrimSpace(raw.ResponseFormat),
	}, true
}

func validateMultipartRequest(w http.ResponseWriter, r *http.Request) ([]byte, string, audioRequest, bool) {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || strings.TrimSpace(params["boundary"]) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_multipart", "multipart/form-data body required")
		return nil, "", audioRequest{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		code := clienterr.CodeBodyReadError
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			status = http.StatusRequestEntityTooLarge
			code = "body_too_large"
		}
		writeJSONError(w, status, code, clienterr.MessageFor(code))
		return nil, "", audioRequest{}, false
	}
	return parseMultipartAudioBody(w, body, contentType, params["boundary"])
}

func parseMultipartAudioBody(w http.ResponseWriter, body []byte, contentType, boundary string) ([]byte, string, audioRequest, bool) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	fields := map[string]string{}
	var file audioFile
	fileParts := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_multipart", "multipart body parse failed")
			return nil, "", audioRequest{}, false
		}
		name := part.FormName()
		if name == "file" {
			fileParts++
			if fileParts > 1 {
				writeJSONError(w, http.StatusBadRequest, "multiple_files", "only one file part is allowed")
				return nil, "", audioRequest{}, false
			}
			data, err := io.ReadAll(part)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
				return nil, "", audioRequest{}, false
			}
			if len(data) == 0 {
				writeJSONError(w, http.StatusBadRequest, "empty_file", "file part must be non-empty")
				return nil, "", audioRequest{}, false
			}
			file = audioFile{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Data: data}
			continue
		}
		if !allowedMultipartField(name) {
			writeJSONError(w, http.StatusBadRequest, "unsupported_field", fmt.Sprintf("multipart field %q is not allowed", name))
			return nil, "", audioRequest{}, false
		}
		value, err := io.ReadAll(part)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
			return nil, "", audioRequest{}, false
		}
		fields[name] = string(value)
	}
	if fileParts == 0 {
		writeJSONError(w, http.StatusBadRequest, "missing_file", "file part required")
		return nil, "", audioRequest{}, false
	}
	model := strings.TrimSpace(fields["model"])
	if model == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, "", audioRequest{}, false
	}
	return body, contentType, audioRequest{
		Model:          model,
		ResponseFormat: strings.TrimSpace(fields["response_format"]),
		File:           file,
		Fields:         fields,
	}, true
}

func allowedMultipartField(name string) bool {
	switch name {
	case "model", "language", "prompt", "response_format", "temperature":
		return true
	default:
		return false
	}
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
