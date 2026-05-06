package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrAccountNotFound 表示 vault 中找不到该 accountID。
var ErrAccountNotFound = errors.New("provider credential account not found")

// CredentialVault 负责把 accountID 解析为上游凭据和账号信息。
type CredentialVault interface {
	// Resolve 按 accountID 查凭据和 AccountInfo。
	// 未找到时返回 ErrAccountNotFound，或返回可被 errors.Is 识别的包装错误。
	Resolve(ctx context.Context, accountID int64) (Credential, AccountInfo, error)
}

type staticVaultEntry struct {
	credential Credential
	account    AccountInfo
}

// StaticVault 是线程安全的内存 CredentialVault 实现。
type StaticVault struct {
	mu      sync.RWMutex
	entries map[int64]staticVaultEntry
}

var _ CredentialVault = (*StaticVault)(nil)

// NewStaticVault 创建空的内存凭据仓库。
func NewStaticVault() *StaticVault {
	return &StaticVault{
		entries: make(map[int64]staticVaultEntry),
	}
}

// Set 写入或覆盖指定 accountID 的凭据和账号信息。
func (v *StaticVault) Set(accountID int64, c Credential, a AccountInfo) error {
	if v == nil {
		return errors.New("provider static vault is nil")
	}
	if accountID == 0 {
		return errors.New("provider static vault accountID must not be zero")
	}
	if c.Type == "" {
		return errors.New("provider static vault credential type must not be empty")
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.entries == nil {
		v.entries = make(map[int64]staticVaultEntry)
	}
	v.entries[accountID] = staticVaultEntry{
		credential: c,
		account:    a,
	}
	return nil
}

// Resolve 按 accountID 返回凭据和账号信息。
func (v *StaticVault) Resolve(ctx context.Context, accountID int64) (Credential, AccountInfo, error) {
	_ = ctx

	if v == nil {
		return Credential{}, AccountInfo{}, fmt.Errorf("static vault is nil: %w", ErrAccountNotFound)
	}

	v.mu.RLock()
	entry, ok := v.entries[accountID]
	v.mu.RUnlock()
	if !ok {
		return Credential{}, AccountInfo{}, fmt.Errorf("account %d: %w", accountID, ErrAccountNotFound)
	}

	return entry.credential, entry.account, nil
}

// Size 返回当前保存的账号数量；nil receiver 返回 0。
func (v *StaticVault) Size() int {
	if v == nil {
		return 0
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.entries)
}
