package hermesops

// catalog.go 构建对话式运维助手(Python runner 里的 LLM)所见的、已脱敏的工具目录。存在两个目录:
//   - ReadOnlyCatalog:ONLY(只)含只读诊断工具(当前/默认的对话面)。mutating 工具永远不会出现
//     在这里,所以 LLM 无法说出一个从未被告知其存在的工具名。
//   - ProposableCatalog:只读工具 PLUS(加上)mutating(B 级)工具,每个 mutating 条目都被显式
//     标记。它让 LLM 能 PROPOSE(提议)一项 mutation——但绝不执行它:propose 路径只运行工具的只读
//     Resolve(dry-run preview + 一次性 correlation_id);mutation 仅在 OPERATOR(运营者)经一条
//     独立的、需运营者认证的路径确认后才运行。在接线处由一个默认 OFF(关)的 KNOB 门控;是否选用它
//     是 Owner 的决定。

// CatalogTool 是面向 LLM 的工具目录里的一个条目:模型选择工具并塑造其参数所需的身份 + 描述 +
// input-schema 提示。它 NO(不)携带任何 Run/Resolve/Mutate 函数——只有描述性的表层。
type CatalogTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]string `json:"input_schema"`
	// Mutating 标记一个 LLM 可 PROPOSE(提议)但绝不执行的工具。它 ONLY(只)由 ProposableCatalog
	// 设置;ReadOnlyCatalog 从不设置它。有 omitempty,false 值在线上不出现,所以这些字段的存在不会改变
	// ReadOnlyCatalog 的 JSON 字节。被提议的 mutating 工具会走 dry-run preview + 运营者确认;
	// LLM-propose 路径永远无法运行其 Mutate。
	Mutating bool `json:"mutating,omitempty"`
	// RequiresConfirmation 告诉 runner/LLM 在 mutation 提交前渲染一个确认步骤(必须由运营者批准)。
	// 与 Mutating 一同设置。
	RequiresConfirmation bool `json:"requires_confirmation,omitempty"`
}

// ReadOnlyCatalog 从 registry 返回 READ-ONLY 诊断工具的目录,按 name 排序。无论其他标志如何,
// mutating 工具在此都被过滤掉——门控是 spec.Mutating,与 dispatch 路径(Run)用来拒绝 mutation
// 的是同一个标志。该结果可安全注入 LLM 的上下文:它只暴露工具身份 + 只读参数提示,绝不暴露函数、
// 凭证或 mutating 能力。
//
// SAFETY(安全):这是对话路径上两道独立只读门控中的 FIRST(第一道)。即便 LLM 不知怎地从带外
// 被告知了某个 mutating 工具名,internal 端点自己的过滤器(以及 Registry.Run 的 ErrNotMutating)
// 仍会拒绝它。单独去掉 THIS(本)过滤器不会暴露 mutation;单独去掉端点过滤器也不会——两者必须都失效。
func (r *Registry) ReadOnlyCatalog() []CatalogTool {
	if r == nil {
		return nil
	}
	out := make([]CatalogTool, 0, len(r.tools))
	for _, s := range r.List() { // List() 已按 name 排序 + 稳定。
		if s.Mutating || !s.ReadOnly {
			// 结构性排除:mutating(或非只读)工具 NEVER(绝不)向 LLM 描述。这是只读保证在
			// 目录侧的那一半。
			continue
		}
		schema := s.InputSchema
		if schema == nil {
			schema = map[string]string{}
		}
		out = append(out, CatalogTool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: copyStringMap(schema),
		})
	}
	return out
}

// ProposableCatalog 返回 LLM 在对话式运维路径中可 PROPOSE(提议)的工具目录:全部只读诊断工具
// PLUS(加上)PROPOSABLE 的 mutating 工具(spec.Proposable——仅限可逆的 B 级),每个 mutating
// 条目都被显式标记 Mutating + RequiresConfirmation。按 name 排序。一个 NOT(不)Proposable 的
// mutating 工具(不可逆 / A 级,例如凭证轮换)被结构性排除——LLM 永远看不到它。
//
// 与 ReadOnlyCatalog 不同,本目录 DOES(确实)包含 mutating 工具——但 ONLY(只)为了让 LLM 能
// PROPOSE(提议)一项。它永远无法 EXECUTE(执行)一项:LLM-propose 路径(internal_tool_handler
// 的 propose 分支,在后续 PR 落地)只运行工具的只读 Resolve 来产出 dry-run preview + 一次性
// correlation_id;真正的 mutation 仅在 OPERATOR(运营者)经一条独立的、需运营者认证的路径确认后
// 才运行。这些标志让 runner/LLM 渲染"这是一项改动——它需要你的确认"。
//
// SAFETY(安全):把一个 mutating 工具的 NAME(名字)+ 参数提示暴露给 LLM,本身并不构成 mutation
// 能力。四道结构性 fail-closed 门控(propose 路径没有 Mutate 方法;confirm 仅经运营者认证;
// correlation_id 一次性 + 绑六元组;KNOB 默认关)都在下游。本方法在 propose 接线落地前 NO(没有)
// 调用方(在那之前故意是死代码),并在接线处由一个默认 OFF(关)的 KNOB 门控——合并它不改变任何
// 生产行为,且 ReadOnlyCatalog 保持逐字节不变。
func (r *Registry) ProposableCatalog() []CatalogTool {
	if r == nil {
		return nil
	}
	out := make([]CatalogTool, 0, len(r.tools))
	for _, s := range r.List() { // List() 已按 name 排序 + 稳定。
		schema := s.InputSchema
		if schema == nil {
			schema = map[string]string{}
		}
		entry := CatalogTool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: copyStringMap(schema),
		}
		switch {
		case s.Mutating && s.Proposable:
			// 一个 PROPOSABLE 的 mutating 工具(可逆的 B 级):LLM 可说出它的名字,runner 渲染
			// 一个确认步骤,执行受运营者 confirm 门控。打上标志好让 runner/LLM 知道它需要确认。
			entry.Mutating = true
			entry.RequiresConfirmation = true
		case s.Mutating:
			// mutating 但 NOT(不)proposable(不可逆 / A 级,例如凭证轮换):经 H1 confirm 路径
			// 运营者专属。NEVER(绝不)展示给 LLM——从 proposable 目录中结构性排除。fail-closed。
			continue
		case s.ReadOnly:
			// 只读诊断:proposable 且无标志,直接运行。
		default:
			// 既非只读也非 mutating:未分类的工具被排除(防御性——每个真实工具都已分类)。
			continue
		}
		out = append(out, entry)
	}
	return out
}

// copyStringMap 返回一个浅拷贝,这样调用方就无法通过返回的目录条目改动 registry 的
// InputSchema map。
func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
