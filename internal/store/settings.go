package store

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Store) GetSettings(ctx context.Context) ([]byte, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM app_settings WHERE id = 1`).Scan(&data)
	if err != nil {
		return nil, err
	}
	return []byte(data), nil
}

func (s *Store) SaveSettings(ctx context.Context, data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid settings json")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_settings (id, data, updated_at)
		VALUES (1, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			data = excluded.data,
			updated_at = excluded.updated_at
	`, string(data))
	return err
}

func (s *Store) HasSettings(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_settings WHERE id = 1`).Scan(&n)
	return n > 0, err
}
