package codexagent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

var errInvalidPayload = errors.New("codex agent: invalid credential payload")

type identityMaterial struct {
	RuntimeID         string `json:"runtime_id"`
	PrivateKeyEncoded string `json:"private_key_pkcs8"`
	UpstreamAccountID string `json:"upstream_account_id"`
	UpstreamUserID    string `json:"upstream_user_id"`
	ImportedTaskID    string `json:"task_id,omitempty"`
	Email             string `json:"email,omitempty"`
	Plan              string `json:"plan,omitempty"`
	FedRAMP           bool   `json:"fedramp,omitempty"`
	privateKey        ed25519.PrivateKey
}

func parseIdentityMaterial(raw []byte) (identityMaterial, error) {
	var material identityMaterial
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&material); err != nil {
		return identityMaterial{}, errInvalidPayload
	}
	material.RuntimeID = strings.TrimSpace(material.RuntimeID)
	material.PrivateKeyEncoded = strings.TrimSpace(material.PrivateKeyEncoded)
	material.UpstreamAccountID = strings.TrimSpace(material.UpstreamAccountID)
	material.UpstreamUserID = strings.TrimSpace(material.UpstreamUserID)
	material.ImportedTaskID = strings.TrimSpace(material.ImportedTaskID)
	material.Email = strings.TrimSpace(material.Email)
	material.Plan = strings.TrimSpace(material.Plan)
	if material.RuntimeID == "" || material.PrivateKeyEncoded == "" || material.UpstreamAccountID == "" || material.UpstreamUserID == "" {
		return identityMaterial{}, errInvalidPayload
	}
	der, err := base64.StdEncoding.DecodeString(material.PrivateKeyEncoded)
	if err != nil {
		return identityMaterial{}, errInvalidPayload
	}
	material.PrivateKeyEncoded = ""
	defer privacy.Zeroize(der)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return identityMaterial{}, errInvalidPayload
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return identityMaterial{}, errInvalidPayload
	}
	material.privateKey = privateKey
	return material, nil
}
