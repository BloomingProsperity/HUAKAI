package moderation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var errModerationInput = errors.New("moderation: user input unavailable")

type extractedInput struct {
	AllText   string
	Excerpt   string
	ImageURLs []string
}

func extractModerationInput(protocol string, body []byte) (extractedInput, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return extractedInput{}, errModerationInput
	}
	switch protocol {
	case "openai_chat":
		return extractMessageInput(body, protocol)
	case "anthropic_messages":
		return extractMessageInput(body, protocol)
	case "openai_responses":
		return extractResponsesInput(body)
	case "gemini":
		return extractGeminiInput(body)
	default:
		return extractedInput{}, errModerationInput
	}
}

func extractMessageInput(body []byte, protocol string) (extractedInput, error) {
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	messages := make([]string, 0, len(payload.Messages))
	imageURLs := make([]string, 0)
	hasUserInput := false
	for _, message := range payload.Messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		text, images, hasContent, err := contentFromParts(message.Content, protocol)
		if err != nil {
			return extractedInput{}, err
		}
		hasUserInput = hasUserInput || hasContent
		if text != "" {
			messages = append(messages, text)
		}
		imageURLs = append(imageURLs, images...)
	}
	return finishExtractedInput(messages, imageURLs, hasUserInput)
}

func extractResponsesInput(body []byte) (extractedInput, error) {
	var payload struct {
		Input json.RawMessage `json:"input"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	if text := rawJSONString(payload.Input); text != "" {
		return finishExtractedInput([]string{text}, nil, true)
	}
	var items []struct {
		Role     string          `json:"role"`
		Type     string          `json:"type"`
		Content  json.RawMessage `json:"content"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(payload.Input, &items); err != nil {
		return extractedInput{}, errModerationInput
	}
	messages := make([]string, 0, len(items))
	imageURLs := make([]string, 0)
	hasUserInput := false
	for _, item := range items {
		role := strings.TrimSpace(item.Role)
		itemType := strings.ToLower(strings.TrimSpace(item.Type))
		if role != "" && !strings.EqualFold(role, "user") {
			continue
		}
		if role == "" && itemType != "input_text" && itemType != "input_image" {
			continue
		}
		switch itemType {
		case "input_text":
			if text := strings.TrimSpace(item.Text); text != "" {
				messages = append(messages, text)
				hasUserInput = true
			}
		case "input_image":
			imageURL := imageURLFromRaw(item.ImageURL)
			if imageURL == "" {
				return extractedInput{}, errModerationInput
			}
			imageURLs = append(imageURLs, imageURL)
			hasUserInput = true
		default:
			text, images, hasContent, err := contentFromParts(item.Content, "openai_responses")
			if err != nil {
				return extractedInput{}, err
			}
			if text != "" {
				messages = append(messages, text)
			}
			imageURLs = append(imageURLs, images...)
			hasUserInput = hasUserInput || hasContent
		}
	}
	return finishExtractedInput(messages, imageURLs, hasUserInput)
}

func extractGeminiInput(body []byte) (extractedInput, error) {
	var payload struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
				FileData *struct {
					FileURI string `json:"fileUri"`
				} `json:"fileData"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := decodeModerationJSON(body, &payload); err != nil {
		return extractedInput{}, err
	}
	messages := make([]string, 0, len(payload.Contents))
	imageURLs := make([]string, 0)
	hasUserInput := false
	for _, content := range payload.Contents {
		role := strings.TrimSpace(content.Role)
		if role != "" && !strings.EqualFold(role, "user") {
			continue
		}
		parts := make([]string, 0, len(content.Parts))
		for _, part := range content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
				hasUserInput = true
			}
			if part.InlineData != nil {
				if imageURL := inlineImageDataURL(part.InlineData.MIMEType, part.InlineData.Data); imageURL != "" {
					imageURLs = append(imageURLs, imageURL)
					hasUserInput = true
				}
			}
			if part.FileData != nil {
				if imageURL := strings.TrimSpace(part.FileData.FileURI); imageURL != "" {
					imageURLs = append(imageURLs, imageURL)
					hasUserInput = true
				}
			}
		}
		if len(parts) > 0 {
			messages = append(messages, strings.Join(parts, "\n"))
		}
	}
	return finishExtractedInput(messages, imageURLs, hasUserInput)
}

func decodeModerationJSON(body []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(dst); err != nil {
		return errModerationInput
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errModerationInput
	}
	return nil
}

func contentFromParts(raw json.RawMessage, protocol string) (string, []string, bool, error) {
	if text := rawJSONString(raw); text != "" {
		return text, nil, true, nil
	}
	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
		Source   *struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
			URL       string `json:"url"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, false, errModerationInput
	}
	out := make([]string, 0, len(parts))
	imageURLs := make([]string, 0)
	hasContent := false
	for _, part := range parts {
		partType := strings.ToLower(strings.TrimSpace(part.Type))
		switch partType {
		case "", "text", "input_text":
			if text := strings.TrimSpace(part.Text); text != "" {
				out = append(out, text)
				hasContent = true
			}
		case "image_url", "input_image":
			if protocol == "anthropic_messages" {
				continue
			}
			imageURL := imageURLFromRaw(part.ImageURL)
			if imageURL == "" {
				return "", nil, false, errModerationInput
			}
			imageURLs = append(imageURLs, imageURL)
			hasContent = true
		case "image":
			if protocol != "anthropic_messages" || part.Source == nil {
				continue
			}
			var imageURL string
			switch strings.ToLower(strings.TrimSpace(part.Source.Type)) {
			case "base64":
				imageURL = inlineImageDataURL(part.Source.MediaType, part.Source.Data)
			case "url":
				imageURL = strings.TrimSpace(part.Source.URL)
			}
			if imageURL == "" {
				return "", nil, false, errModerationInput
			}
			imageURLs = append(imageURLs, imageURL)
			hasContent = true
		}
	}
	return strings.Join(out, "\n"), imageURLs, hasContent, nil
}

func rawJSONString(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func imageURLFromRaw(raw json.RawMessage) string {
	if value := rawJSONString(raw); value != "" {
		return value
	}
	var value struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value.URL)
}

func inlineImageDataURL(mediaType, data string) string {
	mediaType = strings.TrimSpace(mediaType)
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(strings.ToLower(mediaType), "image/") || data == "" {
		return ""
	}
	return "data:" + mediaType + ";base64," + data
}

func finishExtractedInput(messages, imageURLs []string, hasUserInput bool) (extractedInput, error) {
	clean := messages[:0]
	for _, message := range messages {
		if message = strings.TrimSpace(message); message != "" {
			clean = append(clean, message)
		}
	}
	if !hasUserInput || len(clean) == 0 && len(imageURLs) == 0 {
		return extractedInput{}, errModerationInput
	}
	var excerpt string
	if len(clean) > 0 {
		excerpt = redactModerationExcerpt(clean[len(clean)-1])
	}
	return extractedInput{
		AllText:   strings.Join(clean, "\n"),
		Excerpt:   excerpt,
		ImageURLs: imageURLs,
	}, nil
}
