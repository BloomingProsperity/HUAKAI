package hermeschat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
)

type streamState struct {
	assistantText strings.Builder
	blocked       bool
}

func (b *Bridge) handleBlock(ctx context.Context, w io.Writer, flusher http.Flusher, prepared PreparedRequest, state *streamState, raw []byte) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return writeAndFlush(w, flusher, raw)
	}
	if state.blocked {
		return nil
	}
	evt := parseSSE(raw)
	switch evt.name {
	case "conversation":
		return handleConversationEvent(w, flusher, prepared, state, evt.data())
	case "token":
		if delta := tokenDeltaFromData(evt.data()); delta != "" {
			state.assistantText.WriteString(delta)
		}
		return writeAndFlush(w, flusher, raw)
	case "done":
		if err := b.persistDone(ctx, prepared, state, evt.data()); err != nil {
			return writePersistError(w, flusher)
		}
		return writeAndFlush(w, flusher, raw)
	default:
		return writeAndFlush(w, flusher, raw)
	}
}

type sseEvent struct {
	name      string
	dataLines []string
}

func (e sseEvent) data() []byte {
	return []byte(strings.Join(e.dataLines, "\n"))
}

func parseSSE(raw []byte) sseEvent {
	var evt sseEvent
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			evt.name = value
		case "data":
			evt.dataLines = append(evt.dataLines, value)
		}
	}
	return evt
}

func conversationIDFromData(data []byte) int64 {
	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0
	}
	return payload.ID
}

func handleConversationEvent(w io.Writer, flusher http.Flusher, prepared PreparedRequest, state *streamState, data []byte) error {
	if conversationIDFromData(data) != prepared.ConversationID {
		state.blocked = true
		return writeConversationMismatchError(w, flusher)
	}
	return nil
}

func tokenDeltaFromData(data []byte) string {
	var payload struct {
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return payload.Delta
}

func totalTokensFromDone(data []byte) *int32 {
	var payload struct {
		TotalTokens int64 `json:"total_tokens"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.TotalTokens < 0 || payload.TotalTokens > math.MaxInt32 {
		return nil
	}
	value := int32(payload.TotalTokens)
	return &value
}
