package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
)

const keyVersion = "l2:v1"

var ignoredTopLevelFields = map[string]struct{}{
	"metadata":  {},
	"stream":    {},
	"timestamp": {},
}

// KeyInput 是 L2 exact response cache 的物理 key 输入。
type KeyInput struct {
	TenantID int64
	Vendor   string
	Model    string
	Body     []byte
}

// BuildKey 计算包含 tenant 隔离边界的稳定物理 key。
func BuildKey(in KeyInput) (string, []byte, error) {
	canonical, err := CanonicalRequestBody(in.Body)
	if err != nil {
		return "", nil, err
	}
	preimage := bytes.NewBuffer(nil)
	preimage.WriteString(keyVersion)
	preimage.WriteByte(0)
	preimage.WriteString(strconv.FormatInt(in.TenantID, 10))
	preimage.WriteByte(0)
	preimage.WriteString(in.Vendor)
	preimage.WriteByte(0)
	preimage.WriteString(in.Model)
	preimage.WriteByte(0)
	preimage.Write(canonical)
	sum := sha256.Sum256(preimage.Bytes())
	return keyVersion + ":" + hex.EncodeToString(sum[:]), canonical, nil
}

// CanonicalRequestBody 去除不会改变 non-streaming 响应语义的顶层字段，
// 并用稳定 object key 顺序序列化，避免 whitespace / 字段顺序影响命中。
func CanonicalRequestBody(body []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("cache canonical request body: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("cache canonical request body: trailing JSON token")
	}
	if obj, ok := root.(map[string]any); ok {
		for k := range ignoredTopLevelFields {
			delete(obj, k)
		}
	}
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalJSON(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		raw, _ := json.Marshal(x)
		buf.Write(raw)
	case json.Number:
		buf.WriteString(x.String())
	case float64:
		raw, _ := json.Marshal(x)
		buf.Write(raw)
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			raw, _ := json.Marshal(k)
			buf.Write(raw)
			buf.WriteByte(':')
			if err := writeCanonicalJSON(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return fmt.Errorf("cache canonical JSON: %w", err)
		}
		buf.Write(raw)
	}
	return nil
}
