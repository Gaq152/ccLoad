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
		schema.DefineSystemSettingsTable,
		schema.DefineAdminSessionsTable,
		schema.DefineLogsTable,
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
			} else {
				if err := ensureChannelsAutoSelectEndpointSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate channels.auto_select_endpoint: %w", err)
				}
			}
		}

		// 增量迁移：确保auth_tokens表有缓存token字段（2025-12新增）
		if tb.Name() == "auth_tokens" {
			if dialect == DialectMySQL {
				if err := ensureAuthTokensCacheFields(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens cache fields: %w", err)
				}
			} else {
				if err := ensureAuthTokensCacheFieldsSQLite(ctx, db); err != nil {
					return fmt.Errorf("migrate auth_tokens cache fields: %w", err)
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

	// 迁移：为没有端点的渠道自动创建默认端点（2025-12新增）
	if err := migrateChannelEndpoints(ctx, db, dialect); err != nil {
		return fmt.Errorf("migrate channel endpoints: %w", err)
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

func initDefaultSettings(ctx context.Context, db *sql.DB, dialect Dialect) error {
	settings := []struct {
		key, value, valueType, desc, defaultVal string
	}{
		{"log_retention_days", "7", "int", "日志保留天数(-1永久保留,1-365天)", "7"},
		{"max_key_retries", "3", "int", "单渠道最大Key重试次数", "3"},
		{"upstream_first_byte_timeout", "0", "duration", "上游首字节超时(秒,0=禁用)", "0"},
		{"non_stream_timeout", "120", "duration", "非流式请求超时(秒,0=禁用)", "120"},
		{"88code_free_only", "false", "bool", "仅允许使用88code免费订阅(free订阅可用时生效)", "false"},
		{"skip_tls_verify", "false", "bool", "跳过TLS证书验证", "false"},
		{"channel_test_content", "sonnet 4.0的发布日期是什么", "string", "渠道测试默认内容", "sonnet 4.0的发布日期是什么"},
		{"channel_stats_range", "today", "string", "渠道管理费用统计范围", "today"},
		{"endpoint_test_count", "3", "int", "端点测速次数(1-10次,取平均值)", "3"},
		{"cooldown_mode", "exponential", "string", "冷却时间模式(exponential=递增,fixed=固定)", "exponential"},
		{"cooldown_fixed_interval", "30", "int", "固定冷却时间间隔(秒,仅fixed模式生效)", "30"},
		{"auto_test_endpoints_interval", "300", "int", "后台自动测速端点间隔(秒,0=禁用)", "300"},
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
