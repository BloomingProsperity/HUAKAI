package invitation

import (
	"crypto/rand"
	"io"
	"strings"
)

const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

type CodeGenerator interface {
	Generate() (string, error)
}

type RandomCodeGenerator struct {
	Reader io.Reader
}

func (g RandomCodeGenerator) Generate() (string, error) {
	reader := g.Reader
	if reader == nil {
		reader = rand.Reader
	}
	buf := make([]byte, CodeLength)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", err
	}
	out := make([]byte, CodeLength)
	for i, b := range buf {
		out[i] = crockfordAlphabet[int(b)&31]
	}
	return string(out), nil
}

func NormalizeCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func ValidCode(raw string) bool {
	code := NormalizeCode(raw)
	if len(code) != CodeLength {
		return false
	}
	for _, ch := range code {
		if !strings.ContainsRune(crockfordAlphabet, ch) {
			return false
		}
	}
	return true
}
