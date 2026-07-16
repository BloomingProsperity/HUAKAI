package usernoticehttp

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
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/usernotice"
)

func TestBroadcast_AdminAuthRequired(t *testing.T) {
	// 变异：跳过 admin 鉴权或角色检查；匿名/viewer 调用方就能广播通知行。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newNoticeHTTPService(&now)
	store.AddActiveUser(7, 101)

	body := []byte(`{"title":"Ops","body":"Tenant notice"}`)
	anon := serveUserNotices(t, svc, fakeAdminAuth{err: admin.ErrAdminUnauthorized}, nil, http.MethodPost, "/v1/admin/notifications/broadcast", body)
	assertNoticeStatus(t, anon, http.StatusUnauthorized)
	viewer := serveUserNotices(t, svc, fakeAdminAuth{identity: admin.AdminIdentity{TokenID: 99, Role: "viewer"}}, nil, http.MethodPost, "/v1/admin/notifications/broadcast", body)
	assertNoticeStatus(t, viewer, http.StatusForbidden)

	count, err := svc.UnreadCount(context.Background(), 7, 101)
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("unread count after forbidden broadcasts=%d want 0", count)
	}
}

func TestBroadcast_Validation(t *testing.T) {
	// 变异：绕过 HTTP/service 校验；空 title/body 或非法 severity 会广播通知行而非返回 400。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newNoticeHTTPService(&now)
	store.AddActiveUser(7, 101)
	auth := fakeAdminAuth{identity: admintest.TenantOperator(99, 7)}

	tests := []struct {
		name string
		body string
	}{
		{name: "empty title", body: `{"title":"","body":"body"}`},
		{name: "empty body", body: `{"title":"title","body":""}`},
		{name: "bad severity", body: `{"title":"title","body":"body","severity":"emergency"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveUserNotices(t, svc, auth, nil, http.MethodPost, "/v1/admin/notifications/broadcast", []byte(tt.body))
			assertNoticeStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestListNotifications_SelfScopedUnreadFirst(t *testing.T) {
	// 变异：handler 信任 query/body 中的 user 作用域，或 service 丢掉 user_id；用户 A 会看到用户 B 的行，或在 unread-only 模式下看到已读行。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newNoticeHTTPService(&now)
	store.AddActiveUser(7, 101)
	store.AddActiveUser(7, 202)
	auth := fakeAdminAuth{identity: admintest.TenantOperator(99, 7)}
	sessionA := &sessionauth.SessionIdentity{TenantID: 7, UserID: 101}

	rec := serveUserNotices(t, svc, auth, nil, http.MethodPost, "/v1/admin/notifications/broadcast", []byte(`{"title":"old","body":"body"}`))
	assertNoticeStatus(t, rec, http.StatusCreated)
	oldA := listHTTPUserNotices(t, svc, sessionA, false)[0]
	rec = serveUserNotices(t, svc, auth, sessionA, http.MethodPost, "/v1/notifications/"+strconv.FormatInt(oldA.ID, 10)+"/read", nil)
	assertNoticeStatus(t, rec, http.StatusOK)

	now = now.Add(time.Minute)
	rec = serveUserNotices(t, svc, auth, nil, http.MethodPost, "/v1/admin/notifications/broadcast", []byte(`{"title":"new","body":"body","severity":"warning"}`))
	assertNoticeStatus(t, rec, http.StatusCreated)

	rec = serveUserNotices(t, svc, auth, sessionA, http.MethodGet, "/v1/notifications?user_id=202", nil)
	assertNoticeStatus(t, rec, http.StatusOK)
	var body notificationListResponse
	decodeNoticeJSON(t, rec, &body)
	if len(body.Items) != 2 || body.Items[0].Title != "new" || body.Items[0].UserID != 101 || body.Items[1].Title != "old" || body.Items[1].ReadAt == nil {
		t.Fatalf("items=%+v want only user A rows newest first with old read", body.Items)
	}

	rec = serveUserNotices(t, svc, auth, sessionA, http.MethodGet, "/v1/notifications?unread_only=true", nil)
	assertNoticeStatus(t, rec, http.StatusOK)
	decodeNoticeJSON(t, rec, &body)
	if len(body.Items) != 1 || body.Items[0].Title != "new" || body.Items[0].ReadAt != nil {
		t.Fatalf("unread items=%+v want only new unread", body.Items)
	}
}

func TestMarkRead_OwnOnly(t *testing.T) {
	// 变异：在 MarkRead 的 service 调用里丢掉 user_id；用户 A 会把用户 B 的通知标记为已读。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newNoticeHTTPService(&now)
	store.AddActiveUser(7, 101)
	store.AddActiveUser(7, 202)
	auth := fakeAdminAuth{identity: admintest.TenantOperator(99, 7)}
	sessionA := &sessionauth.SessionIdentity{TenantID: 7, UserID: 101}

	rec := serveUserNotices(t, svc, auth, nil, http.MethodPost, "/v1/admin/notifications/broadcast", []byte(`{"title":"read","body":"body"}`))
	assertNoticeStatus(t, rec, http.StatusCreated)
	rowB, err := svc.ListForUser(context.Background(), usernotice.ListInput{TenantID: 7, UserID: 202, UnreadOnly: true, Limit: 50})
	if err != nil {
		t.Fatalf("ListForUser B: %v", err)
	}
	rec = serveUserNotices(t, svc, auth, sessionA, http.MethodPost, "/v1/notifications/"+strconv.FormatInt(rowB[0].ID, 10)+"/read", nil)
	assertNoticeStatus(t, rec, http.StatusNotFound)

	countB, err := svc.UnreadCount(context.Background(), 7, 202)
	if err != nil {
		t.Fatalf("UnreadCount B: %v", err)
	}
	if countB != 1 {
		t.Fatalf("user B unread count=%d want 1 after user A cross-read attempt", countB)
	}
}

func TestUnreadCountRequiresSession(t *testing.T) {
	// 变异：允许匿名查询收件箱计数；调用方不需用户 session 就能探测通知状态。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, _ := newNoticeHTTPService(&now)

	rec := serveUserNotices(t, svc, fakeAdminAuth{}, nil, http.MethodGet, "/v1/notifications/unread-count", nil)
	assertNoticeStatus(t, rec, http.StatusUnauthorized)
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

func newNoticeHTTPService(now *time.Time) (*usernotice.Service, *usernotice.MemoryStore) {
	store := usernotice.NewMemoryStore()
	svc := usernotice.NewService(store, usernotice.WithClock(func() time.Time {
		if now == nil {
			return time.Now().UTC()
		}
		return now.UTC()
	}))
	return svc, store
}

func listHTTPUserNotices(t *testing.T, svc *usernotice.Service, session *sessionauth.SessionIdentity, unreadOnly bool) []notificationResponse {
	t.Helper()
	target := "/v1/notifications"
	if unreadOnly {
		target += "?unread_only=true"
	}
	rec := serveUserNotices(t, svc, fakeAdminAuth{}, session, http.MethodGet, target, nil)
	assertNoticeStatus(t, rec, http.StatusOK)
	var body notificationListResponse
	decodeNoticeJSON(t, rec, &body)
	return body.Items
}

func serveUserNotices(t *testing.T, svc *usernotice.Service, auth AdminAuth, session *sessionauth.SessionIdentity, method, target string, body []byte) *httptest.ResponseRecorder {
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

func assertNoticeStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func decodeNoticeJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON: %v body=%s", err, rec.Body.String())
	}
}
