package videoclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const (
	providerVideo = "video"
	taskTypeVideo = "video_generate"
)

type Request struct {
	RequestID      string          `json:"request_id,omitempty"`
	RequestIDAlias string          `json:"requestId,omitempty"`
	APIKeyID       int64           `json:"api_key_id,omitempty"`
	APIKeyIDAlias  int64           `json:"apiKeyId,omitempty"`
	Model          string          `json:"model,omitempty"`
	Prompt         string          `json:"prompt,omitempty"`
	Image          json.RawMessage `json:"image,omitempty"`
	Duration       json.RawMessage `json:"duration,omitempty"`
	Width          json.RawMessage `json:"width,omitempty"`
	Height         json.RawMessage `json:"height,omitempty"`
	FPS            json.RawMessage `json:"fps,omitempty"`
	Seed           json.RawMessage `json:"seed,omitempty"`
	N              json.RawMessage `json:"n,omitempty"`
	ResponseFormat string          `json:"response_format,omitempty"`
}

func translateSubmit(raw json.RawMessage) (mediatask.SubmitInput, error) {
	raw = trimRaw(raw)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return mediatask.SubmitInput{}, fmt.Errorf("%w: invalid video json", mediatask.ErrInvalidInput)
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(req.RequestIDAlias)
	}
	if requestID == "" {
		requestID = "video-" + uuid.NewString()
	}
	apiKeyID, err := mediatask.ResolveAPIKeySelection(req.APIKeyID, req.APIKeyIDAlias)
	if err != nil {
		return mediatask.SubmitInput{}, err
	}
	return mediatask.SubmitInput{
		RequestID:   requestID,
		TaskType:    taskTypeVideo,
		Provider:    providerVideo,
		InputParams: raw,
		APIKeyID:    apiKeyID,
	}, nil
}

func trimRaw(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
