package app

import (
	"strings"

	"ccLoad/internal/model"
	"ccLoad/internal/util"

	"github.com/bytedance/sonic"
)

type fastBillingInfo struct {
	IsFast      bool
	ServiceTier string
	Multiplier  float64
}

func resolveFastBilling(cfg *model.Config, actualModel string, requestBody []byte, res *fwResult) fastBillingInfo {
	serviceTier := ""
	if res != nil {
		serviceTier = strings.TrimSpace(res.ServiceTier)
	}
	if serviceTier == "" {
		serviceTier = extractRequestServiceTier(requestBody)
	}
	serviceTier = strings.ToLower(strings.TrimSpace(serviceTier))

	info := fastBillingInfo{
		ServiceTier: serviceTier,
		Multiplier:  1,
	}
	if serviceTier == "fast" || serviceTier == "priority" {
		info.IsFast = true
		if cfg != nil {
			info.Multiplier = cfg.FastBillingConfig.ResolveMultiplier(actualModel)
		} else {
			info.Multiplier = model.DefaultFastBillingConfig().ResolveMultiplier(actualModel)
		}
	}
	return info
}

func applyFastBillingCost(baseCost float64, info fastBillingInfo) float64 {
	if !info.IsFast {
		return baseCost
	}
	return baseCost * info.Multiplier
}

func applyFastBillingToResult(cfg *model.Config, actualModel string, requestBody []byte, res *fwResult) {
	if res == nil {
		return
	}

	info := resolveFastBilling(cfg, actualModel, requestBody, res)
	res.ServiceTier = info.ServiceTier
	res.IsFast = info.IsFast
	res.FastMultiplier = info.Multiplier
	if res.FastMultiplier == 0 && !res.IsFast {
		res.FastMultiplier = 1
	}

	if !hasConsumedTokens(res) {
		return
	}

	baseCost := util.CalculateCost(
		actualModel,
		res.InputTokens,
		res.OutputTokens,
		res.CacheReadInputTokens,
		res.CacheCreationInputTokens,
	)
	res.BaseCostUSD = baseCost
	res.CostUSD = applyFastBillingCost(baseCost, info)
	res.CostCalculated = true
}

func extractRequestServiceTier(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var req struct {
		ServiceTier string `json:"service_tier"`
	}
	if err := sonic.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.ServiceTier
}
