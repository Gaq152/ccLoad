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
	responseTier := ""
	if res != nil {
		responseTier = normalizeServiceTier(res.ServiceTier)
	}
	requestTier := normalizeServiceTier(extractRequestServiceTier(requestBody))

	serviceTier := responseTier
	if isFastServiceTier(requestTier) {
		// Fast/priority 是请求侧主动选择的计费模式；部分 Codex 上游响应仍回 default，
		// 不能用响应层级反向清掉请求已经选择的 Fast 计费信号。
		serviceTier = requestTier
	}
	if serviceTier == "" {
		serviceTier = requestTier
	}

	info := fastBillingInfo{
		ServiceTier: serviceTier,
		Multiplier:  1,
	}
	if isFastServiceTier(serviceTier) {
		info.IsFast = true
		if cfg != nil {
			info.Multiplier = cfg.FastBillingConfig.ResolveMultiplier(actualModel)
		} else {
			info.Multiplier = model.DefaultFastBillingConfig().ResolveMultiplier(actualModel)
		}
	}
	return info
}

func normalizeServiceTier(serviceTier string) string {
	return strings.ToLower(strings.TrimSpace(serviceTier))
}

func isFastServiceTier(serviceTier string) bool {
	serviceTier = normalizeServiceTier(serviceTier)
	return serviceTier == "fast" || serviceTier == "priority"
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
