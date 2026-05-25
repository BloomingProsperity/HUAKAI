// extensions.go 负责 TLS extension 解码与 ClientHello extension 视图。
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	extServerName           uint16 = 0x0000
	extStatusRequest        uint16 = 0x0005
	extSupportedGroups      uint16 = 0x000a
	extECPointFormats       uint16 = 0x000b
	extSignatureAlgorithms  uint16 = 0x000d
	extALPN                 uint16 = 0x0010
	extSignedCertTimestamp  uint16 = 0x0012
	extPadding              uint16 = 0x0015
	extExtendedMasterSecret uint16 = 0x0017
	extSessionTicket        uint16 = 0x0023
	extPreSharedKey         uint16 = 0x0029
	extEarlyData            uint16 = 0x002a
	extSupportedVersions    uint16 = 0x002b
	extPSKKeyExchangeModes  uint16 = 0x002d
	extKeyShare             uint16 = 0x0033
	extEncryptedClientHello uint16 = 0xfe0d
)

type extension struct {
	Type                uint16   `json:"type"`
	TypeHex             string   `json:"type_hex"`
	TypeName            string   `json:"type_name,omitempty"`
	IsGREASE            bool     `json:"is_grease,omitempty"`
	DataLen             int      `json:"data_len"`
	SNIHostname         string   `json:"-"`
	SupportedVersions   []uint16 `json:"supported_versions,omitempty"`
	KeyShareGroups      []uint16 `json:"key_share_groups,omitempty"`
	SignatureAlgorithms []uint16 `json:"signature_algorithms,omitempty"`
	SupportedGroups     []uint16 `json:"supported_groups,omitempty"`
	ALPNProtocols       []string `json:"alpn_protocols,omitempty"`
	ECPointFormats      []int    `json:"ec_point_formats,omitempty"`
	PaddingLen          int      `json:"padding_len,omitempty"`
	PSKModes            []int    `json:"psk_modes,omitempty"`
	ECHPresent          bool     `json:"ech_present,omitempty"`
}

func parseExtensions(data []byte) ([]extension, error) {
	var out []extension
	pos := 0
	for pos < len(data) {
		if len(data)-pos < 4 {
			return nil, errors.New("extension header too short")
		}
		typ := binary.BigEndian.Uint16(data[pos : pos+2])
		dataLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if len(data)-pos < dataLen {
			return nil, fmt.Errorf("extension %s truncated", hex16(typ))
		}
		out = append(out, parseExtension(typ, data[pos:pos+dataLen]))
		pos += dataLen
	}
	return out, nil
}

func parseExtension(typ uint16, data []byte) extension {
	ext := extension{
		Type:     typ,
		TypeHex:  hex16(typ),
		TypeName: extensionName(typ),
		IsGREASE: isGREASE(typ),
		DataLen:  len(data),
	}
	if ext.IsGREASE {
		return ext
	}
	switch typ {
	case extServerName:
		ext.SNIHostname = parseServerName(data)
	case extSupportedVersions:
		ext.SupportedVersions = parseSupportedVersions(data)
	case extKeyShare:
		ext.KeyShareGroups = parseKeyShare(data)
	case extSignatureAlgorithms:
		ext.SignatureAlgorithms = parseUint16List(data)
	case extSupportedGroups:
		ext.SupportedGroups = parseUint16List(data)
	case extALPN:
		ext.ALPNProtocols = parseALPN(data)
	case extECPointFormats:
		ext.ECPointFormats = uint8sToInts(parseECPointFormats(data))
	case extPadding:
		ext.PaddingLen = len(data)
	case extPSKKeyExchangeModes:
		ext.PSKModes = uint8sToInts(parsePSKModes(data))
	case extEncryptedClientHello:
		ext.ECHPresent = true
	}
	return ext
}

func parseServerName(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+listLen {
		return ""
	}
	pos := 2
	end := 2 + listLen
	for pos+3 <= end {
		nameType := data[pos]
		nameLen := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		pos += 3
		if pos+nameLen > end {
			return ""
		}
		if nameType == 0 {
			return string(data[pos : pos+nameLen])
		}
		pos += nameLen
	}
	return ""
}

func parseSupportedVersions(data []byte) []uint16 {
	if len(data) < 1 {
		return nil
	}
	listLen := int(data[0])
	if len(data) < 1+listLen || listLen%2 != 0 {
		return nil
	}
	out := make([]uint16, listLen/2)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(data[1+i*2 : 1+i*2+2])
	}
	return out
}

func parseKeyShare(data []byte) []uint16 {
	if len(data) < 2 {
		return nil
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+listLen {
		return nil
	}
	var out []uint16
	pos := 2
	end := 2 + listLen
	for pos+4 <= end {
		group := binary.BigEndian.Uint16(data[pos : pos+2])
		keyLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		out = append(out, group)
		pos += 4 + keyLen
	}
	return out
}

func parseUint16List(data []byte) []uint16 {
	if len(data) < 2 {
		return nil
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+listLen || listLen%2 != 0 {
		return nil
	}
	out := make([]uint16, listLen/2)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(data[2+i*2 : 2+i*2+2])
	}
	return out
}

func parseALPN(data []byte) []string {
	if len(data) < 2 {
		return nil
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+listLen {
		return nil
	}
	var out []string
	pos := 2
	end := 2 + listLen
	for pos < end {
		nameLen := int(data[pos])
		pos++
		if pos+nameLen > end {
			return nil
		}
		out = append(out, string(data[pos:pos+nameLen]))
		pos += nameLen
	}
	return out
}

func parseECPointFormats(data []byte) []uint8 {
	if len(data) < 1 {
		return nil
	}
	listLen := int(data[0])
	if len(data) < 1+listLen {
		return nil
	}
	return append([]uint8(nil), data[1:1+listLen]...)
}

func parsePSKModes(data []byte) []uint8 {
	if len(data) < 1 {
		return nil
	}
	listLen := int(data[0])
	if len(data) < 1+listLen {
		return nil
	}
	return append([]uint8(nil), data[1:1+listLen]...)
}

func extensionName(t uint16) string {
	switch t {
	case extServerName:
		return "server_name"
	case extStatusRequest:
		return "status_request"
	case extSupportedGroups:
		return "supported_groups"
	case extECPointFormats:
		return "ec_point_formats"
	case extSignatureAlgorithms:
		return "signature_algorithms"
	case extALPN:
		return "application_layer_protocol_negotiation"
	case extSignedCertTimestamp:
		return "signed_certificate_timestamp"
	case extPadding:
		return "padding"
	case extExtendedMasterSecret:
		return "extended_master_secret"
	case extSessionTicket:
		return "session_ticket"
	case extPreSharedKey:
		return "pre_shared_key"
	case extEarlyData:
		return "early_data"
	case extSupportedVersions:
		return "supported_versions"
	case extPSKKeyExchangeModes:
		return "psk_key_exchange_modes"
	case extKeyShare:
		return "key_share"
	case extEncryptedClientHello:
		return "encrypted_client_hello"
	default:
		return ""
	}
}

func uint8sToInts(values []uint8) []int {
	if len(values) == 0 {
		return nil
	}
	out := make([]int, len(values))
	for i, value := range values {
		out[i] = int(value)
	}
	return out
}

func hex16(v uint16) string {
	return fmt.Sprintf("0x%04x", v)
}
