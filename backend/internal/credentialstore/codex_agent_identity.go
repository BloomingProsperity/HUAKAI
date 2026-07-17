package credentialstore

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

var codexAgentIdentityFields = map[string]struct{}{
	"runtime_id":          {},
	"private_key_pkcs8":   {},
	"upstream_account_id": {},
	"upstream_user_id":    {},
	"task_id":             {},
	"email":               {},
	"plan":                {},
	"fedramp":             {},
}

type codexAgentIdentityHandler struct{}

func (codexAgentIdentityHandler) Vendor() string      { return VendorOpenAI }
func (codexAgentIdentityHandler) AuthMode() string    { return AuthModeCodexAgentIdentity }
func (codexAgentIdentityHandler) RuntimeKind() string { return RuntimeCodexAgentIdentity }
func (codexAgentIdentityHandler) Refreshable() bool   { return false }
func (codexAgentIdentityHandler) AllowGrace() bool    { return false }

func (codexAgentIdentityHandler) ValidatePayload(raw []byte) error {
	fields, err := parsePayloadFields(raw)
	if err != nil {
		return err
	}
	for key := range fields {
		if _, ok := codexAgentIdentityFields[key]; !ok {
			return fmt.Errorf("%w: openai/%s contains unsupported field %s", ErrInvalidPayload, AuthModeCodexAgentIdentity, key)
		}
	}
	for _, key := range []string{"runtime_id", "private_key_pkcs8", "upstream_account_id", "upstream_user_id"} {
		if fieldString(fields, key) == "" {
			return fmt.Errorf("%w: openai/%s requires %s", ErrInvalidPayload, AuthModeCodexAgentIdentity, key)
		}
	}
	for key, limit := range map[string]int{
		"runtime_id": 512, "private_key_pkcs8": 8192,
		"upstream_account_id": 512, "upstream_user_id": 512,
		"task_id": 4096, "email": 320, "plan": 256,
	} {
		if len(strings.TrimSpace(fieldString(fields, key))) > limit {
			return fmt.Errorf("%w: openai/%s %s is too large", ErrInvalidPayload, AuthModeCodexAgentIdentity, key)
		}
	}
	if rawFedRAMP, ok := fields["fedramp"]; ok {
		var value bool
		if err := json.Unmarshal(rawFedRAMP, &value); err != nil {
			return fmt.Errorf("%w: openai/%s fedramp must be boolean", ErrInvalidPayload, AuthModeCodexAgentIdentity)
		}
	}
	encoded := fieldString(fields, "private_key_pkcs8")
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("%w: openai/%s private_key_pkcs8 must be standard base64", ErrInvalidPayload, AuthModeCodexAgentIdentity)
	}
	defer privacy.Zeroize(der)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return fmt.Errorf("%w: openai/%s private_key_pkcs8 must contain PKCS#8", ErrInvalidPayload, AuthModeCodexAgentIdentity)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: openai/%s private_key_pkcs8 must contain Ed25519", ErrInvalidPayload, AuthModeCodexAgentIdentity)
	}
	privacy.Zeroize(privateKey)
	return nil
}

func (codexAgentIdentityHandler) RuntimeMaterial(raw []byte) (RuntimeMaterial, error) {
	if err := (codexAgentIdentityHandler{}).ValidatePayload(raw); err != nil {
		return RuntimeMaterial{}, err
	}
	return RuntimeMaterial{}, fmt.Errorf("%w: openai/%s requires the dynamic credential resolver", ErrRuntimeMaterial, AuthModeCodexAgentIdentity)
}
