package moderation

import (
	"encoding/json"
	"strings"
)

func extractCompletionsInput(body []byte) (extractedInput, error) {
	var payload struct {
		Prompt json.RawMessage `json:"prompt"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	texts := stringOrStringArray(payload.Prompt)
	if len(texts) == 0 {
		return extractedInput{}, errModerationInput
	}
	return finishExtractedInput(texts, nil, true)
}

func extractEmbeddingsInput(body []byte) (extractedInput, error) {
	var payload struct {
		Input json.RawMessage `json:"input"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	texts := stringOrStringArray(payload.Input)
	if len(texts) == 0 {
		// 已登记协议：token 数组等非文本输入本切不扫，视为无用户文本。
		return extractedInput{}, nil
	}
	return finishExtractedInput(texts, nil, true)
}

func extractRerankInput(body []byte) (extractedInput, error) {
	var payload struct {
		Query     string            `json:"query"`
		Documents []json.RawMessage `json:"documents"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	texts := make([]string, 0, len(payload.Documents)+1)
	if query := strings.TrimSpace(payload.Query); query != "" {
		texts = append(texts, query)
	}
	for _, document := range payload.Documents {
		texts = append(texts, documentTexts(document)...)
	}
	if len(texts) == 0 {
		return extractedInput{}, nil
	}
	return finishExtractedInput(texts, nil, true)
}

func extractImagesInput(body []byte) (extractedInput, error) {
	return extractNamedStringFields(body, "prompt")
}

func extractAudioSpeechInput(body []byte) (extractedInput, error) {
	var payload struct {
		Input string `json:"input"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	if text := strings.TrimSpace(payload.Input); text == "" {
		return extractedInput{}, errModerationInput
	}
	return finishExtractedInput([]string{strings.TrimSpace(payload.Input)}, nil, true)
}

func extractAudioTranscriptInput(body []byte) (extractedInput, error) {
	return extractNamedStringFields(body, "prompt")
}

func extractVideoInput(body []byte) (extractedInput, error) {
	texts, err := namedStringFields(body, "prompt")
	if err != nil {
		return extractedInput{}, err
	}
	if len(texts) == 0 {
		return extractedInput{}, errModerationInput
	}
	return finishExtractedInput(texts, nil, true)
}

func extractMediaTaskInput(body []byte) (extractedInput, error) {
	raw := body
	var envelope struct {
		InputParams json.RawMessage `json:"input_params"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.InputParams) > 0 {
		raw = envelope.InputParams
	}
	texts, err := namedStringFields(raw, "prompt", "text", "caption", "input", "query", "title", "gpt_description_prompt", "tags")
	if err != nil {
		return extractedInput{}, err
	}
	if len(texts) == 0 {
		return extractedInput{}, nil
	}
	return finishExtractedInput(texts, nil, true)
}

func extractNamedStringFields(body []byte, fields ...string) (extractedInput, error) {
	texts, err := namedStringFields(body, fields...)
	if err != nil {
		return extractedInput{}, err
	}
	if len(texts) == 0 {
		return extractedInput{}, nil
	}
	return finishExtractedInput(texts, nil, true)
}

func namedStringFields(body []byte, fields ...string) ([]string, error) {
	var obj map[string]json.RawMessage
	if err := decodeModerationJSON(body, &obj); err != nil {
		return nil, err
	}
	texts := make([]string, 0, len(fields))
	for _, field := range fields {
		raw, ok := obj[field]
		if !ok {
			continue
		}
		texts = append(texts, stringOrStringArray(raw)...)
	}
	return texts, nil
}

func documentTexts(raw json.RawMessage) []string {
	if text := rawJSONString(raw); text != "" {
		return []string{text}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if text := rawJSONString(obj["text"]); text != "" {
		return []string{text}
	}
	return nil
}

func stringOrStringArray(raw json.RawMessage) []string {
	if text := rawJSONString(raw); text != "" {
		return []string{text}
	}
	var items []string
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(item); text != "" {
			out = append(out, text)
		}
	}
	return out
}
