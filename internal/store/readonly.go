package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// OpenReadOnly opens a database that must never be written: the MCP server's
// downloaded copy (DD-15). mode=ro and query_only make SQLite refuse every
// write, whatever the SQL says. immutable=1 tells SQLite the file cannot change
// underneath it, so it creates no -wal or -shm files that could outlive a swap
// of the file. The schema migrations Open runs are writes, so they are skipped.
func OpenReadOnly(ctx context.Context, path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// sql.Open connects lazily; ping so a missing file fails here, not later.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &SQLite{db: db}, nil
}

// Query runs one statement for the MCP query tool and returns at most maxRows
// rows. truncated reports that more rows were left unread.
func (s *SQLite) Query(ctx context.Context, query string, maxRows int) (cols []string, rows [][]any, truncated bool, err error) {
	rs, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, false, fmt.Errorf("query: %w", err)
	}
	defer rs.Close()
	cols, err = rs.Columns()
	if err != nil {
		return nil, nil, false, fmt.Errorf("columns: %w", err)
	}
	for rs.Next() {
		if len(rows) == maxRows {
			return cols, rows, true, nil
		}
		row := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, nil, false, fmt.Errorf("scan: %w", err)
		}
		rows = append(rows, row)
	}
	if err := rs.Err(); err != nil {
		return nil, nil, false, fmt.Errorf("query: %w", err)
	}
	return cols, rows, false, nil
}

// LastFinished is when the latest finished run ended, which is how fresh the
// data is. It is nil when no run has finished.
func (s *SQLite) LastFinished(ctx context.Context) (*time.Time, error) {
	// ORDER BY, not MAX: an aggregate loses the column's TIMESTAMP type, and
	// the driver then returns a string instead of a time.Time.
	var t time.Time
	err := s.db.QueryRowContext(ctx, `SELECT finished_at FROM runs
		WHERE finished_at IS NOT NULL ORDER BY finished_at DESC LIMIT 1`).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last finished run: %w", err)
	}
	return &t, nil
}
