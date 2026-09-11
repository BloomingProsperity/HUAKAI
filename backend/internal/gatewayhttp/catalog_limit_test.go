package gatewayhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestBindCatalogOutputLimitCopiesPositiveCatalog(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	catalog := 8192
	bindCatalogOutputLimit(env, &catalog)
	if env.RequestMeta.CatalogMaxOutputTokens == nil || *env.RequestMeta.CatalogMaxOutputTokens != 8192 {
		t.Fatalf("目录上限未写入 envelope: %+v", env.RequestMeta.CatalogMaxOutputTokens)
	}
	catalog = 1
	if *env.RequestMeta.CatalogMaxOutputTokens != 8192 {
		t.Fatal("必须拷贝，不能与 Resolved 共用指针")
	}
}

func TestBindCatalogOutputLimitSkipsMissingOrNonPositive(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	bindCatalogOutputLimit(env, nil)
	if env.RequestMeta.CatalogMaxOutputTokens != nil {
		t.Fatal("未登记不得写入")
	}
	zero := 0
	bindCatalogOutputLimit(env, &zero)
	if env.RequestMeta.CatalogMaxOutputTokens != nil {
		t.Fatal("非正目录不得写入")
	}
}

func TestBindCatalogOutputLimitNilEnvelope(t *testing.T) {
	catalog := 8192
	bindCatalogOutputLimit(nil, &catalog)
}
