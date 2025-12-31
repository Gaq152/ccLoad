package schema

// DefineChannelsTable 定义channels表结构
func DefineChannelsTable() *TableBuilder {
	return NewTable("channels").
		Column("id INT PRIMARY KEY AUTO_INCREMENT").
		Column("name VARCHAR(191) NOT NULL UNIQUE").
		Column("url VARCHAR(191) NOT NULL").
		Column("priority INT NOT NULL DEFAULT 0").
		Column("models TEXT NOT NULL").
		Column("model_redirects TEXT NOT NULL").
		Column("channel_type VARCHAR(64) NOT NULL DEFAULT 'anthropic'").
		Column("enabled TINYINT NOT NULL DEFAULT 1").
		Column("cooldown_until BIGINT NOT NULL DEFAULT 0").
		Column("cooldown_duration_ms BIGINT NOT NULL DEFAULT 0").
		Column("rr_key_index INT NOT NULL DEFAULT 0").
		Column("auto_select_endpoint TINYINT NOT NULL DEFAULT 1"). // 自动选择最快端点（默认开启）
		Column("quota_config TEXT DEFAULT NULL").                  // 用量监控配置（JSON格式）
		Column("preset VARCHAR(32) DEFAULT NULL").                 // Codex预设类型：official=官方, custom=自定义
		Column("openai_compat TINYINT NOT NULL DEFAULT 0").        // OpenAI兼容模式（Gemini渠道使用/v1/chat/completions格式）
		Column("sort_order INT NOT NULL DEFAULT 0").               // 同优先级内的排序顺序（拖拽排序用）
		Column("created_at BIGINT NOT NULL").
		Column("updated_at BIGINT NOT NULL").
		Index("idx_channels_enabled", "enabled").
		Index("idx_channels_priority", "priority DESC").
		Index("idx_channels_type_enabled", "channel_type, enabled").
		Index("idx_channels_cooldown", "cooldown_until")
}

// DefineAPIKeysTable 定义api_keys表结构
func DefineAPIKeysTable() *TableBuilder {
	return NewTable("api_keys").
		Column("id INT PRIMARY KEY AUTO_INCREMENT").
		Column("channel_id INT NOT NULL").
		Column("key_index INT NOT NULL").
		Column("api_key TEXT NOT NULL").                           // 扩展为TEXT以支持较长的Key
		Column("key_strategy VARCHAR(32) NOT NULL DEFAULT 'sequential'").
		Column("cooldown_until BIGINT NOT NULL DEFAULT 0").
		Column("cooldown_duration_ms BIGINT NOT NULL DEFAULT 0").
		Column("access_token TEXT DEFAULT NULL").                  // OAuth access_token（官方预设使用）
		Column("id_token TEXT DEFAULT NULL").                      // OAuth id_token（官方预设使用）
		Column("refresh_token TEXT DEFAULT NULL").                 // OAuth refresh_token（官方预设使用）
		Column("token_expires_at BIGINT NOT NULL DEFAULT 0").      // Token过期时间戳（官方预设使用）
		Column("created_at BIGINT NOT NULL").
		Column("updated_at BIGINT NOT NULL").
		Column("UNIQUE KEY uk_channel_key (channel_id, key_index)").
		Column("FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE").
		Index("idx_api_keys_cooldown", "cooldown_until").
		Index("idx_api_keys_channel_cooldown", "channel_id, cooldown_until")
}

// DefineChannelModelsTable 定义channel_models表结构
func DefineChannelModelsTable() *TableBuilder {
	return NewTable("channel_models").
		Column("channel_id INT NOT NULL").
		Column("model VARCHAR(191) NOT NULL").
		Column("created_at BIGINT NOT NULL DEFAULT 0").
		Column("PRIMARY KEY (channel_id, model)").
		Column("FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE").
		Index("idx_channel_models_model", "model")
}

// DefineAuthTokensTable 定义auth_tokens表结构
func DefineAuthTokensTable() *TableBuilder {
	return NewTable("auth_tokens").
		Column("id INT PRIMARY KEY AUTO_INCREMENT").
		Column("token VARCHAR(100) NOT NULL UNIQUE").
		Column("description VARCHAR(512) NOT NULL").
		Column("created_at BIGINT NOT NULL").
		Column("expires_at BIGINT NOT NULL DEFAULT 0").
		Column("last_used_at BIGINT NOT NULL DEFAULT 0").
		Column("is_active TINYINT NOT NULL DEFAULT 1").
		Column("all_channels TINYINT NOT NULL DEFAULT 1"). // 是否允许使用所有渠道（1=全部，0=仅指定渠道）
		Column("success_count INT NOT NULL DEFAULT 0").
		Column("failure_count INT NOT NULL DEFAULT 0").
		Column("stream_avg_ttfb DOUBLE NOT NULL DEFAULT 0.0").
		Column("non_stream_avg_rt DOUBLE NOT NULL DEFAULT 0.0").
		Column("stream_count INT NOT NULL DEFAULT 0").
		Column("non_stream_count INT NOT NULL DEFAULT 0").
		Column("prompt_tokens_total BIGINT NOT NULL DEFAULT 0").
		Column("completion_tokens_total BIGINT NOT NULL DEFAULT 0").
		Column("cache_read_tokens_total BIGINT NOT NULL DEFAULT 0").
		Column("cache_creation_tokens_total BIGINT NOT NULL DEFAULT 0").
		Column("total_cost_usd DOUBLE NOT NULL DEFAULT 0.0").
		Index("idx_auth_tokens_active", "is_active").
		Index("idx_auth_tokens_expires", "expires_at")
}

// DefineSystemSettingsTable 定义system_settings表结构
func DefineSystemSettingsTable() *TableBuilder {
	return NewTable("system_settings").
		Column("`key` VARCHAR(128) PRIMARY KEY").
		Column("value TEXT NOT NULL").
		Column("value_type VARCHAR(32) NOT NULL").
		Column("description VARCHAR(512) NOT NULL").
		Column("default_value VARCHAR(512) NOT NULL").
		Column("updated_at BIGINT NOT NULL")
}

// DefineAdminSessionsTable 定义admin_sessions表结构
func DefineAdminSessionsTable() *TableBuilder {
	return NewTable("admin_sessions").
		Column("token VARCHAR(64) PRIMARY KEY"). // SHA256哈希(64字符十六进制,2025-12改为存储哈希而非明文)
		Column("expires_at BIGINT NOT NULL").
		Column("created_at BIGINT NOT NULL").
		Index("idx_admin_sessions_expires", "expires_at")
}

// DefineChannelEndpointsTable 定义channel_endpoints表结构（多端点管理）
func DefineChannelEndpointsTable() *TableBuilder {
	return NewTable("channel_endpoints").
		Column("id INT PRIMARY KEY AUTO_INCREMENT").
		Column("channel_id INT NOT NULL").
		Column("url VARCHAR(512) NOT NULL").
		Column("is_active TINYINT NOT NULL DEFAULT 0").       // 当前选中的端点
		Column("latency_ms INT DEFAULT NULL").                // 最近测速延迟(ms)，NULL表示未测试
		Column("status_code INT DEFAULT NULL").               // 最近测速HTTP状态码，NULL表示未测试
		Column("last_test_at BIGINT NOT NULL DEFAULT 0").     // 最后测速时间戳
		Column("sort_order INT NOT NULL DEFAULT 0").          // 排序顺序
		Column("created_at BIGINT NOT NULL").
		Column("FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE").
		Index("idx_channel_endpoints_channel", "channel_id").
		Index("idx_channel_endpoints_active", "channel_id, is_active")
}

// DefineTokenChannelsTable 定义token_channels表结构（令牌-渠道关联，多对多）
// 用于控制API令牌可以使用哪些渠道
// 设计说明：
//   - 如果令牌的 all_channels=1，则不查询此表，允许使用所有渠道
//   - 如果令牌的 all_channels=0，则只能使用此表中关联的渠道
//   - 新建渠道时，不会自动添加到任何令牌（需手动配置）
func DefineTokenChannelsTable() *TableBuilder {
	return NewTable("token_channels").
		Column("token_id INT NOT NULL").
		Column("channel_id INT NOT NULL").
		Column("created_at BIGINT NOT NULL").
		Column("PRIMARY KEY (token_id, channel_id)").
		Column("FOREIGN KEY (token_id) REFERENCES auth_tokens(id) ON DELETE CASCADE").
		Column("FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE").
		Index("idx_token_channels_token", "token_id").
		Index("idx_token_channels_channel", "channel_id")
}

// DefineLogsTable 定义logs表结构
func DefineLogsTable() *TableBuilder {
	return NewTable("logs").
		Column("id INT PRIMARY KEY AUTO_INCREMENT").
		Column("time BIGINT NOT NULL").
		Column("model VARCHAR(191) NOT NULL DEFAULT ''").
		Column("channel_id INT NOT NULL DEFAULT 0").
		Column("status_code INT NOT NULL").
		Column("message TEXT NOT NULL").
		Column("duration DOUBLE NOT NULL DEFAULT 0.0").
		Column("is_streaming TINYINT NOT NULL DEFAULT 0").
		Column("first_byte_time DOUBLE NOT NULL DEFAULT 0.0").
		Column("api_key_used VARCHAR(191) NOT NULL DEFAULT ''").
		Column("api_base_url VARCHAR(512) NOT NULL DEFAULT ''"). // 使用的API端点URL（新增2025-12）
		Column("auth_token_id BIGINT NOT NULL DEFAULT 0"). // 客户端使用的API令牌ID（新增2025-12）
		Column("client_ip VARCHAR(45) NOT NULL DEFAULT ''"). // 客户端IP地址（新增2025-12）
		Column("input_tokens INT NOT NULL DEFAULT 0").
		Column("output_tokens INT NOT NULL DEFAULT 0").
		Column("cache_read_input_tokens INT NOT NULL DEFAULT 0").
		Column("cache_creation_input_tokens INT NOT NULL DEFAULT 0").
		Column("cost DOUBLE NOT NULL DEFAULT 0.0").
		Index("idx_logs_time_model", "time, model").
		Index("idx_logs_time_channel", "time, channel_id").
		Index("idx_logs_time_status", "time, status_code").
		Index("idx_logs_time_channel_model", "time, channel_id, model").
		Index("idx_logs_time_auth_token", "time, auth_token_id") // 按时间+令牌查询
}

// DefineDailyStatsTable 定义daily_stats表结构（每日统计聚合）
// 设计说明：
//   - 每天凌晨聚合前一天的日志数据，保留更长时间（默认365天）
//   - 按 channel_id + channel_type + model + auth_token_id 维度聚合
//   - 日志清理后仍可查询历史统计数据
func DefineDailyStatsTable() *TableBuilder {
	return NewTable("daily_stats").
		Column("id INT PRIMARY KEY AUTO_INCREMENT").
		Column("date DATE NOT NULL").                              // 统计日期（YYYY-MM-DD）
		Column("channel_id INT NOT NULL").                         // 渠道ID
		Column("channel_type VARCHAR(64) NOT NULL DEFAULT ''").    // 渠道类型（冗余存储，避免JOIN）
		Column("model VARCHAR(191) NOT NULL DEFAULT ''").          // 模型名称
		Column("auth_token_id BIGINT NOT NULL DEFAULT 0").         // API令牌ID（0表示未知）
		Column("success_count INT NOT NULL DEFAULT 0").            // 成功请求数
		Column("error_count INT NOT NULL DEFAULT 0").              // 失败请求数
		Column("total_count INT NOT NULL DEFAULT 0").              // 总请求数
		Column("input_tokens BIGINT NOT NULL DEFAULT 0").          // 输入Token总数
		Column("output_tokens BIGINT NOT NULL DEFAULT 0").         // 输出Token总数
		Column("cache_read_tokens BIGINT NOT NULL DEFAULT 0").     // 缓存读取Token总数
		Column("cache_creation_tokens BIGINT NOT NULL DEFAULT 0"). // 缓存创建Token总数
		Column("total_cost DOUBLE NOT NULL DEFAULT 0.0").          // 总成本（USD）
		Column("avg_duration DOUBLE NOT NULL DEFAULT 0.0").        // 平均响应时间（秒）
		Column("avg_first_byte_time DOUBLE NOT NULL DEFAULT 0.0"). // 平均首字节时间（秒）
		Column("stream_count INT NOT NULL DEFAULT 0").             // 流式请求数
		Column("non_stream_count INT NOT NULL DEFAULT 0").         // 非流式请求数
		Column("created_at BIGINT NOT NULL").                      // 记录创建时间
		Column("UNIQUE KEY uk_daily_stats (date, channel_id, model, auth_token_id)").
		Index("idx_daily_stats_date", "date").
		Index("idx_daily_stats_channel", "date, channel_id").
		Index("idx_daily_stats_channel_type", "date, channel_type").
		Index("idx_daily_stats_token", "date, auth_token_id")
}
