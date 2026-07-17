package claudecookie

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	StatusReady     = "ready"
	StatusConsumed  = "consumed"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
	DefaultTTL      = 10 * time.Minute
	terminalTTL     = 24 * time.Hour
	intakeVersion   = int32(1)
)

var (
	ErrSessionNotFound = errors.New("claude cookie intake session not found")
	ErrSessionExpired  = errors.New("claude cookie intake session expired")
	ErrSessionConsumed = errors.New("claude cookie intake session already consumed")
	ErrSessionClosed   = errors.New("claude cookie intake session closed")
	ErrSessionChanged  = errors.New("claude cookie intake session changed")
)

type Session struct {
	ID                      string            `json:"id"`
	TenantID                int64             `json:"tenant_id"`
	SourceKind              intake.SourceKind `json:"source_kind"`
	Vendor                  string            `json:"vendor"`
	AuthMode                string            `json:"auth_mode"`
	Status                  string            `json:"status"`
	CandidateCommitment     string            `json:"-"`
	RedactedContext         map[string]any    `json:"redacted_context"`
	ExpiresAt               time.Time         `json:"expires_at"`
	ConsumedAt              *time.Time        `json:"consumed_at,omitempty"`
	ResultProviderAccountID int64             `json:"result_provider_account_id,omitempty"`
	ResultCredentialID      int64             `json:"result_credential_id,omitempty"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
	ActorID                 string            `json:"-"`
	ActorRole               string            `json:"-"`
}

type CreateInput struct {
	TenantID     int64
	Candidate    credentialacq.CredentialCandidate
	Organization Organization
	ActorID      string
	ActorRole    string
	RequestID    string
	ExpiresAt    time.Time
}

type LoadedCandidate struct {
	Session   Session
	Candidate credentialacq.CredentialCandidate
}

type Store struct {
	db     db.DBTX
	cipher *credentialstore.Cipher
	now    func() time.Time
}

type storedCandidate struct {
	Vendor               string         `json:"vendor"`
	AuthMode             string         `json:"auth_mode"`
	Payload              []byte         `json:"payload"`
	RedactedContext      map[string]any `json:"redacted_context"`
	ExternalAccountID    string         `json:"external_account_id,omitempty"`
	ExternalSubjectID    string         `json:"external_subject_id,omitempty"`
	ExternalAccountEmail string         `json:"external_account_email,omitempty"`
	AccountIDSource      string         `json:"account_id_source,omitempty"`
}

type sessionRow struct {
	session          Session
	encrypted        []byte
	encryptionScheme *string
	keyID            *string
	nonce            []byte
	aadHash          *string
}

func NewStore(database db.DBTX, keys credentialstore.KeyProvider) *Store {
	return &Store{db: database, cipher: credentialstore.NewCipher(keys), now: time.Now}
}

func (s *Store) WithNow(now func() time.Time) *Store {
	copy := *s
	copy.now = now
	return &copy
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Session, error) {
	if s == nil || s.db == nil || s.cipher == nil {
		return Session{}, ErrSessionClosed
	}
	in.ActorID = strings.TrimSpace(in.ActorID)
	in.ActorRole = strings.TrimSpace(in.ActorRole)
	if in.TenantID <= 0 || in.ActorID == "" || in.ActorRole != "tenant_operator" {
		return Session{}, fmt.Errorf("%w: invalid tenant or actor", ErrInvalidInput)
	}
	candidate := in.Candidate
	if candidate.TenantID != 0 && candidate.TenantID != in.TenantID {
		return Session{}, fmt.Errorf("%w: candidate tenant mismatch", ErrInvalidInput)
	}
	candidate.TenantID = in.TenantID
	if credentialstore.Normalize(candidate.Vendor) != credentialstore.VendorAnthropic ||
		credentialstore.Normalize(candidate.AuthMode) != credentialstore.AuthModeClaudeAIOAuth || len(candidate.Payload) == 0 {
		return Session{}, fmt.Errorf("%w: candidate mode mismatch", ErrInvalidInput)
	}
	contextMap := cloneContext(candidate.RedactedContext)
	contextMap["organization_id"] = strings.TrimSpace(in.Organization.ID)
	if name := strings.TrimSpace(in.Organization.Name); name != "" {
		contextMap["organization_name"] = name
	}
	if kind := strings.TrimSpace(in.Organization.Type); kind != "" {
		contextMap["organization_type"] = kind
	}
	contextMap, err := credentialacq.ValidateRedactedContext(contextMap)
	if err != nil {
		return Session{}, err
	}
	encoded, err := json.Marshal(storedCandidate{
		Vendor: candidate.Vendor, AuthMode: candidate.AuthMode, Payload: candidate.Payload,
		RedactedContext: contextMap, ExternalAccountID: candidate.ExternalAccountID,
		ExternalSubjectID: candidate.ExternalSubjectID, ExternalAccountEmail: candidate.ExternalAccountEmail,
		AccountIDSource: candidate.AccountIDSource,
	})
	if err != nil {
		return Session{}, err
	}
	defer privacy.Zeroize(encoded)
	id := uuid.NewString()
	envelope, err := s.cipher.Encrypt(ctx, encoded, intakeAAD(in.TenantID, id))
	if err != nil {
		return Session{}, err
	}
	commitment := commitmentFor(envelope.Ciphertext)
	now := s.nowTime()
	expiresAt := in.ExpiresAt.UTC()
	if expiresAt.IsZero() {
		expiresAt = now.Add(DefaultTTL)
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(DefaultTTL)) {
		return Session{}, fmt.Errorf("%w: expires_at is outside the allowed window", ErrInvalidInput)
	}
	redactedRaw, err := json.Marshal(contextMap)
	if err != nil {
		return Session{}, err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO account_credential_intake_sessions (
			id, tenant_id, source_kind, vendor, auth_mode, status,
			encrypted_candidate, encryption_scheme, encryption_key_id, encryption_nonce, encryption_aad_hash,
			candidate_commitment, redacted_context, actor_id, actor_role, request_id,
			expires_at, created_at, updated_at
		) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14,$15,NULLIF($16,''),$17,$18,$18)`,
		id, in.TenantID, intake.SourceClaudeCookie, credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, StatusReady,
		envelope.Ciphertext, envelope.EncryptionScheme, envelope.KeyID, envelope.Nonce, envelope.AADHash,
		commitment, redactedRaw, in.ActorID, in.ActorRole, strings.TrimSpace(in.RequestID), expiresAt, now)
	if err != nil {
		return Session{}, err
	}
	return Session{
		ID: id, TenantID: in.TenantID, SourceKind: intake.SourceClaudeCookie,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Status: StatusReady, CandidateCommitment: commitment, RedactedContext: contextMap,
		ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) Load(ctx context.Context, tenantID int64, sessionID string) (LoadedCandidate, error) {
	if s == nil || s.db == nil || s.cipher == nil {
		return LoadedCandidate{}, ErrSessionClosed
	}
	canonicalID, err := canonicalSessionID(sessionID)
	if err != nil || tenantID <= 0 {
		return LoadedCandidate{}, ErrSessionNotFound
	}
	row, err := s.loadRow(ctx, s.db, tenantID, canonicalID)
	if err != nil {
		return LoadedCandidate{}, err
	}
	if row.session.Status != StatusReady {
		return LoadedCandidate{}, sessionStatusError(row.session.Status)
	}
	if !row.session.ExpiresAt.After(s.nowTime()) {
		_ = s.expireOne(ctx, tenantID, canonicalID)
		return LoadedCandidate{}, ErrSessionExpired
	}
	if commitmentFor(row.encrypted) != row.session.CandidateCommitment {
		return LoadedCandidate{}, ErrSessionChanged
	}
	plaintext, err := s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext: row.encrypted, Nonce: row.nonce, KeyID: dereference(row.keyID),
		EncryptionScheme: dereference(row.encryptionScheme), AADHash: dereference(row.aadHash),
	}, intakeAAD(tenantID, canonicalID))
	if err != nil {
		return LoadedCandidate{}, err
	}
	defer privacy.Zeroize(plaintext)
	var stored storedCandidate
	if err := json.Unmarshal(plaintext, &stored); err != nil {
		return LoadedCandidate{}, ErrSessionChanged
	}
	defer privacy.Zeroize(stored.Payload)
	if credentialstore.Normalize(stored.Vendor) != credentialstore.VendorAnthropic ||
		credentialstore.Normalize(stored.AuthMode) != credentialstore.AuthModeClaudeAIOAuth || len(stored.Payload) == 0 {
		privacy.Zeroize(stored.Payload)
		return LoadedCandidate{}, ErrSessionChanged
	}
	return LoadedCandidate{Session: row.session, Candidate: credentialacq.CredentialCandidate{
		TenantID: tenantID, Vendor: stored.Vendor, AuthMode: stored.AuthMode,
		Payload: append([]byte(nil), stored.Payload...), RedactedContext: cloneContext(stored.RedactedContext),
		ExternalAccountID: stored.ExternalAccountID, ExternalSubjectID: stored.ExternalSubjectID,
		ExternalAccountEmail: stored.ExternalAccountEmail, AccountIDSource: stored.AccountIDSource,
	}}, nil
}

func (s *Store) Consume(ctx context.Context, database db.DBTX, tenantID int64, sessionID, commitment string, providerAccountID, credentialID int64) error {
	if s == nil || database == nil || tenantID <= 0 || providerAccountID <= 0 || credentialID <= 0 {
		return ErrSessionClosed
	}
	canonicalID, err := canonicalSessionID(sessionID)
	if err != nil || !validCommitment(commitment) {
		return ErrSessionNotFound
	}
	now := s.nowTime()
	tag, err := database.Exec(ctx, `
		UPDATE account_credential_intake_sessions
		SET status=$1, encrypted_candidate=NULL, encryption_scheme=NULL, encryption_key_id=NULL,
			encryption_nonce=NULL, encryption_aad_hash=NULL, consumed_at=$2,
			result_provider_account_id=$3, result_credential_id=$4, updated_at=$2
		WHERE id=$5::uuid AND tenant_id=$6 AND status=$7 AND expires_at>$2 AND candidate_commitment=$8`,
		StatusConsumed, now, providerAccountID, credentialID, canonicalID, tenantID, StatusReady, commitment)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	row, loadErr := s.loadRow(ctx, database, tenantID, canonicalID)
	if loadErr != nil {
		return loadErr
	}
	if row.session.Status == StatusReady && !row.session.ExpiresAt.After(now) {
		return ErrSessionExpired
	}
	if row.session.Status == StatusReady && subtle.ConstantTimeCompare([]byte(row.session.CandidateCommitment), []byte(commitment)) != 1 {
		return ErrSessionChanged
	}
	return sessionStatusError(row.session.Status)
}

func (s *Store) ExpireReady(ctx context.Context, limit int) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrSessionClosed
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	now := s.nowTime()
	tag, err := s.db.Exec(ctx, `
		WITH picked AS (
			SELECT id FROM account_credential_intake_sessions
			WHERE status=$1 AND expires_at<=$2 ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $3
		)
		UPDATE account_credential_intake_sessions AS sessions
		SET status=$4, encrypted_candidate=NULL, encryption_scheme=NULL, encryption_key_id=NULL,
			encryption_nonce=NULL, encryption_aad_hash=NULL, updated_at=$2
		FROM picked WHERE sessions.id=picked.id`, StatusReady, now, limit, StatusExpired)
	return tag.RowsAffected(), err
}

func (s *Store) DeleteTerminal(ctx context.Context, limit int) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrSessionClosed
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	cutoff := s.nowTime().Add(-terminalTTL)
	tag, err := s.db.Exec(ctx, `
		DELETE FROM account_credential_intake_sessions WHERE id IN (
			SELECT id FROM account_credential_intake_sessions
			WHERE status IN ($1,$2,$3) AND updated_at<$4 ORDER BY updated_at,id LIMIT $5
		)`, StatusConsumed, StatusExpired, StatusCancelled, cutoff, limit)
	return tag.RowsAffected(), err
}

func (s *Store) expireOne(ctx context.Context, tenantID int64, sessionID string) error {
	now := s.nowTime()
	_, err := s.db.Exec(ctx, `
		UPDATE account_credential_intake_sessions
		SET status=$1, encrypted_candidate=NULL, encryption_scheme=NULL, encryption_key_id=NULL,
			encryption_nonce=NULL, encryption_aad_hash=NULL, updated_at=$2
		WHERE id=$3::uuid AND tenant_id=$4 AND status=$5 AND expires_at<=$2`,
		StatusExpired, now, sessionID, tenantID, StatusReady)
	return err
}

func (s *Store) loadRow(ctx context.Context, database db.DBTX, tenantID int64, sessionID string) (sessionRow, error) {
	var row sessionRow
	var sourceKind string
	var redactedRaw []byte
	var resultProviderAccountID, resultCredentialID *int64
	err := database.QueryRow(ctx, `
		SELECT id::text,tenant_id,source_kind,vendor,auth_mode,status,candidate_commitment,redacted_context,
			actor_id,actor_role,expires_at,consumed_at,result_provider_account_id,result_credential_id,created_at,updated_at,
			encrypted_candidate,encryption_scheme,encryption_key_id,encryption_nonce,encryption_aad_hash
		FROM account_credential_intake_sessions WHERE id=$1::uuid AND tenant_id=$2`, sessionID, tenantID).Scan(
		&row.session.ID, &row.session.TenantID, &sourceKind, &row.session.Vendor, &row.session.AuthMode,
		&row.session.Status, &row.session.CandidateCommitment, &redactedRaw, &row.session.ActorID, &row.session.ActorRole,
		&row.session.ExpiresAt, &row.session.ConsumedAt, &resultProviderAccountID, &resultCredentialID,
		&row.session.CreatedAt, &row.session.UpdatedAt, &row.encrypted, &row.encryptionScheme,
		&row.keyID, &row.nonce, &row.aadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionRow{}, ErrSessionNotFound
	}
	if err != nil {
		return sessionRow{}, err
	}
	row.session.SourceKind = intake.SourceKind(sourceKind)
	if resultProviderAccountID != nil {
		row.session.ResultProviderAccountID = *resultProviderAccountID
	}
	if resultCredentialID != nil {
		row.session.ResultCredentialID = *resultCredentialID
	}
	if len(redactedRaw) > 0 && json.Unmarshal(redactedRaw, &row.session.RedactedContext) != nil {
		return sessionRow{}, ErrSessionChanged
	}
	if row.session.RedactedContext == nil {
		row.session.RedactedContext = map[string]any{}
	}
	return row, nil
}

func intakeAAD(tenantID int64, sessionID string) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: tenantID, Vendor: credentialstore.VendorAnthropic,
		AuthMode: credentialstore.AuthModeClaudeAIOAuth, Version: intakeVersion,
		Context: "account-credential-intake:" + sessionID,
	}
}

func commitmentFor(ciphertext []byte) string {
	sum := sha256.Sum256(ciphertext)
	return hex.EncodeToString(sum[:])
}

func validCommitment(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func canonicalSessionID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func sessionStatusError(status string) error {
	switch status {
	case StatusConsumed:
		return ErrSessionConsumed
	case StatusExpired:
		return ErrSessionExpired
	case StatusCancelled:
		return ErrSessionClosed
	default:
		return ErrSessionChanged
	}
}

func cloneContext(input map[string]any) map[string]any {
	out := make(map[string]any, len(input)+3)
	for key, value := range input {
		out[key] = value
	}
	return out
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (s *Store) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
