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

	// 填充别名信息（只读展示）
	aliasMap := util.GetModelAliasesReverse()
	for _, e := range entries {
		if aliases, ok := aliasMap[e.Model]; ok {
			e.Aliases = aliases
		}
	}

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

	// 转换为 model.ModelPricingEntry
	entries := make([]*model.ModelPricingEntry, 0, len(defaults))
	for _, d := range defaults {
		entries = append(entries, &model.ModelPricingEntry{
			Model:           d.Model,
			DisplayName:     d.Model, // 默认显示名 = 模型名
			ChannelType:     d.ChannelType,
			InputPrice:      d.InputPrice,
			OutputPrice:     d.OutputPrice,
			InputPriceHigh:  d.InputPriceHigh,
			OutputPriceHigh: d.OutputPriceHigh,
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

// refreshPricingCache 从数据库重新加载定价到内存缓存
func (s *Server) refreshPricingCache() {
	ctx := context.Background()
	entries, err := s.store.ListModelPricing(ctx)
	if err != nil {
		log.Printf("[ERROR] refreshPricingCache failed: %v", err)
		return
	}

	dbEntries := make([]util.DBPricingEntry, 0, len(entries))
	for _, e := range entries {
		dbEntries = append(dbEntries, util.DBPricingEntry{
			Model:                e.Model,
			DisplayName:          e.DisplayName,
			ChannelType:          e.ChannelType,
			InputPrice:           e.InputPrice,
			OutputPrice:          e.OutputPrice,
			InputPriceHigh:       e.InputPriceHigh,
			OutputPriceHigh:      e.OutputPriceHigh,
			CacheReadMultiplier:  e.CacheReadMultiplier,
			CacheWriteMultiplier: e.CacheWriteMultiplier,
		})
	}

	util.SetDBPricing(dbEntries)
	log.Printf("[INFO] Pricing cache refreshed: %d entries", len(dbEntries))
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
	if e.ChannelType != "anthropic" && e.ChannelType != "openai" && e.ChannelType != "gemini" {
		return fmt.Errorf("invalid channel_type: %s (allowed: anthropic, openai, gemini)", e.ChannelType)
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
	if e.CacheReadMultiplier < 0 {
		return fmt.Errorf("cache_read_multiplier must be >= 0")
	}
	if e.CacheWriteMultiplier < 0 {
		return fmt.Errorf("cache_write_multiplier must be >= 0")
	}

	return nil
}
