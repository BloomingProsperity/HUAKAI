package hermesops

// CatalogTool 是提供给 MCP 客户端的工具合同，不携带任何执行函数或敏感数据。
type CatalogTool struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description"`
	InputSchema          map[string]any `json:"inputSchema"`
	ReadOnly             bool           `json:"-"`
	Mutating             bool           `json:"-"`
	RequiresConfirmation bool           `json:"-"`
}

// CatalogForRole 返回当前管理员真正有权使用的工具。改动型工具只有在允许提议、工具本身可提议
// 且必须人工确认时才会出现；模型永远看不到不可提议的危险操作。
func (r *Registry) CatalogForRole(role string, includeProposable bool) []CatalogTool {
	if r == nil {
		return nil
	}
	out := make([]CatalogTool, 0, len(r.tools))
	for _, spec := range r.List() {
		if !RoleAllowed(role, spec.RequiredRole) {
			continue
		}
		switch {
		case spec.ReadOnly && !spec.Mutating:
			out = append(out, catalogToolFromSpec(spec))
		case includeProposable && spec.Mutating && spec.Proposable && spec.RequiresConfirmation:
			out = append(out, catalogToolFromSpec(spec))
		}
	}
	return out
}

func catalogToolFromSpec(spec ToolSpec) CatalogTool {
	return CatalogTool{
		Name:                 spec.Name,
		Description:          spec.Description,
		InputSchema:          cloneJSONMap(spec.InputSchema),
		ReadOnly:             spec.ReadOnly,
		Mutating:             spec.Mutating,
		RequiresConfirmation: spec.RequiresConfirmation,
	}
}

func cloneJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch typed := value.(type) {
		case map[string]any:
			out[key] = cloneJSONMap(typed)
		case []string:
			out[key] = append([]string(nil), typed...)
		case []any:
			out[key] = append([]any(nil), typed...)
		default:
			out[key] = typed
		}
	}
	return out
}

// copyStringMap 复制结构化标签，防止工具结果把存储层的 map 暴露给调用方修改。
func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
