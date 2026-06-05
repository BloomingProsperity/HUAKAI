// HUAKAI · iKun

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestPaymentProviderConfigServicePersistsAndUpdatesRuntime(t *testing.T) {
	ctx := context.Background()
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	runtime := payment.NewService(payment.NewMemoryStore())
	svc := &paymentProviderConfigService{settings: settings, runtime: runtime}

	cfg, err := svc.SetProviderRuntimeConfig(ctx, payment.ProviderRuntimeConfigInput{
		ProviderKind: payment.ProviderTaobao,
		Enabled:      true,
		CheckoutURL:  "https://pay.example/taobao",
		UpdatedBy:    "99",
	})
	if err != nil {
		t.Fatalf("SetProviderRuntimeConfig: %v", err)
	}
	if !cfg.Enabled || cfg.CheckoutURL != "https://pay.example/taobao" {
		t.Fatalf("cfg=%+v want enabled taobao checkout URL", cfg)
	}
	stored, err := settings.Get(ctx, platformsettings.KeyPaymentProviderConfig)
	if err != nil {
		t.Fatalf("settings.Get: %v", err)
	}
	if stored.Source != platformsettings.SourceDB || stored.UpdatedBy != "99" {
		t.Fatalf("stored=%+v want db source updated_by 99", stored)
	}
	if _, err := runtime.CreateOrder(ctx, payment.CreateOrderInput{
		TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "taobao-runtime", ProviderKind: payment.ProviderTaobao,
	}); err != nil {
		t.Fatalf("runtime taobao create after PUT: %v", err)
	}
}

func TestApplyStoredPaymentProviderConfigPrewarmsRuntime(t *testing.T) {
	ctx := context.Background()
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(ctx, platformsettings.UpsertInput{
		Key:       platformsettings.KeyPaymentProviderConfig,
		Value:     `{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":true,"checkout_url":"https://pay.example/taobao"}}`,
		UpdatedBy: "99",
	}); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	runtime := payment.NewService(payment.NewMemoryStore())

	if err := applyStoredPaymentProviderConfig(ctx, settings, runtime); err != nil {
		t.Fatalf("applyStoredPaymentProviderConfig: %v", err)
	}
	if _, err := runtime.CreateOrder(ctx, payment.CreateOrderInput{
		TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "taobao-prewarm", ProviderKind: payment.ProviderTaobao,
	}); err != nil {
		t.Fatalf("runtime taobao create after prewarm: %v", err)
	}
}

func TestPaymentProviderConfigRejectsEnabledTaobaoWithoutCheckoutURL(t *testing.T) {
	ctx := context.Background()
	svc := &paymentProviderConfigService{
		settings: platformsettings.NewService(platformsettings.NewMemoryStore(), nil),
		runtime:  payment.NewService(payment.NewMemoryStore()),
	}
	_, err := svc.SetProviderRuntimeConfig(ctx, payment.ProviderRuntimeConfigInput{
		ProviderKind: payment.ProviderTaobao,
		Enabled:      true,
		UpdatedBy:    "99",
	})
	if !errors.Is(err, payment.ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput", err)
	}
	if _, err := svc.runtime.CreateOrder(ctx, payment.CreateOrderInput{
		TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "taobao-invalid", ProviderKind: payment.ProviderTaobao,
	}); !errors.Is(err, payment.ErrProviderUnknown) {
		t.Fatalf("runtime changed after invalid config err=%v want ErrProviderUnknown", err)
	}
}
