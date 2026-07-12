package auditledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const canonicalLedgerSchemaVersion = "trust.ledger.v1"

// CanonicalPayload 返回用于 ledger 条目哈希的确定性字节负载。
// 它有意排除 Signature 与 MerkleRoot。
func CanonicalPayload(entry LedgerEntry) []byte {
	payload, _ := canonicalPayload(entry)
	return payload
}

func canonicalPayload(entry LedgerEntry) ([]byte, error) {
	var hopChain any = entry.HopChain
	if entry.HopChain == nil {
		hopChain = []any{}
	}
	hopJSON, err := canonicalJSON(hopChain)
	if err != nil {
		return nil, fmt.Errorf("hop_chain: %w", err)
	}
	modelJSON, err := canonicalJSON(entry.ModelChain)
	if err != nil {
		return nil, fmt.Errorf("model_chain: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	writeJSONField(&buf, "schema_version", canonicalLedgerSchemaVersion, true)
	writeJSONField(&buf, "ledger_id", entry.LedgerID, false)
	writeJSONField(&buf, "occurred_at", canonicalTimestamp(entry.Timestamp), false)
	writeJSONField(&buf, "request_id", entry.RequestID, false)
	scopeRef := entry.TenantScopeRef
	if scopeRef == "" {
		scopeRef = TenantScopeRef(entry.TenantID)
	}
	writeJSONField(&buf, "tenant_scope_ref", scopeRef, false)
	writeRawJSONField(&buf, "hop_chain", hopJSON, false)
	writeRawJSONField(&buf, "model_chain", modelJSON, false)
	writeJSONField(&buf, "prev_merkle_root", hex.EncodeToString(entry.PrevMerkleRoot[:]), false)
	writeJSONField(&buf, "pubkey_fingerprint", entry.PubkeyFingerprint, false)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// TenantScopeRef 是用于规范化回执的稳定且不可逆的租户引用。
// 它避免在公开的证明材料中暴露原始 tenant_id。
func TenantScopeRef(tenantID int64) string {
	if tenantID == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("huakai-ledger-tenant-scope-v1:%d", tenantID)))
	return "tenant:" + hex.EncodeToString(sum[:8])
}

func canonicalTimestamp(ts string) string {
	if ts == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func writeJSONField(buf *bytes.Buffer, key, value string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	writeJSONString(buf, value)
}

func writeRawJSONField(buf *bytes.Buffer, key string, value []byte, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	buf.Write(value)
}

func writeJSONString(buf *bytes.Buffer, value string) {
	raw, _ := json.Marshal(value)
	buf.Write(raw)
}

func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonicalJSONValue(&buf, decoded); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalJSONValue(buf *bytes.Buffer, v any) error {
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
		writeJSONString(buf, x)
	case json.Number:
		if err := validateJSONNumber(x.String()); err != nil {
			return err
		}
		buf.WriteString(x.String())
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalJSONValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, key)
			buf.WriteByte(':')
			if err := writeCanonicalJSONValue(buf, x[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(raw)
	}
	return nil
}

func validateJSONNumber(raw string) error {
	if raw == "" {
		return fmt.Errorf("empty json number")
	}
	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		return err
	}
	return nil
}
