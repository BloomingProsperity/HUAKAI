package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReferralRoutesMounted(t *testing.T) {
	r := buildTestRouter(t)
	for _, target := range []string{
		"/v1/me/referrals",
		"/v1/me/referrals/rewards",
		"/v1/admin/referrals",
		"/v1/admin/referrals/overview",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)

		r.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Fatalf("GET %s returned 404; referral record route must be mounted", target)
		}
	}
}
