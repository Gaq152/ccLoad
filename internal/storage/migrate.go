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
			} else {
				if err := ensureLogsAPIBaseURLSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_base_url: %w", err)
				}
				if err := ensureLogsAPIKeyHashSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_key_hash: %w", err)
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
			}
		}

		// 增量迁移：确保model_pricing表有aliases和is_predefined字段（2026-04新增）
		if tb.Name() == "model_pricing" {
			if dialect == DialectMySQL {
				if err := ensurePricingAliases(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.aliases: %w", err)
				}
				if err := ensurePricingIsPredefined(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.is_predefined: %w", err)
				}
			} else {
				if err := ensurePricingAliasesSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.aliases: %w", err)
				}
				if err := ensurePricingIsPredefinedSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate model_pricing.is_predefined: %w", err)
				}
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

	// 清理废弃的配置项（2025-12清理）
	if err := removeDeprecatedSettings(ctx, db); err != nil {
		return fmt.Errorf("remove deprecated settings: %w", err)
	}

	// 迁移：为没有端点的渠道自动创建默认端点（2025-12新增）
	if err := migrateChannelEndpoints(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate channel endpoints: %w", err)
	}

	// 迁移：确保所有多端点渠道至少有一个激活端点（2025-12新增）
	if err := ensureActiveEndpoints(ctx, db); err != nil {
		return fmt.Errorf("ensure active endpoints: %w", err)
	}

	// 迁移：升级 Codex 官方预设渠道的 extractor 脚本和模型列表（2026-02新增）
	if err := migrateCodexPresetData(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate codex preset data: %w", err)
	}

	// 迁移：Kiro 预设端点和用量查询 URL 从旧域名迁移到新域名（2026-04新增）
	if err := migrateKiroEndpoints(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate kiro endpoints: %w", err)
	}

	// 迁移：为已有定价条目填充别名和预定义标记（2026-04新增）
	if err := migratePricingAliasesAndPredefined(ctx, db); err != nil {
		return fmt.Errorf("migrate pricing aliases/predefined: %w", err)
	}

	return nil
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

func initDefaultSettings(ctx context.Context, db *sql.DB, dialect Dialect) error {
	settings := []struct {
		key, value, valueType, desc, defaultVal string
	}{
		{"log_retention_days", "7", "int", "日志保留天数(-1永久保留,1-365天)", "7"},
		{"stats_retention_days", "365", "int", "统计数据保留天数(-1永久保留,1-3650天)", "365"},
		{"max_key_retries", "3", "int", "单渠道最大Key重试次数", "3"},
		{"channel_test_content", "sonnet 4.0的发布日期是什么", "string", "渠道测试默认内容", "sonnet 4.0的发布日期是什么"},
		{"channel_stats_fields", "calls,rate,first_byte,input,output,cache_read,cache_creation,cost", "string", "渠道统计显示字段(逗号分隔)", "calls,rate,first_byte,input,output,cache_read,cache_creation,cost"},
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
	latestModels := util.PredefinedModels(util.ChannelTypeCodex)
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
		  AND e.url LIKE '%` + oldDomain + `%'
		  AND e.channel_id NOT IN (
			SELECT channel_id FROM channel_endpoints WHERE url LIKE '%` + newDomain + `%'
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
		  AND quota_config LIKE '%` + oldDomain + `%'
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
// model_pricing 表迁移：aliases + is_predefined（2026-04新增）
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

func ensurePricingIsPredefined(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='model_pricing' AND COLUMN_NAME='is_predefined'",
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check is_predefined existence: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE model_pricing ADD COLUMN is_predefined TINYINT NOT NULL DEFAULT 0")
	return err
}

func ensurePricingIsPredefinedSQLite(ctx context.Context, db *sql.DB) error {
	if hasColumnSQLite(ctx, db, "model_pricing", "is_predefined") {
		return nil
	}
	_, err := db.ExecContext(ctx, "ALTER TABLE model_pricing ADD COLUMN is_predefined TINYINT NOT NULL DEFAULT 0")
	return err
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

// migratePricingAliasesAndPredefined 为已有定价条目填充别名和预定义标记
// 仅对 aliases 为空的行执行（幂等）
func migratePricingAliasesAndPredefined(ctx context.Context, db *sql.DB) error {
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

	// 构建预定义模型集合
	predefinedSet := make(map[string]bool)
	for channelType, models := range util.GetPredefinedModelSets() {
		_ = channelType
		for _, m := range models {
			predefinedSet[m] = true
		}
	}

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
		isPredefined := 0
		if predefinedSet[r.model] {
			isPredefined = 1
		}
		if aliases == "" && isPredefined == 0 {
			continue
		}
		if _, err := db.ExecContext(ctx,
			"UPDATE model_pricing SET aliases = ?, is_predefined = ? WHERE id = ?",
			aliases, isPredefined, r.id,
		); err != nil {
			log.Printf("[WARN] [Migrate] 定价别名迁移失败 model=%s: %v", r.model, err)
			continue
		}
		migrated++
	}

	if migrated > 0 {
		log.Printf("[INFO] [Migrate] 定价别名/预定义迁移完成: %d 条", migrated)
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
