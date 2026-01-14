package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Trace 请求追踪记录
type Trace struct {
	ID            int64   `json:"id"`
	Time          int64   `json:"time"`            // 毫秒时间戳
	ChannelID     int     `json:"channel_id"`
	ChannelName   string  `json:"channel_name"`
	ChannelType   string  `json:"channel_type"`
	Model         string  `json:"model"`
	RequestPath   string  `json:"request_path"`    // 请求路径（端点）
	StatusCode    int     `json:"status_code"`
	Duration      float64 `json:"duration"`
	IsStreaming   bool    `json:"is_streaming"`
	IsTest        bool    `json:"is_test"`         // 是否为测试请求
	InputTokens   int     `json:"input_tokens"`    // 输入 tokens
	OutputTokens  int     `json:"output_tokens"`   // 输出 tokens
	RequestBody   string  `json:"request_body,omitempty"`
	ResponseBody  string  `json:"response_body,omitempty"`
	ClientIP      string  `json:"client_ip"`
	APIKeyUsed    string  `json:"api_key_used"`
	AuthTokenName string  `json:"auth_token_name"` // API 令牌名称
}

// TraceListItem 追踪记录列表项（不含请求体/响应体，减少传输量）
type TraceListItem struct {
	ID            int64   `json:"id"`
	Time          int64   `json:"time"`
	ChannelID     int     `json:"channel_id"`
	ChannelName   string  `json:"channel_name"`
	ChannelType   string  `json:"channel_type"`
	Model         string  `json:"model"`
	RequestPath   string  `json:"request_path"`
	StatusCode    int     `json:"status_code"`
	Duration      float64 `json:"duration"`
	IsStreaming   bool    `json:"is_streaming"`
	IsTest        bool    `json:"is_test"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	ClientIP      string  `json:"client_ip"`
	APIKeyUsed    string  `json:"api_key_used"`
	AuthTokenName string  `json:"auth_token_name"` // API 令牌名称
}

// TraceStats 追踪统计信息
type TraceStats struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Error   int `json:"error"`
}

// TraceStore 请求追踪存储（独立 SQLite 数据库）
type TraceStore struct {
	db *sql.DB
}

// NewTraceStore 创建独立的追踪存储
// 数据库路径：data/debug_traces.db
func NewTraceStore(dbPath string) (*TraceStore, error) {
	// 创建数据目录
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	// 打开独立的 SQLite 数据库
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_fk=1&_pragma=journal_mode=WAL", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开追踪数据库失败: %w", err)
	}

	// 单连接模式（与主库一致，避免并发写入问题）
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// 执行迁移
	if err := migrateTracesDB(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("追踪数据库迁移失败: %w", err)
	}

	log.Printf("[INFO] 追踪数据库已初始化: %s", dbPath)
	return &TraceStore{db: db}, nil
}

// migrateTracesDB 创建追踪表
func migrateTracesDB(ctx context.Context, db *sql.DB) error {
	createSQL := `
CREATE TABLE IF NOT EXISTS traces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    time BIGINT NOT NULL,
    channel_id INT NOT NULL DEFAULT 0,
    channel_name VARCHAR(191) NOT NULL DEFAULT '',
    channel_type VARCHAR(64) NOT NULL DEFAULT '',
    model VARCHAR(191) NOT NULL DEFAULT '',
    request_path VARCHAR(255) NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    duration DOUBLE NOT NULL DEFAULT 0.0,
    is_streaming TINYINT NOT NULL DEFAULT 0,
    is_test TINYINT NOT NULL DEFAULT 0,
    input_tokens INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    request_body TEXT,
    response_body TEXT,
    client_ip VARCHAR(45) NOT NULL DEFAULT '',
    api_key_used VARCHAR(191) NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_traces_time ON traces(time DESC);
`
	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		return err
	}

	// 迁移：为旧表添加新字段（如果不存在）
	_, _ = db.ExecContext(ctx, "ALTER TABLE traces ADD COLUMN is_test TINYINT NOT NULL DEFAULT 0")
	_, _ = db.ExecContext(ctx, "ALTER TABLE traces ADD COLUMN request_path VARCHAR(255) NOT NULL DEFAULT ''")
	_, _ = db.ExecContext(ctx, "ALTER TABLE traces ADD COLUMN input_tokens INT NOT NULL DEFAULT 0")
	_, _ = db.ExecContext(ctx, "ALTER TABLE traces ADD COLUMN output_tokens INT NOT NULL DEFAULT 0")

	return nil
}

// Save 保存追踪记录
func (s *TraceStore) Save(ctx context.Context, t *Trace) (int64, error) {
	query := `
INSERT INTO traces (time, channel_id, channel_name, channel_type, model, request_path, status_code, duration, is_streaming, is_test, input_tokens, output_tokens, request_body, response_body, client_ip, api_key_used)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`
	result, err := s.db.ExecContext(ctx, query,
		t.Time, t.ChannelID, t.ChannelName, t.ChannelType, t.Model, t.RequestPath,
		t.StatusCode, t.Duration, t.IsStreaming, t.IsTest, t.InputTokens, t.OutputTokens,
		t.RequestBody, t.ResponseBody, t.ClientIP, t.APIKeyUsed,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// List 获取追踪记录列表（按时间倒序，不含请求体/响应体）
func (s *TraceStore) List(ctx context.Context, limit int) ([]*TraceListItem, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	query := `
SELECT id, time, channel_id, channel_name, channel_type, model, request_path, status_code, duration, is_streaming, is_test, input_tokens, output_tokens, client_ip, api_key_used
FROM traces
ORDER BY time DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*TraceListItem
	for rows.Next() {
		item := &TraceListItem{}
		var isStreaming, isTest int
		if err := rows.Scan(
			&item.ID, &item.Time, &item.ChannelID, &item.ChannelName, &item.ChannelType,
			&item.Model, &item.RequestPath, &item.StatusCode, &item.Duration, &isStreaming, &isTest,
			&item.InputTokens, &item.OutputTokens, &item.ClientIP, &item.APIKeyUsed,
		); err != nil {
			return nil, err
		}
		item.IsStreaming = isStreaming == 1
		item.IsTest = isTest == 1
		items = append(items, item)
	}

	return items, rows.Err()
}

// Get 获取单条追踪记录详情（含请求体/响应体）
func (s *TraceStore) Get(ctx context.Context, id int64) (*Trace, error) {
	query := `
SELECT id, time, channel_id, channel_name, channel_type, model, request_path, status_code, duration, is_streaming, is_test, input_tokens, output_tokens, request_body, response_body, client_ip, api_key_used
FROM traces
WHERE id = ?
`
	trace := &Trace{}
	var isStreaming, isTest int
	var requestBody, responseBody sql.NullString

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&trace.ID, &trace.Time, &trace.ChannelID, &trace.ChannelName, &trace.ChannelType,
		&trace.Model, &trace.RequestPath, &trace.StatusCode, &trace.Duration, &isStreaming, &isTest,
		&trace.InputTokens, &trace.OutputTokens, &requestBody, &responseBody, &trace.ClientIP, &trace.APIKeyUsed,
	)
	if err != nil {
		return nil, err
	}

	trace.IsStreaming = isStreaming == 1
	trace.IsTest = isTest == 1
	trace.RequestBody = requestBody.String
	trace.ResponseBody = responseBody.String

	return trace, nil
}

// Clear 清空所有追踪记录
func (s *TraceStore) Clear(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM traces")
	if err != nil {
		return err
	}
	// 回收空间
	_, _ = s.db.ExecContext(ctx, "VACUUM")
	return nil
}

// Count 获取追踪记录总数
func (s *TraceStore) Count(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM traces").Scan(&count)
	return count, err
}

// Close 关闭数据库连接
func (s *TraceStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Stats 获取追踪统计信息
func (s *TraceStore) Stats(ctx context.Context) (*TraceStats, error) {
	query := `
SELECT
    COUNT(*) as total,
    SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END) as success,
    SUM(CASE WHEN status_code >= 400 OR status_code = 0 THEN 1 ELSE 0 END) as error
FROM traces
`
	stats := &TraceStats{}
	err := s.db.QueryRowContext(ctx, query).Scan(&stats.Total, &stats.Success, &stats.Error)
	if err != nil {
		return nil, err
	}
	return stats, nil
}
