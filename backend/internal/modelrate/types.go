package modelrate

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	ActionUpsert = "upsert"
	ActionDelete = "delete"

	SourceOfficial = "official"
	SourceOverride = "override"

	// MaxUSDPerMillion 是单桶绝对价上限,防止把官方价改成天文数字。
	MaxUSDPerMillion = "10000"
)

// RateBuckets 是可选的输入/输出/缓存绝对单价。空指针表示该桶不覆盖,沿用官方价。
type RateBuckets struct {
	Input           *decimal.Decimal
	Output          *decimal.Decimal
	CacheRead       *decimal.Decimal
	CacheCreation   *decimal.Decimal
	CacheCreation5m *decimal.Decimal
	CacheCreation1h *decimal.Decimal
}

func (b RateBuckets) HasAny() bool {
	return b.Input != nil || b.Output != nil || b.CacheRead != nil ||
		b.CacheCreation != nil || b.CacheCreation5m != nil || b.CacheCreation1h != nil
}

func (b RateBuckets) Clone() RateBuckets {
	return RateBuckets{
		Input:           cloneDecimal(b.Input),
		Output:          cloneDecimal(b.Output),
		CacheRead:       cloneDecimal(b.CacheRead),
		CacheCreation:   cloneDecimal(b.CacheCreation),
		CacheCreation5m: cloneDecimal(b.CacheCreation5m),
		CacheCreation1h: cloneDecimal(b.CacheCreation1h),
	}
}

// Override 是一条平台级绝对价覆盖。
type Override struct {
	ID        int64
	Vendor    string
	Model     string
	Rates     RateBuckets
	CreatedBy string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type UpsertParams struct {
	Vendor    string
	Model     string
	Rates     RateBuckets
	Actor     string
	ActorRole string
}

type DeleteParams struct {
	Vendor    string
	Model     string
	Actor     string
	ActorRole string
}

type VerifyResult struct {
	OK     bool
	RowID  int64
	Reason string
}

// CatalogItem 同时给出官方价与生效价,供运营对照。
type CatalogItem struct {
	Vendor     string
	Model      string
	Official   RateBuckets
	Effective  RateBuckets
	Overridden bool
	Source     string
}

func cloneDecimal(v *decimal.Decimal) *decimal.Decimal {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func normalizeVendor(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeModel(v string) string {
	return strings.TrimSpace(v)
}

func bucketsJSON(b RateBuckets) json.RawMessage {
	obj := map[string]string{}
	writeBucket := func(key string, value *decimal.Decimal) {
		if value != nil {
			obj[key] = value.String()
		}
	}
	writeBucket("input_micro_usd", b.Input)
	writeBucket("output_micro_usd", b.Output)
	writeBucket("cache_read_micro_usd", b.CacheRead)
	writeBucket("cache_creation_micro_usd", b.CacheCreation)
	writeBucket("cache_creation_5m_micro_usd", b.CacheCreation5m)
	writeBucket("cache_creation_1h_micro_usd", b.CacheCreation1h)
	raw, _ := json.Marshal(obj)
	return raw
}
