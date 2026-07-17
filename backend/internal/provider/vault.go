package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrAccountNotFound 表示 vault 中找不到该 accountID。
var ErrAccountNotFound = errors.New("provider credential account not found")

// CredentialVault 负责把 (tenantID, accountID) 解析为上游凭据和账号信息。
// tenantID 强制 caller 显式声明租户边界, 防 cross-tenant credential 误发。
type CredentialVault interface {
	// Resolve 按 (tenantID, accountID) 查凭据和 AccountInfo。
	// 未找到时返回 ErrAccountNotFound, 或返回可被 errors.Is 识别的包装错误。
	// account.TenantID != tenantID → 返 ErrAccountNotFound (不暴露存在性)。
	Resolve(ctx context.Context, tenantID, accountID int64) (Credential, AccountInfo, error)
}

// DynamicCredentialInput 描述需要在每次请求前动态生成的凭据。
type DynamicCredentialInput struct {
	TenantID            int64
	ProviderAccountID   int64
	AccountCredentialID int64
	CredentialVersion   int32
	Vendor              string
	AuthMode            string
	Payload             []byte
}

// DynamicCredentialResolver 把加密仓库解出的长期材料转换成单次出站凭据。
// 实现不得把长期秘密放进返回值。
type DynamicCredentialResolver interface {
	ResolveDynamicCredential(context.Context, DynamicCredentialInput) (Credential, error)
}

// DynamicCredentialRecoverer 在上游明确拒绝某个短期动态绑定时，原账号生成
// 一份新凭据。recovered=false 表示该账号不适用此恢复协议。
type DynamicCredentialRecoverer interface {
	ShouldRecoverDynamicCredential(AccountInfo, int, []byte) bool
	RecoverDynamicCredential(context.Context, AccountInfo, Credential) (credential Credential, recovered bool, err error)
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

// Resolve 按 (tenantID, accountID) 返回凭据和账号信息。tenantID 与 entry
// 的 account.TenantID 不一致时返 ErrAccountNotFound (不区分租户错配跟账号
// 不存在, 避免存在性侧信道)。
func (v *StaticVault) Resolve(ctx context.Context, tenantID, accountID int64) (Credential, AccountInfo, error) {
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
	if tenantID != 0 && entry.account.TenantID != 0 && entry.account.TenantID != tenantID {
		return Credential{}, AccountInfo{}, fmt.Errorf("account %d tenant mismatch: %w", accountID, ErrAccountNotFound)
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
