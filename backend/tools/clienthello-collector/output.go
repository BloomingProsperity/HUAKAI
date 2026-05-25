// output.go 负责 collector JSON 结构定义与序列化。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type valueHex struct {
	Value    uint16 `json:"value"`
	ValueHex string `json:"value_hex"`
	IsGREASE bool   `json:"is_grease,omitempty"`
}

type greaseValue struct {
	Kind     string `json:"kind"`
	Value    uint16 `json:"value"`
	ValueHex string `json:"value_hex"`
}

type CollectorOutput struct {
	SchemaVersion       string        `json:"schema_version"`
	ModeName            string        `json:"mode_name"`
	CaptureTime         string        `json:"capture_time"`
	CollectedAt         string        `json:"collected_at"`
	SampleCount         int           `json:"sample_count"`
	TargetHost          string        `json:"target_host"`
	CaptureTargetHost   string        `json:"capture_target_host,omitempty"`
	TLSBackend          string        `json:"tls_backend"`
	LegacyVersion       uint16        `json:"legacy_version"`
	LegacyVersionHex    string        `json:"legacy_version_hex"`
	RandomLen           int           `json:"random_len"`
	LegacySessionIDLen  int           `json:"legacy_session_id_len"`
	CipherSuites        []valueHex    `json:"cipher_suites"`
	CompressionMethods  []byte        `json:"compression_methods"`
	Extensions          []extension   `json:"extensions"`
	SNI                 string        `json:"sni"`
	ALPNProtocols       []string      `json:"alpn_protocols"`
	SupportedGroups     []uint16      `json:"supported_groups"`
	ECPointFormats      []byte        `json:"ec_point_formats"`
	SupportedVersions   []uint16      `json:"supported_versions"`
	KeyShareGroups      []uint16      `json:"key_share_groups,omitempty"`
	SignatureAlgorithms []uint16      `json:"signature_algorithms,omitempty"`
	PSKModes            []int         `json:"psk_modes,omitempty"`
	PaddingLen          int           `json:"padding_len,omitempty"`
	EarlyDataEnabled    bool          `json:"early_data_enabled"`
	GREASEValues        []greaseValue `json:"grease_values,omitempty"`
	JA3                 ja3Result     `json:"ja3"`
	JA4                 ja4Result     `json:"ja4"`
}

func outputFromRecord(record []byte, modeName, targetHost, captureTargetHost string) (*CollectorOutput, error) {
	ch, err := parseClientHelloFromRecord(record)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ja3 := computeJA3(ch)
	ja4 := computeJA4(ch)
	sni := ch.sni()
	redactedSNI := ""
	if sni != "" {
		redactedSNI = "<redacted>"
	}
	return &CollectorOutput{
		SchemaVersion:       "1.0",
		ModeName:            modeName,
		CaptureTime:         now,
		CollectedAt:         now,
		SampleCount:         1,
		TargetHost:          targetHost,
		CaptureTargetHost:   captureTargetHost,
		TLSBackend:          "nodejs/openssl",
		LegacyVersion:       ch.LegacyVersion,
		LegacyVersionHex:    hex16(ch.LegacyVersion),
		RandomLen:           ch.RandomLen,
		LegacySessionIDLen:  ch.LegacySessionIDLen,
		CipherSuites:        cipherSuitesForJSON(ch.CipherSuites),
		CompressionMethods:  ch.CompressionMethods,
		Extensions:          ch.Extensions,
		SNI:                 redactedSNI,
		ALPNProtocols:       ch.alpnProtocols(),
		SupportedGroups:     ch.supportedGroups(),
		ECPointFormats:      uint8sToBytes(ch.ecPointFormats()),
		SupportedVersions:   ch.supportedVersions(),
		KeyShareGroups:      ch.keyShareGroups(),
		SignatureAlgorithms: ch.signatureAlgorithms(),
		PSKModes:            ch.pskModes(),
		PaddingLen:          ch.paddingLen(),
		EarlyDataEnabled:    ch.hasExtension(extEarlyData),
		GREASEValues:        greaseValuesForJSON(ch),
		JA3:                 ja3,
		JA4:                 ja4,
	}, nil
}

func writeCollectorOutput(output *CollectorOutput, outPath string) error {
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	data = append(data, '\n')
	if outPath == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(outPath, data, 0o640); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

func cipherSuitesForJSON(values []uint16) []valueHex {
	out := make([]valueHex, len(values))
	for i, value := range values {
		out[i] = valueHex{Value: value, ValueHex: hex16(value), IsGREASE: isGREASE(value)}
	}
	return out
}

func greaseValuesForJSON(ch *clientHello) []greaseValue {
	var out []greaseValue
	for _, value := range ch.CipherSuites {
		if isGREASE(value) {
			out = append(out, greaseValue{Kind: "cipher_suite", Value: value, ValueHex: hex16(value)})
		}
	}
	for _, ext := range ch.Extensions {
		if isGREASE(ext.Type) {
			out = append(out, greaseValue{Kind: "extension", Value: ext.Type, ValueHex: hex16(ext.Type)})
		}
		for _, value := range ext.SupportedGroups {
			if isGREASE(value) {
				out = append(out, greaseValue{Kind: "supported_group", Value: value, ValueHex: hex16(value)})
			}
		}
		for _, value := range ext.KeyShareGroups {
			if isGREASE(value) {
				out = append(out, greaseValue{Kind: "key_share_group", Value: value, ValueHex: hex16(value)})
			}
		}
		for _, value := range ext.SupportedVersions {
			if isGREASE(value) {
				out = append(out, greaseValue{Kind: "supported_version", Value: value, ValueHex: hex16(value)})
			}
		}
	}
	return out
}

func uint8sToBytes(values []uint8) []byte {
	if len(values) == 0 {
		return nil
	}
	return append([]byte(nil), values...)
}
