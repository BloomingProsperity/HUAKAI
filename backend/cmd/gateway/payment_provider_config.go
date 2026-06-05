// HUAKAI · iKun

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type paymentProviderConfigService struct {
	settings *platformsettings.Service
	runtime  *payment.Service
}

type storedPaymentProviderConfigs struct {
	Manual storedPaymentProviderConfig `json:"manual"`
	Taobao storedPaymentProviderConfig `json:"taobao"`
}

type storedPaymentProviderConfig struct {
	Enabled     bool   `json:"enabled"`
	CheckoutURL string `json:"checkout_url"`
}

func paymentProviderConfigRouteService(d *deps) paymenthttp.ProviderRuntimeConfigService {
	if d == nil || d.paymentService == nil {
		return nil
	}
	return &paymentProviderConfigService{settings: d.platformSettings, runtime: d.paymentService}
}

func (s *paymentProviderConfigService) GetProviderRuntimeConfig(ctx context.Context, kind payment.ProviderKind) (payment.ProviderRuntimeConfig, error) {
	if s == nil || s.runtime == nil {
		return payment.ProviderRuntimeConfig{}, payment.ErrStoreNotConfigured
	}
	return s.runtime.GetProviderRuntimeConfig(ctx, kind)
}

func (s *paymentProviderConfigService) SetProviderRuntimeConfig(ctx context.Context, in payment.ProviderRuntimeConfigInput) (payment.ProviderRuntimeConfig, error) {
	if s == nil || s.runtime == nil {
		return payment.ProviderRuntimeConfig{}, payment.ErrStoreNotConfigured
	}
	if err := validatePaymentProviderConfigInput(in); err != nil {
		return payment.ProviderRuntimeConfig{}, err
	}
	if s.settings != nil {
		doc, err := s.documentFromRuntime(ctx)
		if err != nil {
			return payment.ProviderRuntimeConfig{}, err
		}
		updateStoredProviderConfig(&doc, in)
		raw, err := json.Marshal(doc)
		if err != nil {
			return payment.ProviderRuntimeConfig{}, err
		}
		if _, err := s.settings.Upsert(ctx, platformsettings.UpsertInput{
			Key:       platformsettings.KeyPaymentProviderConfig,
			Value:     string(raw),
			UpdatedBy: in.UpdatedBy,
			ActorID:   in.UpdatedBy,
			ActorRole: "platform_admin",
		}); err != nil {
			return payment.ProviderRuntimeConfig{}, err
		}
	}
	return s.runtime.SetProviderRuntimeConfig(ctx, in)
}

func applyStoredPaymentProviderConfig(ctx context.Context, settings *platformsettings.Service, runtime *payment.Service) error {
	if settings == nil || runtime == nil {
		return nil
	}
	setting, err := settings.Get(ctx, platformsettings.KeyPaymentProviderConfig)
	if err != nil {
		return err
	}
	if setting.Source != platformsettings.SourceDB {
		return nil
	}
	doc, err := parseStoredPaymentProviderConfigs(setting.Value)
	if err != nil {
		return err
	}
	if _, err := runtime.SetProviderRuntimeConfig(ctx, payment.ProviderRuntimeConfigInput{
		ProviderKind: payment.ProviderManual,
		Enabled:      doc.Manual.Enabled,
		CheckoutURL:  doc.Manual.CheckoutURL,
		UpdatedBy:    "platform_settings",
	}); err != nil {
		return fmt.Errorf("apply manual payment provider config: %w", err)
	}
	if _, err := runtime.SetProviderRuntimeConfig(ctx, payment.ProviderRuntimeConfigInput{
		ProviderKind: payment.ProviderTaobao,
		Enabled:      doc.Taobao.Enabled,
		CheckoutURL:  doc.Taobao.CheckoutURL,
		UpdatedBy:    "platform_settings",
	}); err != nil {
		return fmt.Errorf("apply taobao payment provider config: %w", err)
	}
	return nil
}

func (s *paymentProviderConfigService) documentFromRuntime(ctx context.Context) (storedPaymentProviderConfigs, error) {
	manual, err := s.runtime.GetProviderRuntimeConfig(ctx, payment.ProviderManual)
	if err != nil {
		return storedPaymentProviderConfigs{}, err
	}
	taobao, err := s.runtime.GetProviderRuntimeConfig(ctx, payment.ProviderTaobao)
	if err != nil {
		return storedPaymentProviderConfigs{}, err
	}
	return storedPaymentProviderConfigs{
		Manual: storedPaymentProviderConfig{Enabled: manual.Enabled, CheckoutURL: manual.CheckoutURL},
		Taobao: storedPaymentProviderConfig{Enabled: taobao.Enabled, CheckoutURL: taobao.CheckoutURL},
	}, nil
}

func updateStoredProviderConfig(doc *storedPaymentProviderConfigs, in payment.ProviderRuntimeConfigInput) {
	cfg := storedPaymentProviderConfig{Enabled: in.Enabled, CheckoutURL: strings.TrimSpace(in.CheckoutURL)}
	if in.ProviderKind == payment.ProviderManual {
		doc.Manual = cfg
		return
	}
	doc.Taobao = cfg
}

func parseStoredPaymentProviderConfigs(raw string) (storedPaymentProviderConfigs, error) {
	var doc storedPaymentProviderConfigs
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return storedPaymentProviderConfigs{}, fmt.Errorf("parse payment provider config: %w", err)
	}
	for _, in := range []payment.ProviderRuntimeConfigInput{
		{ProviderKind: payment.ProviderManual, Enabled: doc.Manual.Enabled, CheckoutURL: doc.Manual.CheckoutURL},
		{ProviderKind: payment.ProviderTaobao, Enabled: doc.Taobao.Enabled, CheckoutURL: doc.Taobao.CheckoutURL},
	} {
		if err := validatePaymentProviderConfigInput(in); err != nil {
			return storedPaymentProviderConfigs{}, err
		}
	}
	return doc, nil
}

func validatePaymentProviderConfigInput(in payment.ProviderRuntimeConfigInput) error {
	checkoutURL := strings.TrimSpace(in.CheckoutURL)
	switch in.ProviderKind {
	case payment.ProviderManual:
		if checkoutURL != "" {
			return payment.ErrInvalidInput
		}
	case payment.ProviderTaobao:
		if in.Enabled && checkoutURL == "" {
			return payment.ErrInvalidInput
		}
	default:
		return payment.ErrProviderUnknown
	}
	return nil
}
