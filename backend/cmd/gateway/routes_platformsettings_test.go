package main

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// TestPlatformSettingsRouteServiceFallsBackWhenServicePointerNil guards the
// typed-nil trap in mountPlatformSettingsRoutes. Mutation check: assign
// d.platformSettings to the service interface first and then test `service ==
// nil`; the nil *platformsettings.Service becomes a non-nil interface, the
// pgPool fallback is skipped, and this test sees a typed-nil service.
func TestPlatformSettingsRouteServiceFallsBackWhenServicePointerNil(t *testing.T) {
	var typedNil *platformsettings.Service

	got := platformSettingsRouteService(&deps{
		platformSettings: typedNil,
		pgPool:           &pgxpool.Pool{},
	})

	if got == nil {
		t.Fatal("nil platformSettings plus pgPool must build a fallback service")
	}
	value := reflect.ValueOf(got)
	if value.Kind() == reflect.Ptr && value.IsNil() {
		t.Fatalf("fallback returned typed-nil %T; pgPool fallback did not engage", got)
	}
	if _, ok := got.(*platformsettings.Service); !ok {
		t.Fatalf("fallback service type=%T, want *platformsettings.Service", got)
	}
}
