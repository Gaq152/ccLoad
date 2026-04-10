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
	AliasesRaw           string   `json:"-"`                        // 数据库存储（逗号分隔）
	Aliases              []string `json:"aliases,omitempty"`         // API 传输（切片形式）
	IsPredefined         bool     `json:"is_predefined"`            // 是否加入预定义模型列表
	CreatedAt            int64    `json:"created_at"`
	UpdatedAt            int64    `json:"updated_at"`
}
