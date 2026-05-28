package privacy

import (
	"bytes"
	"encoding/json"
	"io"
)

// StrictDecodeJSON 解码单个 JSON 值，并拒绝后续任何 JSON token。
func StrictDecodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, ErrUnsafePayload
	}
	return v, nil
}
