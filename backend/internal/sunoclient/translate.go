package sunoclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const providerSuno = "suno"

type Request struct {
	RequestID            string          `json:"request_id,omitempty"`
	RequestIDAlias       string          `json:"requestId,omitempty"`
	GPTDescriptionPrompt string          `json:"gpt_description_prompt,omitempty"`
	Prompt               string          `json:"prompt,omitempty"`
	MV                   string          `json:"mv,omitempty"`
	Title                string          `json:"title,omitempty"`
	Tags                 string          `json:"tags,omitempty"`
	ContinueAt           json.RawMessage `json:"continue_at,omitempty"`
	ContinueClipID       string          `json:"continue_clip_id,omitempty"`
	MakeInstrumental     *bool           `json:"make_instrumental,omitempty"`
	ModelVersion         string          `json:"model_version,omitempty"`
	CustomMode           bool            `json:"custom_mode,omitempty"`
	Input                string          `json:"input,omitempty"`
	NotifyHook           string          `json:"notify_hook,omitempty"`
}

func translateSubmit(action string, raw json.RawMessage) (mediatask.SubmitInput, error) {
	raw = trimRaw(raw)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return mediatask.SubmitInput{}, fmt.Errorf("%w: invalid suno json", mediatask.ErrInvalidInput)
	}
	taskType, err := sunoTaskType(action, req.CustomMode)
	if err != nil {
		return mediatask.SubmitInput{}, err
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(req.RequestIDAlias)
	}
	if requestID == "" {
		requestID = "suno-" + uuid.NewString()
	}
	return mediatask.SubmitInput{
		RequestID:   requestID,
		TaskType:    taskType,
		Provider:    providerSuno,
		InputParams: raw,
	}, nil
}

func sunoTaskType(action string, customMode bool) (string, error) {
	action = strings.TrimSpace(action)
	if action == "" {
		if customMode {
			return "suno_custom", nil
		}
		return "suno_generate", nil
	}
	for _, r := range action {
		if isActionChar(r) {
			continue
		}
		return "", fmt.Errorf("%w: unsupported suno action", mediatask.ErrInvalidInput)
	}
	action = strings.ToLower(strings.ReplaceAll(action, "-", "_"))
	return "suno_" + action, nil
}

func isActionChar(r rune) bool {
	return r == '-' || r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

func trimRaw(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
