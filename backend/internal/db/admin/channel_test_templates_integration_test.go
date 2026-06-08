//go:build integration_pg

package admin

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestChannelTestTemplateCRUD(t *testing.T) {
	// MUTATION: make CreateChannelTestTemplate a no-op; the follow-up get/list checks cannot find the template.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := insertCatalogTenant(t, ctx, pool, "channel-test-template-"+suffix)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM channel_test_templates WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})

	created, err := q.CreateChannelTestTemplate(ctx, CreateChannelTestTemplateParams{
		TenantID:     tenantID,
		Name:         "quota check " + suffix,
		Method:       "POST",
		Path:         "/v1/chat/completions",
		BodyTemplate: `{"model":"health-check","messages":[{"role":"user","content":"ping"}]}`,
		Headers:      []byte(`{"X-Test-Mode":"true"}`),
	})
	if err != nil {
		t.Fatalf("CreateChannelTestTemplate: %v", err)
	}
	if created.ID <= 0 || created.TenantID != tenantID || created.Name == "" {
		t.Fatalf("created template invalid: %+v", created)
	}

	got, err := q.GetChannelTestTemplate(ctx, GetChannelTestTemplateParams{
		TenantID: tenantID,
		ID:       created.ID,
	})
	if err != nil {
		t.Fatalf("GetChannelTestTemplate: %v", err)
	}
	if got.Name != created.Name || got.Method != "POST" || got.Path != "/v1/chat/completions" ||
		got.BodyTemplate != created.BodyTemplate || len(got.Headers) == 0 || !reflect.DeepEqual(got.Headers, created.Headers) {
		t.Fatalf("got template=%+v want created %+v", got, created)
	}

	items, err := q.ListChannelTestTemplatesByTenant(ctx, ListChannelTestTemplatesByTenantParams{
		TenantID:   tenantID,
		PageLimit:  10,
		PageOffset: 0,
	})
	if err != nil {
		t.Fatalf("ListChannelTestTemplatesByTenant: %v", err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("list templates=%+v want created ID %d", items, created.ID)
	}

	deleted, err := q.DeleteChannelTestTemplate(ctx, DeleteChannelTestTemplateParams{
		TenantID: tenantID,
		ID:       created.ID,
	})
	if err != nil {
		t.Fatalf("DeleteChannelTestTemplate: %v", err)
	}
	if deleted.ID != created.ID || deleted.Name != created.Name {
		t.Fatalf("deleted=%+v want ID/name from created %+v", deleted, created)
	}

	items, err = q.ListChannelTestTemplatesByTenant(ctx, ListChannelTestTemplatesByTenantParams{
		TenantID:   tenantID,
		PageLimit:  10,
		PageOffset: 0,
	})
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("templates after delete=%+v want empty", items)
	}
}
