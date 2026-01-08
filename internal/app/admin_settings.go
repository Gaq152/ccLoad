package app

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
)

// 配置验证常量
const (
	LogRetentionDaysMin      = 1
	LogRetentionDaysMax      = 365
	LogRetentionDaysDisabled = -1 // 永久保留
)

// AdminListSettings 获取所有配置项
// GET /admin/settings
func (s *Server) AdminListSettings(c *gin.Context) {
	settings, err := s.configService.ListAllSettings(c.Request.Context())
	if err != nil {
		log.Printf("[ERROR] AdminListSettings failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{"settings": settings})
}

// AdminGetSetting 获取单个配置项
// GET /admin/settings/:key
func (s *Server) AdminGetSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "missing setting key")
		return
	}

	// 从缓存读取
	setting := s.configService.GetSetting(key)
	if setting == nil {
		RespondErrorMsg(c, http.StatusNotFound, fmt.Sprintf("setting not found: %s", key))
		return
	}

	RespondJSON(c, http.StatusOK, setting)
}

// AdminUpdateSetting 更新配置项
// PUT /admin/settings/:key
func (s *Server) AdminUpdateSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "missing setting key")
		return
	}

	var req SettingUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	// 验证值的合法性
	setting := s.configService.GetSetting(key)
	if setting == nil {
		RespondErrorMsg(c, http.StatusNotFound, fmt.Sprintf("setting not found: %s", key))
		return
	}

	if err := validateSettingValue(key, setting.ValueType, req.Value); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid value for type %s: %v", setting.ValueType, err))
		return
	}

	// 更新配置
	if err := s.configService.UpdateSetting(c.Request.Context(), key, req.Value); err != nil {
		log.Printf("[ERROR] AdminUpdateSetting failed for key=%s: %v", key, err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 冷却配置更新后需要刷新内存中的配置
	s.refreshCooldownConfigIfNeeded(key)

	log.Printf("[INFO] Setting updated: %s = %s", key, req.Value)

	RespondJSON(c, http.StatusOK, gin.H{
		"message": "配置已保存",
		"key":     key,
		"value":   req.Value,
	})
}

// AdminResetSetting 重置配置为默认值
// POST /admin/settings/:key/reset
func (s *Server) AdminResetSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "missing setting key")
		return
	}

	// 获取默认值
	setting := s.configService.GetSetting(key)
	if setting == nil {
		RespondErrorMsg(c, http.StatusNotFound, fmt.Sprintf("setting not found: %s", key))
		return
	}

	// 重置为默认值
	if err := s.configService.UpdateSetting(c.Request.Context(), key, setting.DefaultValue); err != nil {
		log.Printf("[ERROR] AdminResetSetting failed for key=%s: %v", key, err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 冷却配置更新后需要刷新内存中的配置
	s.refreshCooldownConfigIfNeeded(key)

	log.Printf("[INFO] Setting reset to default: %s = %s", key, setting.DefaultValue)

	RespondJSON(c, http.StatusOK, gin.H{
		"message": "配置已重置为默认值",
		"key":     key,
		"value":   setting.DefaultValue,
	})
}

// AdminBatchUpdateSettings 批量更新配置(事务保护)
// POST /admin/settings/batch
func (s *Server) AdminBatchUpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if len(req) == 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "no settings to update")
		return
	}

	// 验证所有配置
	for key, value := range req {
		setting := s.configService.GetSetting(key)
		if setting == nil {
			RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("unknown setting: %s", key))
			return
		}

		if err := validateSettingValue(key, setting.ValueType, value); err != nil {
			RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid value for %s: %v", key, err))
			return
		}
	}

	// 批量更新(事务保护)
	if err := s.configService.BatchUpdateSettings(c.Request.Context(), req); err != nil {
		log.Printf("[ERROR] AdminBatchUpdateSettings failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 检查是否包含冷却相关配置，如有则刷新
	for key := range req {
		s.refreshCooldownConfigIfNeeded(key)
	}

	log.Printf("[INFO] Batch updated %d settings", len(req))

	RespondJSON(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("已保存 %d 项配置", len(req)),
	})
}

// validateSettingValue 验证配置值的合法性
func validateSettingValue(key, valueType, value string) error {
	switch valueType {
	case "int":
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a valid integer")
		}
		// 按配置项定义具体约束
		switch key {
		case "max_key_retries":
			if intVal < 1 {
				return fmt.Errorf("max_key_retries must be >= 1")
			}
		case "log_retention_days":
			if intVal != LogRetentionDaysDisabled && (intVal < LogRetentionDaysMin || intVal > LogRetentionDaysMax) {
				return fmt.Errorf("log_retention_days must be %d (永久) or %d-%d", LogRetentionDaysDisabled, LogRetentionDaysMin, LogRetentionDaysMax)
			}
		default:
			if intVal < -1 {
				return fmt.Errorf("value must be >= -1")
			}
		}

	case "bool":
		if value != "true" && value != "false" && value != "1" && value != "0" {
			return fmt.Errorf("must be true/false or 1/0")
		}

	case "duration":
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("duration must be an integer (seconds)")
		}
		if intVal < 0 {
			return fmt.Errorf("duration must be >= 0 (0 = disabled)")
		}

	case "string":
		// 字符串无需额外验证

	default:
		return fmt.Errorf("unknown value type: %s", valueType)
	}

	return nil
}

// refreshCooldownConfigIfNeeded 检查并刷新冷却配置
// 当更新 cooldown_mode 或 cooldown_fixed_interval 时，需要同步刷新内存中的配置
func (s *Server) refreshCooldownConfigIfNeeded(key string) {
	if key != "cooldown_mode" && key != "cooldown_fixed_interval" {
		return
	}

	// 重新读取两个冷却配置并刷新内存
	mode := s.configService.GetString("cooldown_mode", "exponential")
	interval := s.configService.GetIntMin("cooldown_fixed_interval", 30, 1)
	util.SetCooldownConfig(mode, interval)
	log.Printf("[INFO] 冷却配置已刷新: mode=%s, fixed_interval=%ds", mode, interval)
}
