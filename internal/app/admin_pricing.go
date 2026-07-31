package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"ccLoad/internal/model"
	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
)

// HandleListModelPricing 获取所有模型定价（附带别名信息）
// GET /admin/pricing
func (s *Server) HandleListModelPricing(c *gin.Context) {
	entries, err := s.store.ListModelPricing(c.Request.Context())
	if err != nil {
		log.Printf("[ERROR] ListModelPricing failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 别名已从数据库读取，无需额外填充
	RespondJSON(c, http.StatusOK, gin.H{"entries": entries})
}

// HandleCreateModelPricing 创建模型定价
// POST /admin/pricing
func (s *Server) HandleCreateModelPricing(c *gin.Context) {
	var entry model.ModelPricingEntry
	if err := c.ShouldBindJSON(&entry); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if err := validatePricingEntry(&entry); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateModelPricing(c.Request.Context(), &entry); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "Duplicate") {
			RespondErrorMsg(c, http.StatusConflict, fmt.Sprintf("模型 %s 已存在", entry.Model))
			return
		}
		log.Printf("[ERROR] CreateModelPricing failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	s.refreshPricingCache()
	log.Printf("[INFO] Model pricing created: %s", entry.Model)

	RespondJSON(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("模型 %s 定价已创建", entry.Model),
		"entry":   entry,
	})
}

// HandleUpdateModelPricing 更新模型定价
// PUT /admin/pricing/:id
func (s *Server) HandleUpdateModelPricing(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid id")
		return
	}

	var entry model.ModelPricingEntry
	if err := c.ShouldBindJSON(&entry); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	entry.ID = id
	if err := validatePricingEntry(&entry); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.UpdateModelPricing(c.Request.Context(), &entry); err != nil {
		if strings.Contains(err.Error(), "not found") {
			RespondErrorMsg(c, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "Duplicate") {
			RespondErrorMsg(c, http.StatusConflict, fmt.Sprintf("模型 %s 已存在", entry.Model))
			return
		}
		log.Printf("[ERROR] UpdateModelPricing failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	s.refreshPricingCache()
	log.Printf("[INFO] Model pricing updated: %s (id=%d)", entry.Model, id)

	RespondJSON(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("模型 %s 定价已更新", entry.Model),
		"entry":   entry,
	})
}

// HandleDeleteModelPricing 删除模型定价
// DELETE /admin/pricing/:id
func (s *Server) HandleDeleteModelPricing(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DeleteModelPricing(c.Request.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			RespondErrorMsg(c, http.StatusNotFound, err.Error())
			return
		}
		log.Printf("[ERROR] DeleteModelPricing failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	s.refreshPricingCache()
	log.Printf("[INFO] Model pricing deleted: id=%d", id)

	RespondJSON(c, http.StatusOK, gin.H{"message": "定价已删除"})
}

// HandleImportDefaultPricing 从硬编码导入默认定价
// POST /admin/pricing/defaults
func (s *Server) HandleImportDefaultPricing(c *gin.Context) {
	defaults := util.GetDefaultPricing()
	if len(defaults) == 0 {
		RespondErrorMsg(c, http.StatusInternalServerError, "no default pricing found")
		return
	}

	// 构建反向别名映射和渠道默认模型集合
	reverseAliases := util.GetModelAliasesReverse()
	defaultSets := util.GetDefaultModelSets()
	defaultSet := make(map[string]bool)
	for _, models := range defaultSets {
		for _, m := range models {
			defaultSet[m] = true
		}
	}

	// 转换为 model.ModelPricingEntry
	entries := make([]*model.ModelPricingEntry, 0, len(defaults))
	for _, d := range defaults {
		var aliases []string
		if aliasList, ok := reverseAliases[d.Model]; ok {
			aliases = aliasList
		}
		isDefault := defaultSet[d.Model]
		for _, alias := range aliases {
			isDefault = isDefault || defaultSet[alias]
		}
		entries = append(entries, &model.ModelPricingEntry{
			Model:               d.Model,
			DisplayName:         d.Model,
			ChannelType:         d.ChannelType,
			InputPrice:          d.InputPrice,
			OutputPrice:         d.OutputPrice,
			CacheReadPrice:      d.CacheReadPrice,
			CacheWritePrice:     d.CacheWritePrice,
			InputPriceHigh:      d.InputPriceHigh,
			OutputPriceHigh:     d.OutputPriceHigh,
			CacheReadPriceHigh:  d.CacheReadPriceHigh,
			CacheWritePriceHigh: d.CacheWritePriceHigh,
			HighPriceThreshold:  d.HighPriceThreshold,
			Aliases:             aliases,
			IsDefault:           isDefault,
		})
	}

	count, err := s.store.BatchCreateModelPricing(c.Request.Context(), entries)
	if err != nil {
		log.Printf("[ERROR] ImportDefaultPricing failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	s.refreshPricingCache()
	log.Printf("[INFO] Imported %d default model pricing entries", count)

	RespondJSON(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("已导入 %d 个默认模型定价", count),
		"count":   count,
	})
}

// refreshPricingCache 从数据库重新加载定价到内存缓存（含默认列表和别名）
func (s *Server) refreshPricingCache() {
	ctx := context.Background()
	entries, err := s.store.ListModelPricing(ctx)
	if err != nil {
		log.Printf("[ERROR] refreshPricingCache failed: %v", err)
		return
	}

	dbEntries := make([]util.DBPricingEntry, 0, len(entries))
	defaultByType := make(map[string][]string) // channelType → models
	aliasMap := make(map[string]string)        // alias → base model

	for _, e := range entries {
		dbEntries = append(dbEntries, util.DBPricingEntry{
			Model:               e.Model,
			DisplayName:         e.DisplayName,
			ChannelType:         e.ChannelType,
			InputPrice:          e.InputPrice,
			OutputPrice:         e.OutputPrice,
			CacheReadPrice:      e.CacheReadPrice,
			CacheWritePrice:     e.CacheWritePrice,
			InputPriceHigh:      e.InputPriceHigh,
			OutputPriceHigh:     e.OutputPriceHigh,
			CacheReadPriceHigh:  e.CacheReadPriceHigh,
			CacheWritePriceHigh: e.CacheWritePriceHigh,
			HighPriceThreshold:  e.HighPriceThreshold,
		})

		// 构建渠道默认模型列表
		if e.IsDefault {
			defaultByType[e.ChannelType] = append(defaultByType[e.ChannelType], e.Model)
		}

		// 构建别名映射
		for _, alias := range e.Aliases {
			aliasMap[alias] = e.Model
		}
	}

	util.SetDBPricing(dbEntries)

	// 数据库存在定价记录后，用户维护的默认列表（包括空列表）成为唯一来源。
	util.ClearDBDefaultModels()
	if len(entries) > 0 {
		for _, ct := range []string{util.ChannelTypeAnthropic, util.ChannelTypeCodex, util.ChannelTypeGemini} {
			util.SetDBDefaultModels(ct, defaultByType[ct])
		}
	}

	// 更新别名缓存
	util.SetDBAliases(aliasMap)

	log.Printf("[INFO] Pricing cache refreshed: %d entries, %d default models, %d aliases",
		len(dbEntries), countModels(defaultByType), len(aliasMap))
}

func countModels(m map[string][]string) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

// loadPricingCache 启动时从数据库加载定价缓存
func (s *Server) loadPricingCache() {
	s.refreshPricingCache()
}

// validatePricingEntry 验证定价条目
func validatePricingEntry(e *model.ModelPricingEntry) error {
	e.Model = strings.TrimSpace(e.Model)
	if e.Model == "" {
		return fmt.Errorf("model name is required")
	}

	e.ChannelType = strings.TrimSpace(e.ChannelType)
	if e.ChannelType == "" {
		return fmt.Errorf("channel_type is required")
	}
	if e.ChannelType != util.ChannelTypeAnthropic && e.ChannelType != util.ChannelTypeCodex && e.ChannelType != util.ChannelTypeGemini {
		return fmt.Errorf("invalid channel_type: %s (allowed: anthropic, codex, gemini)", e.ChannelType)
	}

	if e.InputPrice < 0 {
		return fmt.Errorf("input_price must be >= 0")
	}
	if e.OutputPrice < 0 {
		return fmt.Errorf("output_price must be >= 0")
	}
	if e.InputPriceHigh < 0 {
		return fmt.Errorf("input_price_high must be >= 0")
	}
	if e.OutputPriceHigh < 0 {
		return fmt.Errorf("output_price_high must be >= 0")
	}
	// 只有启用高档输入价时才保存阈值；无分档模型明确存 0。
	if e.InputPriceHigh > 0 && e.HighPriceThreshold == 0 {
		e.HighPriceThreshold = util.DefaultHighPriceThresholdForChannel(e.ChannelType)
	} else if e.InputPriceHigh <= 0 {
		e.HighPriceThreshold = 0
	}
	if e.HighPriceThreshold < 0 {
		return fmt.Errorf("high_price_threshold must be >= 0")
	}
	if e.CacheReadPrice < 0 {
		return fmt.Errorf("cache_read_price must be >= 0")
	}
	if e.CacheWritePrice < 0 {
		return fmt.Errorf("cache_write_price must be >= 0")
	}
	if e.CacheReadPriceHigh < 0 {
		return fmt.Errorf("cache_read_price_high must be >= 0")
	}
	if e.CacheWritePriceHigh < 0 {
		return fmt.Errorf("cache_write_price_high must be >= 0")
	}

	return nil
}
