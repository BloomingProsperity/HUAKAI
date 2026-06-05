package billing

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

// CacheOverrideScope 标识缓存价覆盖的三层 scope。
type CacheOverrideScope string

const (
	// CacheOverrideScopeGlobal 全局倍率,优先级最低。
	CacheOverrideScopeGlobal CacheOverrideScope = "global"
	// CacheOverrideScopeModel 每模型倍率,优先级居中(盖 global)。
	CacheOverrideScopeModel CacheOverrideScope = "model"
	// CacheOverrideScopeTenant 每租户倍率,优先级最高(盖 model / global)。
	CacheOverrideScopeTenant CacheOverrideScope = "tenant"
)

const (
	cacheOverrideActionUpsert = "upsert"
	cacheOverrideActionDelete = "delete"
)

var (
	// ErrCacheOverrideInvalid 倍率/scope/key 非法。
	ErrCacheOverrideInvalid = errors.New("billing: invalid cache price override")
	// ErrCacheOverrideSignerMissing store 未配置审计签名器。
	ErrCacheOverrideSignerMissing = errors.New("billing: cache override audit signer missing")
	// ErrCacheOverrideNotFound 删除一个不存在的覆盖。
	ErrCacheOverrideNotFound = errors.New("billing: cache price override not found")
)

// CacheOverrideKey 唯一定位一条覆盖记录。Global 时 Model/TenantID 为空/零;
// Model 时仅 Model 有值;Tenant 时仅 TenantID 有值。
type CacheOverrideKey struct {
	Scope    CacheOverrideScope
	Model    string
	TenantID int64
}

// CacheOverrideRecord 一条覆盖记录(对外只读快照)。
type CacheOverrideRecord struct {
	Key        CacheOverrideKey
	Multiplier decimal.Decimal
	UpdatedAt  time.Time
}

// CacheOverrideAuditEntry 是覆盖变更审计 hash-chain 的一环。
type CacheOverrideAuditEntry struct {
	Seq        int64
	OccurredAt time.Time
	ActorID    string
	Action     string
	Scope      CacheOverrideScope
	Model      string
	TenantID   int64
	OldRatio   *string
	NewRatio   *string
	PrevHash   []byte
	EntryHash  []byte
	Signature  []byte
	KeyID      string
}

// CacheOverrideStore 持有三层缓存价覆盖,变更走签名 hash-chain 审计。
//
// 优先级(取第一个有设的):tenant > model > global > 官方价(1.0)。
// 倍率是相对官方价的乘数;不设任何覆盖时 ResolveMultiplier 返回 1.0,
// 保证默认行为与官方价完全一致。
type CacheOverrideStore struct {
	signer *sign.Signer
	now    func() time.Time

	mu      sync.RWMutex
	records map[CacheOverrideKey]CacheOverrideRecord
	chain   []CacheOverrideAuditEntry
}

// NewCacheOverrideStore 构造一个空 store(默认全部走官方价)。
// signer 用于审计 hash-chain 签名;为 nil 时任何写操作返回
// ErrCacheOverrideSignerMissing(money 路径不静默吞错)。
func NewCacheOverrideStore(signer *sign.Signer, now func() time.Time) *CacheOverrideStore {
	if now == nil {
		now = time.Now
	}
	return &CacheOverrideStore{
		signer:  signer,
		now:     now,
		records: make(map[CacheOverrideKey]CacheOverrideRecord),
	}
}

// ResolveMultiplier 按优先级 tenant > model > global 取第一个已设倍率,
// 都没有则返回官方价倍率 1.0。这是计费点应调用的解析函数。
func (s *CacheOverrideStore) ResolveMultiplier(tenantID int64, model string) decimal.Decimal {
	if s == nil {
		return decimal.NewFromInt(1)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if tenantID > 0 {
		if rec, ok := s.records[CacheOverrideKey{Scope: CacheOverrideScopeTenant, TenantID: tenantID}]; ok {
			return rec.Multiplier
		}
	}
	if m := normalizeModel(model); m != "" {
		if rec, ok := s.records[CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: m}]; ok {
			return rec.Multiplier
		}
	}
	if rec, ok := s.records[CacheOverrideKey{Scope: CacheOverrideScopeGlobal}]; ok {
		return rec.Multiplier
	}
	return decimal.NewFromInt(1)
}

// List 返回当前所有覆盖记录的稳定排序快照(global, model, tenant)。
func (s *CacheOverrideStore) List() []CacheOverrideRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CacheOverrideRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return cacheOverrideKeyLess(out[i].Key, out[j].Key)
	})
	return out
}

// Set 设置/更新某 scope 的倍率,并追加一条签名审计记录。倍率必须为正。
func (s *CacheOverrideStore) Set(actorID string, key CacheOverrideKey, multiplier decimal.Decimal) (CacheOverrideRecord, error) {
	normKey, err := normalizeOverrideKey(key)
	if err != nil {
		return CacheOverrideRecord{}, err
	}
	if !multiplier.IsPositive() {
		return CacheOverrideRecord{}, ErrCacheOverrideInvalid
	}
	if s.signer == nil {
		return CacheOverrideRecord{}, ErrCacheOverrideSignerMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldRatio *string
	if prev, ok := s.records[normKey]; ok {
		txt := prev.Multiplier.String()
		oldRatio = &txt
	}
	newRatio := multiplier.String()
	if err := s.appendAuditLocked(actorID, cacheOverrideActionUpsert, normKey, oldRatio, &newRatio); err != nil {
		return CacheOverrideRecord{}, err
	}
	rec := CacheOverrideRecord{Key: normKey, Multiplier: multiplier, UpdatedAt: s.now().UTC()}
	s.records[normKey] = rec
	return rec, nil
}

// Delete 清除某 scope 的倍率(回到官方价),并追加一条签名审计记录。
func (s *CacheOverrideStore) Delete(actorID string, key CacheOverrideKey) error {
	normKey, err := normalizeOverrideKey(key)
	if err != nil {
		return err
	}
	if s.signer == nil {
		return ErrCacheOverrideSignerMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.records[normKey]
	if !ok {
		return ErrCacheOverrideNotFound
	}
	oldRatio := prev.Multiplier.String()
	if err := s.appendAuditLocked(actorID, cacheOverrideActionDelete, normKey, &oldRatio, nil); err != nil {
		return err
	}
	delete(s.records, normKey)
	return nil
}

// AuditChain 返回审计 hash-chain 的快照(按 Seq 升序)。
func (s *CacheOverrideStore) AuditChain() []CacheOverrideAuditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CacheOverrideAuditEntry, len(s.chain))
	copy(out, s.chain)
	return out
}

func normalizeModel(model string) string {
	return strings.TrimSpace(model)
}

func normalizeOverrideKey(key CacheOverrideKey) (CacheOverrideKey, error) {
	switch key.Scope {
	case CacheOverrideScopeGlobal:
		return CacheOverrideKey{Scope: CacheOverrideScopeGlobal}, nil
	case CacheOverrideScopeModel:
		m := normalizeModel(key.Model)
		if m == "" {
			return CacheOverrideKey{}, ErrCacheOverrideInvalid
		}
		return CacheOverrideKey{Scope: CacheOverrideScopeModel, Model: m}, nil
	case CacheOverrideScopeTenant:
		if key.TenantID <= 0 {
			return CacheOverrideKey{}, ErrCacheOverrideInvalid
		}
		return CacheOverrideKey{Scope: CacheOverrideScopeTenant, TenantID: key.TenantID}, nil
	default:
		return CacheOverrideKey{}, ErrCacheOverrideInvalid
	}
}

func cacheOverrideScopeRank(scope CacheOverrideScope) int {
	switch scope {
	case CacheOverrideScopeGlobal:
		return 0
	case CacheOverrideScopeModel:
		return 1
	case CacheOverrideScopeTenant:
		return 2
	default:
		return 3
	}
}

func cacheOverrideKeyLess(a, b CacheOverrideKey) bool {
	if ra, rb := cacheOverrideScopeRank(a.Scope), cacheOverrideScopeRank(b.Scope); ra != rb {
		return ra < rb
	}
	if a.Model != b.Model {
		return a.Model < b.Model
	}
	return a.TenantID < b.TenantID
}

func (s *CacheOverrideStore) appendAuditLocked(actorID, action string, key CacheOverrideKey, oldRatio, newRatio *string) error {
	actor := strings.TrimSpace(actorID)
	if actor == "" {
		return ErrCacheOverrideInvalid
	}
	var prevHash []byte
	if n := len(s.chain); n > 0 {
		prevHash = append([]byte(nil), s.chain[n-1].EntryHash...)
	}
	entry := CacheOverrideAuditEntry{
		Seq:        int64(len(s.chain) + 1),
		OccurredAt: s.now().UTC(),
		ActorID:    actor,
		Action:     action,
		Scope:      key.Scope,
		Model:      key.Model,
		TenantID:   key.TenantID,
		OldRatio:   cloneStr(oldRatio),
		NewRatio:   cloneStr(newRatio),
		PrevHash:   prevHash,
		KeyID:      s.signer.Fingerprint(),
	}
	canonical := canonicalCacheOverridePayload(entry)
	sum := sha256.Sum256(append(canonical, entry.PrevHash...))
	entry.EntryHash = append([]byte(nil), sum[:]...)
	entry.Signature = s.signer.Sign(entry.EntryHash)
	s.chain = append(s.chain, entry)
	return nil
}

// VerifyChain 验证审计 hash-chain 的完整性:prev_hash 链、entry_hash 重算、
// key_id 与签名。任一不符返回 false + 原因。空链视为通过。
func (s *CacheOverrideStore) VerifyChain() (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.signer == nil {
		return false, "signer missing"
	}
	pub := s.signer.PublicKey()
	var previous []byte
	for _, entry := range s.chain {
		if !bytes.Equal(entry.PrevHash, previous) {
			return false, "prev_hash mismatch at seq " + strconv.FormatInt(entry.Seq, 10)
		}
		canonical := canonicalCacheOverridePayload(entry)
		sum := sha256.Sum256(append(canonical, entry.PrevHash...))
		if !bytes.Equal(entry.EntryHash, sum[:]) {
			return false, "entry_hash mismatch at seq " + strconv.FormatInt(entry.Seq, 10)
		}
		if entry.KeyID != sign.Fingerprint(pub) {
			return false, "key_id mismatch at seq " + strconv.FormatInt(entry.Seq, 10)
		}
		if err := sign.Verify(pub, entry.EntryHash, entry.Signature); err != nil {
			return false, "signature mismatch at seq " + strconv.FormatInt(entry.Seq, 10)
		}
		previous = append(previous[:0], entry.EntryHash...)
	}
	return true, ""
}

func canonicalCacheOverridePayload(entry CacheOverrideAuditEntry) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	writeCanonStr(&buf, "actor_id", entry.ActorID, true)
	writeCanonStr(&buf, "action", entry.Action, false)
	writeCanonStr(&buf, "scope", string(entry.Scope), false)
	writeCanonStr(&buf, "model", entry.Model, false)
	writeCanonInt(&buf, "tenant_id", entry.TenantID, false)
	writeCanonOptStr(&buf, "old_ratio", entry.OldRatio, false)
	writeCanonOptStr(&buf, "new_ratio", entry.NewRatio, false)
	writeCanonStr(&buf, "occurred_at", entry.OccurredAt.UTC().Format(time.RFC3339Nano), false)
	writeCanonInt(&buf, "seq", entry.Seq, false)
	buf.WriteByte('}')
	return buf.Bytes()
}

func writeCanonStr(buf *bytes.Buffer, key, value string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONStr(buf, key)
	buf.WriteByte(':')
	writeJSONStr(buf, value)
}

func writeCanonInt(buf *bytes.Buffer, key string, value int64, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONStr(buf, key)
	buf.WriteByte(':')
	buf.WriteString(strconv.FormatInt(value, 10))
}

func writeCanonOptStr(buf *bytes.Buffer, key string, value *string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	writeJSONStr(buf, key)
	buf.WriteByte(':')
	if value == nil {
		buf.WriteString("null")
		return
	}
	writeJSONStr(buf, *value)
}

func writeJSONStr(buf *bytes.Buffer, value string) {
	raw, _ := json.Marshal(value)
	buf.Write(raw)
}

func cloneStr(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
