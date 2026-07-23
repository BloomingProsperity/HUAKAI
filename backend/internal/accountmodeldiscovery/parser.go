package accountmodeldiscovery

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

func parsePage(kind parserKind, family string, body []byte) ([]Model, url.Values, error) {
	switch kind {
	case parserOpenAI:
		return parseOpenAIModels(family, body)
	case parserCodex:
		return parseCodexModels(family, body)
	case parserAnthropic:
		return parseAnthropicModels(family, body)
	case parserGemini:
		return parseGeminiModels(family, body)
	case parserCloudCode:
		return parseCloudCodeModels(family, body)
	default:
		return nil, nil, fmt.Errorf("未知模型目录解析器 %q", kind)
	}
}

func parseOpenAIModels(family string, body []byte) ([]Model, url.Values, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, err
	}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		var capabilities []string
		switch family {
		case registrydefault.ProtocolOpenAIChat:
			capabilities = modelsync.ClassifyOpenAIModel(item.ID)
		case registrydefault.ProtocolGrokChat:
			capabilities = modelsync.ClassifyGrokModel(item.ID)
		default:
			// 其余 openai-compatible 车道也按模型名派生媒体能力,嵌入/重排模型
			// 不再被一刀切标成 chat(媒体能力门 fail-closed 会拒 chat-only 声明)。
			capabilities = modelsync.ClassifyOpenAICompatibleModel(item.ID)
		}
		models = append(models, Model{ID: item.ID, DisplayName: item.ID, ProtocolFamily: family, Capabilities: capabilities})
	}
	return models, nil, nil
}

func parseCodexModels(family string, body []byte) ([]Model, url.Values, error) {
	var payload struct {
		Models []struct {
			Slug            string   `json:"slug"`
			DisplayName     string   `json:"display_name"`
			InputModalities []string `json:"input_modalities"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, err
	}
	models := make([]Model, 0, len(payload.Models))
	for _, item := range payload.Models {
		capabilities := []string{"chat", "responses", "tools"}
		for _, modality := range item.InputModalities {
			switch strings.ToLower(strings.TrimSpace(modality)) {
			case "image":
				capabilities = append(capabilities, "vision")
			case "audio":
				capabilities = append(capabilities, "audio_input")
			}
		}
		models = append(models, Model{ID: item.Slug, DisplayName: item.DisplayName, ProtocolFamily: family, Capabilities: capabilities})
	}
	return models, nil, nil
}

func parseAnthropicModels(family string, body []byte) ([]Model, url.Values, error) {
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, err
	}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, Model{ID: item.ID, DisplayName: item.DisplayName, ProtocolFamily: family, Capabilities: []string{"messages", "stream", "tools"}})
	}
	if payload.HasMore && strings.TrimSpace(payload.LastID) != "" {
		return models, url.Values{"after_id": []string{strings.TrimSpace(payload.LastID)}}, nil
	}
	return models, nil, nil
}

func parseGeminiModels(family string, body []byte) ([]Model, url.Values, error) {
	var payload struct {
		Models []struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			InputTokenLimit            int      `json:"inputTokenLimit"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, err
	}
	models := make([]Model, 0, len(payload.Models))
	for _, item := range payload.Models {
		id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
		models = append(models, Model{ID: id, DisplayName: item.DisplayName, ProtocolFamily: family,
			ContextWindow: item.InputTokenLimit,
			Capabilities:  modelsync.NormalizeGeminiCapabilities(id, item.SupportedGenerationMethods)})
	}
	if token := strings.TrimSpace(payload.NextPageToken); token != "" {
		return models, url.Values{"pageToken": []string{token}}, nil
	}
	return models, nil, nil
}

func parseCloudCodeModels(family string, body []byte) ([]Model, url.Values, error) {
	var payload struct {
		Models map[string]struct {
			DisplayName        string          `json:"displayName"`
			SupportsImages     *bool           `json:"supportsImages"`
			SupportsThinking   *bool           `json:"supportsThinking"`
			MaxTokens          int             `json:"maxTokens"`
			SupportedMimeTypes map[string]bool `json:"supportedMimeTypes"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, err
	}
	models := make([]Model, 0, len(payload.Models))
	for id, item := range payload.Models {
		capabilities := modelsync.NormalizeGeminiCapabilities(id, []string{"generateContent", "countTokens"})
		capabilities = append(capabilities, "chat", "stream", "tools")
		if item.SupportsImages != nil && *item.SupportsImages {
			capabilities = append(capabilities, "vision")
		}
		if item.SupportsThinking != nil && *item.SupportsThinking {
			capabilities = append(capabilities, "reasoning")
		}
		models = append(models, Model{ID: id, DisplayName: item.DisplayName, ProtocolFamily: family,
			ContextWindow: item.MaxTokens, Capabilities: capabilities})
	}
	return models, nil, nil
}
