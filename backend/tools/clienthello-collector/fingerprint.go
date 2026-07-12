// fingerprint.go 负责 JA3 input 与 JA4 五段 hash 计算。
package main

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const emptyRenegotiationSCSV uint16 = 0x00ff

type ja3Result struct {
	InputString string `json:"input_string"`
	Hash        string `json:"hash"`
}

type ja4Result struct {
	Raw  string `json:"raw"`
	Hash string `json:"hash"`
}

func computeJA3(ch *clientHello) ja3Result {
	version := ja3Version(ch)
	ciphers := filterGREASE(ch.CipherSuites)
	cleanCiphers := make([]uint16, 0, len(ciphers))
	for _, cipher := range ciphers {
		if cipher != emptyRenegotiationSCSV {
			cleanCiphers = append(cleanCiphers, cipher)
		}
	}
	exts := filterGREASE(ch.extensionTypes())
	cleanExts := make([]uint16, 0, len(exts))
	for _, ext := range exts {
		if ext != extPadding && ext != extPreSharedKey {
			cleanExts = append(cleanExts, ext)
		}
	}
	input := strings.Join([]string{
		strconv.Itoa(int(version)),
		uint16sToDash(cleanCiphers),
		uint16sToDash(cleanExts),
		uint16sToDash(filterGREASE(ch.supportedGroups())),
		uint8sToDash(ch.ecPointFormats()),
	}, ",")
	return ja3Result{
		InputString: input,
		Hash:        fmt.Sprintf("%x", md5.Sum([]byte(input))),
	}
}

func computeJA4(ch *clientHello) ja4Result {
	protocol := "t"
	version := ja4Version(ch)
	sniFlag := "i"
	if ch.sni() != "" {
		sniFlag = "d"
	}
	ciphers := filterGREASE(ch.CipherSuites)
	cleanCiphers := make([]uint16, 0, len(ciphers))
	for _, cipher := range ciphers {
		if cipher != emptyRenegotiationSCSV {
			cleanCiphers = append(cleanCiphers, cipher)
		}
	}
	cipherCount := len(cleanCiphers)
	if cipherCount > 99 {
		cipherCount = 99
	}
	extTypes := filterGREASE(ch.extensionTypes())
	extCount := len(extTypes)
	if extCount > 99 {
		extCount = 99
	}
	alpnFirst := ja4ALPNToken(ch.alpnProtocols())
	sortedCiphers := append([]uint16(nil), cleanCiphers...)
	sort.Slice(sortedCiphers, func(i, j int) bool { return sortedCiphers[i] < sortedCiphers[j] })
	cipherHash := sha256Truncate12(uint16sToComma(sortedCiphers))
	extForHash := make([]uint16, 0, len(extTypes))
	for _, ext := range extTypes {
		if ext != extServerName && ext != extALPN {
			extForHash = append(extForHash, ext)
		}
	}
	sort.Slice(extForHash, func(i, j int) bool { return extForHash[i] < extForHash[j] })
	extHash := sha256Truncate12(uint16sToComma(extForHash) + "_" + alpnFirst)
	raw := fmt.Sprintf("%s%s%s%02d%02d_%s_%s_%s", protocol, version, sniFlag, cipherCount, extCount, alpnFirst, cipherHash, extHash)
	return ja4Result{Raw: raw, Hash: raw}
}

func ja4ALPNToken(alpns []string) string {
	if len(alpns) == 0 {
		return "00"
	}
	// HUAKAI 模板沿用历史上的 Gemini 约定：
	// 同时声明 h2,http/1.1 时，按 http/1.1 的 token "ht" 编码。
	alpn := alpns[len(alpns)-1]
	if len(alpn) >= 2 {
		return alpn[:2]
	}
	if len(alpn) == 1 {
		return alpn + "0"
	}
	return "00"
}

func ja3Version(ch *clientHello) uint16 {
	for _, version := range ch.supportedVersions() {
		if version == 0x0304 {
			return version
		}
	}
	var max uint16
	for _, version := range ch.supportedVersions() {
		if !isGREASE(version) && version > max {
			max = version
		}
	}
	if max != 0 {
		return max
	}
	return ch.LegacyVersion
}

func ja4Version(ch *clientHello) string {
	var max uint16
	for _, version := range ch.supportedVersions() {
		if !isGREASE(version) && version > max {
			max = version
		}
	}
	if max == 0 {
		max = ch.LegacyVersion
	}
	switch max {
	case 0x0304:
		return "13"
	case 0x0303:
		return "12"
	case 0x0302:
		return "11"
	case 0x0301:
		return "10"
	case 0x0300:
		return "s3"
	case 0x0200:
		return "s2"
	default:
		return fmt.Sprintf("%02x", max>>8)
	}
}

func isGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && byte(v>>8) == byte(v)
}

func filterGREASE(values []uint16) []uint16 {
	out := make([]uint16, 0, len(values))
	for _, value := range values {
		if !isGREASE(value) {
			out = append(out, value)
		}
	}
	return out
}

func uint16sToDash(values []uint16) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(int(value))
	}
	return strings.Join(parts, "-")
}

func uint8sToDash(values []uint8) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(int(value))
	}
	return strings.Join(parts, "-")
}

func uint16sToComma(values []uint16) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(int(value))
	}
	return strings.Join(parts, ",")
}

func sha256Truncate12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)[:12]
}
