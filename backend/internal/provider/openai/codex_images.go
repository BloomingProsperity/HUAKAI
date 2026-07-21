package openai

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const (
	codexImageGenerationPath = "/v1/images/generations"
	codexImageEditPath       = "/v1/images/edits"
	codexImageVariationPath  = "/v1/images/variations"
	codexImageBaseModel      = "gpt-5.5"
	codexImageResultLimit    = 32 << 20
)

type codexImageRequest struct {
	Prompt             string
	ResponseFormat     string
	InputImages        []string
	MaskImage          string
	StringOptions      map[string]string
	IntegerOptions     map[string]int64
	RequestedToolModel string
}

type codexImageResult struct {
	Base64        string
	RevisedPrompt string
	OutputFormat  string
	Size          string
	Quality       string
	Background    string
}

func isCodexImagesEndpoint(path string) bool {
	path = strings.TrimSpace(path)
	switch {
	case strings.HasSuffix(path, codexImageGenerationPath):
		return true
	case strings.HasSuffix(path, codexImageEditPath):
		return true
	case strings.HasSuffix(path, codexImageVariationPath):
		return true
	default:
		return false
	}
}

func buildCodexImagesResponsesBody(in provider.BuildInput) ([]byte, error) {
	parsed, err := parseCodexImageRequest(in)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.Prompt) == "" {
		return nil, errors.New("图片请求缺少 prompt")
	}
	action := "generate"
	if strings.HasSuffix(strings.TrimSpace(in.EndpointPath), codexImageEditPath) {
		action = "edit"
		if len(parsed.InputImages) == 0 {
			return nil, errors.New("图片编辑请求缺少输入图片")
		}
	}
	if strings.HasSuffix(strings.TrimSpace(in.EndpointPath), codexImageVariationPath) {
		return nil, errors.New("codex 图片通道暂不支持 variations")
	}

	baseModel := firstNonEmptyCodexExtra(in.Credential.Extra, "codex_image_base_model", "image_base_model")
	if baseModel == "" {
		baseModel = codexImageBaseModel
	}
	toolModel := strings.TrimSpace(parsed.RequestedToolModel)
	if toolModel == "" {
		toolModel = strings.TrimSpace(in.UpstreamModelID)
	}
	if toolModel == "" {
		return nil, errors.New("图片工具模型为空")
	}

	content := make([]map[string]any, 0, len(parsed.InputImages)+1)
	content = append(content, map[string]any{"type": "input_text", "text": parsed.Prompt})
	for _, imageURL := range parsed.InputImages {
		content = append(content, map[string]any{"type": "input_image", "image_url": imageURL})
	}
	tool := map[string]any{
		"type":   "image_generation",
		"action": action,
		"model":  toolModel,
	}
	for key, value := range parsed.StringOptions {
		if value != "" {
			tool[key] = value
		}
	}
	for key, value := range parsed.IntegerOptions {
		tool[key] = value
	}
	if parsed.MaskImage != "" {
		tool["input_image_mask"] = map[string]any{"image_url": parsed.MaskImage}
	}
	body := map[string]any{
		"model":        baseModel,
		"instructions": "Use the image generation tool to complete the image request.",
		"input": []map[string]any{{
			"type": "message", "role": "user", "content": content,
		}},
		"tools":       []map[string]any{tool},
		"tool_choice": map[string]any{"type": "image_generation"},
		"stream":      true,
		"store":       false,
	}
	return json.Marshal(body)
}

func parseCodexImageRequest(in provider.BuildInput) (codexImageRequest, error) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(in.InboundContentType))
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		boundary := strings.TrimSpace(params["boundary"])
		if boundary == "" {
			return codexImageRequest{}, errors.New("multipart 请求缺少 boundary")
		}
		return parseCodexMultipartImageRequest(in.InboundBody, boundary, in.UpstreamModelID)
	}
	return parseCodexJSONImageRequest(in.InboundBody, in.UpstreamModelID)
}

func parseCodexJSONImageRequest(raw []byte, fallbackModel string) (codexImageRequest, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return codexImageRequest{}, errors.New("图片请求体必须是 JSON object")
	}
	out := newCodexImageRequest(fallbackModel)
	decodeStringField(body, "prompt", &out.Prompt)
	decodeStringField(body, "response_format", &out.ResponseFormat)
	decodeStringField(body, "model", &out.RequestedToolModel)
	for _, key := range []string{"size", "quality", "background", "output_format", "moderation", "input_fidelity", "style"} {
		var value string
		decodeStringField(body, key, &value)
		if value != "" {
			out.StringOptions[key] = value
		}
	}
	for _, key := range []string{"n", "output_compression", "partial_images"} {
		var value int64
		if rawValue, ok := body[key]; ok && json.Unmarshal(rawValue, &value) == nil {
			out.IntegerOptions[key] = value
		}
	}
	for _, key := range []string{"image_url", "image", "images"} {
		out.InputImages = append(out.InputImages, decodeImageReferences(body[key])...)
	}
	out.MaskImage = firstImageReference(body["mask_url"], body["mask"])
	return out, nil
}

func parseCodexMultipartImageRequest(raw []byte, boundary, fallbackModel string) (codexImageRequest, error) {
	out := newCodexImageRequest(fallbackModel)
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return codexImageRequest{}, fmt.Errorf("读取 multipart 图片请求失败: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(part, codexImageResultLimit+1))
		_ = part.Close()
		if readErr != nil {
			return codexImageRequest{}, fmt.Errorf("读取 multipart 字段失败: %w", readErr)
		}
		if len(data) > codexImageResultLimit {
			return codexImageRequest{}, errors.New("multipart 图片字段过大")
		}
		name := strings.TrimSpace(part.FormName())
		if part.FileName() != "" {
			dataURL := imageDataURL(data, part.Header.Get("Content-Type"))
			if name == "mask" {
				out.MaskImage = dataURL
			} else if name == "image" || name == "image[]" {
				out.InputImages = append(out.InputImages, dataURL)
			}
			continue
		}
		value := strings.TrimSpace(string(data))
		switch name {
		case "prompt":
			out.Prompt = value
		case "model":
			out.RequestedToolModel = value
		case "response_format":
			out.ResponseFormat = value
		case "image_url":
			if value != "" {
				out.InputImages = append(out.InputImages, value)
			}
		case "mask_url":
			out.MaskImage = value
		case "size", "quality", "background", "output_format", "moderation", "input_fidelity", "style":
			if value != "" {
				out.StringOptions[name] = value
			}
		case "n", "output_compression", "partial_images":
			var number int64
			if _, err := fmt.Sscan(value, &number); err == nil {
				out.IntegerOptions[name] = number
			}
		}
	}
	return out, nil
}

func newCodexImageRequest(model string) codexImageRequest {
	return codexImageRequest{
		RequestedToolModel: strings.TrimSpace(model),
		StringOptions:      make(map[string]string),
		IntegerOptions:     make(map[string]int64),
	}
}

func decodeStringField(body map[string]json.RawMessage, key string, target *string) {
	if target == nil {
		return
	}
	var value string
	if json.Unmarshal(body[key], &value) == nil {
		*target = strings.TrimSpace(value)
	}
}

func decodeImageReferences(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil && strings.TrimSpace(single) != "" {
		return []string{strings.TrimSpace(single)}
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		out := make([]string, 0, len(list))
		for _, item := range list {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	return nil
}

func firstImageReference(values ...json.RawMessage) string {
	for _, raw := range values {
		if refs := decodeImageReferences(raw); len(refs) > 0 {
			return refs[0]
		}
	}
	return ""
}

func imageDataURL(data []byte, contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// TranslateCodexImagesResponse 把订阅账号返回的 Responses SSE 规整为公开 Images JSON。
func TranslateCodexImagesResponse(raw []byte, responseFormat string) ([]byte, error) {
	if len(raw) > codexImageResultLimit {
		return nil, errors.New("codex 图片响应超过大小上限")
	}
	results, createdAt, usage, err := collectCodexImageResults(raw)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("codex 图片响应未包含生成结果")
	}
	data := make([]map[string]any, 0, len(results))
	for _, result := range results {
		item := make(map[string]any)
		if result.RevisedPrompt != "" {
			item["revised_prompt"] = result.RevisedPrompt
		}
		if strings.EqualFold(strings.TrimSpace(responseFormat), "url") {
			item["url"] = "data:" + imageMIMEType(result.OutputFormat) + ";base64," + result.Base64
		} else {
			item["b64_json"] = result.Base64
		}
		data = append(data, item)
	}
	response := map[string]any{"created": createdAt, "data": data}
	if len(usage) > 0 {
		response["usage"] = usage
	}
	first := results[0]
	for key, value := range map[string]string{
		"background":    first.Background,
		"output_format": first.OutputFormat,
		"quality":       first.Quality,
		"size":          first.Size,
	} {
		if value != "" {
			response[key] = value
		}
	}
	return json.Marshal(response)
}

func collectCodexImageResults(raw []byte) ([]codexImageResult, int64, map[string]any, error) {
	var completed map[string]any
	fallback := make([]map[string]any, 0)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), codexImageResultLimit)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var event map[string]any
		if json.Unmarshal(payload, &event) != nil {
			continue
		}
		typeName, _ := event["type"].(string)
		switch typeName {
		case "response.output_item.done":
			if item, ok := event["item"].(map[string]any); ok {
				fallback = append(fallback, item)
			}
		case "response.completed":
			completed = event
		case "response.failed", "error":
			return nil, 0, nil, errors.New("codex 图片生成失败")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, nil, fmt.Errorf("读取 Codex 图片事件失败: %w", err)
	}
	if completed == nil {
		return nil, 0, nil, errors.New("codex 图片响应未完成")
	}
	response, _ := completed["response"].(map[string]any)
	createdAt := int64(numberValue(response["created_at"]))
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	items := outputItems(response["output"])
	if len(items) == 0 {
		items = fallback
	}
	results := make([]codexImageResult, 0, len(items))
	for _, item := range items {
		if result, ok := decodeCodexImageResult(item); ok {
			results = append(results, result)
		}
	}
	usage := objectValue(response["tool_usage"])
	if imageUsage := objectValue(usage["image_gen"]); len(imageUsage) > 0 {
		usage = imageUsage
	} else {
		usage = objectValue(response["usage"])
	}
	return results, createdAt, usage, nil
}

func outputItems(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func decodeCodexImageResult(item map[string]any) (codexImageResult, bool) {
	if stringValue(item["type"]) != "image_generation_call" {
		return codexImageResult{}, false
	}
	result := codexImageResult{
		Base64:        stringValue(item["result"]),
		RevisedPrompt: stringValue(item["revised_prompt"]),
		OutputFormat:  stringValue(item["output_format"]),
		Size:          stringValue(item["size"]),
		Quality:       stringValue(item["quality"]),
		Background:    stringValue(item["background"]),
	}
	return result, result.Base64 != ""
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func numberValue(value any) float64 {
	number, _ := value.(float64)
	return number
}

func imageMIMEType(outputFormat string) string {
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
