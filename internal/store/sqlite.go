package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/jkhaynes/pricewatch/internal/card"
)

//go:embed schema.sql
var schema string

type SQLite struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*SQLite, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite has one writer; one connection removes lock contention entirely
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := addColumn(ctx, db, "runs", "requests", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLite{db: db}, nil
}

// addColumn adds a column that schema.sql declares to a table an older schema
// created. CREATE TABLE IF NOT EXISTS leaves existing tables untouched.
func addColumn(ctx context.Context, db *sql.DB, table, column, decl string) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return fmt.Errorf("inspect %s: %w", table, err)
	}
	if n > 0 {
		return nil
	}
	// table, column and decl are constants from this package, never input.
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+decl); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) ReplaceCollection(ctx context.Context, rows []card.Row) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM collection`); err != nil {
		return fmt.Errorf("clear collection: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO collection
		(collection_key, tcg_region, card_name, card_number, sort_number, expansion, rarity,
		 variant, language, condition, quantity, tcgc_price, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.Key(), r.Region, r.Name, r.Number, r.SortNumber, r.Expansion,
			r.Rarity, r.Variant, r.Language, r.Condition, r.Quantity, r.Price, r.Note); err != nil {
			return fmt.Errorf("insert %s: %w", r.Key(), err)
		}
	}
	return tx.Commit()
}

// mappingCols is shared by every query that scans a card.Mapping.
const mappingCols = `m.collection_key, m.source, COALESCE(m.source_card_id,''),
	COALESCE(m.variant,''), m.status, COALESCE(m.reason,'')`

func (s *SQLite) PutMapping(ctx context.Context, m card.Mapping) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO card_map
		(collection_key, source, source_card_id, variant, status, reason, resolved_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(collection_key, source) DO UPDATE SET
		  source_card_id=excluded.source_card_id, variant=excluded.variant,
		  status=excluded.status, reason=excluded.reason, resolved_at=excluded.resolved_at`,
		m.Key, m.Source, nullIfEmpty(m.SourceCardID), nullIfEmpty(string(m.Variant)),
		string(m.Status), nullIfEmpty(m.Reason), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("put mapping %s/%s: %w", m.Source, m.Key, err)
	}
	return nil
}

func (s *SQLite) Mapping(ctx context.Context, source, key string) (card.Mapping, bool, error) {
	m, err := scanMapping(s.db.QueryRowContext(ctx, `SELECT `+mappingCols+`
		FROM card_map m WHERE m.collection_key = ? AND m.source = ?`, key, source))
	if errors.Is(err, sql.ErrNoRows) {
		return card.Mapping{}, false, nil
	}
	if err != nil {
		return card.Mapping{}, false, fmt.Errorf("mapping %s/%s: %w", source, key, err)
	}
	return m, true, nil
}

func (s *SQLite) Unresolved(ctx context.Context, source string) ([]card.Mapping, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mappingCols+`
		FROM card_map m
		WHERE m.source = ? AND m.status <> 'resolved'
		  AND EXISTS (SELECT 1 FROM collection c WHERE c.collection_key = m.collection_key)
		ORDER BY m.status, m.collection_key`, source)
	if err != nil {
		return nil, fmt.Errorf("query unresolved: %w", err)
	}
	return collectMappings(rows)
}

func (s *SQLite) MappingCounts(ctx context.Context, source string) (map[card.Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.status, COUNT(*) FROM card_map m
		WHERE m.source = ?
		  AND EXISTS (SELECT 1 FROM collection c WHERE c.collection_key = m.collection_key)
		GROUP BY m.status`, source)
	if err != nil {
		return nil, fmt.Errorf("query counts: %w", err)
	}
	defer rows.Close()
	counts := map[card.Status]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("scan counts: %w", err)
		}
		counts[card.Status(st)] = n
	}
	return counts, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanMapping(sc scanner) (card.Mapping, error) {
	var m card.Mapping
	var variant, st string
	err := sc.Scan(&m.Key, &m.Source, &m.SourceCardID, &variant, &st, &m.Reason)
	m.Variant, m.Status = card.Variant(variant), card.Status(st)
	return m, err
}

func collectMappings(rows *sql.Rows) ([]card.Mapping, error) {
	defer rows.Close()
	var out []card.Mapping
	for rows.Next() {
		m, err := scanMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mapping: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
