package storage

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsureLogsRequestTypeSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE logs (
			id INTEGER PRIMARY KEY,
			model TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO logs (model) VALUES ('gpt-5.6-sol');
	`); err != nil {
		t.Fatal(err)
	}

	if err := ensureLogsRequestTypeSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}
	// 增量迁移必须可重复执行，避免服务重启时重复加列。
	if err := ensureLogsRequestTypeSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}

	var requestType string
	if err := db.QueryRowContext(ctx, "SELECT request_type FROM logs WHERE id = 1").Scan(&requestType); err != nil {
		t.Fatal(err)
	}
	if requestType != "" {
		t.Fatalf("历史日志 request_type = %q, want empty string", requestType)
	}
}
