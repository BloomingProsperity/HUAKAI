package credentialacq

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type BedrockBootstrapInput struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	ExpiresAt       time.Time
}

type AzureBootstrapInput struct {
	APIKey      string
	AccessToken string
	TenantID    string
	Deployment  string
	BaseURL     string
	ExpiresAt   time.Time
}

type VertexServiceAccountInput struct {
	ClientEmail           string
	PrivateKey            string
	ProjectID             string
	Location              string
	MetadataTokenEndpoint string
	AccessToken           string
	Scope                 string
	ExpiresAt             time.Time
}

func BuildBedrockCandidate(in BedrockBootstrapInput) (CredentialCandidate, error) {
	fields := map[string]any{
		"aws_access_key_id":     strings.TrimSpace(in.AccessKeyID),
		"aws_secret_access_key": strings.TrimSpace(in.SecretAccessKey),
		"aws_region":            strings.TrimSpace(in.Region),
	}
	if strings.TrimSpace(in.SessionToken) != "" {
		fields["aws_session_token"] = strings.TrimSpace(in.SessionToken)
	}
	if !in.ExpiresAt.IsZero() {
		fields["expires_at"] = in.ExpiresAt.UTC().Format(time.RFC3339)
	}
	payload, _ := json.Marshal(fields)
	return CredentialCandidate{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeBedrock,
		Payload: payload, RedactedContext: map[string]any{"cloud_bootstrap": "bedrock", "region": strings.TrimSpace(in.Region)},
	}, nil
}

func BuildAzureCandidate(in AzureBootstrapInput) (CredentialCandidate, error) {
	fields := map[string]any{
		"tenant_id":  strings.TrimSpace(in.TenantID),
		"deployment": strings.TrimSpace(in.Deployment),
		"base_url":   strings.TrimSpace(in.BaseURL),
	}
	if strings.TrimSpace(in.APIKey) != "" {
		fields["azure_api_key"] = strings.TrimSpace(in.APIKey)
	}
	if strings.TrimSpace(in.AccessToken) != "" {
		fields["access_token"] = strings.TrimSpace(in.AccessToken)
	}
	if !in.ExpiresAt.IsZero() {
		fields["expires_at"] = in.ExpiresAt.UTC().Format(time.RFC3339)
	}
	payload, _ := json.Marshal(fields)
	return CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure,
		Payload: payload, RedactedContext: map[string]any{"cloud_bootstrap": "azure", "deployment": strings.TrimSpace(in.Deployment)},
	}, nil
}

func BuildVertexCandidate(vendor string, in VertexServiceAccountInput) (CredentialCandidate, error) {
	vendor = credentialstore.Normalize(vendor)
	mode := credentialstore.AuthModeVertexSA
	if vendor == credentialstore.VendorAnthropic {
		mode = credentialstore.AuthModeVertexAnthropic
	}
	clientEmail := strings.TrimSpace(in.ClientEmail)
	clientEmailHash := ""
	if clientEmail != "" {
		sum := sha256.Sum256([]byte(clientEmail))
		clientEmailHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	fields := map[string]any{
		"client_email":            clientEmail,
		"project_id":              strings.TrimSpace(in.ProjectID),
		"location":                strings.TrimSpace(in.Location),
		"metadata_token_endpoint": strings.TrimSpace(in.MetadataTokenEndpoint),
		"scope":                   strings.TrimSpace(in.Scope),
	}
	if strings.TrimSpace(in.PrivateKey) != "" {
		fields["private_key"] = strings.TrimSpace(in.PrivateKey)
	}
	if strings.TrimSpace(in.AccessToken) != "" {
		fields["access_token"] = strings.TrimSpace(in.AccessToken)
	}
	if !in.ExpiresAt.IsZero() {
		fields["expires_at"] = in.ExpiresAt.UTC().Format(time.RFC3339)
	}
	payload, _ := json.Marshal(fields)
	return CredentialCandidate{
		Vendor: vendor, AuthMode: mode, Payload: payload,
		RedactedContext: map[string]any{
			"cloud_bootstrap": "vertex",
			"client_email":    clientEmailHash,
			"project_id":      strings.TrimSpace(in.ProjectID),
			"location":        strings.TrimSpace(in.Location),
		},
	}, nil
}
