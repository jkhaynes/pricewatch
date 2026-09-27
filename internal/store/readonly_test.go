package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// readOnlyCopy writes a database with the given rows, closes it, and reopens
// it read-only, the way the MCP server sees its downloaded copy.
func readOnlyCopy(t *testing.T, rows ...card.Row) *SQLite {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	w, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceCollection(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(t.Context(), path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { ro.Close() })
	return ro
}

func TestOpenReadOnlyRefusesWrites(t *testing.T) {
	ro := readOnlyCopy(t, row("Mudkip", "59/109", "Normal"))
	ctx := t.Context()
	tests := []struct{ name, sql string }{
		{"insert", `INSERT INTO quota (source, day, used) VALUES ('pw', '2026-09-26', 1)`},
		{"update", `UPDATE collection SET quantity = 9`},
		{"delete", `DELETE FROM collection`},
		{"drop", `DROP TABLE collection`},
		{"create", `CREATE TABLE x (y INTEGER)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := ro.Query(ctx, tt.sql, 10); err == nil {
				t.Errorf("%s succeeded on a read-only database", tt.sql)
			}
		})
	}
	// Turning query_only off does not help: the file itself is read-only.
	if _, _, _, err := ro.Query(ctx, `PRAGMA query_only = 0`, 10); err != nil {
		t.Logf("pragma refused too: %v", err)
	}
	if _, _, _, err := ro.Query(ctx, `DELETE FROM collection`, 10); err == nil {
		t.Error("DELETE succeeded after PRAGMA query_only = 0")
	}
	_, rows, _, err := ro.Query(ctx, `SELECT COUNT(*) FROM collection`, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n := rows[0][0].(int64); n != 1 {
		t.Errorf("collection has %d rows after the write attempts, want 1", n)
	}
}

func TestOpenReadOnlyNeedsAnExistingFile(t *testing.T) {
	if _, err := OpenReadOnly(t.Context(), filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("OpenReadOnly on a missing file succeeded")
	}
}

func TestQueryCapsRows(t *testing.T) {
	ro := readOnlyCopy(t, row("A", "1/109", "Normal"), row("B", "2/109", "Normal"), row("C", "3/109", "Normal"))
	tests := []struct {
		name          string
		maxRows       int
		wantRows      int
		wantTruncated bool
	}{
		{"under the cap", 5, 3, false},
		{"exactly the cap", 3, 3, false},
		{"over the cap", 2, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows, truncated, err := ro.Query(t.Context(),
				`SELECT card_name, card_number FROM collection ORDER BY card_name`, tt.maxRows)
			if err != nil {
				t.Fatal(err)
			}
			if len(cols) != 2 || cols[0] != "card_name" || cols[1] != "card_number" {
				t.Errorf("columns = %v", cols)
			}
			if len(rows) != tt.wantRows || truncated != tt.wantTruncated {
				t.Errorf("got %d rows, truncated=%v; want %d, %v", len(rows), truncated, tt.wantRows, tt.wantTruncated)
			}
			if rows[0][0] != "A" {
				t.Errorf("first row = %v, want A first", rows[0])
			}
		})
	}
}

func TestQueryReportsBadSQL(t *testing.T) {
	ro := readOnlyCopy(t)
	if _, _, _, err := ro.Query(t.Context(), `SELEKT 1`, 10); err == nil {
		t.Error("a syntax error returned no error")
	}
}

func TestLastFinished(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	got, err := s.LastFinished(ctx)
	if err != nil || got != nil {
		t.Fatalf("with no runs: got %v, %v; want nil, nil", got, err)
	}
	id, err := s.StartRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, id, 1, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRun(ctx); err != nil { // a later run that never finished
		t.Fatal(err)
	}
	got, err = s.LastFinished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || time.Since(*got) > time.Minute {
		t.Errorf("LastFinished = %v, want the finished run's time", got)
	}
}
