package codexagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const registrationLeaseDuration = 35 * time.Second

type Service struct {
	store       *taskStore
	credentials *credentialstore.Store
	client      *registrationClient
	now         func() time.Time
}

func NewService(store *taskStore, credentials *credentialstore.Store, client *registrationClient) *Service {
	return &Service{store: store, credentials: credentials, client: client, now: time.Now}
}

func (s *Service) ResolveDynamicCredential(ctx context.Context, input provider.DynamicCredentialInput) (provider.Credential, error) {
	if input.Vendor != credentialstore.VendorOpenAI || input.AuthMode != credentialstore.AuthModeCodexAgentIdentity {
		return provider.Credential{}, errors.New("codex agent: dynamic mode mismatch")
	}
	material, err := parseIdentityMaterial(input.Payload)
	if err != nil {
		return provider.Credential{}, err
	}
	defer privacy.Zeroize(material.privateKey)
	subject := taskSubject{
		TenantID: input.TenantID, ProviderAccountID: input.ProviderAccountID,
		AccountCredentialID: input.AccountCredentialID, CredentialVersion: input.CredentialVersion,
		RuntimeID: material.RuntimeID,
	}
	return s.resolve(ctx, subject, material)
}

func (s *Service) RecoverDynamicCredential(ctx context.Context, account provider.AccountInfo, rejected provider.Credential) (provider.Credential, bool, error) {
	if account.AccountType != credentialstore.AuthModeCodexAgentIdentity {
		return provider.Credential{}, false, nil
	}
	if s == nil || s.credentials == nil || s.store == nil || s.client == nil {
		return provider.Credential{}, true, errors.New("codex agent: runtime service not configured")
	}
	record, err := s.credentials.ResolveActive(ctx, account.TenantID, account.AccountID)
	if err != nil {
		return provider.Credential{}, true, err
	}
	defer privacy.Zeroize(record.PlaintextPayload)
	if record.ID != account.AccountCredentialID || record.CredentialVersion != int32(account.CredentialVersion) ||
		record.Vendor != credentialstore.VendorOpenAI || record.AuthMode != credentialstore.AuthModeCodexAgentIdentity {
		return provider.Credential{}, true, errors.New("codex agent: active credential changed during recovery")
	}
	material, err := parseIdentityMaterial(record.PlaintextPayload)
	if err != nil {
		return provider.Credential{}, true, err
	}
	defer privacy.Zeroize(material.privateKey)
	subject := taskSubject{
		TenantID: record.TenantID, ProviderAccountID: record.ProviderAccountID,
		AccountCredentialID: record.ID, CredentialVersion: record.CredentialVersion,
		RuntimeID: material.RuntimeID,
	}
	if _, err := s.store.invalidate(ctx, subject, rejected.RuntimeRef); err != nil {
		return provider.Credential{}, true, err
	}
	credential, err := s.resolve(ctx, subject, material)
	return credential, true, err
}

func (s *Service) ShouldRecoverDynamicCredential(account provider.AccountInfo, status int, body []byte) bool {
	return account.AccountType == credentialstore.AuthModeCodexAgentIdentity && InvalidTaskResponse(status, body)
}

func (s *Service) resolve(ctx context.Context, subject taskSubject, material identityMaterial) (provider.Credential, error) {
	if s == nil || s.store == nil || s.client == nil {
		return provider.Credential{}, errors.New("codex agent: runtime service not configured")
	}
	if err := validateSubject(subject); err != nil {
		return provider.Credential{}, err
	}
	if err := s.store.ensureRow(ctx, subject, material.ImportedTaskID); err != nil {
		return provider.Credential{}, err
	}
	for {
		row, err := s.store.load(ctx, subject)
		if err != nil {
			return provider.Credential{}, err
		}
		if len(row.EncryptedTask) > 0 {
			taskID, err := s.store.decryptTask(ctx, row)
			if err != nil {
				return provider.Credential{}, err
			}
			assertion, err := buildAssertion(material.RuntimeID, taskID, material.privateKey, s.now())
			if err != nil {
				return provider.Credential{}, err
			}
			extra := map[string]string{
				"chatgpt_account_id": material.UpstreamAccountID,
				"originator":         "codex_cli_rs",
			}
			if material.FedRAMP {
				extra["x_openai_fedramp"] = "true"
			}
			return provider.Credential{
				Type: provider.CredentialTypeUpstreamPassthrough, Value: assertion,
				RuntimeRef: row.TaskFingerprint,
				Extra:      extra,
			}, nil
		}
		now := s.now().UTC()
		if row.RetryAfter != nil && row.RetryAfter.After(now) {
			return provider.Credential{}, errors.New("codex agent: registration retry deferred")
		}
		lease, acquired, err := s.store.tryAcquire(ctx, subject, registrationLeaseDuration)
		if err != nil {
			return provider.Credential{}, err
		}
		if acquired {
			taskID, registerErr := s.client.register(ctx, subject.ProviderAccountID, material)
			if registerErr != nil {
				if failErr := s.store.fail(ctx, subject, lease, "registration_failed"); failErr != nil {
					return provider.Credential{}, fmt.Errorf("codex agent: registration failed and backoff update failed: %w", failErr)
				}
				return provider.Credential{}, registerErr
			}
			completed, completeErr := s.store.complete(ctx, subject, lease, taskID)
			if completeErr != nil {
				return provider.Credential{}, completeErr
			}
			if !completed {
				return provider.Credential{}, errors.New("codex agent: registration lease lost")
			}
			continue
		}
		wait := bindingWait(row, s.now().UTC())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return provider.Credential{}, fmt.Errorf("codex agent: waiting for task binding: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

var _ provider.DynamicCredentialResolver = (*Service)(nil)
var _ provider.DynamicCredentialRecoverer = (*Service)(nil)
