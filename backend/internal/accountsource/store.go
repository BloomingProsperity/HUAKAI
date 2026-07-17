package accountsource

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const sessionVersion = int32(1)

type Store struct {
	db     db.DBTX
	cipher *credentialstore.Cipher
	now    func() time.Time
}

type storedRow struct {
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
	if in.TenantID <= 0 || strings.TrimSpace(in.ActorID) == "" || in.ActorRole != "tenant_operator" || len(in.Items) == 0 || len(in.Items) > MaxItems {
		return Session{}, ErrInvalidInput
	}
	if in.SourceKind != intake.SourceCRSSync && in.SourceKind != intake.SourceAccountRecovery {
		return Session{}, ErrInvalidInput
	}
	for index := range in.Items {
		candidate := &in.Items[index].Candidate
		if candidate.TenantID != 0 && candidate.TenantID != in.TenantID {
			return Session{}, ErrInvalidInput
		}
		candidate.TenantID = in.TenantID
		candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
		candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
		if candidate.Vendor == "" || candidate.AuthMode == "" || len(candidate.Payload) == 0 {
			return Session{}, ErrInvalidInput
		}
	}
	encoded, err := json.Marshal(in.Items)
	if err != nil {
		return Session{}, err
	}
	defer privacy.Zeroize(encoded)
	id := uuid.NewString()
	envelope, err := s.cipher.Encrypt(ctx, encoded, sessionAAD(in.TenantID, in.SourceKind, id))
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
		return Session{}, ErrInvalidInput
	}
	contextRaw, err := json.Marshal(cloneContext(in.RedactedContext))
	if err != nil {
		return Session{}, err
	}
	_, err = s.db.Exec(ctx, `
		WITH inserted AS (
		INSERT INTO account_source_intake_sessions (
			id,tenant_id,source_kind,status,encrypted_items,encryption_scheme,encryption_key_id,
			encryption_nonce,encryption_aad_hash,source_commitment,item_count,redacted_context,
			actor_id,actor_role,request_id,expires_at,created_at,updated_at
		) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14,NULLIF($15,''),$16,$17,$17)
		RETURNING tenant_id
		)
		INSERT INTO admin_audit_events (
			tenant_id,actor_id,actor_role,action,target_type,target_id,request_id,payload,occurred_at
		)
		SELECT tenant_id,$13,$14,'preview_account_source','tenant',tenant_id,NULLIF($15,''),
			jsonb_build_object('source_kind',$3::text,'item_count',$11::integer,'session_id',$1::text),$17
		FROM inserted`,
		id, in.TenantID, in.SourceKind, StatusReady, envelope.Ciphertext, envelope.EncryptionScheme,
		envelope.KeyID, envelope.Nonce, envelope.AADHash, commitment, len(in.Items), contextRaw,
		strings.TrimSpace(in.ActorID), in.ActorRole, strings.TrimSpace(in.RequestID), expiresAt, now)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, TenantID: in.TenantID, SourceKind: in.SourceKind, Status: StatusReady,
		SourceCommitment: commitment, ItemCount: len(in.Items), RedactedContext: cloneContext(in.RedactedContext),
		ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) Load(ctx context.Context, tenantID int64, sessionID string) (Loaded, error) {
	canonicalID, err := canonicalSessionID(sessionID)
	if s == nil || s.db == nil || s.cipher == nil || err != nil || tenantID <= 0 {
		return Loaded{}, ErrSessionNotFound
	}
	row, err := s.loadRow(ctx, tenantID, canonicalID)
	if err != nil {
		return Loaded{}, err
	}
	if row.session.Status != StatusReady {
		return Loaded{}, ErrSessionClosed
	}
	if !row.session.ExpiresAt.After(s.nowTime()) {
		_ = s.expireOne(ctx, tenantID, canonicalID)
		return Loaded{}, ErrSessionExpired
	}
	if subtle.ConstantTimeCompare([]byte(commitmentFor(row.encrypted)), []byte(row.session.SourceCommitment)) != 1 {
		return Loaded{}, ErrSessionChanged
	}
	plaintext, err := s.cipher.Decrypt(ctx, credentialstore.Envelope{
		Ciphertext: row.encrypted, Nonce: row.nonce, KeyID: dereference(row.keyID),
		EncryptionScheme: dereference(row.encryptionScheme), AADHash: dereference(row.aadHash),
	}, sessionAAD(tenantID, row.session.SourceKind, canonicalID))
	if err != nil {
		return Loaded{}, err
	}
	defer privacy.Zeroize(plaintext)
	var items []Item
	if json.Unmarshal(plaintext, &items) != nil || len(items) != row.session.ItemCount || len(items) == 0 || len(items) > MaxItems {
		ZeroizeItems(items)
		return Loaded{}, ErrSessionChanged
	}
	for index := range items {
		items[index].Candidate.TenantID = tenantID
		items[index].Candidate.Payload = append([]byte(nil), items[index].Candidate.Payload...)
	}
	return Loaded{Session: row.session, Items: items}, nil
}

func (s *Store) ExpireReady(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	now := s.nowTime()
	tag, err := s.db.Exec(ctx, `
		WITH picked AS (
			SELECT id FROM account_source_intake_sessions
			WHERE status=$1 AND expires_at<=$2 ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $3
		)
		UPDATE account_source_intake_sessions AS sessions
		SET status=$4,encrypted_items=NULL,encryption_scheme=NULL,encryption_key_id=NULL,
			encryption_nonce=NULL,encryption_aad_hash=NULL,updated_at=$2
		FROM picked WHERE sessions.id=picked.id`, StatusReady, now, limit, StatusExpired)
	return tag.RowsAffected(), err
}

func (s *Store) loadRow(ctx context.Context, tenantID int64, sessionID string) (storedRow, error) {
	var row storedRow
	var sourceKind string
	var redactedRaw []byte
	err := s.db.QueryRow(ctx, `
		SELECT id::text,tenant_id,source_kind,status,source_commitment,item_count,redacted_context,
			expires_at,created_at,updated_at,encrypted_items,encryption_scheme,encryption_key_id,
			encryption_nonce,encryption_aad_hash
		FROM account_source_intake_sessions WHERE id=$1::uuid AND tenant_id=$2`, sessionID, tenantID).Scan(
		&row.session.ID, &row.session.TenantID, &sourceKind, &row.session.Status,
		&row.session.SourceCommitment, &row.session.ItemCount, &redactedRaw,
		&row.session.ExpiresAt, &row.session.CreatedAt, &row.session.UpdatedAt,
		&row.encrypted, &row.encryptionScheme, &row.keyID, &row.nonce, &row.aadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedRow{}, ErrSessionNotFound
	}
	if err != nil {
		return storedRow{}, err
	}
	row.session.SourceKind = intake.SourceKind(sourceKind)
	if len(redactedRaw) > 0 && json.Unmarshal(redactedRaw, &row.session.RedactedContext) != nil {
		return storedRow{}, ErrSessionChanged
	}
	if row.session.RedactedContext == nil {
		row.session.RedactedContext = map[string]any{}
	}
	return row, nil
}

func (s *Store) expireOne(ctx context.Context, tenantID int64, sessionID string) error {
	now := s.nowTime()
	_, err := s.db.Exec(ctx, `
		UPDATE account_source_intake_sessions
		SET status=$1,encrypted_items=NULL,encryption_scheme=NULL,encryption_key_id=NULL,
			encryption_nonce=NULL,encryption_aad_hash=NULL,updated_at=$2
		WHERE id=$3::uuid AND tenant_id=$4 AND status=$5 AND expires_at<=$2`,
		StatusExpired, now, sessionID, tenantID, StatusReady)
	return err
}

func sessionAAD(tenantID int64, source intake.SourceKind, sessionID string) credentialstore.AAD {
	return credentialstore.AAD{TenantID: tenantID, Vendor: "account_source", AuthMode: string(source),
		Version: sessionVersion, Context: "account-source-intake:" + sessionID}
}

func commitmentFor(ciphertext []byte) string {
	sum := sha256.Sum256(ciphertext)
	return hex.EncodeToString(sum[:])
}

func canonicalSessionID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func cloneContext(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
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

func ZeroizeItems(items []Item) {
	for index := range items {
		privacy.Zeroize(items[index].Candidate.Payload)
		items[index].Candidate.Payload = nil
	}
}

func (s *Store) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
