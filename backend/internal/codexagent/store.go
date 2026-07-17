package codexagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const taskAADContext = "codex-agent-task-binding-v1"

type taskSubject struct {
	TenantID            int64
	ProviderAccountID   int64
	AccountCredentialID int64
	CredentialVersion   int32
	RuntimeID           string
}

type taskBinding struct {
	ID                  int64
	Subject             taskSubject
	EncryptedTask       []byte
	EncryptionScheme    string
	KeyID               string
	Nonce               []byte
	AADHash             string
	TaskFingerprint     string
	LeaseToken          string
	LeaseFence          int64
	LeaseExpiresAt      *time.Time
	RetryAfter          *time.Time
	ConsecutiveFailures int
}

type taskLease struct {
	Token string
	Fence int64
}

type taskStore struct {
	pool   *pgxpool.Pool
	cipher *credentialstore.Cipher
	keys   credentialstore.KeyProvider
	now    func() time.Time
}

func newTaskStore(pool *pgxpool.Pool, keys credentialstore.KeyProvider) *taskStore {
	return &taskStore{pool: pool, cipher: credentialstore.NewCipher(keys), keys: keys, now: time.Now}
}

func (s *taskStore) ensureRow(ctx context.Context, subject taskSubject, importedTask string) error {
	if s == nil || s.pool == nil || s.cipher == nil {
		return errors.New("codex agent: task store not configured")
	}
	runtimeHash := sha256Hex(subject.RuntimeID)
	if importedTask == "" {
		_, err := s.pool.Exec(ctx, `
INSERT INTO codex_agent_task_bindings (
    tenant_id, provider_account_id, account_credential_id, credential_version, runtime_id_hash
) SELECT $1, $2, $3, $4, $5
  FROM account_credentials ac
  JOIN provider_accounts pa ON pa.id = ac.provider_account_id AND pa.tenant_id = ac.tenant_id
 WHERE ac.id = $3 AND ac.tenant_id = $1 AND ac.provider_account_id = $2
   AND ac.credential_version = $4 AND ac.deleted_at IS NULL AND pa.deleted_at IS NULL
ON CONFLICT (tenant_id, provider_account_id, account_credential_id, credential_version) DO NOTHING`,
			subject.TenantID, subject.ProviderAccountID, subject.AccountCredentialID, subject.CredentialVersion, runtimeHash)
		return err
	}
	envelope, fingerprint, err := s.encryptTask(ctx, subject, importedTask)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO codex_agent_task_bindings (
    tenant_id, provider_account_id, account_credential_id, credential_version, runtime_id_hash,
    encrypted_task_id, encryption_scheme, key_id, nonce, aad_hash, task_fingerprint
) SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
  FROM account_credentials ac
  JOIN provider_accounts pa ON pa.id = ac.provider_account_id AND pa.tenant_id = ac.tenant_id
 WHERE ac.id = $3 AND ac.tenant_id = $1 AND ac.provider_account_id = $2
   AND ac.credential_version = $4 AND ac.deleted_at IS NULL AND pa.deleted_at IS NULL
ON CONFLICT (tenant_id, provider_account_id, account_credential_id, credential_version) DO NOTHING`,
		subject.TenantID, subject.ProviderAccountID, subject.AccountCredentialID, subject.CredentialVersion, runtimeHash,
		envelope.Ciphertext, envelope.EncryptionScheme, envelope.KeyID, envelope.Nonce, envelope.AADHash, fingerprint)
	return err
}

func (s *taskStore) load(ctx context.Context, subject taskSubject) (taskBinding, error) {
	var row taskBinding
	row.Subject = subject
	var encrypted, nonce []byte
	var scheme, keyID, aadHash, fingerprint, leaseToken *string
	err := s.pool.QueryRow(ctx, `
SELECT id, encrypted_task_id, encryption_scheme, key_id, nonce, aad_hash, task_fingerprint,
       lease_token, lease_fence, lease_expires_at, retry_after, consecutive_failures
FROM codex_agent_task_bindings
WHERE tenant_id = $1 AND provider_account_id = $2
  AND account_credential_id = $3 AND credential_version = $4
  AND runtime_id_hash = $5`, subject.TenantID, subject.ProviderAccountID,
		subject.AccountCredentialID, subject.CredentialVersion, sha256Hex(subject.RuntimeID)).Scan(
		&row.ID, &encrypted, &scheme, &keyID, &nonce, &aadHash, &fingerprint,
		&leaseToken, &row.LeaseFence, &row.LeaseExpiresAt, &row.RetryAfter, &row.ConsecutiveFailures)
	if err != nil {
		return taskBinding{}, err
	}
	row.EncryptedTask = encrypted
	row.Nonce = nonce
	row.EncryptionScheme = stringValue(scheme)
	row.KeyID = stringValue(keyID)
	row.AADHash = stringValue(aadHash)
	row.TaskFingerprint = stringValue(fingerprint)
	row.LeaseToken = stringValue(leaseToken)
	return row, nil
}

func (s *taskStore) decryptTask(ctx context.Context, row taskBinding) (string, error) {
	if len(row.EncryptedTask) == 0 {
		return "", nil
	}
	plaintext, err := s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext: row.EncryptedTask, EncryptionScheme: row.EncryptionScheme,
		KeyID: row.KeyID, Nonce: row.Nonce, AADHash: row.AADHash,
	}, taskAAD(row.Subject))
	if err != nil {
		return "", err
	}
	defer privacy.Zeroize(plaintext)
	return string(append([]byte(nil), plaintext...)), nil
}

func (s *taskStore) tryAcquire(ctx context.Context, subject taskSubject, duration time.Duration) (taskLease, bool, error) {
	token := uuid.NewString()
	now := s.now().UTC()
	var fence int64
	err := s.pool.QueryRow(ctx, `
UPDATE codex_agent_task_bindings
SET lease_token = $6,
    lease_fence = lease_fence + 1,
    lease_expires_at = $7,
    updated_at = $5
WHERE tenant_id = $1 AND provider_account_id = $2
  AND account_credential_id = $3 AND credential_version = $4
  AND encrypted_task_id IS NULL
  AND (retry_after IS NULL OR retry_after <= $5)
  AND (lease_token IS NULL OR lease_expires_at <= $5)
RETURNING lease_fence`, subject.TenantID, subject.ProviderAccountID,
		subject.AccountCredentialID, subject.CredentialVersion, now, token, now.Add(duration)).Scan(&fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskLease{}, false, nil
	}
	if err != nil {
		return taskLease{}, false, err
	}
	return taskLease{Token: token, Fence: fence}, true, nil
}

func (s *taskStore) complete(ctx context.Context, subject taskSubject, lease taskLease, taskID string) (bool, error) {
	envelope, fingerprint, err := s.encryptTask(ctx, subject, taskID)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE codex_agent_task_bindings
SET encrypted_task_id = $7, encryption_scheme = $8, key_id = $9,
    nonce = $10, aad_hash = $11, task_fingerprint = $12,
    lease_token = NULL, lease_expires_at = NULL, retry_after = NULL,
    consecutive_failures = 0, last_error_class = NULL, updated_at = $6
WHERE tenant_id = $1 AND provider_account_id = $2
  AND account_credential_id = $3 AND credential_version = $4
  AND lease_token = $5 AND lease_fence = $13`,
		subject.TenantID, subject.ProviderAccountID, subject.AccountCredentialID, subject.CredentialVersion,
		lease.Token, s.now().UTC(), envelope.Ciphertext, envelope.EncryptionScheme,
		envelope.KeyID, envelope.Nonce, envelope.AADHash, fingerprint, lease.Fence)
	return tag.RowsAffected() == 1, err
}

func (s *taskStore) fail(ctx context.Context, subject taskSubject, lease taskLease, errorClass string) error {
	now := s.now().UTC()
	tag, err := s.pool.Exec(ctx, `
UPDATE codex_agent_task_bindings
SET lease_token = NULL, lease_expires_at = NULL,
    consecutive_failures = consecutive_failures + 1,
    retry_after = $6::timestamptz + LEAST(interval '5 minutes', interval '5 seconds' * power(2, LEAST(consecutive_failures, 6))),
    last_error_class = $7, updated_at = $6
WHERE tenant_id = $1 AND provider_account_id = $2
  AND account_credential_id = $3 AND credential_version = $4
  AND lease_token = $5 AND lease_fence = $8`,
		subject.TenantID, subject.ProviderAccountID, subject.AccountCredentialID, subject.CredentialVersion,
		lease.Token, now, errorClass, lease.Fence)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("codex agent: registration failure lease lost")
	}
	return nil
}

func (s *taskStore) invalidate(ctx context.Context, subject taskSubject, expectedFingerprint string) (bool, error) {
	if expectedFingerprint == "" {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE codex_agent_task_bindings
SET encrypted_task_id = NULL, encryption_scheme = NULL, key_id = NULL,
    nonce = NULL, aad_hash = NULL, task_fingerprint = NULL,
    retry_after = NULL, consecutive_failures = 0, last_error_class = NULL,
    updated_at = $6
WHERE tenant_id = $1 AND provider_account_id = $2
  AND account_credential_id = $3 AND credential_version = $4
  AND task_fingerprint = $5`, subject.TenantID, subject.ProviderAccountID,
		subject.AccountCredentialID, subject.CredentialVersion, expectedFingerprint, s.now().UTC())
	return tag.RowsAffected() == 1, err
}

func (s *taskStore) encryptTask(ctx context.Context, subject taskSubject, taskID string) (credentialstore.Envelope, string, error) {
	if taskID == "" {
		return credentialstore.Envelope{}, "", errors.New("codex agent: empty task")
	}
	envelope, err := s.cipher.Encrypt(ctx, []byte(taskID), taskAAD(subject))
	if err != nil {
		return credentialstore.Envelope{}, "", err
	}
	key, err := s.keys.CurrentKey(ctx)
	if err != nil {
		return credentialstore.Envelope{}, "", err
	}
	defer privacy.Zeroize(key.Material)
	fingerprint := credentialstore.HMACFingerprint(key, "codex_agent_task", []byte(taskID))
	if fingerprint == "" {
		return credentialstore.Envelope{}, "", errors.New("codex agent: task fingerprint failed")
	}
	return envelope, fingerprint, nil
}

func taskAAD(subject taskSubject) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: subject.TenantID, ProviderAccountID: subject.ProviderAccountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgentIdentity,
		Version: subject.CredentialVersion, Context: fmt.Sprintf("%s:%d", taskAADContext, subject.AccountCredentialID),
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func bindingWait(row taskBinding, now time.Time) time.Duration {
	wait := 250 * time.Millisecond
	for _, deadline := range []*time.Time{row.LeaseExpiresAt, row.RetryAfter} {
		if deadline == nil || !deadline.After(now) {
			continue
		}
		until := deadline.Sub(now)
		if until < wait {
			wait = until
		}
	}
	if wait < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	return wait
}

func validateSubject(subject taskSubject) error {
	if subject.TenantID <= 0 || subject.ProviderAccountID <= 0 || subject.AccountCredentialID <= 0 ||
		subject.CredentialVersion <= 0 || subject.RuntimeID == "" {
		return fmt.Errorf("codex agent: invalid task subject")
	}
	return nil
}
