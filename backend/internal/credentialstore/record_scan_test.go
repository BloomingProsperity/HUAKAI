package credentialstore

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type credentialScanRowStub struct {
	scan func(...any) error
}

func (s credentialScanRowStub) Scan(dest ...any) error {
	return s.scan(dest...)
}

func TestScanCredentialRecordMapsEveryBaseColumn(t *testing.T) {
	stamp := time.Date(2026, 7, 22, 12, 34, 56, 0, time.UTC)
	value := "fingerprint"
	got, err := scanCredentialRecord(credentialScanRowStub{scan: func(dest ...any) error {
		if len(dest) != 26 {
			t.Fatalf("扫描目标数量=%d，want=26", len(dest))
		}
		*dest[0].(*int64) = 11
		*dest[1].(*int64) = 12
		*dest[2].(*int64) = 13
		*dest[3].(*string) = "claude"
		*dest[4].(*string) = "oauth"
		*dest[5].(*string) = "active"
		*dest[6].(*int32) = 14
		*dest[7].(*[]byte) = []byte("ciphertext")
		*dest[8].(*string) = "aes-gcm"
		*dest[9].(*string) = "key-1"
		*dest[10].(*[]byte) = []byte("nonce")
		*dest[11].(*string) = "aad"
		*dest[12].(**string) = &value
		*dest[13].(**string) = &value
		for _, index := range []int{14, 15, 16, 17, 18, 22, 23, 24, 25} {
			*dest[index].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: stamp, Valid: true}
		}
		*dest[19].(**string) = &value
		*dest[20].(**string) = &value
		*dest[21].(*int32) = 15
		return nil
	}})
	if err != nil {
		t.Fatalf("scanCredentialRecord() error = %v", err)
	}
	if got.ID != 11 || got.TenantID != 12 || got.ProviderAccountID != 13 || got.Vendor != "claude" || got.AuthMode != "oauth" || got.State != "active" || got.CredentialVersion != 14 {
		t.Fatalf("基础字段映射错误：%+v", got)
	}
	if string(got.EncryptedPayload) != "ciphertext" || string(got.Nonce) != "nonce" || got.EncryptionScheme != "aes-gcm" || got.KeyID != "key-1" || got.AADHash != "aad" {
		t.Fatalf("密文包字段映射错误：%+v", got)
	}
	if got.PayloadFingerprint == nil || got.RefreshTokenFingerprint == nil || got.LastRefreshOutcome == nil || got.FailureClass == nil || *got.PayloadFingerprint != value || *got.RefreshTokenFingerprint != value || *got.LastRefreshOutcome != value || *got.FailureClass != value || got.FailureCount != 15 {
		t.Fatalf("指纹或刷新字段映射错误：%+v", got)
	}
	if !got.AccessExpiresAt.Equal(stamp) || !got.RefreshExpiresAt.Equal(stamp) || !got.RefreshBeforeAt.Equal(stamp) || !got.GraceUntil.Equal(stamp) || !got.LastRefreshAt.Equal(stamp) || !got.NextAttemptAt.Equal(stamp) || !got.CreatedAt.Equal(stamp) || !got.UpdatedAt.Equal(stamp) || !got.DeletedAt.Equal(stamp) {
		t.Fatalf("时间字段映射错误：%+v", got)
	}
}

func TestScanCredentialRecordMapsNoRows(t *testing.T) {
	_, err := scanCredentialRecord(credentialScanRowStub{scan: func(...any) error {
		return pgx.ErrNoRows
	}})
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err=%v，want ErrCredentialNotFound", err)
	}
}
