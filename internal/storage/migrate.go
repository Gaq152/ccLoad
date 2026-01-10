package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"ccLoad/internal/storage/schema"
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
		schema.DefineLogsTable,
		schema.DefineDailyStatsTable, // 每日统计聚合表（2025-12新增）
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
			} else {
				if err := ensureLogsAPIBaseURLSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate logs.api_base_url: %w", err)
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
			} else {
				if err := ensureAuthTokensCacheFieldsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens cache fields: %w", err)
				}
				if err := ensureAuthTokensAllChannelsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens.all_channels: %w", err)
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
