// 包 provider — Adapter 静态注册表实现。
//
// 启动期注册各 vendor adapter 到 protocol family 字符串；运行期 dispatcher
// 按 protocolFamily 查表取 adapter。注册表本身不可变（启动期一次性 Register
// 完后只读），无需锁。
package provider

import (
	"errors"
	"fmt"
)

// ErrAdapterNotRegistered 表示 protocolFamily 没有注册对应的 adapter。
// 调用方判定走 errors.Is(err, ErrAdapterNotRegistered)。
var ErrAdapterNotRegistered = errors.New("provider: 未注册该 protocol family 的 adapter")

// ErrDuplicateRegistration 表示 protocolFamily 重复注册。
var ErrDuplicateRegistration = errors.New("provider: protocol family 重复注册")

// StaticRegistry 是 Adapter 的只读静态注册表。零值不可用 — 必须经
// NewStaticRegistry 构造。
type StaticRegistry struct {
	adapters map[string]Adapter
}

// NewStaticRegistry 返回空注册表。
func NewStaticRegistry() *StaticRegistry {
	return &StaticRegistry{adapters: make(map[string]Adapter)}
}

// Register 注册 protocolFamily → adapter 映射。重复注册返回
// ErrDuplicateRegistration（不覆盖）。protocolFamily 为空或 adapter 为
// nil 时返回 error。
func (r *StaticRegistry) Register(protocolFamily string, a Adapter) error {
	if protocolFamily == "" {
		return errors.New("provider: protocolFamily 不能为空")
	}
	if a == nil {
		return errors.New("provider: adapter 不能为 nil")
	}
	if _, ok := r.adapters[protocolFamily]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateRegistration, protocolFamily)
	}
	r.adapters[protocolFamily] = a
	return nil
}

// MustRegister 是 Register 的 panic 版本，仅用于启动期 init。
func (r *StaticRegistry) MustRegister(protocolFamily string, a Adapter) {
	if err := r.Register(protocolFamily, a); err != nil {
		panic(err)
	}
}

// For 返回 protocolFamily 对应的 adapter。未注册时返回
// ErrAdapterNotRegistered。
func (r *StaticRegistry) For(protocolFamily string) (Adapter, error) {
	if r == nil || r.adapters == nil {
		return nil, ErrAdapterNotRegistered
	}
	a, ok := r.adapters[protocolFamily]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotRegistered, protocolFamily)
	}
	return a, nil
}

// RegisteredProtocolFamilies 返回所有已注册的 protocolFamily 字符串
// （用于 admin 渲染 / 文档生成）。
func (r *StaticRegistry) RegisteredProtocolFamilies() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.adapters))
	for pf := range r.adapters {
		out = append(out, pf)
	}
	return out
}
