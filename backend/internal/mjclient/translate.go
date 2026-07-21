package mjclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const providerMidjourney = "midjourney"

var submitTaskTypes = map[string]string{
	"imagine":               "mj_imagine",
	"describe":              "mj_describe",
	"blend":                 "mj_blend",
	"change":                "mj_change",
	"simple-change":         "mj_simple_change",
	"action":                "mj_action",
	"modal":                 "mj_modal",
	"shorten":               "mj_shorten",
	"edits":                 "mj_edits",
	"video":                 "mj_video",
	"upload-discord-images": "mj_upload_discord_images",
}

type Request struct {
	RequestID      string          `json:"request_id,omitempty"`
	RequestIDAlias string          `json:"requestId,omitempty"`
	APIKeyID       int64           `json:"api_key_id,omitempty"`
	APIKeyIDAlias  int64           `json:"apiKeyId,omitempty"`
	Prompt         string          `json:"prompt,omitempty"`
	CustomID       string          `json:"customId,omitempty"`
	BotType        string          `json:"botType,omitempty"`
	NotifyHook     string          `json:"notifyHook,omitempty"`
	Action         string          `json:"action,omitempty"`
	State          string          `json:"state,omitempty"`
	Base64Array    []string        `json:"base64Array,omitempty"`
	Index          json.RawMessage `json:"index,omitempty"`
	MaskBase64     string          `json:"maskBase64,omitempty"`
	SourceBase64   string          `json:"sourceBase64,omitempty"`
	TargetBase64   string          `json:"targetBase64,omitempty"`
}

func translateSubmit(action string, raw json.RawMessage) (mediatask.SubmitInput, error) {
	action = strings.TrimSpace(action)
	taskType, ok := submitTaskTypes[action]
	if !ok {
		return mediatask.SubmitInput{}, fmt.Errorf("%w: unsupported mj action", mediatask.ErrInvalidInput)
	}
	return translateTask(taskType, raw)
}

func translateSwap(raw json.RawMessage) (mediatask.SubmitInput, error) {
	return translateTask("mj_swap_face", raw)
}

func translateTask(taskType string, raw json.RawMessage) (mediatask.SubmitInput, error) {
	raw = trimRaw(raw)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return mediatask.SubmitInput{}, fmt.Errorf("%w: invalid mj json", mediatask.ErrInvalidInput)
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(req.RequestIDAlias)
	}
	if requestID == "" {
		requestID = "mj-" + uuid.NewString()
	}
	apiKeyID, err := mediatask.ResolveAPIKeySelection(req.APIKeyID, req.APIKeyIDAlias)
	if err != nil {
		return mediatask.SubmitInput{}, err
	}
	return mediatask.SubmitInput{
		RequestID:   requestID,
		TaskType:    taskType,
		Provider:    providerMidjourney,
		InputParams: raw,
		APIKeyID:    apiKeyID,
	}, nil
}

func trimRaw(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
