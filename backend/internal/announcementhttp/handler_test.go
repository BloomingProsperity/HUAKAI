package announcementhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/announcement"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestAnnounceUserSeesActiveOnly(t *testing.T) {
	// 变异:ListActive 忽略 active/expires_at/published_at 过滤;未激活、已过期或尚未发布的行会泄漏到本响应中。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "active-now", Body: "visible", PublishedAt: ptrTime(now.Add(-time.Hour))})
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "inactive", Body: "hidden", Active: ptrBool(false), PublishedAt: ptrTime(now.Add(-time.Hour))})
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "expired", Body: "hidden", PublishedAt: ptrTime(now.Add(-2 * time.Hour)), ExpiresAt: ptrTime(now.Add(-time.Minute))})
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "future", Body: "hidden", PublishedAt: ptrTime(now.Add(time.Hour))})

	rec := serveAnnouncements(t, svc, fakeAdminAuth{}, nil, http.MethodGet, "/v1/announcements?tenant_id=7", nil)

	assertStatus(t, rec, http.StatusOK)
	var body announcementListResponse
	decodeJSON(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].Title != "active-now" {
		t.Fatalf("items=%+v want only active-now", body.Items)
	}
}

func TestAnnounceTenantScoped(t *testing.T) {
	// 变异:从 ListActive 去掉 tenant_id 谓词;租户 B 的行会出现在租户 A 处。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "tenant-a", Body: "a"})
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 8, Title: "tenant-b", Body: "b"})

	rec := serveAnnouncements(t, svc, fakeAdminAuth{}, nil, http.MethodGet, "/v1/announcements?tenant_id=7", nil)

	assertStatus(t, rec, http.StatusOK)
	var body announcementListResponse
	decodeJSON(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].TenantID != 7 || body.Items[0].Title != "tenant-a" {
		t.Fatalf("items=%+v want only tenant 7 announcement", body.Items)
	}
}

func TestAnnounceAdminCRUD(t *testing.T) {
	// 变异:admin 的 create/update/delete 与用户读取没有共用同一条 store/list 路径;后续某次读取会读到陈旧数据。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	auth := fakeAdminAuth{identity: admintest.Platform(99)}

	create := `{"tenant_id":7,"title":"Ops window","body":"Maintenance at 23:00 UTC","severity":"warning"}`
	rec := serveAnnouncements(t, svc, auth, nil, http.MethodPost, "/v1/admin/announcements", []byte(create))
	assertStatus(t, rec, http.StatusCreated)
	var created announcementResponse
	decodeJSON(t, rec, &created)
	if created.ID <= 0 || created.TenantID != 7 || created.Title != "Ops window" || created.Severity != "warning" {
		t.Fatalf("created=%+v", created)
	}

	rec = serveAnnouncements(t, svc, auth, nil, http.MethodGet, "/v1/admin/announcements?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	var adminList announcementListResponse
	decodeJSON(t, rec, &adminList)
	if len(adminList.Items) != 1 || adminList.Items[0].ID != created.ID {
		t.Fatalf("admin list=%+v want created id %d", adminList.Items, created.ID)
	}

	rec = serveAnnouncements(t, svc, auth, nil, http.MethodGet, "/v1/announcements?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	var userList announcementListResponse
	decodeJSON(t, rec, &userList)
	if len(userList.Items) != 1 || userList.Items[0].Title != "Ops window" {
		t.Fatalf("user list=%+v want created announcement", userList.Items)
	}

	update := `{"title":"Ops window updated"}`
	rec = serveAnnouncements(t, svc, auth, nil, http.MethodPut, "/v1/admin/announcements/"+strconv.FormatInt(created.ID, 10)+"?tenant_id=7", []byte(update))
	assertStatus(t, rec, http.StatusOK)
	var updated announcementResponse
	decodeJSON(t, rec, &updated)
	if updated.Title != "Ops window updated" || updated.Body != "Maintenance at 23:00 UTC" {
		t.Fatalf("updated=%+v want title changed and body preserved", updated)
	}

	rec = serveAnnouncements(t, svc, auth, nil, http.MethodDelete, "/v1/admin/announcements/"+strconv.FormatInt(created.ID, 10)+"?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	var deleted deleteResponse
	decodeJSON(t, rec, &deleted)
	if !deleted.Deleted || deleted.ID != created.ID {
		t.Fatalf("delete response=%+v", deleted)
	}

	rec = serveAnnouncements(t, svc, auth, nil, http.MethodGet, "/v1/announcements?tenant_id=7", nil)
	assertStatus(t, rec, http.StatusOK)
	decodeJSON(t, rec, &userList)
	if len(userList.Items) != 0 {
		t.Fatalf("user list after delete=%+v want empty", userList.Items)
	}
}

func TestAnnounceValidation(t *testing.T) {
	// 变异:绕过 HTTP/service 校验;非法 payload 被持久化而非返回 400。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	auth := fakeAdminAuth{identity: admintest.Platform(99)}

	tests := []struct {
		name string
		body string
	}{
		{name: "empty title", body: `{"tenant_id":7,"title":"","body":"body"}`},
		{name: "empty body", body: `{"tenant_id":7,"title":"title","body":""}`},
		{name: "bad severity", body: `{"tenant_id":7,"title":"title","body":"body","severity":"emergency"}`},
		{name: "expires before published", body: `{"tenant_id":7,"title":"title","body":"body","published_at":"2026-06-06T12:00:00Z","expires_at":"2026-06-06T12:00:00Z"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAnnouncements(t, svc, auth, nil, http.MethodPost, "/v1/admin/announcements", []byte(tt.body))
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestAnnounceAdminAuthRequired(t *testing.T) {
	// 变异:把任何已解析出的 admin token 都当作有权限;非 admin 角色也能 create/update/delete。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	existing := mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "locked", Body: "body"})
	nonAdmin := fakeAdminAuth{identity: admin.AdminIdentity{TokenID: 100, Role: "viewer"}}

	create := serveAnnouncements(t, svc, nonAdmin, nil, http.MethodPost, "/v1/admin/announcements", []byte(`{"tenant_id":7,"title":"bad","body":"bad"}`))
	assertStatus(t, create, http.StatusForbidden)
	update := serveAnnouncements(t, svc, nonAdmin, nil, http.MethodPut, "/v1/admin/announcements/"+strconv.FormatInt(existing.ID, 10)+"?tenant_id=7", []byte(`{"title":"bad"}`))
	assertStatus(t, update, http.StatusForbidden)
	del := serveAnnouncements(t, svc, nonAdmin, nil, http.MethodDelete, "/v1/admin/announcements/"+strconv.FormatInt(existing.ID, 10)+"?tenant_id=7", nil)
	assertStatus(t, del, http.StatusForbidden)

	items, err := svc.ListAllAdmin(context.Background(), announcement.ListAdminInput{TenantID: 7, Limit: 50})
	if err != nil {
		t.Fatalf("ListAllAdmin: %v", err)
	}
	if len(items) != 1 || items[0].Title != "locked" {
		t.Fatalf("items after forbidden mutations=%+v want original locked announcement", items)
	}
}

func TestAnnounceUserSessionScopeTakesPrecedenceOverPublicTenantQuery(t *testing.T) {
	// 变异:优先用公开的 tenant_id query 而非已认证 session 的 scope;租户 8 的数据被返回给租户 7 的 session。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "session-tenant", Body: "a"})
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 8, Title: "query-tenant", Body: "b"})
	session := &sessionauth.SessionIdentity{TenantID: 7, UserID: 42}

	rec := serveAnnouncements(t, svc, fakeAdminAuth{}, session, http.MethodGet, "/v1/announcements?tenant_id=8", nil)

	assertStatus(t, rec, http.StatusOK)
	var body announcementListResponse
	decodeJSON(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].Title != "session-tenant" {
		t.Fatalf("items=%+v want session-scoped tenant 7", body.Items)
	}
}

func TestAnnounceUserBearerSessionScopeTakesPrecedenceOverPublicTenantQuery(t *testing.T) {
	// 变异:忽略有效的 session bearer 而信任公开的 tenant_id query;租户 8 的数据被返回给租户 7 的 session。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newAnnouncementTestService(t, now)
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 7, Title: "bearer-tenant", Body: "a"})
	mustCreateAnnouncement(t, svc, announcement.CreateInput{TenantID: 8, Title: "query-tenant", Body: "b"})

	router := chi.NewRouter()
	MountUserRoutes(router, UserDeps{
		Service:  svc,
		Sessions: fakeSessionValidator{tenantID: 7, userID: 42},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/announcements?tenant_id=8", nil)
	req.Header.Set("Authorization", "Bearer session-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	var body announcementListResponse
	decodeJSON(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].Title != "bearer-tenant" {
		t.Fatalf("items=%+v want bearer session-scoped tenant 7", body.Items)
	}
}

type fakeAdminAuth struct {
	identity admin.AdminIdentity
	err      error
}

func (a fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.identity, nil
}

type fakeSessionValidator struct {
	tenantID int64
	userID   int64
	err      error
}

func (v fakeSessionValidator) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	if v.err != nil {
		return usersession.ValidatedSession{}, v.err
	}
	return usersession.ValidatedSession{TenantID: v.tenantID, UserID: v.userID, FamilyID: "family", TokenID: "token", Generation: 1}, nil
}

func newAnnouncementTestService(t *testing.T, now time.Time) *announcement.Service {
	t.Helper()
	return announcement.NewService(announcement.NewMemoryStore(), announcement.WithClock(func() time.Time { return now }))
}

func mustCreateAnnouncement(t *testing.T, svc *announcement.Service, in announcement.CreateInput) announcement.Announcement {
	t.Helper()
	created, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create seed %q: %v", in.Title, err)
	}
	return created
}

func serveAnnouncements(t *testing.T, svc *announcement.Service, auth AdminAuth, session *sessionauth.SessionIdentity, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	MountUserRoutes(router, UserDeps{Service: svc})
	MountAdminRoutes(router, AdminDeps{Auth: auth, Service: svc})
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != nil {
		req = req.WithContext(sessionauth.ContextWithSession(req.Context(), *session))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON: %v body=%s", err, rec.Body.String())
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func ptrBool(v bool) *bool {
	return &v
}
