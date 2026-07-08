package model

import "testing"

func TestFastBillingConfigResolveMultiplierUsesDefaults(t *testing.T) {
	var cfg *FastBillingConfig

	if got := cfg.ResolveMultiplier("gpt-5.4"); got != 2 {
		t.Fatalf("gpt-5.4 multiplier = %v, want 2", got)
	}
	if got := cfg.ResolveMultiplier("gpt-5.5-20260701"); got != 2.5 {
		t.Fatalf("gpt-5.5 snapshot multiplier = %v, want 2.5", got)
	}
	if got := cfg.ResolveMultiplier("gpt-5.6"); got != 1 {
		t.Fatalf("unknown fast model multiplier = %v, want 1", got)
	}
}

func TestFastBillingConfigResolveMultiplierUsesLongestPrefix(t *testing.T) {
	cfg := &FastBillingConfig{
		Multipliers: map[string]float64{
			"gpt-5.5":          2.5,
			"gpt-5.5-20260701": 3,
		},
	}

	if got := cfg.ResolveMultiplier("gpt-5.5-20260701"); got != 3 {
		t.Fatalf("longest prefix multiplier = %v, want 3", got)
	}
}

func TestFastBillingConfigResolveMultiplierEmptySavedConfigIsAuthoritative(t *testing.T) {
	cfg := &FastBillingConfig{Multipliers: map[string]float64{}}

	if got := cfg.ResolveMultiplier("gpt-5.5"); got != 1 {
		t.Fatalf("empty saved config multiplier = %v, want 1", got)
	}
}

func TestFastBillingConfigValidateRejectsInvalidRows(t *testing.T) {
	tests := []struct {
		name string
		cfg  *FastBillingConfig
	}{
		{
			name: "empty model",
			cfg:  &FastBillingConfig{Multipliers: map[string]float64{"": 2}},
		},
		{
			name: "negative multiplier",
			cfg:  &FastBillingConfig{Multipliers: map[string]float64{"gpt-5.5": -1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}
