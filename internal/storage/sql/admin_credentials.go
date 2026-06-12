package sql

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GetAdminPasswordHash 获取管理员密码bcrypt哈希（单行表，无记录返回 ""）
func (s *SQLStore) GetAdminPasswordHash(ctx context.Context) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT password_hash FROM admin_credentials WHERE id = 1
	`).Scan(&hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return hash, nil
}

// SaveAdminPasswordHash 保存管理员密码bcrypt哈希（REPLACE 覆盖单行）
func (s *SQLStore) SaveAdminPasswordHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `
		REPLACE INTO admin_credentials (id, password_hash, updated_at)
		VALUES (1, ?, ?)
	`, hash, timeToUnix(time.Now()))
	return err
}
