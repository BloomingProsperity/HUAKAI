package announcementhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/announcement"
)

// fakeService:非 nil 后端,良性零值——让 handler 越过鉴权前 nil 后端 503 兜底走到真鉴权。
type fakeService struct{}

func (fakeService) Create(context.Context, announcement.CreateInput) (announcement.Announcement, error) {
	return announcement.Announcement{}, nil
}
func (fakeService) Update(context.Context, announcement.UpdateInput) (announcement.Announcement, error) {
	return announcement.Announcement{}, nil
}
func (fakeService) Delete(context.Context, int64, int64) error { return nil }
func (fakeService) ListActive(context.Context, announcement.ListActiveInput) ([]announcement.Announcement, error) {
	return nil, nil
}
func (fakeService) ListAllAdmin(context.Context, announcement.ListAdminInput) ([]announcement.Announcement, error) {
	return nil, nil
}

func mountAnnouncementAdmin(knob bool) http.Handler {
	r := chi.NewRouter()
	MountAdminRoutes(r, AdminDeps{Auth: adminsessionauthtest.Resolver(knob), Service: fakeService{}})
	return r
}

// SessionSafe 写端点(公告增改删)过鉴权≠401。
// 变异:摘任一路由的 safe → 该路由 session 写 401 → RED。
func TestAnnouncementSessionSafeWrites(t *testing.T) {
	h := mountAnnouncementAdmin(true)
	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/v1/admin/announcements"},
		{http.MethodPut, "/v1/admin/announcements/5"},
		{http.MethodDelete, "/v1/admin/announcements/5"},
	} {
		if code := adminsessionauthtest.Status(h, tc.m, tc.p, adminsessionauthtest.SessionBearer); code == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写 %s %s 应过鉴权(≠401),得 401", tc.m, tc.p)
		}
	}
}

// knob 关:session 写回退令牌通道被拒 401。
func TestAnnouncementKnobOff(t *testing.T) {
	h := mountAnnouncementAdmin(false)
	if code := adminsessionauthtest.Status(h, http.MethodDelete, "/v1/admin/announcements/5", adminsessionauthtest.SessionBearer); code != http.StatusUnauthorized {
		t.Fatalf("knob 关时 session 写应被拒 401,得 %d", code)
	}
}

// token 通道豁免:hk_admin 令牌写过鉴权≠401。
func TestAnnouncementTokenExempt(t *testing.T) {
	h := mountAnnouncementAdmin(true)
	if code := adminsessionauthtest.Status(h, http.MethodDelete, "/v1/admin/announcements/5", adminsessionauthtest.TokenBearer); code == http.StatusUnauthorized {
		t.Fatalf("hk_admin 令牌写应过鉴权(≠401),得 401")
	}
}
