package app

import (
	"testing"

	"ccLoad/internal/model"
)

func TestResolveFastBillingUsesRequestTierWhenResponseMissing(t *testing.T) {
	cfg := &model.Config{
		FastBillingConfig: &model.FastBillingConfig{
			Multipliers: map[string]float64{"gpt-5.5": 2.5},
		},
	}
	res := &fwResult{}

	info := resolveFastBilling(cfg, "gpt-5.5-20260701", []byte(`{"service_tier":"fast"}`), res)

	if !info.IsFast {
		t.Fatal("IsFast = false, want true")
	}
	if info.ServiceTier != "fast" {
		t.Fatalf("ServiceTier = %q, want fast", info.ServiceTier)
	}
	if info.Multiplier != 2.5 {
		t.Fatalf("Multiplier = %v, want 2.5", info.Multiplier)
	}
}

func TestResolveFastBillingRequestFastOverridesResponseDefault(t *testing.T) {
	cfg := &model.Config{
		FastBillingConfig: &model.FastBillingConfig{
			Multipliers: map[string]float64{"gpt-5.5": 2.5},
		},
	}
	res := &fwResult{ServiceTier: "default"}

	info := resolveFastBilling(cfg, "gpt-5.5", []byte(`{"service_tier":"fast"}`), res)

	if !info.IsFast {
		t.Fatal("IsFast = false, want true")
	}
	if info.ServiceTier != "fast" {
		t.Fatalf("ServiceTier = %q, want fast", info.ServiceTier)
	}
	if info.Multiplier != 2.5 {
		t.Fatalf("Multiplier = %v, want 2.5", info.Multiplier)
	}
}

func TestResolveFastBillingRequestPriorityOverridesResponseDefault(t *testing.T) {
	cfg := &model.Config{
		FastBillingConfig: &model.FastBillingConfig{
			Multipliers: map[string]float64{"gpt-5.5": 2.5},
		},
	}
	res := &fwResult{ServiceTier: "default"}

	info := resolveFastBilling(cfg, "gpt-5.5", []byte(`{"service_tier":"priority"}`), res)

	if !info.IsFast {
		t.Fatal("IsFast = false, want true")
	}
	if info.ServiceTier != "priority" {
		t.Fatalf("ServiceTier = %q, want priority", info.ServiceTier)
	}
	if info.Multiplier != 2.5 {
		t.Fatalf("Multiplier = %v, want 2.5", info.Multiplier)
	}
}

func TestApplyFastBillingCostMultipliesOnlyFastRequests(t *testing.T) {
	fast := fastBillingInfo{IsFast: true, ServiceTier: "priority", Multiplier: 2.5}
	if got := applyFastBillingCost(0.01, fast); got != 0.025 {
		t.Fatalf("fast cost = %v, want 0.025", got)
	}

	standard := fastBillingInfo{IsFast: false, ServiceTier: "default", Multiplier: 2.5}
	if got := applyFastBillingCost(0.01, standard); got != 0.01 {
		t.Fatalf("standard cost = %v, want 0.01", got)
	}
}
