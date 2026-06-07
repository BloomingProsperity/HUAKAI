package registry

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestUpdateModelCapabilitiesPersistsPublicDescriptors(t *testing.T) {
	maxOutput := 8192
	mode := "chat"
	db := &modelCapabilityDBStub{
		row: modelCapabilityScanRow{
			modelID:         42,
			capabilities:    []byte(`{"function_calling":true,"tool_choice":true,"vision":true}`),
			maxOutputTokens: &maxOutput,
			modelMode:       &mode,
		},
	}

	got, err := updateModelCapabilities(context.Background(), db, UpdateModelCapabilitiesParams{
		ModelID:         42,
		Capabilities:    map[string]bool{"vision": true, "function_calling": true, "tool_choice": true},
		MaxOutputTokens: &maxOutput,
		ModelMode:       ptrString(" chat "),
	})
	if err != nil {
		t.Fatalf("UpdateModelCapabilities: %v", err)
	}
	for _, want := range []string{
		"UPDATE models",
		"capabilities = $2::jsonb",
		"max_output_tokens = $3::integer",
		"model_mode = NULLIF(BTRIM($4::text), '')",
		"WHERE id = $1",
		"deleted_at IS NULL",
	} {
		if !strings.Contains(db.sql, want) {
			t.Fatalf("update SQL missing %q:\n%s", want, db.sql)
		}
	}
	if len(db.args) != 4 || db.args[0] != int64(42) {
		t.Fatalf("query args=%+v want model id plus descriptor values", db.args)
	}
	var sent map[string]bool
	if err := json.Unmarshal(db.args[1].([]byte), &sent); err != nil {
		t.Fatalf("capabilities arg is not json object: %v arg=%v", err, db.args[1])
	}
	if !reflect.DeepEqual(sent, map[string]bool{"function_calling": true, "tool_choice": true, "vision": true}) {
		t.Fatalf("capabilities arg=%+v want descriptor map", sent)
	}
	if db.args[2] != &maxOutput || db.args[3] != "chat" {
		t.Fatalf("query args=%+v want max pointer and trimmed mode", db.args)
	}
	if got.ModelID != 42 || got.MaxOutputTokens == nil || *got.MaxOutputTokens != 8192 || got.ModelMode != "chat" {
		t.Fatalf("update result=%+v want persisted descriptors", got)
	}
	if !reflect.DeepEqual(got.Capabilities, sent) {
		t.Fatalf("result capabilities=%+v want %+v", got.Capabilities, sent)
	}
}

func TestUpdateModelCapabilitiesNormalizesEmptyDescriptors(t *testing.T) {
	db := &modelCapabilityDBStub{
		row: modelCapabilityScanRow{
			modelID:      43,
			capabilities: []byte(`{}`),
		},
	}

	got, err := updateModelCapabilities(context.Background(), db, UpdateModelCapabilitiesParams{ModelID: 43})
	if err != nil {
		t.Fatalf("UpdateModelCapabilities empty: %v", err)
	}
	var sent map[string]bool
	if err := json.Unmarshal(db.args[1].([]byte), &sent); err != nil {
		t.Fatalf("capabilities arg is not json object: %v", err)
	}
	if len(sent) != 0 || len(got.Capabilities) != 0 || got.MaxOutputTokens != nil || got.ModelMode != "" {
		t.Fatalf("empty descriptors sent=%+v got=%+v want empty map and nil optional values", sent, got)
	}
}

func TestUpdateModelCapabilitiesMissingModelReturnsUnknownModel(t *testing.T) {
	db := &modelCapabilityDBStub{row: modelCapabilityScanRow{err: pgx.ErrNoRows}}

	_, err := updateModelCapabilities(context.Background(), db, UpdateModelCapabilitiesParams{
		ModelID:      99,
		Capabilities: map[string]bool{"vision": true},
	})
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err=%v want ErrUnknownModel", err)
	}
}

func TestCapabilityBindingEnumReject(t *testing.T) {
	db := &modelCapabilityBindingDBStub{
		row: modelCapabilityBindingScanRow{
			modelID:    42,
			capability: "vision",
			enabled:    true,
			source:     "operator",
		},
	}

	_, err := upsertModelCapabilityBinding(context.Background(), db, UpsertModelCapabilityBindingParams{
		TenantID:   7,
		Scope:      "tenant",
		ModelID:    42,
		Capability: "made_up_capability",
		Enabled:    true,
		Source:     "operator",
	})
	if !errors.Is(err, ErrInvalidModelCapability) {
		t.Fatalf("invalid capability err=%v want ErrInvalidModelCapability", err)
	}
	if db.calls != 0 {
		t.Fatalf("invalid capability touched db calls=%d; MUTATION: removing enum validation lets invalid values persist", db.calls)
	}

	got, err := upsertModelCapabilityBinding(context.Background(), db, UpsertModelCapabilityBindingParams{
		TenantID:   7,
		Scope:      "tenant",
		ModelID:    42,
		Capability: "vision",
		Enabled:    true,
		Source:     "operator",
	})
	if err != nil {
		t.Fatalf("valid capability upsert: %v", err)
	}
	if db.calls != 1 {
		t.Fatalf("valid capability db calls=%d want 1", db.calls)
	}
	if got.ModelID != 42 || got.Capability != "vision" || !got.Enabled || got.Source != "operator" {
		t.Fatalf("binding=%+v want persisted vision binding", got)
	}
}

type modelCapabilityDBStub struct {
	sql  string
	args []any
	row  modelCapabilityScanRow
}

func (s *modelCapabilityDBStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.sql = sql
	s.args = append([]any(nil), args...)
	return s.row
}

type modelCapabilityScanRow struct {
	modelID         int64
	capabilities    []byte
	maxOutputTokens *int
	modelMode       *string
	err             error
}

func (r modelCapabilityScanRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.modelID
	*dest[1].(*[]byte) = r.capabilities
	*dest[2].(**int) = r.maxOutputTokens
	*dest[3].(**string) = r.modelMode
	return nil
}

func ptrString(v string) *string {
	return &v
}

type modelCapabilityBindingDBStub struct {
	calls int
	sql   string
	args  []any
	row   modelCapabilityBindingScanRow
}

func (s *modelCapabilityBindingDBStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.calls++
	s.sql = sql
	s.args = append([]any(nil), args...)
	return s.row
}

type modelCapabilityBindingScanRow struct {
	modelID    int64
	capability string
	value      *string
	params     []byte
	enabled    bool
	source     string
	err        error
}

func (r modelCapabilityBindingScanRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.modelID
	*dest[1].(*string) = r.capability
	*dest[2].(**string) = r.value
	*dest[3].(*[]byte) = r.params
	*dest[4].(*bool) = r.enabled
	*dest[5].(*string) = r.source
	return nil
}
