// Package relaybody 把出站请求体里的 "model" 字段改写成 registry 解析后的上游模型 id。
// 公共别名路由到不同上游模型时,出站 JSON/表单必须带真实上游 model,否则上游按别名执行错
// 模型(或 404)、计费/审计与实际不符。chat 路径已在 gatewayhttp 内联改写;audio/image 复用本包。
package relaybody

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// RewriteModel 返回改写 "model" 后的请求体、对应 Content-Type、以及是否为 multipart 重编码。
// 关键:仅当 body 里现有 model 与 upstreamModel **不同**(发生别名重映射)时才改写;相同则逐字
// 转发(不重编码,保留原 boundary 与字节,既省开销又不破坏既有逐字转发语义)。
func RewriteModel(body []byte, contentType, upstreamModel string) (newBody []byte, newContentType string, isMultipart bool) {
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" || len(body) == 0 {
		return body, contentType, false
	}
	if mediaType, params, err := mime.ParseMediaType(contentType); err == nil &&
		strings.EqualFold(mediaType, "multipart/form-data") && strings.TrimSpace(params["boundary"]) != "" {
		if nb, nct, ok := rewriteMultipartModel(body, params["boundary"], upstreamModel); ok {
			return nb, nct, true
		}
		return body, contentType, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body, contentType, false
	}
	modelRaw, has := obj["model"]
	if !has {
		return body, contentType, false
	}
	var cur string
	if err := json.Unmarshal(modelRaw, &cur); err == nil && strings.TrimSpace(cur) == upstreamModel {
		return body, contentType, false
	}
	mr, err := json.Marshal(upstreamModel)
	if err != nil {
		return body, contentType, false
	}
	obj["model"] = mr
	out, err := json.Marshal(obj)
	if err != nil {
		return body, contentType, false
	}
	return out, contentType, false
}

func rewriteMultipartModel(body []byte, boundary, upstreamModel string) ([]byte, string, bool) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	sawModel := false
	origModel := ""
	for {
		part, perr := reader.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, "", false
		}
		var fw io.Writer
		var werr error
		if part.FileName() != "" {
			fw, werr = writer.CreateFormFile(part.FormName(), part.FileName())
		} else {
			fw, werr = writer.CreateFormField(part.FormName())
		}
		if werr != nil {
			return nil, "", false
		}
		if part.FormName() == "model" {
			sawModel = true
			data, rerr := io.ReadAll(part)
			if rerr != nil {
				return nil, "", false
			}
			origModel = string(data)
			if _, werr = fw.Write([]byte(upstreamModel)); werr != nil {
				return nil, "", false
			}
		} else if _, werr = io.Copy(fw, part); werr != nil {
			return nil, "", false
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", false
	}
	if !sawModel || strings.TrimSpace(origModel) == upstreamModel {
		return nil, "", false
	}
	return buf.Bytes(), writer.FormDataContentType(), true
}
