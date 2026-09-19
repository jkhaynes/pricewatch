package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *SQLite) QuotaUsed(ctx context.Context, source, day string) (int, error) {
	var used int
	err := s.db.QueryRowContext(ctx, `SELECT used FROM quota WHERE source=? AND day=?`, source, day).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("quota %s/%s: %w", source, day, err)
	}
	return used, nil
}

func (s *SQLite) QuotaAdd(ctx context.Context, source, day string, n int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO quota (source, day, used) VALUES (?,?,?)
		ON CONFLICT(source, day) DO UPDATE SET used = used + excluded.used`, source, day, n)
	if err != nil {
		return fmt.Errorf("quota add %s/%s: %w", source, day, err)
	}
	return nil
}

func (s *SQLite) QuotaSet(ctx context.Context, source, day string, used int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO quota (source, day, used) VALUES (?,?,?)
		ON CONFLICT(source, day) DO UPDATE SET used = excluded.used`, source, day, used)
	if err != nil {
		return fmt.Errorf("quota set %s/%s: %w", source, day, err)
	}
	return nil
}
