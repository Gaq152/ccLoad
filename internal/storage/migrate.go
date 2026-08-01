package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"ccLoad/internal/storage/schema"
	"ccLoad/internal/util"
)

// Dialect 数据库方言
type Dialect int

const (
	DialectSQLite Dialect = iota
	DialectMySQL
)

// migrateSQLite 执行SQLite数据库迁移
func migrateSQLite(ctx context.Context, db *sql.DB) error {
	return migrate(ctx, db, DialectSQLite)
}

// migrateMySQL 执行MySQL数据库迁移
func migrateMySQL(ctx context.Context, db *sql.DB) error {
	return migrate(ctx, db, DialectMySQL)
}

// migrate 统一迁移逻辑
func migrate(ctx context.Context, db *sql.DB, dialect Dialect) error {
	// 表定义（顺序重要：外键依赖）
	tables := []func() *schema.TableBuilder{
		schema.DefineChannelsTable,
		schema.DefineAPIKeysTable,
		schema.DefineChannelModelsTable,
		schema.DefineChannelEndpointsTable, // 多端点管理表
		schema.DefineAuthTokensTable,
		schema.DefineTokenChannelsTable, // 令牌-渠道关联表（依赖 auth_tokens 和 channels）
		schema.DefineSystemSettingsTable,
		schema.DefineAdminSessionsTable,
		schema.DefineAdmin2FATable,         // 管理员两步验证(TOTP)表（2026-06新增）
		schema.DefineAdminCredentialsTable, // 管理员密码表（2026-06新增，密码哈希落库替代环境变量）
		schema.DefineLogsTable,
		schema.DefineDailyStatsTable,   // 每日统计聚合表（2025-12新增）
		schema.DefineModelPricingTable, // 模型定价管理表（2026-03新增）
	}

	// 创建表和索引
	for _, defineTable := range tables {
		tb := defineTable()

		// 创建表
		if _, err := db.ExecContext(ctx, buildDDL(tb, dialect)); err != nil {
			return fmt.Errorf("create %s table: %w", tb.Name(), err)
		}

		// 增量迁移：确保logs新增字段存在（2025-12新增）
		if tb.Name() == "logs" {
			if dialect == DialectMySQL {
				if err := ensureLogsRequestType(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.request_type: %w", err)
				}
				if err := ensureLogsAuthTokenID(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.auth_token_id: %w", err)
				}
				if err := ensureLogsClientIP(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.client_ip: %w", err)
				}
				if err := ensureLogsAPIBaseURL(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_base_url: %w", err)
				}
				if err := ensureLogsAPIKeyHash(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_key_hash: %w", err)
				}
				if err := ensureLogsFastBillingFields(ctx, db); err != nil {
					return fmt.Errorf("migrate logs fast billing fields: %w", err)
				}
			} else {
				if err := ensureLogsRequestTypeSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.request_type: %w", err)
				}
				if err := ensureLogsAPIBaseURLSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_base_url: %w", err)
				}
				if err := ensureLogsAPIKeyHashSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_key_hash: %w", err)
				}
				if err := ensureLogsFastBillingFieldsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs fast billing fields: %w", err)
				}
			}
		}

		// 增量迁移：确保channels表有auto_select_endpoint字段（2025-12新增）
		if tb.Name() == "channels" {
			if dialect == DialectMySQL {
				if err := ensureChannelsAutoSelectEndpoint(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.auto_select_endpoint: %w", err)
				}
				if err := ensureChannelsQuotaConfig(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.quota_config: %w", err)
				}
				if err := ensureChannelsFastBillingConfig(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.fast_billing_config: %w", err)
				}
				if err := ensureChannelsPreset(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.preset: %w", err)
				}
				if err := ensureChannelsOpenAICompat(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.openai_compat: %w", err)
				}
				if err := ensureChannelsSortOrder(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.sort_order: %w", err)
				}
			} else {
				if err := ensureChannelsAutoSelectEndpointSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.auto_select_endpoint: %w", err)
				}
				if err := ensureChannelsQuotaConfigSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.quota_config: %w", err)
				}
				if err := ensureChannelsFastBillingConfigSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.fast_billing_config: %w", err)
				}
				if err := ensureChannelsPresetSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.preset: %w", err)
				}
				if err := ensureChannelsOpenAICompatSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.openai_compat: %w", err)
				}
				if err := ensureChannelsSortOrderSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.sort_order: %w", err)
				}
			}
		}

		// 增量迁移：确保api_keys表有OAuth字段（Codex官方预设使用）
		if tb.Name() == "api_keys" {
			if dialect == DialectMySQL {
				if err := ensureAPIKeysOAuthFields(ctx, db); err != nil {
					return fmt.Errorf("migrate api_keys oauth fields: %w", err)
				}
				if err := ensureAPIKeysDeviceFingerprint(ctx, db); err != nil {
					return fmt.Errorf("migrate api_keys.device_fingerprint: %w", err)
				}
			} else {
				if err := ensureAPIKeysOAuthFieldsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate api_keys oauth fields: %w", err)
				}
				if err := ensureAPIKeysDeviceFingerprintSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate api_keys.device_fingerprint: %w", err)
				}
			}
		}

		// 增量迁移：确保auth_tokens表有缓存token字段和all_channels字段
		if tb.Name() == "auth_tokens" {
			if dialect == DialectMySQL {
				if err := ensureAuthTokensCacheFields(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens cache fields: %w", err)
				}
				if err := ensureAuthTokensAllChannels(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.all_channels: %w", err)
				}
				if err := ensureAuthTokensTokenEncrypted(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.token_encrypted: %w", err)
				}
				if err := ensureAuthTokensTokenHint(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.token_hint: %w", err)
				}
				if err := ensureAuthTokensQuotaLimit(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.quota_limit_usd: %w", err)
				}
			} else {
				if err := ensureAuthTokensCacheFieldsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens cache fields: %w", err)
				}
				if err := ensureAuthTokensAllChannelsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.all_channels: %w", err)
				}
				if err := ensureAuthTokensTokenEncryptedSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.token_encrypted: %w", err)
				}
				if err := ensureAuthTokensTokenHintSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.token_hint: %w", err)
				}
				if err := ensureAuthTokensQuotaLimitSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.quota_limit_usd: %w", err)
				}
			}
		}

		// 增量迁移：确保model_pricing表的扩展定价字段存在
		if tb.Name() == "model_pricing" {
			if dialect == DialectMySQL {
				if err := ensurePricingAliases(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.aliases: %w", err)
				}
				if err := ensurePricingDefault(ctx, db, dialect); err != nil {
					return fmt.Errorf("migrate model_pricing.is_default: %w", err)
				}
				if err := ensurePricingHighPriceThreshold(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.high_price_threshold: %w", err)
				}
			} else {
				if err := ensurePricingAliasesSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.aliases: %w", err)
				}
				if err := ensurePricingDefault(ctx, db, dialect); err != nil {
					return fmt.Errorf("migrate model_pricing.is_default: %w", err)
				}
				if err := ensurePricingHighPriceThresholdSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.high_price_threshold: %w", err)
				}
			}
			if err := normalizePricingHighPriceThreshold(ctx, db); err != nil {
				return fmt.Errorf("normalize model_pricing.high_price_threshold: %w", err)
			}
			if err := ensurePricingAbsoluteCachePrices(ctx, db, dialect); err != nil {
				return fmt.Errorf("migrate model_pricing cache prices: %w", err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE model_pricing SET channel_type = 'codex' WHERE channel_type = 'openai'"); err != nil {
				return fmt.Errorf("migrate model_pricing channel type: %w", err)
			}
			if err := normalizeModelsDevPricingPrefixes(ctx, db); err != nil {
				return fmt.Errorf("normalize models.dev model prefixes: %w", err)
			}
		}

		// 增量迁移：确保channel_endpoints表有status_code字段（2025-12新增）
		if tb.Name() == "channel_endpoints" {
			if dialect == DialectMySQL {
				if err := ensureEndpointsStatusCode(ctx, db); err != nil {
					return fmt.Errorf("migrate channel_endpoints.status_code: %w", err)
				}
			} else {
				if err := ensureEndpointsStatusCodeSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channel_endpoints.status_code: %w", err)
				}
			}
		}

		// 创建索引
		for _, idx := range buildIndexes(tb, dialect) {
			if err := createIndex(ctx, db, idx, dialect); err != nil {
				return err
			}
		}
	}

	// 初始化默认配置
	if err := initDefaultSettings(ctx, db, dialect); err != nil {
		return err
	}
	if err := migrateChannelStatsFieldsDefault(ctx, db); err != nil {
		return fmt.Errorf("migrate channel stats fields default: %w", err)
	}

	// 清理废弃的配置项（2025-12清理）
	if err := removeDeprecatedSettings(ctx, db); err != nil {
		return fmt.Errorf("remove deprecated settings: %w", err)
	}

	// 迁移：为没有端点的渠道自动创建默认端点（2025-12新增）
	if err := migrateChannelEndpoints(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate channel endpoints: %w", err)
	}

	// 迁移：清理同一渠道下 URL 完全相同的重复端点（2026-06修复）
	if err := dedupeChannelEndpoints(ctx, db); err != nil {
		return fmt.Errorf("dedupe channel endpoints: %w", err)
	}

	// 迁移：确保所有多端点渠道至少有一个激活端点（2025-12新增）
	if err := ensureActiveEndpoints(ctx, db); err != nil {
		return fmt.Errorf("ensure active endpoints: %w", err)
	}

	// 迁移：为 Kiro 预设渠道补齐缺失的备用端点（2026-06修复）
	if err := migrateKiroBackupEndpoints(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate kiro backup endpoints: %w", err)
	}

	// 迁移：升级 Codex 官方预设渠道的 extractor 脚本和模型列表（2026-02新增）
	if err := migrateCodexPresetData(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate codex preset data: %w", err)
	}

	// 迁移：Kiro 预设端点和用量查询 URL 从旧域名迁移到新域名（2026-04新增）
	if err := migrateKiroEndpoints(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate kiro endpoints: %w", err)
	}

	// 迁移：为已有定价条目填充内置别名（默认列表标记由 is_default 专项迁移处理）
	if err := migratePricingAliases(ctx, db); err != nil {
		return fmt.Errorf("migrate pricing aliases: %w", err)
	}

	return nil
}

func ensureLogsRequestType(ctx context.Context, db *sql.DB) error {
	exists, err := hasColumnMySQL(ctx, db, "logs", "request_type")
	if err != nil || exists {
		return err
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE logs ADD COLUMN request_type VARCHAR(32) NOT NULL DEFAULT '' COMMENT '请求类型(responses/compact/search)'")
	return err
}

func ensureLogsRequestTypeSQLite(ctx context.Context, db *sql.DB) error {
	if hasColumnSQLite(ctx, db, "logs", "request_type") {
		return nil
	}
	_, err := db.ExecContext(ctx, "ALTER TABLE logs ADD COLUMN request_type TEXT NOT NULL DEFAULT ''")
	return err
}

// migrateChannelEndpoints 为没有端点的渠道自动创建默认端点（2025-12新增）
func migrateChannelEndpoints(ctx context.Context, db *sql.DB, dialect Dialect) error {
	// 查找有URL但没有端点的渠道
	query := `
		SELECT c.id, c.url
		FROM channels c
		LEFT JOIN channel_endpoints e ON c.id = e.channel_id
		WHERE c.url != '' AND e.id IS NULL
	`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query channels without endpoints: %w", err)
	}
	defer rows.Close()

	type channelURL struct {
		id  int64
		url string
	}
	var toMigrate []channelURL
	for rows.Next() {
		var ch channelURL
		if err := rows.Scan(&ch.id, &ch.url); err != nil {
			return fmt.Errorf("scan channel: %w", err)
		}
		toMigrate = append(toMigrate, ch)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate channels: %w", err)
	}

	if len(toMigrate) == 0 {
		return nil
	}

	// 为每个渠道创建默认端点（区分数据库方言）
	var insertQuery string
	if dialect == DialectMySQL {
		insertQuery = `
			INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at)
			VALUES (?, ?, 1, 0, UNIX_TIMESTAMP())
		`
	} else {
		insertQuery = `
			INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at)
			VALUES (?, ?, 1, 0, unixepoch())
		`
	}
	for _, ch := range toMigrate {
		_, err := db.ExecContext(ctx, insertQuery, ch.id, ch.url)
		if err != nil {
			return fmt.Errorf("insert endpoint for channel %d: %w", ch.id, err)
		}
	}

	return nil
}

// dedupeChannelEndpoints 清理同一渠道下 URL 完全相同的重复端点（2026-06修复）
// 历史上 SyncActiveEndpointURL 可能把激活端点的 URL 改写成与同渠道另一端点相同，
// 由于 channel_endpoints 表无 (channel_id,url) 唯一约束而产生重复行，
// 表现为端点管理弹窗出现两个相同端点。
// 每个 (channel_id, url) 仅保留一行（优先保留激活项，其次 sort_order/id 最小者）。
func dedupeChannelEndpoints(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT id, channel_id, url
		FROM channel_endpoints
		ORDER BY channel_id, url, is_active DESC, sort_order ASC, id ASC
	`)
	if err != nil {
		return fmt.Errorf("query endpoints for dedupe: %w", err)
	}
	defer rows.Close()

	type key struct {
		channelID int64
		url       string
	}
	seen := make(map[key]struct{})
	var toDelete []int64
	for rows.Next() {
		var id, channelID int64
		var url string
		if err := rows.Scan(&id, &channelID, &url); err != nil {
			return fmt.Errorf("scan endpoint: %w", err)
		}
		k := key{channelID: channelID, url: url}
		if _, ok := seen[k]; ok {
			// 同组第一行（排序后即保留项）已记录，其余视为重复删除
			toDelete = append(toDelete, id)
		} else {
			seen[k] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate endpoints: %w", err)
	}
	if len(toDelete) == 0 {
		return nil
	}

	for _, id := range toDelete {
		if _, err := db.ExecContext(ctx, "DELETE FROM channel_endpoints WHERE id = ?", id); err != nil {
			return fmt.Errorf("delete duplicate endpoint %d: %w", id, err)
		}
	}
	log.Printf("[迁移] 清理了 %d 个重复端点", len(toDelete))
	return nil
}

// migrateKiroBackupEndpoints 为 Kiro 预设渠道补齐缺失的备用端点（2026-06修复）
// Kiro 固定使用两个端点：q.us-east-1（主，generateAssistantResponse）+
// codewhisperer.us-east-1（备用，旧域名）。历史上备用端点只由前端硬编码展示、未必落库，
// 叠加旧 SyncActiveEndpointURL 改写 URL 的 bug 导致备用端点丢失。
// 此处确保每个 Kiro 渠道在库中都有备用端点，使数据库成为端点的唯一数据源。
// 前置：本函数在 ensureActiveEndpoints 之后调用，此时每个 Kiro 渠道已有主端点。
func migrateKiroBackupEndpoints(ctx context.Context, db *sql.DB, dialect Dialect) error {
	const backupURL = "https://codewhisperer.us-east-1.amazonaws.com"

	// 找出 preset='kiro' 且缺少备用端点的渠道
	rows, err := db.QueryContext(ctx, `
		SELECT c.id
		FROM channels c
		WHERE c.preset = 'kiro'
		  AND NOT EXISTS (
		    SELECT 1 FROM channel_endpoints e
		    WHERE e.channel_id = c.id AND e.url = ?
		  )
	`, backupURL)
	if err != nil {
		return fmt.Errorf("query kiro channels missing backup endpoint: %w", err)
	}
	defer rows.Close()

	var channelIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan channel id: %w", err)
		}
		channelIDs = append(channelIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate kiro channels: %w", err)
	}
	if len(channelIDs) == 0 {
		return nil
	}

	// 备用端点为非激活，追加到端点列表末尾（sort_order = 当前最大值 + 1）
	var insertSQL string
	if dialect == DialectMySQL {
		insertSQL = `
			INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at)
			VALUES (?, ?, 0, ?, UNIX_TIMESTAMP())`
	} else {
		insertSQL = `
			INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at)
			VALUES (?, ?, 0, ?, unixepoch())`
	}

	for _, id := range channelIDs {
		var maxSort sql.NullInt64
		if err := db.QueryRowContext(ctx,
			"SELECT MAX(sort_order) FROM channel_endpoints WHERE channel_id = ?", id,
		).Scan(&maxSort); err != nil {
			return fmt.Errorf("query max sort_order for channel %d: %w", id, err)
		}
		nextSort := 0
		if maxSort.Valid {
			nextSort = int(maxSort.Int64) + 1
		}
		if _, err := db.ExecContext(ctx, insertSQL, id, backupURL, nextSort); err != nil {
			return fmt.Errorf("insert backup endpoint for channel %d: %w", id, err)
		}
	}

	log.Printf("[迁移] 为 %d 个 Kiro 渠道补齐备用端点", len(channelIDs))
	return nil
}

// ensureActiveEndpoints 确保所有有端点的渠道至少有一个激活端点（2025-12新增）
func ensureActiveEndpoints(ctx context.Context, db *sql.DB) error {
	// 查找有端点但没有激活端点的渠道
	query := `
		SELECT DISTINCT e.channel_id
		FROM channel_endpoints e
		WHERE e.channel_id NOT IN (
			SELECT channel_id FROM channel_endpoints WHERE is_active = 1
		)
	`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query channels without active endpoint: %w", err)
	}
	defer rows.Close()

	var channelIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan channel id: %w", err)
		}
		channelIDs = append(channelIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate channels: %w", err)
	}

	if len(channelIDs) == 0 {
		return nil
	}

	// 为每个渠道激活第一个端点（按sort_order排序）
	for _, channelID := range channelIDs {
		_, err := db.ExecContext(ctx, `
			UPDATE channel_endpoints
			SET is_active = 1
			WHERE channel_id = ? AND id = (
				SELECT id FROM (
					SELECT id FROM channel_endpoints
					WHERE channel_id = ?
					ORDER BY sort_order ASC
					LIMIT 1
				) AS t
			)
		`, channelID, channelID)
		if err != nil {
			return fmt.Errorf("activate first endpoint for channel %d: %w", channelID, err)
		}
	}

	return nil
}

// ensureLogsAuthTokenID 确保logs表有auth_token_id字段(MySQL增量迁移,2025-12新增)
func ensureLogsAuthTokenID(ctx context.Context, db *sql.DB) error {
	// 检查字段是否存在
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND COLUMN_NAME='auth_token_id'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check column existence: %w", err)
	}

	// 字段已存在,跳过
	if count > 0 {
		return nil
	}

	// 添加auth_token_id字段
	_, err = db.ExecContext(ctx,
		"ALTER TABLE logs ADD COLUMN auth_token_id BIGINT NOT NULL DEFAULT 0 COMMENT '客户端使用的API令牌ID(新增2025-12)'",
	)
	if err != nil {
		return fmt.Errorf("add auth_token_id column: %w", err)
	}

	return nil
}

// ensureLogsClientIP 确保logs表有client_ip字段(MySQL增量迁移,2025-12新增)
func ensureLogsClientIP(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND COLUMN_NAME='client_ip'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check column existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE logs ADD COLUMN client_ip VARCHAR(45) NOT NULL DEFAULT '' COMMENT '客户端IP地址(新增2025-12)'",
	)
	if err != nil {
		return fmt.Errorf("add client_ip column: %w", err)
	}

	return nil
}

// ensureLogsAPIBaseURL 确保logs表有api_base_url字段(MySQL增量迁移,2025-12新增)
func ensureLogsAPIBaseURL(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND COLUMN_NAME='api_base_url'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check column existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE logs ADD COLUMN api_base_url VARCHAR(512) NOT NULL DEFAULT '' COMMENT '使用的API端点URL(新增2025-12)'",
	)
	if err != nil {
		return fmt.Errorf("add api_base_url column: %w", err)
	}

	return nil
}

// ensureLogsAPIBaseURLSQLite 确保logs表有api_base_url字段(SQLite增量迁移,2025-12新增)
func ensureLogsAPIBaseURLSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(logs)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "api_base_url" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE logs ADD COLUMN api_base_url TEXT NOT NULL DEFAULT ''",
	)
	if err != nil {
		return fmt.Errorf("add api_base_url column: %w", err)
	}

	return nil
}

// ensureChannelsAutoSelectEndpoint 确保channels表有auto_select_endpoint字段(MySQL增量迁移,2025-12新增)
// ensureLogsAPIKeyHash ensures logs.api_key_hash exists for MySQL.
func ensureLogsAPIKeyHash(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND COLUMN_NAME='api_key_hash'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check column existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE logs ADD COLUMN api_key_hash VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'API Key SHA256指纹(新增2026-03)'",
	)
	if err != nil {
		return fmt.Errorf("add api_key_hash column: %w", err)
	}

	return nil
}

// ensureLogsAPIKeyHashSQLite ensures logs.api_key_hash exists for SQLite.
func ensureLogsAPIKeyHashSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(logs)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "api_key_hash" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE logs ADD COLUMN api_key_hash TEXT NOT NULL DEFAULT ''",
	)
	if err != nil {
		return fmt.Errorf("add api_key_hash column: %w", err)
	}

	return nil
}

func ensureLogsFastBillingFields(ctx context.Context, db *sql.DB) error {
	fields := []struct {
		name string
		ddl  string
	}{
		{"is_fast", "ALTER TABLE logs ADD COLUMN is_fast TINYINT NOT NULL DEFAULT 0 COMMENT '是否Fast计费'"},
		{"service_tier", "ALTER TABLE logs ADD COLUMN service_tier VARCHAR(32) NOT NULL DEFAULT '' COMMENT '上游service_tier'"},
		{"reasoning_effort", "ALTER TABLE logs ADD COLUMN reasoning_effort VARCHAR(32) NOT NULL DEFAULT '' COMMENT 'Codex思考强度'"},
		{"fast_multiplier", "ALTER TABLE logs ADD COLUMN fast_multiplier DOUBLE NOT NULL DEFAULT 1.0 COMMENT 'Fast计费倍率'"},
	}
	for _, field := range fields {
		exists, err := hasColumnMySQL(ctx, db, "logs", field.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, field.ddl); err != nil {
			return fmt.Errorf("add logs.%s column: %w", field.name, err)
		}
	}
	return nil
}

func ensureLogsFastBillingFieldsSQLite(ctx context.Context, db *sql.DB) error {
	fields := []struct {
		name string
		ddl  string
	}{
		{"is_fast", "ALTER TABLE logs ADD COLUMN is_fast INTEGER NOT NULL DEFAULT 0"},
		{"service_tier", "ALTER TABLE logs ADD COLUMN service_tier TEXT NOT NULL DEFAULT ''"},
		{"reasoning_effort", "ALTER TABLE logs ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT ''"},
		{"fast_multiplier", "ALTER TABLE logs ADD COLUMN fast_multiplier REAL NOT NULL DEFAULT 1.0"},
	}
	for _, field := range fields {
		if hasColumnSQLite(ctx, db, "logs", field.name) {
			continue
		}
		if _, err := db.ExecContext(ctx, field.ddl); err != nil {
			return fmt.Errorf("add logs.%s column: %w", field.name, err)
		}
	}
	return nil
}

func ensureChannelsAutoSelectEndpoint(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='channels' AND COLUMN_NAME='auto_select_endpoint'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check column existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN auto_select_endpoint TINYINT NOT NULL DEFAULT 1 COMMENT '自动选择最快端点(默认开启,新增2025-12)'",
	)
	if err != nil {
		return fmt.Errorf("add auto_select_endpoint column: %w", err)
	}

	return nil
}

// ensureChannelsAutoSelectEndpointSQLite 确保channels表有auto_select_endpoint字段(SQLite增量迁移,2025-12新增)
func ensureChannelsAutoSelectEndpointSQLite(ctx context.Context, db *sql.DB) error {
	// SQLite 用 PRAGMA table_info 检查字段是否存在
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(channels)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "auto_select_endpoint" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	// 添加字段（默认开启）
	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN auto_select_endpoint INTEGER NOT NULL DEFAULT 1",
	)
	if err != nil {
		return fmt.Errorf("add auto_select_endpoint column: %w", err)
	}

	return nil
}

// ensureAuthTokensCacheFieldsSQLite 确保auth_tokens表有缓存token字段(SQLite增量迁移,2025-12新增)
func ensureAuthTokensCacheFieldsSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(auth_tokens)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasCacheRead := false
	hasCacheCreation := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "cache_read_tokens_total" {
			hasCacheRead = true
		}
		if name == "cache_creation_tokens_total" {
			hasCacheCreation = true
		}
	}

	if !hasCacheRead {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE auth_tokens ADD COLUMN cache_read_tokens_total INTEGER NOT NULL DEFAULT 0",
		)
		if err != nil {
			return fmt.Errorf("add cache_read_tokens_total column: %w", err)
		}
	}

	if !hasCacheCreation {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE auth_tokens ADD COLUMN cache_creation_tokens_total INTEGER NOT NULL DEFAULT 0",
		)
		if err != nil {
			return fmt.Errorf("add cache_creation_tokens_total column: %w", err)
		}
	}

	return nil
}

// ensureAuthTokensCacheFields 确保auth_tokens表有缓存token字段(MySQL增量迁移,2025-12新增)
func ensureAuthTokensCacheFields(ctx context.Context, db *sql.DB) error {
	// 检查cache_read_tokens_total字段是否存在
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_tokens' AND COLUMN_NAME='cache_read_tokens_total'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check cache_read_tokens_total existence: %w", err)
	}

	// 字段已存在,跳过
	if count > 0 {
		return nil
	}

	// 添加cache_read_tokens_total字段
	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN cache_read_tokens_total BIGINT NOT NULL DEFAULT 0 COMMENT '累计缓存读Token数'",
	)
	if err != nil {
		return fmt.Errorf("add cache_read_tokens_total column: %w", err)
	}

	// 添加cache_creation_tokens_total字段
	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN cache_creation_tokens_total BIGINT NOT NULL DEFAULT 0 COMMENT '累计缓存写Token数'",
	)
	if err != nil {
		return fmt.Errorf("add cache_creation_tokens_total column: %w", err)
	}

	return nil
}

// ensureEndpointsStatusCode 确保channel_endpoints表有status_code字段(MySQL增量迁移,2025-12新增)
func ensureEndpointsStatusCode(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='channel_endpoints' AND COLUMN_NAME='status_code'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check status_code existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channel_endpoints ADD COLUMN status_code INT DEFAULT NULL COMMENT '最近测速HTTP状态码'",
	)
	if err != nil {
		return fmt.Errorf("add status_code column: %w", err)
	}

	return nil
}

// ensureEndpointsStatusCodeSQLite 确保channel_endpoints表有status_code字段(SQLite增量迁移,2025-12新增)
func ensureEndpointsStatusCodeSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(channel_endpoints)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "status_code" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channel_endpoints ADD COLUMN status_code INTEGER DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add status_code column: %w", err)
	}

	return nil
}

// ensureAuthTokensAllChannels 确保auth_tokens表有all_channels字段(MySQL增量迁移)
func ensureAuthTokensAllChannels(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_tokens' AND COLUMN_NAME='all_channels'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check all_channels existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN all_channels TINYINT NOT NULL DEFAULT 1 COMMENT '是否允许使用所有渠道'",
	)
	if err != nil {
		return fmt.Errorf("add all_channels column: %w", err)
	}

	return nil
}

// ensureAuthTokensAllChannelsSQLite 确保auth_tokens表有all_channels字段(SQLite增量迁移)
func ensureAuthTokensAllChannelsSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(auth_tokens)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "all_channels" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN all_channels INTEGER NOT NULL DEFAULT 1",
	)
	if err != nil {
		return fmt.Errorf("add all_channels column: %w", err)
	}

	return nil
}

// ensureChannelsQuotaConfig 确保channels表有quota_config字段(MySQL增量迁移,2025-12新增)
func ensureChannelsQuotaConfig(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='channels' AND COLUMN_NAME='quota_config'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check quota_config existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN quota_config TEXT DEFAULT NULL COMMENT '用量监控配置(JSON格式,新增2025-12)'",
	)
	if err != nil {
		return fmt.Errorf("add quota_config column: %w", err)
	}

	return nil
}

// ensureChannelsQuotaConfigSQLite 确保channels表有quota_config字段(SQLite增量迁移,2025-12新增)
func ensureChannelsQuotaConfigSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(channels)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "quota_config" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN quota_config TEXT DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add quota_config column: %w", err)
	}

	return nil
}

func ensureChannelsFastBillingConfig(ctx context.Context, db *sql.DB) error {
	exists, err := hasColumnMySQL(ctx, db, "channels", "fast_billing_config")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN fast_billing_config TEXT DEFAULT NULL COMMENT 'Fast模式计费倍率配置(JSON格式,新增2026-07)'",
	)
	if err != nil {
		return fmt.Errorf("add fast_billing_config column: %w", err)
	}
	return nil
}

func ensureChannelsFastBillingConfigSQLite(ctx context.Context, db *sql.DB) error {
	if hasColumnSQLite(ctx, db, "channels", "fast_billing_config") {
		return nil
	}
	_, err := db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN fast_billing_config TEXT DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add fast_billing_config column: %w", err)
	}
	return nil
}

func buildDDL(tb *schema.TableBuilder, dialect Dialect) string {
	if dialect == DialectMySQL {
		return tb.BuildMySQL()
	}
	return tb.BuildSQLite()
}

func buildIndexes(tb *schema.TableBuilder, dialect Dialect) []schema.IndexDef {
	if dialect == DialectMySQL {
		return tb.GetIndexesMySQL()
	}
	return tb.GetIndexesSQLite()
}

func createIndex(ctx context.Context, db *sql.DB, idx schema.IndexDef, dialect Dialect) error {
	_, err := db.ExecContext(ctx, idx.SQL)
	if err == nil {
		return nil
	}

	// MySQL 5.6不支持IF NOT EXISTS，忽略重复索引错误
	if dialect == DialectMySQL && strings.Contains(err.Error(), "Duplicate key name") {
		return nil
	}

	// SQLite的IF NOT EXISTS应该不会报错，但如果报错则返回
	return fmt.Errorf("create index: %w", err)
}

// removeDeprecatedSettings 删除废弃的配置项（2025-12清理）
func removeDeprecatedSettings(ctx context.Context, db *sql.DB) error {
	deprecatedKeys := []string{
		"upstream_first_byte_timeout",
		"non_stream_timeout",
		"88code_free_only",
		"skip_tls_verify",
		"channel_stats_range",
	}

	for _, key := range deprecatedKeys {
		_, err := db.ExecContext(ctx, "DELETE FROM system_settings WHERE key = ?", key)
		if err != nil {
			return fmt.Errorf("delete deprecated setting %s: %w", key, err)
		}
	}

	return nil
}

func migrateChannelStatsFieldsDefault(ctx context.Context, db *sql.DB) error {
	const oldDefault = "calls,rate,first_byte,input,output,cache_read,cache_creation,cost"
	const newDefault = "calls,rate,cache_rate,first_byte,input,output,cache_read,cache_creation,cost"

	if _, err := db.ExecContext(ctx, `
		UPDATE system_settings
		SET value = ?, default_value = ?
		WHERE key = 'channel_stats_fields'
		  AND (value = ? OR value = '')
	`, newDefault, newDefault, oldDefault); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE system_settings
		SET default_value = ?
		WHERE key = 'channel_stats_fields'
	`, newDefault); err != nil {
		return err
	}

	return nil
}

func initDefaultSettings(ctx context.Context, db *sql.DB, dialect Dialect) error {
	settings := []struct {
		key, value, valueType, desc, defaultVal string
	}{
		{"log_retention_days", "7", "int", "日志保留天数(-1永久保留,1-365天)", "7"},
		{"stats_retention_days", "365", "int", "统计数据保留天数(-1永久保留,1-3650天)", "365"},
		{"max_key_retries", "3", "int", "单渠道最大Key重试次数", "3"},
		{"channel_test_content", "sonnet 4.0的发布日期是什么", "string", "渠道测试默认内容", "sonnet 4.0的发布日期是什么"},
		{"channel_stats_fields", "calls,rate,cache_rate,first_byte,input,output,cache_read,cache_creation,cost", "string", "渠道统计显示字段(逗号分隔)", "calls,rate,cache_rate,first_byte,input,output,cache_read,cache_creation,cost"},
		{"nav_visible_pages", "stats,trends,model-test", "string", "导航栏可选页面(stats=调用统计,trends=请求趋势,model-test=模型测试)", "stats,trends,model-test"},
		{"endpoint_test_count", "3", "int", "端点测速次数(1-10次,取平均值)", "3"},
		{"cooldown_mode", "exponential", "string", "冷却时间模式(exponential=递增,fixed=固定)", "exponential"},
		{"cooldown_fixed_interval", "30", "int", "固定冷却时间间隔(秒,仅fixed模式生效)", "30"},
		{"auto_test_endpoints_interval", "300", "int", "后台自动测速端点间隔(秒,0=禁用)", "300"},
		{"channel_load_balance", "true", "bool", "渠道负载均衡(同优先级+同预设随机打乱)", "true"},
		{"quota_request_concurrency", "10", "int", "用量查询并发数(定时自动刷新,1-50)", "10"},
		{"quota_batch_concurrency", "10", "int", "批量查询并发数(手动刷新,1-50)", "10"},
		{"monitor_enabled", "false", "bool", "请求监控开关(重启后保持状态)", "false"},
		{"sse_keepalive_seconds", "0", "int", "SSE心跳保活间隔(秒,0=关闭,1-100)。优化Cloudflare免费CDN约100秒长连接超时:上游响应慢时提前发送SSE心跳防止连接被切断,建议60", "0"},
		{"non_stream_timeout_seconds", "300", "int", "非流式请求整体超时(秒,30-3600,修改后重启生效)。大请求(如压缩上下文)耗时较长时调大,HTTP传输层WriteTimeout会据此自动放宽", "300"},
		{"turnstile_enabled", "false", "bool", "登录页 Cloudflare Turnstile 人机验证(防脚本爆破密码,公网部署建议开启;需同时配置 Site Key 和 Secret Key 才生效)", "false"},
		{"turnstile_site_key", "", "string", "Turnstile Site Key(公开密钥,用于登录页渲染验证组件,在 Cloudflare 控制台获取)", ""},
		{"turnstile_secret_key", "", "string", "Turnstile Secret Key(私密密钥,用于服务端校验验证结果,切勿泄露)", ""},
		{"twofa_login_required", "true", "bool", "绑定两步验证后登录是否需要验证码(关闭后登录仅需密码;修改密码始终需要验证码)", "true"},
	}

	var query string
	if dialect == DialectMySQL {
		query = "INSERT IGNORE INTO system_settings (`key`, value, value_type, description, default_value, updated_at) VALUES (?, ?, ?, ?, ?, UNIX_TIMESTAMP())"
	} else {
		query = "INSERT OR IGNORE INTO system_settings (key, value, value_type, description, default_value, updated_at) VALUES (?, ?, ?, ?, ?, unixepoch())"
	}

	for _, s := range settings {
		if _, err := db.ExecContext(ctx, query, s.key, s.value, s.valueType, s.desc, s.defaultVal); err != nil {
			return fmt.Errorf("insert default setting %s: %w", s.key, err)
		}
	}

	return nil
}

// ensureChannelsPreset 确保channels表有preset字段(MySQL增量迁移)
func ensureChannelsPreset(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='channels' AND COLUMN_NAME='preset'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check preset existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN preset VARCHAR(32) DEFAULT NULL COMMENT 'Codex预设类型:official=官方,custom=自定义'",
	)
	if err != nil {
		return fmt.Errorf("add preset column: %w", err)
	}

	return nil
}

// ensureChannelsPresetSQLite 确保channels表有preset字段(SQLite增量迁移)
func ensureChannelsPresetSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(channels)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "preset" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN preset TEXT DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add preset column: %w", err)
	}

	return nil
}

// ensureAPIKeysOAuthFields 确保api_keys表有OAuth字段(MySQL增量迁移)
func ensureAPIKeysOAuthFields(ctx context.Context, db *sql.DB) error {
	// 检查并添加 access_token 字段
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_keys' AND COLUMN_NAME='access_token'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check access_token existence: %w", err)
	}

	if count == 0 {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN access_token TEXT DEFAULT NULL COMMENT 'OAuth access_token(官方预设使用)'",
		)
		if err != nil {
			return fmt.Errorf("add access_token column: %w", err)
		}
	}

	// 检查并添加 id_token 字段
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_keys' AND COLUMN_NAME='id_token'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check id_token existence: %w", err)
	}

	if count == 0 {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN id_token TEXT DEFAULT NULL COMMENT 'OAuth id_token(官方预设使用)'",
		)
		if err != nil {
			return fmt.Errorf("add id_token column: %w", err)
		}
	}

	// 检查并添加 refresh_token 字段
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_keys' AND COLUMN_NAME='refresh_token'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check refresh_token existence: %w", err)
	}

	if count == 0 {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN refresh_token TEXT DEFAULT NULL COMMENT 'OAuth refresh_token(官方预设使用)'",
		)
		if err != nil {
			return fmt.Errorf("add refresh_token column: %w", err)
		}
	}

	// 检查并添加 token_expires_at 字段
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_keys' AND COLUMN_NAME='token_expires_at'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check token_expires_at existence: %w", err)
	}

	if count == 0 {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN token_expires_at BIGINT NOT NULL DEFAULT 0 COMMENT 'Token过期时间戳(官方预设使用)'",
		)
		if err != nil {
			return fmt.Errorf("add token_expires_at column: %w", err)
		}
	}

	return nil
}

// ensureAPIKeysOAuthFieldsSQLite 确保api_keys表有OAuth字段(SQLite增量迁移)
func ensureAPIKeysOAuthFieldsSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(api_keys)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasAccessToken := false
	hasIDToken := false
	hasRefreshToken := false
	hasTokenExpiresAt := false

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		switch name {
		case "access_token":
			hasAccessToken = true
		case "id_token":
			hasIDToken = true
		case "refresh_token":
			hasRefreshToken = true
		case "token_expires_at":
			hasTokenExpiresAt = true
		}
	}

	if !hasAccessToken {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN access_token TEXT DEFAULT NULL",
		)
		if err != nil {
			return fmt.Errorf("add access_token column: %w", err)
		}
	}

	if !hasIDToken {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN id_token TEXT DEFAULT NULL",
		)
		if err != nil {
			return fmt.Errorf("add id_token column: %w", err)
		}
	}

	if !hasRefreshToken {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN refresh_token TEXT DEFAULT NULL",
		)
		if err != nil {
			return fmt.Errorf("add refresh_token column: %w", err)
		}
	}

	if !hasTokenExpiresAt {
		_, err = db.ExecContext(ctx,
			"ALTER TABLE api_keys ADD COLUMN token_expires_at INTEGER NOT NULL DEFAULT 0",
		)
		if err != nil {
			return fmt.Errorf("add token_expires_at column: %w", err)
		}
	}

	return nil
}

// ensureAPIKeysDeviceFingerprint 确保api_keys表有device_fingerprint字段(MySQL增量迁移)
func ensureAPIKeysDeviceFingerprint(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='api_keys' AND COLUMN_NAME='device_fingerprint'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check device_fingerprint existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE api_keys ADD COLUMN device_fingerprint VARCHAR(128) DEFAULT NULL COMMENT 'Kiro设备指纹(64位hex字符串)'",
	)
	if err != nil {
		return fmt.Errorf("add device_fingerprint column: %w", err)
	}

	return nil
}

// ensureAPIKeysDeviceFingerprintSQLite 确保api_keys表有device_fingerprint字段(SQLite增量迁移)
func ensureAPIKeysDeviceFingerprintSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(api_keys)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "device_fingerprint" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE api_keys ADD COLUMN device_fingerprint TEXT DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add device_fingerprint column: %w", err)
	}

	return nil
}

// ensureChannelsOpenAICompat 确保channels表有openai_compat字段(MySQL增量迁移)
func ensureChannelsOpenAICompat(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='channels' AND COLUMN_NAME='openai_compat'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check openai_compat existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN openai_compat TINYINT NOT NULL DEFAULT 0 COMMENT 'OpenAI兼容模式(Gemini渠道使用/v1/chat/completions格式)'",
	)
	if err != nil {
		return fmt.Errorf("add openai_compat column: %w", err)
	}

	return nil
}

// ensureChannelsOpenAICompatSQLite 确保channels表有openai_compat字段(SQLite增量迁移)
func ensureChannelsOpenAICompatSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(channels)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "openai_compat" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN openai_compat INTEGER NOT NULL DEFAULT 0",
	)
	if err != nil {
		return fmt.Errorf("add openai_compat column: %w", err)
	}

	return nil
}

// ensureChannelsSortOrder 确保channels表有sort_order字段(MySQL增量迁移,拖拽排序用)
func ensureChannelsSortOrder(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='channels' AND COLUMN_NAME='sort_order'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check sort_order existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN sort_order INT NOT NULL DEFAULT 0 COMMENT '同优先级内的排序顺序(拖拽排序用)'",
	)
	if err != nil {
		return fmt.Errorf("add sort_order column: %w", err)
	}

	return nil
}

// ensureChannelsSortOrderSQLite 确保channels表有sort_order字段(SQLite增量迁移,拖拽排序用)
func ensureChannelsSortOrderSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(channels)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "sort_order" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE channels ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0",
	)
	if err != nil {
		return fmt.Errorf("add sort_order column: %w", err)
	}

	return nil
}

// codexExtractorV2 是升级后的 Codex 用量提取脚本（支持 Free/Plus/Team 不同窗口结构）
const codexExtractorV2 = `function(response) {
  const data = typeof response === 'string' ? JSON.parse(response) : response;

  if (!data.rate_limit) {
    return { isValid: false, error: "响应格式错误：缺少 rate_limit" };
  }

  const rl = data.rate_limit;
  const primary = rl.primary_window;

  if (!primary) {
    return { isValid: false, error: "响应格式错误：缺少 primary_window" };
  }

  var plan = data.plan_type || '';
  var hasDualWindow = !!rl.secondary_window;

  var remaining, detail;
  if (hasDualWindow) {
    var h5 = Math.round(100 - primary.used_percent);
    var weekly = Math.round(100 - rl.secondary_window.used_percent);
    var h5Reset = new Date(primary.reset_at * 1000).toLocaleString();
    var weeklyReset = new Date(rl.secondary_window.reset_at * 1000).toLocaleString();
    remaining = h5 + '|' + weekly;
    detail = plan + ' | 5h重置: ' + h5Reset + ' | 周重置: ' + weeklyReset;
  } else {
    var weeklyPct = Math.round(100 - primary.used_percent);
    var resetTime = new Date(primary.reset_at * 1000).toLocaleString();
    remaining = '-|' + weeklyPct;
    detail = plan + ' | 重置: ' + resetTime;
  }

  return {
    isValid: true,
    remaining: remaining,
    unit: '',
    detail: detail,
    limitReached: rl.limit_reached || false
  };
}`

// migrateCodexPresetData 升级 Codex 官方预设渠道的 extractor 脚本和模型列表（2026-02新增）
// 每次启动时幂等执行：
// 1. 将旧版 extractor 脚本替换为 V2（支持 5h+周窗口）
// 2. 为已有渠道补充新增的预设模型（只增不删）
func migrateCodexPresetData(ctx context.Context, db *sql.DB, dialect Dialect) error {
	// 查找所有 Codex 官方预设渠道
	rows, err := db.QueryContext(ctx, `
		SELECT id, models, quota_config
		FROM channels
		WHERE channel_type = 'codex' AND preset = 'official'
	`)
	if err != nil {
		return fmt.Errorf("query codex channels: %w", err)
	}
	defer rows.Close()

	type codexChannel struct {
		id          int64
		models      string
		quotaConfig *string
	}
	var channels []codexChannel
	for rows.Next() {
		var ch codexChannel
		if err := rows.Scan(&ch.id, &ch.models, &ch.quotaConfig); err != nil {
			return fmt.Errorf("scan codex channel: %w", err)
		}
		channels = append(channels, ch)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate codex channels: %w", err)
	}

	if len(channels) == 0 {
		return nil
	}

	// 获取最新的预设模型列表
	latestModels := util.DefaultModels(util.ChannelTypeCodex)
	if len(latestModels) == 0 {
		return nil
	}

	for _, ch := range channels {
		// === 1. 升级 extractor 脚本 ===
		if ch.quotaConfig != nil && *ch.quotaConfig != "" {
			var qc map[string]any
			if err := json.Unmarshal([]byte(*ch.quotaConfig), &qc); err == nil {
				oldScript, _ := qc["extractor_script"].(string)
				// 只在脚本是旧版时才更新（通过特征判断）
				if oldScript != "" && !strings.Contains(oldScript, "secondary_window") {
					qc["extractor_script"] = codexExtractorV2
					newJSON, err := json.Marshal(qc)
					if err == nil {
						_, err = db.ExecContext(ctx,
							"UPDATE channels SET quota_config = ? WHERE id = ?",
							string(newJSON), ch.id,
						)
						if err != nil {
							log.Printf("Warning: migrate codex extractor for channel %d: %v", ch.id, err)
						}
					}
				}
			}
		}

		// === 2. 补充缺失的预设模型 ===
		var existingModels []string
		if err := json.Unmarshal([]byte(ch.models), &existingModels); err != nil {
			continue
		}

		// 构建已有模型集合
		existingSet := make(map[string]bool, len(existingModels))
		for _, m := range existingModels {
			existingSet[m] = true
		}

		// 找出缺失的模型
		var newModels []string
		for _, m := range latestModels {
			if !existingSet[m] {
				newModels = append(newModels, m)
			}
		}

		if len(newModels) == 0 {
			continue
		}

		// 更新 channels.models JSON 字段
		updatedModels := append(existingModels, newModels...)
		modelsJSON, err := json.Marshal(updatedModels)
		if err != nil {
			continue
		}
		_, err = db.ExecContext(ctx,
			"UPDATE channels SET models = ? WHERE id = ?",
			string(modelsJSON), ch.id,
		)
		if err != nil {
			log.Printf("Warning: migrate codex models for channel %d: %v", ch.id, err)
			continue
		}

		// 同步到 channel_models 索引表
		var insertSQL string
		if dialect == DialectSQLite {
			insertSQL = `INSERT OR IGNORE INTO channel_models (channel_id, model) VALUES (?, ?)`
		} else {
			insertSQL = `INSERT IGNORE INTO channel_models (channel_id, model) VALUES (?, ?)`
		}
		for _, m := range newModels {
			if _, err := db.ExecContext(ctx, insertSQL, ch.id, m); err != nil {
				log.Printf("Warning: insert model %s for channel %d: %v", m, ch.id, err)
			}
		}
	}

	return nil
}

// migrateKiroEndpoints 迁移 Kiro 预设渠道的端点和用量查询 URL（2026-04新增）
// 旧域名 codewhisperer.us-east-1.amazonaws.com → 新域名 q.us-east-1.amazonaws.com
// 1. channel_endpoints 表：为旧端点渠道新增新域名端点
// 2. quota_config：替换 request_url 中的旧域名
// 3. channels.url：替换主 URL 字段中的旧域名
func migrateKiroEndpoints(ctx context.Context, db *sql.DB, dialect Dialect) error {
	const (
		oldDomain = "codewhisperer.us-east-1.amazonaws.com"
		newDomain = "q.us-east-1.amazonaws.com"
		oldURL    = "https://" + oldDomain
		newURL    = "https://" + newDomain
	)

	// === 1. 端点迁移：为只有旧端点的 Kiro 渠道新增新域名端点 ===
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT e.channel_id
		FROM channel_endpoints e
		JOIN channels c ON c.id = e.channel_id
		WHERE c.preset = 'kiro'
		  AND e.url LIKE '%`+oldDomain+`%'
		  AND e.channel_id NOT IN (
			SELECT channel_id FROM channel_endpoints WHERE url LIKE '%`+newDomain+`%'
		  )
	`)
	if err != nil {
		return fmt.Errorf("query kiro channels with old endpoints: %w", err)
	}
	defer rows.Close()

	var channelIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan channel id: %w", err)
		}
		channelIDs = append(channelIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate kiro channels: %w", err)
	}

	if len(channelIDs) > 0 {
		var insertSQL string
		if dialect == DialectMySQL {
			insertSQL = `INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at) VALUES (?, ?, 1, 0, UNIX_TIMESTAMP())`
		} else {
			insertSQL = `INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at) VALUES (?, ?, 1, 0, unixepoch())`
		}
		for _, chID := range channelIDs {
			if _, err := db.ExecContext(ctx, insertSQL, chID, newURL); err != nil {
				log.Printf("[WARN] [Migrate] Kiro 端点迁移失败 (channel=%d): %v", chID, err)
			} else {
				log.Printf("[INFO] [Migrate] Kiro 渠道 #%d 已新增端点: %s", chID, newURL)
			}
		}
	}

	// === 2. 用量查询 URL 迁移：替换 quota_config 中的旧域名 ===
	qRows, err := db.QueryContext(ctx, `
		SELECT id, quota_config
		FROM channels
		WHERE preset = 'kiro' AND quota_config IS NOT NULL AND quota_config != ''
		  AND quota_config LIKE '%`+oldDomain+`%'
	`)
	if err != nil {
		return fmt.Errorf("query kiro quota configs: %w", err)
	}
	defer qRows.Close()

	type kiroQuota struct {
		id          int64
		quotaConfig string
	}
	var toUpdate []kiroQuota
	for qRows.Next() {
		var kq kiroQuota
		if err := qRows.Scan(&kq.id, &kq.quotaConfig); err != nil {
			return fmt.Errorf("scan kiro quota: %w", err)
		}
		toUpdate = append(toUpdate, kq)
	}
	if err := qRows.Err(); err != nil {
		return fmt.Errorf("iterate kiro quotas: %w", err)
	}

	for _, kq := range toUpdate {
		newConfig := strings.ReplaceAll(kq.quotaConfig, oldURL, newURL)
		if newConfig == kq.quotaConfig {
			continue
		}
		if _, err := db.ExecContext(ctx, "UPDATE channels SET quota_config = ? WHERE id = ?", newConfig, kq.id); err != nil {
			log.Printf("[WARN] [Migrate] Kiro 用量 URL 迁移失败 (channel=%d): %v", kq.id, err)
		} else {
			log.Printf("[INFO] [Migrate] Kiro 渠道 #%d 用量查询 URL 已更新", kq.id)
		}
	}

	// === 3. 更新 channels.url 主 URL 字段 ===
	if _, err := db.ExecContext(ctx, `
		UPDATE channels SET url = REPLACE(url, '`+oldURL+`', '`+newURL+`')
		WHERE preset = 'kiro' AND url LIKE '%`+oldDomain+`%'
	`); err != nil {
		log.Printf("[WARN] [Migrate] Kiro 渠道主 URL 迁移失败: %v", err)
	}

	return nil
}

// ============================================================
// model_pricing 表迁移：aliases、默认列表与绝对缓存价格
// ============================================================

func ensurePricingAliases(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='model_pricing' AND COLUMN_NAME='aliases'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check aliases existence: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE model_pricing ADD COLUMN aliases TEXT DEFAULT ''")
	return err
}

func ensurePricingAliasesSQLite(ctx context.Context, db *sql.DB) error {
	if hasColumnSQLite(ctx, db, "model_pricing", "aliases") {
		return nil
	}
	_, err := db.ExecContext(ctx, "ALTER TABLE model_pricing ADD COLUMN aliases TEXT DEFAULT ''")
	return err
}

func pricingHasColumn(ctx context.Context, db *sql.DB, dialect Dialect, column string) (bool, error) {
	if dialect == DialectMySQL {
		return hasColumnMySQL(ctx, db, "model_pricing", column)
	}
	return hasColumnSQLite(ctx, db, "model_pricing", column), nil
}

// ensurePricingDefault 把旧 is_predefined 标记迁移为与渠道页共用的默认列表标记。
func ensurePricingDefault(ctx context.Context, db *sql.DB, dialect Dialect) error {
	exists, err := pricingHasColumn(ctx, db, dialect, "is_default")
	if err != nil {
		return err
	}
	if !exists {
		columnType := "TINYINT"
		if dialect == DialectSQLite {
			columnType = "INTEGER"
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE model_pricing ADD COLUMN is_default %s NOT NULL DEFAULT -1", columnType)); err != nil {
			return err
		}
	}

	hasLegacy, err := pricingHasColumn(ctx, db, dialect, "is_predefined")
	if err != nil {
		return err
	}
	if hasLegacy {
		_, err = db.ExecContext(ctx, "UPDATE model_pricing SET is_default = is_predefined WHERE is_default < 0")
		return err
	}

	// 极旧数据库没有 is_predefined 时，先完成安全初始化；内置默认定价重新导入后会写入准确标记。
	_, err = db.ExecContext(ctx, "UPDATE model_pricing SET is_default = 0 WHERE is_default < 0")
	return err
}

// ensurePricingAbsoluteCachePrices 将旧缓存倍率一次性换算为基础/高位绝对价格。
func ensurePricingAbsoluteCachePrices(ctx context.Context, db *sql.DB, dialect Dialect) error {
	columns := []string{"cache_read_price", "cache_write_price", "cache_read_price_high", "cache_write_price_high"}
	for _, column := range columns {
		exists, err := pricingHasColumn(ctx, db, dialect, column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE model_pricing ADD COLUMN %s DOUBLE NOT NULL DEFAULT -1", column)); err != nil {
			return fmt.Errorf("add %s: %w", column, err)
		}
	}

	hasReadMultiplier, err := pricingHasColumn(ctx, db, dialect, "cache_read_multiplier")
	if err != nil {
		return err
	}
	hasWriteMultiplier, err := pricingHasColumn(ctx, db, dialect, "cache_write_multiplier")
	if err != nil {
		return err
	}
	readMultiplierExpr := "0"
	writeMultiplierExpr := "0"
	if hasReadMultiplier {
		readMultiplierExpr = "cache_read_multiplier"
	}
	if hasWriteMultiplier {
		writeMultiplierExpr = "cache_write_multiplier"
	}

	query := fmt.Sprintf(`SELECT id, model, input_price, input_price_high,
		cache_read_price, cache_write_price, cache_read_price_high, cache_write_price_high,
		%s, %s
		FROM model_pricing
		WHERE cache_read_price < 0 OR cache_write_price < 0
		   OR cache_read_price_high < 0 OR cache_write_price_high < 0`, readMultiplierExpr, writeMultiplierExpr)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	type legacyPricingRow struct {
		id                                                   int64
		model                                                string
		input, inputHigh                                     float64
		cacheRead, cacheWrite, cacheReadHigh, cacheWriteHigh float64
		readMultiplier, writeMultiplier                      float64
	}
	var pending []legacyPricingRow
	for rows.Next() {
		var row legacyPricingRow
		if err := rows.Scan(&row.id, &row.model, &row.input, &row.inputHigh,
			&row.cacheRead, &row.cacheWrite, &row.cacheReadHigh, &row.cacheWriteHigh,
			&row.readMultiplier, &row.writeMultiplier); err != nil {
			_ = rows.Close()
			return err
		}
		pending = append(pending, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, row := range pending {
		readMultiplier := row.readMultiplier
		if readMultiplier <= 0 {
			readMultiplier = util.LegacyCacheReadMultiplier(row.model)
		}
		writeMultiplier := row.writeMultiplier
		if writeMultiplier <= 0 {
			writeMultiplier = 1.25
		}
		if row.cacheRead < 0 {
			row.cacheRead = row.input * readMultiplier
		}
		if row.cacheWrite < 0 {
			row.cacheWrite = row.input * writeMultiplier
		}
		if row.cacheReadHigh < 0 {
			row.cacheReadHigh = 0
			if row.inputHigh > 0 {
				row.cacheReadHigh = row.inputHigh * readMultiplier
			}
		}
		if row.cacheWriteHigh < 0 {
			row.cacheWriteHigh = 0
			if row.inputHigh > 0 {
				row.cacheWriteHigh = row.inputHigh * writeMultiplier
			}
		}
		if _, err := db.ExecContext(ctx, `UPDATE model_pricing
			SET cache_read_price = ?, cache_write_price = ?, cache_read_price_high = ?, cache_write_price_high = ?
			WHERE id = ?`, row.cacheRead, row.cacheWrite, row.cacheReadHigh, row.cacheWriteHigh, row.id); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		log.Printf("[INFO] [Migrate] 缓存倍率已迁移为绝对价格: %d 条", len(pending))
	}
	return nil
}

// ensurePricingHighPriceThreshold 为旧库增加每模型高价档阈值。
func ensurePricingHighPriceThreshold(ctx context.Context, db *sql.DB) error {
	exists, err := hasColumnMySQL(ctx, db, "model_pricing", "high_price_threshold")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE model_pricing ADD COLUMN high_price_threshold BIGINT NOT NULL DEFAULT 0")
	return err
}

func ensurePricingHighPriceThresholdSQLite(ctx context.Context, db *sql.DB) error {
	if hasColumnSQLite(ctx, db, "model_pricing", "high_price_threshold") {
		return nil
	}
	_, err := db.ExecContext(ctx, "ALTER TABLE model_pricing ADD COLUMN high_price_threshold BIGINT NOT NULL DEFAULT 0")
	return err
}

// normalizePricingHighPriceThreshold 只让真正存在高档输入价的模型保留阈值。
func normalizePricingHighPriceThreshold(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `UPDATE model_pricing
		SET high_price_threshold = CASE
			WHEN input_price_high <= 0 THEN 0
			WHEN high_price_threshold > 0 THEN high_price_threshold
			WHEN channel_type = 'gemini' THEN 200000
			ELSE 272000
		END`)
	return err
}

// normalizeModelsDevPricingPrefixes 修复早期 models.dev 导入使用的点号命名空间。
// models.dev 目录中的 anthropic.claude-* / openai.gpt-* / google.gemini-*
// 是目录 ID，不是调用时使用的真实模型名。旧 ID 会作为别名保留，以兼容已有日志或配置。
func normalizeModelsDevPricingPrefixes(ctx context.Context, db *sql.DB) error {
	type pricingRow struct {
		id      int64
		model   string
		aliases string
	}

	rows, err := db.QueryContext(ctx, "SELECT id, model, COALESCE(aliases, '') FROM model_pricing")
	if err != nil {
		return err
	}
	var entries []*pricingRow
	for rows.Next() {
		row := &pricingRow{}
		if err := rows.Scan(&row.id, &row.model, &row.aliases); err != nil {
			_ = rows.Close()
			return err
		}
		entries = append(entries, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	byModel := make(map[string]*pricingRow, len(entries))
	for _, row := range entries {
		byModel[strings.ToLower(strings.TrimSpace(row.model))] = row
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	normalizedCount := 0
	mergedCount := 0
	for _, row := range entries {
		normalizedModel, ok := normalizeModelsDevPricingPrefix(row.model)
		if !ok {
			continue
		}

		if target := byModel[strings.ToLower(normalizedModel)]; target != nil && target.id != row.id {
			target.aliases = mergePricingAliasStrings(target.aliases, row.aliases, row.model)
			if _, err := tx.ExecContext(ctx, "UPDATE model_pricing SET aliases = ? WHERE id = ?", target.aliases, target.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM model_pricing WHERE id = ?", row.id); err != nil {
				return err
			}
			delete(byModel, strings.ToLower(strings.TrimSpace(row.model)))
			mergedCount++
			continue
		}

		row.aliases = mergePricingAliasStrings(row.aliases, row.model)
		if _, err := tx.ExecContext(ctx, "UPDATE model_pricing SET model = ?, aliases = ? WHERE id = ?", normalizedModel, row.aliases, row.id); err != nil {
			return err
		}
		delete(byModel, strings.ToLower(strings.TrimSpace(row.model)))
		row.model = normalizedModel
		byModel[strings.ToLower(normalizedModel)] = row
		normalizedCount++
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if normalizedCount > 0 || mergedCount > 0 {
		log.Printf("[INFO] [Migrate] models.dev 模型前缀已修复: 重命名 %d，合并重复 %d", normalizedCount, mergedCount)
	}
	return nil
}

func normalizeModelsDevPricingPrefix(modelName string) (string, bool) {
	trimmed := strings.TrimSpace(modelName)
	lower := strings.ToLower(trimmed)
	for _, namespace := range []string{"anthropic.", "openai.", "google."} {
		if strings.HasPrefix(lower, namespace) && len(trimmed) > len(namespace) {
			return strings.ToLower(strings.TrimSpace(trimmed[len(namespace):])), true
		}
	}
	return trimmed, false
}

func mergePricingAliasStrings(groups ...string) string {
	seen := make(map[string]struct{})
	aliases := make([]string, 0)
	for _, group := range groups {
		for _, alias := range strings.Split(group, ",") {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			key := strings.ToLower(alias)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			aliases = append(aliases, alias)
		}
	}
	return strings.Join(aliases, ",")
}

// hasColumnMySQL 检查 MySQL 表是否有指定列
func hasColumnMySQL(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?",
		table, column,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check %s.%s existence: %w", table, column, err)
	}
	return count > 0, nil
}

// hasColumnSQLite 检查 SQLite 表是否有指定列
func hasColumnSQLite(ctx context.Context, db *sql.DB, table, column string) bool {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// migratePricingAliases 为已有定价条目填充内置别名。
// 仅对 aliases 为空的行执行（幂等），不会覆盖用户维护的默认列表标记。
func migratePricingAliases(ctx context.Context, db *sql.DB) error {
	// 检查是否有需要迁移的行（aliases 为空且有数据的行）
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM model_pricing WHERE aliases = '' OR aliases IS NULL").Scan(&total); err != nil {
		return nil // 表可能不存在，跳过
	}
	if total == 0 {
		return nil
	}

	// 构建反向别名映射：base model → 逗号分隔的别名
	reverseAliases := util.GetModelAliasesReverse()

	// 查询所有需要迁移的行
	rows, err := db.QueryContext(ctx, "SELECT id, model FROM model_pricing WHERE aliases = '' OR aliases IS NULL")
	if err != nil {
		return fmt.Errorf("query pricing for migration: %w", err)
	}
	defer rows.Close()

	type pricingRow struct {
		id    int64
		model string
	}
	var toMigrate []pricingRow
	for rows.Next() {
		var r pricingRow
		if err := rows.Scan(&r.id, &r.model); err != nil {
			return fmt.Errorf("scan pricing row: %w", err)
		}
		toMigrate = append(toMigrate, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	migrated := 0
	for _, r := range toMigrate {
		aliases := ""
		if aliasList, ok := reverseAliases[r.model]; ok {
			aliases = strings.Join(aliasList, ",")
		}
		if aliases == "" {
			continue
		}
		if _, err := db.ExecContext(ctx,
			"UPDATE model_pricing SET aliases = ? WHERE id = ?",
			aliases, r.id,
		); err != nil {
			log.Printf("[WARN] [Migrate] 定价别名迁移失败 model=%s: %v", r.model, err)
			continue
		}
		migrated++
	}

	if migrated > 0 {
		log.Printf("[INFO] [Migrate] 定价别名迁移完成: %d 条", migrated)
	}
	return nil
}

// ensureAuthTokensTokenEncrypted 确保auth_tokens表有token_encrypted字段(MySQL增量迁移)
func ensureAuthTokensTokenEncrypted(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_tokens' AND COLUMN_NAME='token_encrypted'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check token_encrypted existence: %w", err)
	}

	if count > 0 {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN token_encrypted TEXT DEFAULT NULL COMMENT 'AES加密的令牌明文'",
	)
	if err != nil {
		return fmt.Errorf("add token_encrypted column: %w", err)
	}

	return nil
}

// ensureAuthTokensTokenEncryptedSQLite 确保auth_tokens表有token_encrypted字段(SQLite增量迁移)
func ensureAuthTokensTokenEncryptedSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(auth_tokens)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "token_encrypted" {
			hasColumn = true
			break
		}
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN token_encrypted TEXT DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add token_encrypted column: %w", err)
	}

	return nil
}

// ensureAuthTokensTokenHint 确保auth_tokens表有token_hint字段(MySQL增量迁移)
func ensureAuthTokensTokenHint(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_tokens' AND COLUMN_NAME='token_hint'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check token_hint existence: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN token_hint VARCHAR(128) DEFAULT NULL COMMENT '令牌明文掩码提示'",
	)
	if err != nil {
		return fmt.Errorf("add token_hint column: %w", err)
	}
	return nil
}

// ensureAuthTokensTokenHintSQLite 确保auth_tokens表有token_hint字段(SQLite增量迁移)
func ensureAuthTokensTokenHintSQLite(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(auth_tokens)")
	if err != nil {
		return fmt.Errorf("check table info: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		if name == "token_hint" {
			hasColumn = true
			break
		}
	}
	if hasColumn {
		return nil
	}
	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN token_hint TEXT DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add token_hint column: %w", err)
	}
	return nil
}

// ensureAuthTokensQuotaLimit 确保auth_tokens表有quota_limit_usd字段(MySQL增量迁移)
func ensureAuthTokensQuotaLimit(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_tokens' AND COLUMN_NAME='quota_limit_usd'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check quota_limit_usd existence: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err = db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN quota_limit_usd DOUBLE DEFAULT NULL COMMENT '令牌额度上限(美元)，NULL表示无限'",
	)
	if err != nil {
		return fmt.Errorf("add quota_limit_usd column: %w", err)
	}
	return nil
}

// ensureAuthTokensQuotaLimitSQLite 确保auth_tokens表有quota_limit_usd字段(SQLite增量迁移)
func ensureAuthTokensQuotaLimitSQLite(ctx context.Context, db *sql.DB) error {
	if hasColumnSQLite(ctx, db, "auth_tokens", "quota_limit_usd") {
		return nil
	}
	_, err := db.ExecContext(ctx,
		"ALTER TABLE auth_tokens ADD COLUMN quota_limit_usd REAL DEFAULT NULL",
	)
	if err != nil {
		return fmt.Errorf("add quota_limit_usd column: %w", err)
	}
	return nil
}
