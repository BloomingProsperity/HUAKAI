package imageshttp

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
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
)

type imageRequest struct {
	Model    string          `json:"model"`
	Prompt   *string         `json:"prompt,omitempty"`
	N        *int            `json:"n,omitempty"`
	Size     string          `json:"size,omitempty"`
	Quality  string          `json:"quality,omitempty"`
	Image    json.RawMessage `json:"image,omitempty"`
	Images   json.RawMessage `json:"images,omitempty"`
	ImageURL string          `json:"image_url,omitempty"`
	Stream   *bool           `json:"stream,omitempty"`
	// ResponseFormat 仅 family 级校验消费(replicate_image 拒 b64_json);
	// body 原样透传,不参与改写。
	ResponseFormat string `json:"response_format,omitempty"`
}

type tokenImageUsage struct {
	InputTokens  int
	OutputTokens int
	ImageTokens  int
}

func validateRequest(w http.ResponseWriter, r *http.Request, endpoint imageEndpoint) ([]byte, imageRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
		return nil, imageRequest{}, false
	}
	var req imageRequest
	// OpenAI 官方 /v1/images/edits、/v1/images/variations 是 multipart/form-data
	// (必传图片文件)。此前一律 json.Unmarshal,multipart body 必失败 → 400
	// invalid_json,标准 SDK 的 images.edit/variations 全断。按 Content-Type 分叉:
	// multipart 从 form 字段取 model/prompt/n/size/quality 做同样校验与计费预估,
	// 原始字节保持不动交给已就绪的 relaybody multipart 改写路径(attempt.go)。
	if boundary, isMultipart := multipartBoundary(r.Header.Get("Content-Type")); isMultipart {
		parsed, perr := parseMultipartImageRequest(body, boundary)
		if perr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_multipart", "failed to parse multipart/form-data image request")
			return nil, imageRequest{}, false
		}
		req = parsed
	} else if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidJSON, clienterr.MessageFor(clienterr.CodeInvalidJSON))
		return nil, imageRequest{}, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model field required")
		return nil, imageRequest{}, false
	}
	if req.Stream != nil && *req.Stream {
		// stream:true 原样透传会让上游回 SSE,token 计费解析失败 → abort 假 502 →
		// 余额退款,但 vendor 已对生成扣费 = 平台漏钱。图片流式中继落地前显式拒绝
		// (new-api d2576dd 已实现流式图片中继,完整能力另列)。
		writeJSONError(w, http.StatusBadRequest, "stream_not_supported", "images API does not support stream:true yet")
		return nil, imageRequest{}, false
	}
	switch endpoint {
	case imageEndpointGenerations, imageEndpointEdits:
		if req.Prompt == nil || strings.TrimSpace(*req.Prompt) == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_prompt", "prompt field required")
			return nil, imageRequest{}, false
		}
	case imageEndpointVariations:
		if req.Prompt != nil && strings.TrimSpace(*req.Prompt) != "" {
			writeJSONError(w, http.StatusBadRequest, "unexpected_prompt", "variations endpoint does not accept prompt")
			return nil, imageRequest{}, false
		}
	}
	if endpoint == imageEndpointEdits || endpoint == imageEndpointVariations {
		if !req.hasImageReference() {
			writeJSONError(w, http.StatusBadRequest, "missing_image_reference", "image, images, or image_url reference required")
			return nil, imageRequest{}, false
		}
	}
	if req.N != nil && *req.N <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_n", "n must be positive")
		return nil, imageRequest{}, false
	}
	return body, req, true
}

// multipartBoundary 解析 Content-Type,返回 boundary 与是否 multipart/form-data。
func multipartBoundary(contentType string) (string, bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return "", false
	}
	b := strings.TrimSpace(params["boundary"])
	return b, b != ""
}

// parseMultipartImageRequest 从已读的 multipart body 字节里提取 image 请求字段
// (不消费 r.Body,原始字节仍交下游)。文件 part(image/image[]/mask)只标记存在,
// 不读内容(省内存);标量字段 model/prompt/n/size/quality 读出填入 imageRequest。
func parseMultipartImageRequest(body []byte, boundary string) (imageRequest, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var req imageRequest
	hasImageFile := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return imageRequest{}, err
		}
		name := part.FormName()
		if part.FileName() != "" {
			if name == "image" || name == "image[]" || strings.HasPrefix(name, "image") {
				hasImageFile = true
			}
			_ = part.Close()
			continue // 文件内容不读
		}
		// 标量字段:限读避免超大 value 占内存(单字段 64KiB 足够)。
		val, err := io.ReadAll(io.LimitReader(part, 64<<10))
		_ = part.Close()
		if err != nil {
			return imageRequest{}, err
		}
		v := strings.TrimSpace(string(val))
		switch name {
		case "model":
			req.Model = v
		case "prompt":
			p := v
			req.Prompt = &p
		case "size":
			req.Size = v
		case "quality":
			req.Quality = v
		case "n":
			if v != "" {
				n, convErr := strconv.Atoi(v)
				if convErr != nil {
					return imageRequest{}, fmt.Errorf("invalid n field: %q", v)
				}
				req.N = &n
			}
		case "stream":
			if b, convErr := strconv.ParseBool(v); convErr == nil {
				req.Stream = &b
			}
		}
	}
	// image 文件 part 满足 edits/variations 的图片引用要求(用 sentinel,使
	// hasImageReference() 无需感知 multipart)。
	if hasImageFile {
		req.Image = json.RawMessage(`"<multipart-file>"`)
	}
	return req, nil
}

func (r imageRequest) Amount() int {
	if r.N == nil {
		return 1
	}
	return *r.N
}

func (r imageRequest) NormalizedQuality() string {
	if strings.TrimSpace(r.Quality) == "" {
		return "standard"
	}
	return strings.TrimSpace(r.Quality)
}

func (r imageRequest) PromptText() string {
	if r.Prompt == nil {
		return ""
	}
	return *r.Prompt
}

func (r imageRequest) hasImageReference() bool {
	if strings.TrimSpace(r.ImageURL) != "" {
		return true
	}
	return len(bytesTrimSpace(r.Image)) > 0 || len(bytesTrimSpace(r.Images)) > 0
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func promptCharCount(prompt string) int {
	return utf8.RuneCountInString(prompt)
}

func estimatePromptTokens(prompt string) int {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return 1
	}
	blocks := []proto.CanonicalContentBlock{{Type: "text", Text: prompt}}
	if n := (tokencheck.HeuristicEstimator{}).Estimate(blocks); n > 0 {
		return n
	}
	return 1
}

func parseTokenImageUsage(raw []byte) (tokenImageUsage, bool) {
	var body struct {
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				ImageTokens int `json:"image_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return tokenImageUsage{}, false
	}
	if body.Usage.InputTokens <= 0 && body.Usage.OutputTokens <= 0 {
		return tokenImageUsage{}, false
	}
	return tokenImageUsage{
		InputTokens:  body.Usage.InputTokens,
		OutputTokens: body.Usage.OutputTokens,
		ImageTokens:  body.Usage.InputTokensDetails.ImageTokens,
	}, true
}

func readUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUpstreamBodyBytes {
		return nil, fmt.Errorf("images upstream response exceeds %d bytes", maxUpstreamBodyBytes)
	}
	return raw, nil
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
