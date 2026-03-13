package sql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"ccLoad/internal/model"
)

// ListModelPricing 获取所有模型定价
func (s *SQLStore) ListModelPricing(ctx context.Context) ([]*model.ModelPricingEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, model, display_name, channel_type,
		       input_price, output_price, input_price_high, output_price_high,
		       cache_read_multiplier, cache_write_multiplier,
		       created_at, updated_at
		FROM model_pricing
		ORDER BY channel_type, model
	`)
	if err != nil {
		return nil, fmt.Errorf("query model pricing: %w", err)
	}
	defer rows.Close()

	var entries []*model.ModelPricingEntry
	for rows.Next() {
		var e model.ModelPricingEntry
		if err := rows.Scan(
			&e.ID, &e.Model, &e.DisplayName, &e.ChannelType,
			&e.InputPrice, &e.OutputPrice, &e.InputPriceHigh, &e.OutputPriceHigh,
			&e.CacheReadMultiplier, &e.CacheWriteMultiplier,
			&e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan model pricing: %w", err)
		}
		entries = append(entries, &e)
	}

	return entries, rows.Err()
}

// GetModelPricing 获取单个模型定价
func (s *SQLStore) GetModelPricing(ctx context.Context, id int64) (*model.ModelPricingEntry, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, model, display_name, channel_type,
		       input_price, output_price, input_price_high, output_price_high,
		       cache_read_multiplier, cache_write_multiplier,
		       created_at, updated_at
		FROM model_pricing
		WHERE id = ?
	`, id)

	var e model.ModelPricingEntry
	if err := row.Scan(
		&e.ID, &e.Model, &e.DisplayName, &e.ChannelType,
		&e.InputPrice, &e.OutputPrice, &e.InputPriceHigh, &e.OutputPriceHigh,
		&e.CacheReadMultiplier, &e.CacheWriteMultiplier,
		&e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("model pricing not found: id=%d", id)
		}
		return nil, fmt.Errorf("query model pricing: %w", err)
	}

	return &e, nil
}

// CreateModelPricing 创建模型定价
func (s *SQLStore) CreateModelPricing(ctx context.Context, entry *model.ModelPricingEntry) error {
	now := timeToUnix(time.Now())
	entry.CreatedAt = now
	entry.UpdatedAt = now

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO model_pricing (model, display_name, channel_type,
		    input_price, output_price, input_price_high, output_price_high,
		    cache_read_multiplier, cache_write_multiplier,
		    created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.Model, entry.DisplayName, entry.ChannelType,
		entry.InputPrice, entry.OutputPrice, entry.InputPriceHigh, entry.OutputPriceHigh,
		entry.CacheReadMultiplier, entry.CacheWriteMultiplier,
		entry.CreatedAt, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert model pricing: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	entry.ID = id

	return nil
}

// UpdateModelPricing 更新模型定价
func (s *SQLStore) UpdateModelPricing(ctx context.Context, entry *model.ModelPricingEntry) error {
	entry.UpdatedAt = timeToUnix(time.Now())

	result, err := s.db.ExecContext(ctx, `
		UPDATE model_pricing
		SET model = ?, display_name = ?, channel_type = ?,
		    input_price = ?, output_price = ?, input_price_high = ?, output_price_high = ?,
		    cache_read_multiplier = ?, cache_write_multiplier = ?,
		    updated_at = ?
		WHERE id = ?
	`, entry.Model, entry.DisplayName, entry.ChannelType,
		entry.InputPrice, entry.OutputPrice, entry.InputPriceHigh, entry.OutputPriceHigh,
		entry.CacheReadMultiplier, entry.CacheWriteMultiplier,
		entry.UpdatedAt, entry.ID,
	)
	if err != nil {
		return fmt.Errorf("update model pricing: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("model pricing not found: id=%d", entry.ID)
	}

	return nil
}

// DeleteModelPricing 删除模型定价
func (s *SQLStore) DeleteModelPricing(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM model_pricing WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete model pricing: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("model pricing not found: id=%d", id)
	}

	return nil
}

// BatchCreateModelPricing 批量导入模型定价（INSERT OR REPLACE）
// 返回成功插入/替换的条数
func (s *SQLStore) BatchCreateModelPricing(ctx context.Context, entries []*model.ModelPricingEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	now := timeToUnix(time.Now())
	count := 0

	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		// 根据驱动选择不同的 UPSERT 语法
		var query string
		if s.driverName == "mysql" {
			query = `
				INSERT INTO model_pricing (model, display_name, channel_type,
				    input_price, output_price, input_price_high, output_price_high,
				    cache_read_multiplier, cache_write_multiplier,
				    created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE
				    display_name = VALUES(display_name),
				    channel_type = VALUES(channel_type),
				    input_price = VALUES(input_price),
				    output_price = VALUES(output_price),
				    input_price_high = VALUES(input_price_high),
				    output_price_high = VALUES(output_price_high),
				    cache_read_multiplier = VALUES(cache_read_multiplier),
				    cache_write_multiplier = VALUES(cache_write_multiplier),
				    updated_at = VALUES(updated_at)
			`
		} else {
			query = `
				INSERT OR REPLACE INTO model_pricing (model, display_name, channel_type,
				    input_price, output_price, input_price_high, output_price_high,
				    cache_read_multiplier, cache_write_multiplier,
				    created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`
		}

		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("prepare statement: %w", err)
		}
		defer stmt.Close()

		for _, e := range entries {
			e.CreatedAt = now
			e.UpdatedAt = now
			_, err := stmt.ExecContext(ctx,
				e.Model, e.DisplayName, e.ChannelType,
				e.InputPrice, e.OutputPrice, e.InputPriceHigh, e.OutputPriceHigh,
				e.CacheReadMultiplier, e.CacheWriteMultiplier,
				e.CreatedAt, e.UpdatedAt,
			)
			if err != nil {
				return fmt.Errorf("insert model pricing %s: %w", e.Model, err)
			}
			count++
		}

		return nil
	})

	return count, err
}
