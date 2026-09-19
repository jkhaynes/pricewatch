package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func openTest(t *testing.T) *SQLite {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func row(name, number, variant string) card.Row {
	return card.Row{Region: "International", Name: name, Number: number, Expansion: "EX Ruby & Sapphire",
		Variant: variant, Language: "English", Quantity: 1}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		s.Close()
	}
}

func TestReplaceCollectionReplaces(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.ReplaceCollection(ctx, []card.Row{row("Mudkip", "59/109", "Normal"), row("Mudkip", "59/109", "Reverse Holo")}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceCollection(ctx, []card.Row{row("Torchic", "73/109", "Normal")}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("collection has %d rows, want 1", n)
	}
}

func TestMappingUpsertAndRead(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	key := row("Mudkip", "59/109", "Normal").Key()

	if _, ok, err := s.Mapping(ctx, "pokewallet", key); err != nil || ok {
		t.Fatalf("Mapping before put: ok=%v err=%v", ok, err)
	}
	steps := []card.Mapping{
		{Key: key, Source: "pokewallet", Status: card.StatusUnmatched, Reason: "unknown expansion"},
		{Key: key, Source: "pokewallet", SourceCardID: "pk_59", Variant: card.VariantNormal, Status: card.StatusResolved},
	}
	for _, want := range steps {
		if err := s.PutMapping(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Mapping(ctx, "pokewallet", key)
		if err != nil || !ok || got != want {
			t.Errorf("Mapping = %+v ok=%v err=%v, want %+v", got, ok, err, want)
		}
	}
}

func TestMappingsAreScopedBySource(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	key := row("Mudkip", "59/109", "Normal").Key()
	pw := card.Mapping{Key: key, Source: "pokewallet", SourceCardID: "pk_59", Variant: card.VariantNormal, Status: card.StatusResolved}
	td := card.Mapping{Key: key, Source: "tcgdex", Status: card.StatusUnmatched, Reason: "not priced"}
	for _, m := range []card.Mapping{pw, td} {
		if err := s.PutMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []card.Mapping{pw, td} {
		if got, _, _ := s.Mapping(ctx, want.Source, key); got != want {
			t.Errorf("Mapping(%s) = %+v, want %+v", want.Source, got, want)
		}
	}
}

func TestUnresolvedAndCountsOnlyCoverCurrentCollection(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	a, b, gone := row("A", "1/109", "Normal"), row("B", "2/109", "Normal"), row("Gone", "3/109", "Normal")
	if err := s.ReplaceCollection(ctx, []card.Row{a, b}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []card.Mapping{
		{Key: a.Key(), Source: "pokewallet", SourceCardID: "pk_1", Variant: card.VariantNormal, Status: card.StatusResolved},
		{Key: b.Key(), Source: "pokewallet", Status: card.StatusAmbiguous, Reason: "two sets"},
		{Key: gone.Key(), Source: "pokewallet", Status: card.StatusUnmatched, Reason: "not in collection any more"},
		{Key: a.Key(), Source: "tcgdex", Status: card.StatusUnmatched, Reason: "other source, not counted"},
	} {
		if err := s.PutMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	un, err := s.Unresolved(ctx, "pokewallet")
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 1 || un[0].Key != b.Key() || un[0].Reason != "two sets" {
		t.Errorf("Unresolved = %+v", un)
	}

	counts, err := s.MappingCounts(ctx, "pokewallet")
	if err != nil {
		t.Fatal(err)
	}
	if counts[card.StatusResolved] != 1 || counts[card.StatusAmbiguous] != 1 || counts[card.StatusUnmatched] != 0 {
		t.Errorf("counts = %v", counts)
	}
}

// The scheduled job commits pricewatch.db and nothing else (DD-13), so a clean
// Close must leave every write in the main file, with no WAL left beside it.
func TestClosedDatabaseFileAloneIsComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	m := card.Mapping{Key: "k", Source: "pw", SourceCardID: "1", Variant: card.VariantNormal, Status: card.StatusResolved}
	if err := s.PutMapping(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Errorf("a WAL file is left after Close (stat err = %v)", err)
	}

	// Copy the main file alone, as the job's commit does, and read it back.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok, err := c.Mapping(t.Context(), "pw", "k"); err != nil || !ok {
		t.Errorf("mapping missing from the copied file: ok=%v err=%v", ok, err)
	}
}

// The cloud database was created by phase 1's schema, before runs had a
// requests column. Opening it must add the column and keep the old rows.
func TestOpenAddsRequestsToAnOlderRunsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE runs (id INTEGER PRIMARY KEY, started_at TIMESTAMP NOT NULL, finished_at TIMESTAMP,
			ok_count INTEGER NOT NULL DEFAULT 0, error_count INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO runs (started_at) VALUES ('2026-09-19 10:07:00')`,
	} {
		if _, err := old.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	id, err := s.StartRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(t.Context(), id, 1, 0, 3); err != nil {
		t.Fatal(err)
	}
	var oldReq, newReq int
	if err := s.db.QueryRowContext(t.Context(), `SELECT
		(SELECT requests FROM runs WHERE id = 1), (SELECT requests FROM runs WHERE id = ?)`, id).Scan(&oldReq, &newReq); err != nil {
		t.Fatal(err)
	}
	if oldReq != 0 || newReq != 3 {
		t.Errorf("requests = %d (old run), %d (new run); want 0 and 3", oldReq, newReq)
	}
}
