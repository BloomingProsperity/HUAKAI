package trustreceipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const canonicalReceiptSchemaVersion = "trust.receipt.v1"

type TrustReceiptV1 struct {
	RequestID                 string
	ReceiptSequence           int
	TenantScopeRef            string
	OccurredAt                time.Time
	Provider                  string
	RequestedModel            string
	RoutedModel               string
	UpstreamModel             string
	DeliveredModel            string
	CostCents                 int64
	TokenCounts               TokenCounts
	PriceSnapshot             PriceSnapshot
	ValidationState           string
	RedactedMetadataAllowlist map[string]any
}

func Canonical(r TrustReceiptV1) ([]byte, error) {
	metadata, err := canonicalMetadata(r.RedactedMetadataAllowlist)
	if err != nil {
		return nil, fmt.Errorf("redacted_metadata_allowlist: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	writeJSONField(&buf, "schema_version", canonicalReceiptSchemaVersion, true)
	writeJSONField(&buf, "receipt_id", ReceiptID(r.RequestID, r.ReceiptSequence), false)
	writeJSONField(&buf, "request_id", r.RequestID, false)
	writeIntField(&buf, "receipt_sequence", int64(r.ReceiptSequence), false)
	writeJSONField(&buf, "tenant_scope_ref", r.TenantScopeRef, false)
	writeJSONField(&buf, "occurred_at", canonicalTimestamp(r.OccurredAt), false)
	writeJSONField(&buf, "provider", r.Provider, false)
	writeJSONField(&buf, "requested_model", r.RequestedModel, false)
	writeJSONField(&buf, "routed_model", r.RoutedModel, false)
	writeJSONField(&buf, "upstream_model", r.UpstreamModel, false)
	writeJSONField(&buf, "delivered_model", r.DeliveredModel, false)
	writeIntField(&buf, "cost_cents", r.CostCents, false)
	writeRawJSONField(&buf, "token_counts", canonicalTokenCounts(r.TokenCounts), false)
	writeRawJSONField(&buf, "price_snapshot", canonicalPriceSnapshot(r.PriceSnapshot), false)
	writeJSONField(&buf, "validation_state", r.ValidationState, false)
	writeRawJSONField(&buf, "redacted_metadata_allowlist", metadata, false)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func CanonicalHash(r TrustReceiptV1) ([32]byte, error) {
	payload, err := Canonical(r)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func canonicalTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func canonicalTokenCounts(counts TokenCounts) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	writeIntField(&buf, "input", counts.Input, true)
	writeIntField(&buf, "output", counts.Output, false)
	writeIntField(&buf, "cached", counts.Cached, false)
	buf.WriteByte('}')
	return buf.Bytes()
}

func canonicalPriceSnapshot(snapshot PriceSnapshot) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	writeIntField(&buf, "rate_table_snapshot_id", snapshot.RateTableSnapshotID, true)
	writeJSONField(&buf, "snapshot_version", snapshot.SnapshotVersion, false)
	writeJSONField(&buf, "currency_code", snapshot.CurrencyCode, false)
	buf.WriteByte('}')
	return buf.Bytes()
}

func canonicalMetadata(metadata map[string]any) ([]byte, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeJSONString(&buf, key)
		buf.WriteByte(':')
		if err := writeMetadataValue(&buf, metadata[key]); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func writeMetadataValue(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case string:
		writeJSONString(buf, v)
	case bool:
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(raw)
	case int64:
		buf.WriteString(strconv.FormatInt(v, 10))
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}
	return nil
}

func writeJSONField(buf *bytes.Buffer, key, value string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	writeJSONString(buf, value)
}

func writeIntField(buf *bytes.Buffer, key string, value int64, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONString(buf, key)
	buf.WriteByte(':')
	buf.WriteString(strconv.FormatInt(value, 10))
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
	buf.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			buf.WriteString(`\\`)
		case '"':
			buf.WriteString(`\"`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u00`)
				writeHexByte(buf, byte(r))
			} else if r < 0x80 {
				buf.WriteByte(byte(r))
			} else {
				writeUnicodeEscape(buf, r)
			}
		}
	}
	buf.WriteByte('"')
}

func writeUnicodeEscape(buf *bytes.Buffer, r rune) {
	if r <= 0xffff {
		buf.WriteString(`\u`)
		writeHex4(buf, uint16(r))
		return
	}
	r -= 0x10000
	hi := uint16(0xd800 + (r >> 10))
	lo := uint16(0xdc00 + (r & 0x3ff))
	buf.WriteString(`\u`)
	writeHex4(buf, hi)
	buf.WriteString(`\u`)
	writeHex4(buf, lo)
}

func writeHex4(buf *bytes.Buffer, value uint16) {
	const digits = "0123456789abcdef"
	buf.WriteByte(digits[(value>>12)&0xf])
	buf.WriteByte(digits[(value>>8)&0xf])
	buf.WriteByte(digits[(value>>4)&0xf])
	buf.WriteByte(digits[value&0xf])
}

func writeHexByte(buf *bytes.Buffer, value byte) {
	const digits = "0123456789abcdef"
	buf.WriteByte(digits[value>>4])
	buf.WriteByte(digits[value&0xf])
}
