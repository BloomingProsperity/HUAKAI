// record.go 负责 TLS record、ClientHello handshake 解析与解析后视图。
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	recordTypeHandshake      byte = 0x16
	handshakeTypeClientHello byte = 0x01
)

var errNotClientHello = errors.New("not a ClientHello")

type clientHello struct {
	LegacyVersion      uint16
	RandomLen          int
	LegacySessionIDLen int
	CipherSuites       []uint16
	CompressionMethods []byte
	Extensions         []extension
}

func readTLSRecord(r io.Reader) ([]byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("read TLS record header: %w", err)
	}
	if header[0] != recordTypeHandshake {
		return nil, fmt.Errorf("%w: record type 0x%02x", errNotClientHello, header[0])
	}
	bodyLen := int(binary.BigEndian.Uint16(header[3:5]))
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("read TLS record body: %w", err)
	}
	record := make([]byte, 0, len(header)+len(body))
	record = append(record, header[:]...)
	record = append(record, body...)
	return record, nil
}

func parseClientHelloFromRecord(data []byte) (*clientHello, error) {
	if len(data) < 5 {
		return nil, errors.New("record header too short")
	}
	if data[0] != recordTypeHandshake {
		return nil, errNotClientHello
	}
	recLen := int(binary.BigEndian.Uint16(data[3:5]))
	if len(data) < 5+recLen {
		return nil, errors.New("record body truncated")
	}
	return parseClientHelloHandshake(data[5 : 5+recLen])
}

func parseClientHelloHandshake(data []byte) (*clientHello, error) {
	if len(data) < 4 {
		return nil, errors.New("handshake header too short")
	}
	if data[0] != handshakeTypeClientHello {
		return nil, fmt.Errorf("%w: handshake type 0x%02x", errNotClientHello, data[0])
	}
	msgLen := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	if len(data) < 4+msgLen {
		return nil, errors.New("ClientHello handshake truncated")
	}
	body := data[4 : 4+msgLen]
	return parseClientHelloBody(body)
}

func parseClientHelloBody(body []byte) (*clientHello, error) {
	ch := &clientHello{}
	pos := 0
	if len(body)-pos < 2 {
		return nil, errors.New("legacy_version too short")
	}
	ch.LegacyVersion = binary.BigEndian.Uint16(body[pos : pos+2])
	pos += 2
	if len(body)-pos < 32 {
		return nil, errors.New("random too short")
	}
	ch.RandomLen = 32
	pos += 32
	if len(body)-pos < 1 {
		return nil, errors.New("session_id_len too short")
	}
	sessionIDLen := int(body[pos])
	pos++
	if len(body)-pos < sessionIDLen {
		return nil, errors.New("session_id truncated")
	}
	ch.LegacySessionIDLen = sessionIDLen
	pos += sessionIDLen
	if len(body)-pos < 2 {
		return nil, errors.New("cipher_suites_len too short")
	}
	cipherBytes := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body)-pos < cipherBytes || cipherBytes%2 != 0 {
		return nil, errors.New("cipher_suites truncated or odd length")
	}
	ch.CipherSuites = make([]uint16, cipherBytes/2)
	for i := range ch.CipherSuites {
		ch.CipherSuites[i] = binary.BigEndian.Uint16(body[pos+i*2 : pos+i*2+2])
	}
	pos += cipherBytes
	if len(body)-pos < 1 {
		return nil, errors.New("compression_methods_len too short")
	}
	compressionLen := int(body[pos])
	pos++
	if len(body)-pos < compressionLen {
		return nil, errors.New("compression_methods truncated")
	}
	ch.CompressionMethods = append([]byte(nil), body[pos:pos+compressionLen]...)
	pos += compressionLen
	if pos >= len(body) {
		return ch, nil
	}
	if len(body)-pos < 2 {
		return nil, errors.New("extensions_len too short")
	}
	extensionsLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body)-pos < extensionsLen {
		return nil, errors.New("extensions truncated")
	}
	extensions, err := parseExtensions(body[pos : pos+extensionsLen])
	if err != nil {
		return nil, err
	}
	ch.Extensions = extensions
	return ch, nil
}

func (ch *clientHello) extensionTypes() []uint16 {
	out := make([]uint16, len(ch.Extensions))
	for i, ext := range ch.Extensions {
		out[i] = ext.Type
	}
	return out
}

func (ch *clientHello) sni() string {
	for _, ext := range ch.Extensions {
		if ext.Type == extServerName {
			return ext.SNIHostname
		}
	}
	return ""
}

func (ch *clientHello) alpnProtocols() []string {
	for _, ext := range ch.Extensions {
		if ext.Type == extALPN {
			return ext.ALPNProtocols
		}
	}
	return nil
}

func (ch *clientHello) supportedGroups() []uint16 {
	for _, ext := range ch.Extensions {
		if ext.Type == extSupportedGroups {
			return ext.SupportedGroups
		}
	}
	return nil
}

func (ch *clientHello) ecPointFormats() []uint8 {
	for _, ext := range ch.Extensions {
		if ext.Type == extECPointFormats {
			out := make([]uint8, len(ext.ECPointFormats))
			for i, value := range ext.ECPointFormats {
				out[i] = uint8(value)
			}
			return out
		}
	}
	return nil
}

func (ch *clientHello) supportedVersions() []uint16 {
	for _, ext := range ch.Extensions {
		if ext.Type == extSupportedVersions {
			return ext.SupportedVersions
		}
	}
	return nil
}

func (ch *clientHello) keyShareGroups() []uint16 {
	for _, ext := range ch.Extensions {
		if ext.Type == extKeyShare {
			return ext.KeyShareGroups
		}
	}
	return nil
}

func (ch *clientHello) signatureAlgorithms() []uint16 {
	for _, ext := range ch.Extensions {
		if ext.Type == extSignatureAlgorithms {
			return ext.SignatureAlgorithms
		}
	}
	return nil
}

func (ch *clientHello) pskModes() []int {
	for _, ext := range ch.Extensions {
		if ext.Type == extPSKKeyExchangeModes {
			return ext.PSKModes
		}
	}
	return nil
}

func (ch *clientHello) paddingLen() int {
	for _, ext := range ch.Extensions {
		if ext.Type == extPadding {
			return ext.DataLen
		}
	}
	return 0
}

func (ch *clientHello) hasExtension(typ uint16) bool {
	for _, ext := range ch.Extensions {
		if ext.Type == typ {
			return true
		}
	}
	return false
}
