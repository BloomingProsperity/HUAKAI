// StaticRegistry 单元测试。
package provider

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"testing"
)

// fakeAdapter 是测试用的最小 Adapter 实现。
type fakeAdapter struct{ name string }

func (f *fakeAdapter) Platform() string { return f.name }
func (f *fakeAdapter) AcceptableCredentialTypes() []CredentialType {
	return []CredentialType{CredentialTypeAPIKey}
}
func (f *fakeAdapter) BuildRequest(ctx context.Context, in BuildInput) (*http.Request, error) {
	return nil, nil
}

func TestStaticRegistry_RegisterAndFor(t *testing.T) {
	r := NewStaticRegistry()
	a := &fakeAdapter{name: "openai"}
	if err := r.Register("openai_chat", a); err != nil {
		t.Fatal(err)
	}
	got, err := r.For("openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	if got != a {
		t.Errorf("For 返回 adapter 不等于注册的实例")
	}
}

func TestStaticRegistry_NotRegistered(t *testing.T) {
	r := NewStaticRegistry()
	_, err := r.For("nonexistent")
	if !errors.Is(err, ErrAdapterNotRegistered) {
		t.Errorf("err=%v want ErrAdapterNotRegistered", err)
	}
}

func TestStaticRegistry_DuplicateRegistration(t *testing.T) {
	r := NewStaticRegistry()
	a := &fakeAdapter{name: "openai"}
	if err := r.Register("openai_chat", a); err != nil {
		t.Fatal(err)
	}
	err := r.Register("openai_chat", a)
	if !errors.Is(err, ErrDuplicateRegistration) {
		t.Errorf("err=%v want ErrDuplicateRegistration", err)
	}
}

func TestStaticRegistry_RejectEmptyFamily(t *testing.T) {
	r := NewStaticRegistry()
	if err := r.Register("", &fakeAdapter{}); err == nil {
		t.Error("空 protocolFamily 应被拒")
	}
}

func TestStaticRegistry_RejectNilAdapter(t *testing.T) {
	r := NewStaticRegistry()
	if err := r.Register("openai_chat", nil); err == nil {
		t.Error("nil adapter 应被拒")
	}
}

func TestStaticRegistry_MustRegisterPanicsOnDuplicate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("重复 MustRegister 应 panic")
		}
	}()
	r := NewStaticRegistry()
	a := &fakeAdapter{}
	r.MustRegister("x", a)
	r.MustRegister("x", a) // panic
}

func TestStaticRegistry_RegisteredProtocolFamilies(t *testing.T) {
	r := NewStaticRegistry()
	r.MustRegister("openai_chat", &fakeAdapter{name: "openai"})
	r.MustRegister("anthropic_messages", &fakeAdapter{name: "anthropic"})
	got := r.RegisteredProtocolFamilies()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "anthropic_messages" || got[1] != "openai_chat" {
		t.Errorf("RegisteredProtocolFamilies=%v", got)
	}
}

func TestStaticRegistry_NilReceiver(t *testing.T) {
	var r *StaticRegistry
	_, err := r.For("openai_chat")
	if !errors.Is(err, ErrAdapterNotRegistered) {
		t.Errorf("nil receiver err=%v want ErrAdapterNotRegistered", err)
	}
	if got := r.RegisteredProtocolFamilies(); got != nil {
		t.Errorf("nil receiver RegisteredProtocolFamilies=%v want nil", got)
	}
}
