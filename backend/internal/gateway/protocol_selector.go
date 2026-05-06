package gateway

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// ProtocolAdapterRegistry 按 ProtocolFamily 字符串返回对应的 UpstreamAdapter。
type ProtocolAdapterRegistry interface {
	For(protocolFamily string) (proto.UpstreamAdapter, error)
}

// ErrUnknownProtocolFamily 表示 protocolFamily 没有注册对应的 adapter。
var ErrUnknownProtocolFamily = errors.New("gateway: 未注册该 protocol family 的 upstream adapter")

var errDuplicateProtocolFamily = errors.New("gateway: protocol family 重复注册")

// StaticProtocolAdapterRegistry 是只读静态注册表；启动期 Register 完成后只读。
type StaticProtocolAdapterRegistry struct {
	adapters map[string]proto.UpstreamAdapter
}

// NewStaticProtocolAdapterRegistry 返回空的协议 adapter 注册表。
func NewStaticProtocolAdapterRegistry() *StaticProtocolAdapterRegistry {
	return &StaticProtocolAdapterRegistry{adapters: make(map[string]proto.UpstreamAdapter)}
}

// Register 注册 protocol family 到 upstream adapter 的映射。
func (r *StaticProtocolAdapterRegistry) Register(family string, a proto.UpstreamAdapter) error {
	if r == nil {
		return errors.New("gateway: protocol adapter registry 是 nil")
	}
	if family == "" {
		return errors.New("gateway: protocol family 不能为空")
	}
	if isNilUpstreamAdapter(a) {
		return errors.New("gateway: upstream adapter 不能为 nil")
	}
	if r.adapters == nil {
		r.adapters = make(map[string]proto.UpstreamAdapter)
	}
	if _, ok := r.adapters[family]; ok {
		return fmt.Errorf("%w: %s", errDuplicateProtocolFamily, family)
	}
	r.adapters[family] = a
	return nil
}

// MustRegister 是 Register 的 panic 版本，仅用于启动期确定性注册。
func (r *StaticProtocolAdapterRegistry) MustRegister(family string, a proto.UpstreamAdapter) {
	if err := r.Register(family, a); err != nil {
		panic(err)
	}
}

// For 返回 protocol family 对应的 upstream adapter。
func (r *StaticProtocolAdapterRegistry) For(family string) (proto.UpstreamAdapter, error) {
	if r == nil || r.adapters == nil {
		return nil, fmt.Errorf("%w: registry 未初始化", ErrUnknownProtocolFamily)
	}
	a, ok := r.adapters[family]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProtocolFamily, family)
	}
	return a, nil
}

// BuildDefaultProtocolAdapterRegistry 构造包含当前已实现 adapters 的默认注册表。
func BuildDefaultProtocolAdapterRegistry() *StaticProtocolAdapterRegistry {
	r := NewStaticProtocolAdapterRegistry()
	r.MustRegister("anthropic_messages", &proto.AnthropicAdapter{CarryForwardSignatureDelta: false})
	r.MustRegister("openai_chat", &proto.OpenAIAdapter{})
	r.MustRegister("openai_responses", &proto.OpenAIAdapter{})
	r.MustRegister("gemini_messages", &proto.GeminiAdapter{})
	return r
}

func isNilUpstreamAdapter(a proto.UpstreamAdapter) bool {
	if a == nil {
		return true
	}
	v := reflect.ValueOf(a)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

var _ ProtocolAdapterRegistry = (*StaticProtocolAdapterRegistry)(nil)
