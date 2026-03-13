package model

// ModelPricingEntry 模型定价条目（数据库存储 + API 传输）
type ModelPricingEntry struct {
	ID                   int64    `json:"id"`
	Model                string   `json:"model"`
	DisplayName          string   `json:"display_name"`
	ChannelType          string   `json:"channel_type"`
	InputPrice           float64  `json:"input_price"`
	OutputPrice          float64  `json:"output_price"`
	InputPriceHigh       float64  `json:"input_price_high"`
	OutputPriceHigh      float64  `json:"output_price_high"`
	CacheReadMultiplier  float64  `json:"cache_read_multiplier"`
	CacheWriteMultiplier float64  `json:"cache_write_multiplier"`
	CreatedAt            int64    `json:"created_at"`
	UpdatedAt            int64    `json:"updated_at"`
	Aliases              []string `json:"aliases,omitempty"` // 只读字段（从硬编码 modelAliases 反查填充，不存库）
}
