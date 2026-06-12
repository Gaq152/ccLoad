package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ccLoad/internal/model"
)

// GetAdmin2FA 获取管理员两步验证配置（单行表，无记录返回 nil, nil）
func (s *SQLStore) GetAdmin2FA(ctx context.Context) (*model.Admin2FA, error) {
	var (
		rec           model.Admin2FA
		encrypted     int
		recoveryCodes sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT secret, secret_encrypted, status, recovery_codes, last_used_step, created_at, activated_at
		FROM admin_2fa WHERE id = 1
	`).Scan(&rec.Secret, &encrypted, &rec.Status, &recoveryCodes, &rec.LastUsedStep, &rec.CreatedAt, &rec.ActivatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	rec.SecretEncrypted = encrypted != 0
	if recoveryCodes.Valid && recoveryCodes.String != "" {
		if err := json.Unmarshal([]byte(recoveryCodes.String), &rec.RecoveryCodes); err != nil {
			return nil, fmt.Errorf("unmarshal recovery codes: %w", err)
		}
	}
	return &rec, nil
}

// SaveAdmin2FA 保存管理员两步验证配置（REPLACE 覆盖单行）
func (s *SQLStore) SaveAdmin2FA(ctx context.Context, rec *model.Admin2FA) error {
	codesJSON, err := json.Marshal(rec.RecoveryCodes)
	if err != nil {
		return fmt.Errorf("marshal recovery codes: %w", err)
	}

	encrypted := 0
	if rec.SecretEncrypted {
		encrypted = 1
	}

	_, err = s.db.ExecContext(ctx, `
		REPLACE INTO admin_2fa (id, secret, secret_encrypted, status, recovery_codes, last_used_step, created_at, activated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)
	`, rec.Secret, encrypted, rec.Status, string(codesJSON), rec.LastUsedStep, rec.CreatedAt, rec.ActivatedAt)
	return err
}

// DeleteAdmin2FA 删除管理员两步验证配置（解绑）
func (s *SQLStore) DeleteAdmin2FA(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_2fa WHERE id = 1`)
	return err
}
