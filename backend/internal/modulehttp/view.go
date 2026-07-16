// Package modulehttp 提供合并后的模块知识视图:它把实时的 moduleregistry
// (运行时 descriptor + 健康探针)与静态的 modulecatalog(从 feature-tree 派生的
// 身份:section、feature id、parity 状态、所属包)连接起来,并暴露:
//
//   - GET /admin/v1/modules (+ ?category=) —— 管理员门控、只读,供 Hermes
//     和 admin UI 查询每个模块的身份 + 能力 + 状态 + 实时探针。
//
// Hermes runner-context 馈送访问器在 H3 波次与其消费者一同加入(在本波次中
// 被排除,以免发布一个未接线、闲置无用的访问器)。
//
// 隐私:此接口面只携带模块身份、枚举状态和简短诊断 detail 字符串 ——
// 绝不含密钥或用户数据。它是运维/助手的根因主干,刻意不在任何请求热路径上。
package modulehttp

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// ModuleView 是一个模块的合并身份 + 运行时状态。
type ModuleView struct {
	ID           string                             `json:"id"`
	Category     string                             `json:"category"`
	Title        string                             `json:"title"`
	Capabilities []string                           `json:"capabilities,omitempty"`
	Activation   *moduleregistry.ActivationSnapshot `json:"activation,omitempty"`
	// 静态覆盖层(来自 feature-tree catalog),无 catalog 匹配时为 nil。
	Catalog *CatalogOverlay `json:"catalog,omitempty"`
	// 来自 registry Snapshot 的实时探针结果。
	LiveProbe moduleregistry.ProbeResult `json:"live_probe"`
}

// CatalogOverlay 是被合并到一个实时模块上的静态知识。
type CatalogOverlay struct {
	Section   string   `json:"section,omitempty"`
	FeatureID string   `json:"feature_id,omitempty"`
	Status    string   `json:"status,omitempty"`
	Parity    string   `json:"parity,omitempty"`
	Pkgs      []string `json:"pkgs,omitempty"`
}

// Source 提供合并视图的两个半边。把它藏在一个 interface 之后,使 handler
// 能用 fake 进行单元测试(无 DB、无真实接线)。
type Source interface {
	// Snapshot 返回实时的 descriptor + 探针结果。
	Snapshot(ctx context.Context) []moduleregistry.ModuleSnapshot
	// CatalogLookup 返回一个包短名对应的静态覆盖层。
	CatalogLookup(pkg string) (modulecatalog.CatalogModule, bool)
	// CatalogPkgFor 把一个实时模块 ID 映射到它的 catalog 包短名。seeds 会
	// 注册一个显式映射;未映射的 ID 产出 ("", false),视图便直接省略静态
	// 覆盖层(纯实时模块)。
	CatalogPkgFor(moduleID string) (string, bool)
}

// ContextSummary 是 Hermes 运维助手所消费的只读模块知识视图(H3 波次 ——
// 其消费者是 GET /v1/hermes/context)。它是跨所有 category 的合并模块身份 +
// 能力 + 实时状态,与运维人员看到的形状相同,这样助手就能把根因回答建立在
// 「什么已接线、健康程度如何」之上。
//
// 此访问器在 H2 落地时被刻意排除(当时尚无消费者);现在它与调用它的 hermes
// context 端点一同发布。它只携带模块身份、枚举状态和简短诊断 detail 字符串
// —— 绝不含密钥或用户数据。
func ContextSummary(ctx context.Context, src Source) []ModuleView {
	if src == nil {
		return []ModuleView{}
	}
	views := Merge(ctx, src, "")
	if views == nil {
		return []ModuleView{}
	}
	return views
}

// Merge 把实时 snapshot 与静态 catalog 连接成运维视图,可按 category 选择性
// 过滤(空 category = 全部)。结果保留 snapshot 的按 ID 排序顺序。
func Merge(ctx context.Context, src Source, category string) []ModuleView {
	snaps := src.Snapshot(ctx)
	views := make([]ModuleView, 0, len(snaps))
	for _, s := range snaps {
		d := s.Descriptor
		if category != "" && d.Category != category {
			continue
		}
		v := ModuleView{
			ID:           d.ID,
			Category:     d.Category,
			Title:        d.Title,
			Capabilities: d.Capabilities,
			Activation:   activationWithProbe(d.Activation, s.Probe.Status),
			LiveProbe:    s.Probe,
		}
		if pkg, ok := src.CatalogPkgFor(d.ID); ok {
			if cm, found := src.CatalogLookup(pkg); found {
				v.Catalog = &CatalogOverlay{
					Section:   cm.Section,
					FeatureID: cm.FeatureID,
					Status:    cm.Status,
					Parity:    cm.Parity,
					Pkgs:      cm.Pkgs,
				}
			}
		}
		views = append(views, v)
	}
	return views
}

func activationWithProbe(src *moduleregistry.ActivationSnapshot, status moduleregistry.ProbeStatus) *moduleregistry.ActivationSnapshot {
	if src == nil {
		return nil
	}
	out := *src
	switch status {
	case moduleregistry.StatusOK:
		verified := true
		out.Verified = &verified
	case moduleregistry.StatusDegraded, moduleregistry.StatusError:
		verified := false
		out.Verified = &verified
	default:
		out.Verified = nil
	}
	return &out
}
