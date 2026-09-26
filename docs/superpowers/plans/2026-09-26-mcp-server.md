# MCP Server (FR-16) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver FR-16: `pricewatch mcp`, a local, read-only MCP server over stdio that lets Claude answer plain-English questions about the collection (value, top cards, lookup, price history, movers, pipeline status, and ad hoc SQL).

**Architecture:**
- **`store` gains a read-only open** (`mode=ro&immutable=1&_pragma=query_only(1)`), a raw `Query` for the ad hoc tool, and `LastFinished` for freshness.
- **`site` exports** `Series`, `ByKey` and a new `Moves(window)`, so the MCP server computes movers with exactly the status page's rules.
- **A new `internal/dbsync` package** keeps a local copy of `pricewatch.db` fresh: a generic `Cache[T io.Closer]` downloads it when older than a TTL and hot-swaps the open handle behind a `sync.RWMutex`; `GitHub(...)` is the download function, using the contents API.
- **A new `internal/ask` package** registers seven typed tools on the official MCP Go SDK's server. Every answer is wrapped in `Answer[T]`, which carries `data_as_of` and a `stale` warning.
- **A new subcommand, `pricewatch mcp`,** wires the cache to the server over stdio.

**Tech Stack:** Go 1.27, standard library, `modernc.org/sqlite`, and one new dependency approved in DD-15: `github.com/modelcontextprotocol/go-sdk` v1.8.0.

**Spec:** `docs/superpowers/specs/2026-09-26-mcp-server-design.md`, plus DD-15 in `docs/PRD.md`. Read both first. DD-5 (ambiguity is reported, not guessed), DD-12 (tiers) and DD-13 (the `db` branch) explain the domain rules the tools enforce.

## Global Constraints

- Dependencies: standard library, `golang.org/x/time/rate`, `modernc.org/sqlite`, and `github.com/modelcontextprotocol/go-sdk` v1.8.0 (DD-15). Nothing else is added directly. The SDK's own transitive dependencies are accepted with it.
- TDD for every task: failing test first, run it and see it fail, minimal code, run it and see it pass, then commit. Tests are table-driven wherever there is more than one case.
- Wrap errors with `%w`. Never swallow an error silently.
- `context.Context` is the first parameter of anything that does IO, and it is honoured.
- Interfaces live in the consuming package (`ask.Store` is declared in `ask`, not in `store`).
- **Read-only.** Nothing in this feature writes to the collection database. The server opens it with `store.OpenReadOnly`.
- **Stdout is the MCP protocol.** Nothing in the `mcp` path writes to stdout except the SDK's transport. Logs go to stderr through `slog`.
- **Variants are never merged.** A tool result row is one `collection_key`. `price_history` accepts only a `collection_key`.
- **Unpriced cards are counted, never valued at $0 silently.**
- The database, the export and the token are personal. Never commit them; `*.db` and `*.csv` are gitignored.
- Before committing, run `go test ./...`, `go vet ./...` and `gofmt -l cmd/ internal/`, and make sure all three are clean.
- End every commit message with the `Co-Authored-By` trailer from the session's attribution instructions.

---

## File map

| File | Status | Responsibility |
|---|---|---|
| `internal/store/readonly.go` | create | `OpenReadOnly`, `Query`, `LastFinished` |
| `internal/store/readonly_test.go` | create | their tests |
| `internal/site/movers.go` | modify | export `Series`, `ByKey`; add `Moves`, `Mover.Key` |
| `internal/site/*.go` | modify | mechanical rename `series`→`Series`, `byKey`→`ByKey` |
| `internal/site/movers_test.go` | modify | test for `Moves` |
| `internal/dbsync/cache.go` | create | `Cache[T]`: TTL, download, hot swap |
| `internal/dbsync/cache_test.go` | create | cache tests with a fake fetch and a fake open |
| `internal/dbsync/github.go` | create | `GitHub(...)` download function |
| `internal/dbsync/github_test.go` | create | tests against `httptest.Server` |
| `internal/ask/ask.go` | create | `Store`, `Acquire`, `Answer[T]`, `NewServer`, `add`, shared `Card` |
| `internal/ask/value.go` | create | `collection_value`, `top_cards` |
| `internal/ask/cards.go` | create | `find_cards`, `price_history` |
| `internal/ask/movers.go` | create | `movers` |
| `internal/ask/status.go` | create | `pipeline_status` |
| `internal/ask/query.go` | create | `query` |
| `internal/ask/*_test.go` | create | fixture, per-tool tests, in-memory end-to-end test |
| `cmd/pricewatch/mcp.go` | create | `pricewatch mcp`: flags, cache, server |
| `cmd/pricewatch/mcp_test.go` | create | wiring test over the in-memory transport |
| `cmd/pricewatch/main.go` | modify | `mcp` case and usage line |
| `README.md` | modify | "Asking Claude about the collection" section |

---

### Task 1: store: read-only open, raw query, freshness

**Files:**
- Create: `internal/store/readonly.go`
- Test: `internal/store/readonly_test.go`

**Interfaces:**
- Consumes: existing `store.Open`, `(*SQLite).ReplaceCollection`, `StartRun`, `FinishRun`, test helpers `openTest`, `row` (in `sqlite_test.go`).
- Produces:
  - `func OpenReadOnly(ctx context.Context, path string) (*SQLite, error)`
  - `func (s *SQLite) Query(ctx context.Context, query string, maxRows int) (cols []string, rows [][]any, truncated bool, err error)`
  - `func (s *SQLite) LastFinished(ctx context.Context) (*time.Time, error)`

**Go notes:**
- Why `immutable=1`: a WAL-mode file opened with plain `mode=ro` still gets `-wal` and `-shm` side files. Those could outlive a swap of the main file and be applied to the new copy. `immutable=1` tells SQLite the file cannot change, so it creates neither. This was verified with modernc before planning, and writes are still refused.
- `Query` scans into `[]any` through a parallel slice of pointers (`ptrs[i] = &row[i]`). `database/sql` has no untyped "give me the row" call, so this is the idiom. There's no direct C# equivalent to `IDataReader.GetValues`.

- [x] **Step 1: Write the failing tests**

`internal/store/readonly_test.go`:

```go
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
```

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/store/ -run 'ReadOnly|Query|LastFinished'`
Expected: FAIL to compile with `undefined: OpenReadOnly` (and `ro.Query`, `s.LastFinished`).

- [x] **Step 3: Implement**

`internal/store/readonly.go`:

```go
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
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/store/`
Expected: PASS (all store tests, old and new).

- [x] **Step 5: Commit**

```bash
git add internal/store/readonly.go internal/store/readonly_test.go
git commit -m "store: open a database read-only, run raw queries, report freshness"
```

---

### Task 2: site: export movers for any window

**Files:**
- Modify: every file in `internal/site/` (mechanical rename)
- Modify: `internal/site/movers.go` (add `Moves`, `Mover.Key`; `Movers` calls `Moves`)
- Test: `internal/site/movers_test.go`

**Interfaces:**
- Consumes: existing `move`, `significant`, `newMover`, `byMagnitude`, test helpers `hist`, `step`, `info`, `keys`, `now`, `day`.
- Produces:
  - `type Series []card.Observation` (renamed from `series`)
  - `func ByKey(obs []card.Observation) map[string]Series` (renamed from `byKey`)
  - `func Moves(hist map[string]Series, info map[string]card.Listing, now time.Time, window time.Duration) []Mover`, every significant move, largest first
  - `func (m Mover) Key() string`

**Go notes:** Capitalisation *is* visibility in Go. Renaming `series` to `Series` is what makes the type usable from `ask`. There's no `internal`/`public` keyword. Package `internal/...` directories add a second boundary: only code under this module can import them.

- [x] **Step 1: Rename, mechanically**

```bash
gofmt -r 'series -> Series' -w internal/site
gofmt -r 'byKey -> ByKey' -w internal/site
```

`gofmt -r` rewrites identifiers in the syntax tree, not comments. Fix the two doc comments by hand in `internal/site/movers.go`:

```go
// Series is one key's market observations, oldest first.
type Series []card.Observation

// ByKey groups observations by collection key, each oldest first.
func ByKey(obs []card.Observation) map[string]Series {
```

Run: `go test ./internal/site/`
Expected: PASS. The rename changes no behaviour.

- [x] **Step 2: Write the failing test**

Append to `internal/site/movers_test.go`:

```go
func TestMovesUsesTheWindow(t *testing.T) {
	h := hist(
		step{"week", 10, 8 * day}, step{"week", 15, time.Hour}, // +50%, baseline 8 days old
		step{"day", 20, 2 * day}, step{"day", 30, time.Hour}, // +50%, baseline 2 days old
		step{"fall", 40, 3 * day}, step{"fall", 30, time.Hour}, // -25%, baseline 3 days old
	)
	cards := info("week", "day", "fall")
	tests := []struct {
		name   string
		window time.Duration
		want   []string
	}{
		{"one day: every baseline is old enough", day, []string{"day", "week", "fall"}},
		{"two and a half days: day's baseline is too new", 60 * time.Hour, []string{"week", "fall"}},
		{"seven days: only week's baseline is old enough", 7 * day, []string{"week"}},
		{"thirty days: no baseline is old enough", 30 * day, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keys(Moves(h, cards, now, tt.window)); !slices.Equal(got, tt.want) {
				t.Errorf("Moves = %v, want %v (largest first, ties by key)", got, tt.want)
			}
		})
	}
	if m := Moves(h, cards, now, day); m[0].Key() != "day" {
		t.Errorf("Key() = %q, want day", m[0].Key())
	}
}
```

- [x] **Step 3: Run the test and see it fail**

Run: `go test ./internal/site/ -run TestMovesUsesTheWindow`
Expected: FAIL to compile with `undefined: Moves`.

- [x] **Step 4: Implement**

In `internal/site/movers.go`, replace the body of `Movers` up to the sort, and add `Moves` and `Key`:

```go
// Key is the mover's collection key.
func (m Mover) Key() string { return m.key }

// Moves returns every significant move over window, largest first: each card's
// latest price against its last price at or before now minus window.
func Moves(hist map[string]Series, info map[string]card.Listing, now time.Time, window time.Duration) []Mover {
	var all []Mover
	for key, s := range hist {
		l, ok := info[key]
		if !ok {
			continue
		}
		was, latest, ok := move(s, now.Add(-window))
		if !ok || !significant(*was.Market, *latest.Market) {
			continue
		}
		all = append(all, newMover(key, l, *was.Market, *latest.Market))
	}
	slices.SortFunc(all, byMagnitude)
	return all
}

// Movers returns the week's biggest move as the spotlight, then the next
// risers and the top fallers (DD-14).
func Movers(hist map[string]Series, info map[string]card.Listing, now time.Time) (spot *Mover, rising, falling []Mover) {
	all := Moves(hist, info, now, moveWindow)
	if len(all) == 0 {
		return nil, nil, nil
	}
	top := all[0]
	// ... unchanged from here: the spotlight's History loop, then rising/falling.
```

Keep everything in `Movers` after `top := all[0]` exactly as it was. Delete only the old loop, the empty check and the `slices.SortFunc` call, which now live in `Moves`.

- [x] **Step 5: Run the tests and see them pass**

Run: `go test ./internal/site/ ./internal/store/ ./cmd/...`
Expected: PASS. `TestMovers` still passes, which proves the refactor kept the page's behaviour.

- [x] **Step 6: Commit**

```bash
git add internal/site
git commit -m "site: export movers over any window for the MCP server"
```

---

### Task 3: dbsync: the TTL cache with hot swap

**Files:**
- Create: `internal/dbsync/cache.go`
- Test: `internal/dbsync/cache_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks. The cache is generic, so it does not import `store`, and its tests use a fake.
- Produces:
  - `type Cache[T io.Closer] struct { Path string; TTL time.Duration; Fetch func(ctx context.Context, w io.Writer) error; Open func(ctx context.Context, path string) (T, error); Now func() time.Time; /* unexported state */ }`
  - `func (c *Cache[T]) Acquire(ctx context.Context) (v T, release func(), stale string, err error)`
  - `func (c *Cache[T]) Refresh(ctx context.Context) error`
  - `func (c *Cache[T]) Close() error`

**Go notes:**
- `Cache[T io.Closer]` is a generic type constrained by an interface, like `where T : IDisposable`. It keeps `dbsync` ignorant of SQLite: the real `T` is `*store.SQLite`, and the test's `T` is a fake.
- Exported config fields plus unexported state, with no constructor, is the `http.Server` pattern: fill in the fields, then call methods.
- `sync.RWMutex` is a value embedded in the struct, not a reference, so a `Cache` must never be copied after use. `go vet` checks this. `release` is literally `c.mu.RUnlock`, a method value, which is a bound delegate in C# terms.

- [x] **Step 1: Write the failing tests**

`internal/dbsync/cache_test.go`:

```go
package dbsync

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// copyOf stands in for an open database: what the file held when opened.
type copyOf struct {
	body   string
	closed bool
}

func (c *copyOf) Close() error { c.closed = true; return nil }

func openFake(_ context.Context, path string) (*copyOf, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &copyOf{body: string(b)}, nil
}

// fetchOf writes body, or fails with err, and counts its calls.
func fetchOf(body string, err error, calls *int) func(context.Context, io.Writer) error {
	return func(_ context.Context, w io.Writer) error {
		*calls++
		if err != nil {
			return err
		}
		_, werr := io.WriteString(w, body)
		return werr
	}
}

func writeAged(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestAcquire(t *testing.T) {
	boom := errors.New("github is down")
	tests := []struct {
		name      string
		age       time.Duration // of the existing copy; 0 means there is none
		noFetch   bool          // --db: never download
		fetchErr  error
		wantCalls int
		wantBody  string
		wantStale bool
		wantErr   bool
	}{
		{name: "fresh copy is used without a download", age: 10 * time.Minute, wantBody: "old"},
		{name: "stale copy is replaced", age: 2 * time.Hour, wantCalls: 1, wantBody: "new"},
		{name: "missing copy is downloaded", wantCalls: 1, wantBody: "new"},
		{name: "failed download falls back to the stale copy", age: 2 * time.Hour, fetchErr: boom, wantCalls: 1, wantBody: "old", wantStale: true},
		{name: "failed download with no copy is an error", fetchErr: boom, wantCalls: 1, wantErr: true},
		{name: "no fetch uses the copy however old", age: 48 * time.Hour, noFetch: true, wantBody: "old"},
		{name: "no fetch and no copy is an error", noFetch: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pricewatch.db")
			if tt.age > 0 {
				writeAged(t, path, "old", now.Add(-tt.age))
			}
			calls := 0
			c := &Cache[*copyOf]{Path: path, TTL: time.Hour, Open: openFake, Now: func() time.Time { return now }}
			if !tt.noFetch {
				c.Fetch = fetchOf("new", tt.fetchErr, &calls)
			}
			t.Cleanup(func() { c.Close() })

			v, release, stale, err := c.Acquire(t.Context())
			if calls != tt.wantCalls {
				t.Errorf("fetched %d times, want %d", calls, tt.wantCalls)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer release()
			if v.body != tt.wantBody {
				t.Errorf("opened %q, want %q", v.body, tt.wantBody)
			}
			if (stale != "") != tt.wantStale {
				t.Errorf("stale = %q, want stale: %v", stale, tt.wantStale)
			}
		})
	}
}

func TestFailedDownloadWaitsATTLBeforeRetrying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricewatch.db")
	writeAged(t, path, "old", now.Add(-2*time.Hour))
	clock, calls := now, 0
	c := &Cache[*copyOf]{Path: path, TTL: time.Hour, Open: openFake, Now: func() time.Time { return clock },
		Fetch: fetchOf("new", errors.New("offline"), &calls)}
	t.Cleanup(func() { c.Close() })
	acquire := func() {
		t.Helper()
		_, release, _, err := c.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release()
	}

	acquire() // fails, falls back to the old copy
	clock = now.Add(30 * time.Minute)
	acquire()
	if calls != 1 {
		t.Errorf("retried after 30 minutes: %d fetches, want 1", calls)
	}
	clock = now.Add(61 * time.Minute)
	acquire()
	if calls != 2 {
		t.Errorf("after a full TTL: %d fetches, want 2", calls)
	}
}

func TestRefreshSwapsTheOpenCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricewatch.db")
	writeAged(t, path, "old", now.Add(-10*time.Minute))
	clock, calls := now, 0
	c := &Cache[*copyOf]{Path: path, TTL: time.Hour, Open: openFake, Now: func() time.Time { return clock },
		Fetch: fetchOf("new", nil, &calls)}
	t.Cleanup(func() { c.Close() })

	first, release, _, err := c.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()

	clock = now.Add(time.Hour) // the copy is now 70 minutes old
	second, release, _, err := c.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
	if first.body != "old" || second.body != "new" || !first.closed {
		t.Errorf("first=%+v second=%+v; want old closed, then new", first, second)
	}

	clock = now.Add(time.Hour + 10*time.Minute) // the new copy is 10 minutes old
	if _, release, _, err := c.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if calls != 1 {
		t.Errorf("%d fetches; the new copy's age must come from the cache's clock", calls)
	}
}
```

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/dbsync/`
Expected: FAIL to compile with `undefined: Cache`.

- [x] **Step 3: Implement**

`internal/dbsync/cache.go`:

```go
// Package dbsync keeps a local copy of the collection database fresh for the
// MCP server (DD-15). It downloads a new copy when the old one is older than a
// TTL, and swaps the open handle without pulling the file out from under a
// query that is still reading it.
package dbsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache hands out an open T for a local file, downloading a new copy of the
// file when it is older than TTL. The file's mtime is its age.
type Cache[T io.Closer] struct {
	Path  string                                          // the local copy
	TTL   time.Duration                                   // how long a copy counts as fresh
	Fetch func(ctx context.Context, w io.Writer) error    // writes a new copy; nil means never download
	Open  func(ctx context.Context, path string) (T, error)
	Now   func() time.Time // nil means time.Now

	refreshing sync.Mutex // one refresh at a time
	retryAt    time.Time  // after a failed download, no retry before this; guarded by refreshing

	mu     sync.RWMutex // read-locked by every Acquire until release; write-locked by a swap
	cur    T
	opened bool
	stale  string // why the copy may be out of date; "" when it is fresh
	err    error  // why there is no copy to hand out
}

// Acquire refreshes the copy if it is due, then returns the open value under a
// read lock. The caller must call release when done with v, and a swap waits
// for every release. stale explains why the copy may be out of date.
func (c *Cache[T]) Acquire(ctx context.Context) (v T, release func(), stale string, err error) {
	// Refresh's error is recorded in c.stale or c.err, which are reported below.
	_ = c.Refresh(ctx)
	c.mu.RLock()
	if !c.opened {
		err := c.err
		c.mu.RUnlock()
		return v, nil, "", err
	}
	return c.cur, c.mu.RUnlock, c.stale, nil
}

// Refresh downloads a new copy if the local one is missing or older than TTL,
// unless a download failed less than a TTL ago. It opens the copy if nothing
// is open yet.
func (c *Cache[T]) Refresh(ctx context.Context) error {
	c.refreshing.Lock()
	defer c.refreshing.Unlock()
	now := c.now()
	info, statErr := os.Stat(c.Path)
	have := statErr == nil
	due := !have || now.Sub(info.ModTime()) >= c.TTL
	if c.Fetch == nil || !due || now.Before(c.retryAt) {
		if !have {
			return c.fail(fmt.Errorf("no local copy at %s: %w", c.Path, statErr))
		}
		return c.openIfClosed(ctx)
	}
	tmp, err := c.download(ctx)
	if err != nil {
		c.retryAt = now.Add(c.TTL)
		if !have {
			return c.fail(fmt.Errorf("no local copy, and the download failed: %w", err))
		}
		c.mu.Lock()
		c.stale = fmt.Sprintf("refresh failed, so this is the copy from %s: %v",
			info.ModTime().UTC().Format(time.RFC3339), err)
		c.mu.Unlock()
		return errors.Join(err, c.openIfClosed(ctx))
	}
	c.retryAt = time.Time{}
	return c.swap(ctx, tmp, now)
}

// Close closes the open copy, if any.
func (c *Cache[T]) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return nil
	}
	c.opened = false
	return c.cur.Close()
}

func (c *Cache[T]) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// fail records that there is no copy to hand out.
func (c *Cache[T]) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	return err
}

func (c *Cache[T]) openIfClosed(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opened {
		return nil
	}
	v, err := c.Open(ctx, c.Path)
	if err != nil {
		c.err = fmt.Errorf("open %s: %w", c.Path, err)
		return c.err
	}
	c.cur, c.opened, c.err = v, true, nil
	return nil
}

// download writes a new copy to a temp file beside Path, so the rename that
// replaces Path stays on one volume.
func (c *Cache[T]) download(ctx context.Context) (string, error) {
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "download-*.db")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	err = c.Fetch(ctx, f)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("write %s: %w", f.Name(), cerr)
	}
	if err != nil {
		// Best effort: a leftover temp file is harmless and the next download
		// makes a new one.
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// swap closes the open copy, moves the new one into place and opens it. The
// write lock waits until every tool call holding a read lock has released it.
func (c *Cache[T]) swap(ctx context.Context, tmp string, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var closeErr error
	if c.opened {
		// Closed before the rename: Windows cannot replace a file that is open.
		closeErr = c.cur.Close()
		c.opened = false
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		c.err = fmt.Errorf("replace %s: %w", c.Path, err)
		return errors.Join(closeErr, c.err)
	}
	// Path's mtime is the copy's age, so set it from the clock that judges it.
	if err := os.Chtimes(c.Path, now, now); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("stamp %s: %w", c.Path, err))
	}
	v, err := c.Open(ctx, c.Path)
	if err != nil {
		c.err = fmt.Errorf("open %s: %w", c.Path, err)
		return errors.Join(closeErr, c.err)
	}
	c.cur, c.opened, c.stale, c.err = v, true, "", nil
	return closeErr
}
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/dbsync/ && go vet ./internal/dbsync/`
Expected: PASS, and vet is clean.

- [x] **Step 5: Commit**

```bash
git add internal/dbsync/cache.go internal/dbsync/cache_test.go
git commit -m "dbsync: cache the database for a TTL and hot-swap it on refresh"
```

---

### Task 4: dbsync: download from GitHub

**Files:**
- Create: `internal/dbsync/github.go`
- Test: `internal/dbsync/github_test.go`

**Interfaces:**
- Consumes: the `Fetch` field type from Task 3, `func(ctx context.Context, w io.Writer) error`.
- Produces: `func GitHub(client *http.Client, api, repo, ref, path, token string) func(context.Context, io.Writer) error`

**Go notes:** `GitHub` returns a closure that captures the request's details, the Go equivalent of returning a `Func<CancellationToken, Stream, Task>`. `io.Copy` streams the body straight to the file, so the database is never held in memory.

- [x] **Step 1: Write the failing tests**

`internal/dbsync/github_test.go`:

```go
package dbsync

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHub(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		status   int
		wantBody string
		wantErr  string // substring; "" means success
		wantHits int
	}{
		{name: "downloads the raw file", token: "tok", status: http.StatusOK, wantBody: "SQLite format 3", wantHits: 1},
		{name: "reports an HTTP failure", token: "tok", status: http.StatusUnauthorized, wantErr: "HTTP 401", wantHits: 1},
		{name: "refuses to call without a token", token: "", wantErr: "PRICEWATCH_GITHUB_TOKEN", wantHits: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Path != "/repos/jkhaynes/pricewatch-data/contents/pricewatch.db" || r.URL.Query().Get("ref") != "db" {
					t.Errorf("requested %s", r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "application/vnd.github.raw" {
					t.Errorf("headers = %v", r.Header)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte("SQLite format 3"))
			}))
			defer srv.Close()

			var got bytes.Buffer
			fetch := GitHub(srv.Client(), srv.URL, "jkhaynes/pricewatch-data", "db", "pricewatch.db", tt.token)
			err := fetch(t.Context(), &got)
			if hits != tt.wantHits {
				t.Errorf("%d requests, want %d", hits, tt.wantHits)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "Bearer") {
					t.Errorf("the error leaks the Authorization header: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.wantBody {
				t.Errorf("wrote %q, want %q", got.String(), tt.wantBody)
			}
		})
	}
}

func TestGitHubHonoursCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var got bytes.Buffer
	if err := GitHub(srv.Client(), srv.URL, "o/r", "db", "f.db", "tok")(ctx, &got); err == nil {
		t.Error("a cancelled context still downloaded")
	}
}
```

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/dbsync/ -run GitHub`
Expected: FAIL to compile with `undefined: GitHub`.

- [x] **Step 3: Implement**

`internal/dbsync/github.go`:

```go
package dbsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// GitHub returns a Fetch that downloads path at ref from repo ("owner/name")
// through the GitHub contents API. The raw media type returns the file's bytes
// rather than JSON, for files up to 100 MB (DD-15).
func GitHub(client *http.Client, api, repo, ref, path, token string) func(context.Context, io.Writer) error {
	u := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", api, repo, path, url.QueryEscape(ref))
	return func(ctx context.Context, w io.Writer) error {
		if token == "" {
			return errors.New("no GitHub token: set PRICEWATCH_GITHUB_TOKEN")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github.raw")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("download %s: %w", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			// The body is only there to make the message useful; failing to
			// read it changes nothing about the failure being reported.
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("download %s: HTTP %d: %s", path, resp.StatusCode, bytes.TrimSpace(msg))
		}
		if _, err := io.Copy(w, resp.Body); err != nil {
			return fmt.Errorf("download %s: %w", path, err)
		}
		return nil
	}
}
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/dbsync/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/dbsync/github.go internal/dbsync/github_test.go
git commit -m "dbsync: download the database from the db branch on GitHub"
```

---

### Task 5: ask: the server, the Answer wrapper and collection_value

**Files:**
- Modify: `go.mod`, `go.sum` (add the SDK)
- Create: `internal/ask/ask.go`, `internal/ask/value.go`
- Test: `internal/ask/fixture_test.go`, `internal/ask/value_test.go`, `internal/ask/server_test.go`

**Interfaces:**
- Consumes: `(*store.SQLite).Listings`, `History`, `LastFinished` (Task 1); `site.ByKey`, `site.Series` (Task 2).
- Produces (later tasks rely on all of these):
  - `type Store interface { Listings(...); History(...); LastFinished(...) }`, grown in Tasks 7 and 8
  - `type Acquire func(ctx context.Context) (st Store, release func(), stale string, err error)`
  - `type Answer[T any] struct { DataAsOf *time.Time; Stale string; Result T }`, JSON `data_as_of`, `stale`, `result`
  - `type tools struct { source string; now func() time.Time; queryTimeout time.Duration }`
  - `func NewServer(acq Acquire, source string, now func() time.Time) *mcp.Server`
  - `func add[In, Out any](s *mcp.Server, acq Acquire, name, doc string, fn func(context.Context, Store, In) (Out, error))`
  - `type Card struct { Key, Name, Expansion, Number, Variant string; Quantity int; Status string; Price *float64; PricedAt *time.Time; Value *float64 }`
  - `func (t tools) cards(ctx context.Context, st Store) ([]Card, map[string]site.Series, error)`
  - `func cents(x float64) float64`
  - test helpers: `newStore(t, rows []card.Row, prices []obs) *store.SQLite`, `row(exp, name, number, variant string, qty int) card.Row`, `type obs struct{ key string; price float64; at time.Time }`, `connect(t, st Store, stale string) *mcp.ClientSession`, `call[T any](t, cs, name string, args map[string]any) Answer[T]`, the `wantTools` slice

**Go notes:**
- `mcp.AddTool[In, Out]` generates each tool's JSON input schema from the `In` struct: `json` tags name the fields, `jsonschema` tags describe them, and a field *without* `omitempty` is required. Pointer fields become nullable. This is compile-time generics plus runtime reflection, roughly what model binding and `[Description]` attributes do in ASP.NET.
- `add` is a generic *function*, not a method, because Go methods cannot have their own type parameters.
- `t.collectionValue` passed as a value is a method value: the receiver is bound, like a C# delegate over an instance method.

- [x] **Step 1: Add the dependency**

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.8.0
```

- [x] **Step 2: Write the failing tests**

`internal/ask/fixture_test.go`:

```go
package ask

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var at = time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

// obs is one market price for a collection key at a time.
type obs struct {
	key   string
	price float64
	at    time.Time
}

func row(exp, name, number, variant string, qty int) card.Row {
	return card.Row{Region: "International", Name: name, Number: number, Expansion: exp,
		Variant: variant, Language: "English", Quantity: qty}
}

// newStore builds a real database: rows resolved at source "pw", and each
// price saved in its own finished run (a run holds one observation per card).
func newStore(t *testing.T, rows []card.Row, prices []obs) *store.SQLite {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "ask.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ReplaceCollection(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		v, err := card.ParseVariant(r.Variant)
		if err != nil {
			t.Fatal(err)
		}
		m := card.Mapping{Key: r.Key(), Source: "pw", SourceCardID: "id-" + r.Name, Variant: v, Status: card.StatusResolved}
		if err := st.PutMapping(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range prices {
		run, err := st.StartRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		o := card.Observation{CardID: p.key, Source: "pw", Price: card.Price{Market: &p.price}, ObservedAt: p.at}
		if err := st.Save(ctx, run, o); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishRun(ctx, run, 1, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// The shared collection most tests use. Mudkip is owned as two prints whose
// prices must never be merged; Treecko has never been priced.
var (
	mudkip    = row("EX Ruby & Sapphire", "Mudkip", "59/109", "Normal", 2)
	mudkipRev = row("EX Ruby & Sapphire", "Mudkip", "59/109", "Reverse Holo", 1)
	treecko   = row("Unseen Forces", "Treecko", "80/115", "Normal", 1)
	tropius   = row("Unseen Forces", "Tropius", "23/115", "Normal", 3)
)

func collection(t *testing.T) *store.SQLite {
	return newStore(t, []card.Row{mudkip, mudkipRev, treecko, tropius}, []obs{
		{mudkip.Key(), 1.00, at.Add(-24 * time.Hour)},
		{mudkip.Key(), 1.50, at}, // latest: 1.50 x 2 = 3.00
		{mudkipRev.Key(), 4.00, at},
		{tropius.Key(), 0.06, at}, // 0.06 x 3 = 0.18
	})
}

func testTools() tools {
	return tools{source: "pw", now: func() time.Time { return at }, queryTimeout: 10 * time.Second}
}

// connect serves NewServer over an in-memory transport and returns a client.
func connect(t *testing.T, st Store, stale string) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	acq := func(context.Context) (Store, func(), string, error) { return st, func() {}, stale, nil }
	ct, sst := mcp.NewInMemoryTransports()
	if _, err := NewServer(acq, "pw", func() time.Time { return at }).Connect(ctx, sst, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call runs a tool over the client and decodes its structured answer.
func call[T any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) Answer[T] {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s returned a tool error: %v", name, res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var a Answer[T]
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a
}
```

`internal/ask/value_test.go`:

```go
package ask

import (
	"slices"
	"testing"
)

func TestCollectionValue(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name         string
		expansion    string
		wantTotal    float64
		wantPriced   int
		wantUnpriced int
		wantExps     []string // largest value first
		wantErr      bool
	}{
		{name: "whole collection", wantTotal: 7.18, wantPriced: 3, wantUnpriced: 1,
			wantExps: []string{"EX Ruby & Sapphire", "Unseen Forces"}},
		{name: "one expansion, any case", expansion: "unseen forces", wantTotal: 0.18, wantPriced: 1, wantUnpriced: 1,
			wantExps: []string{"Unseen Forces"}},
		{name: "unknown expansion is an error, not $0", expansion: "Nope", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().collectionValue(t.Context(), st, ValueIn{Expansion: tt.expansion})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Total != tt.wantTotal || got.Priced != tt.wantPriced || got.Unpriced != tt.wantUnpriced {
				t.Errorf("got total %v, %d priced, %d unpriced; want %v, %d, %d",
					got.Total, got.Priced, got.Unpriced, tt.wantTotal, tt.wantPriced, tt.wantUnpriced)
			}
			var exps []string
			for _, e := range got.Expansions {
				exps = append(exps, e.Expansion)
			}
			if !slices.Equal(exps, tt.wantExps) {
				t.Errorf("expansions = %v, want %v", exps, tt.wantExps)
			}
		})
	}
}
```

`internal/ask/server_test.go`:

```go
package ask

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// wantTools is every tool the server offers. Each task that adds a tool adds it here.
var wantTools = []string{"collection_value"}

func TestServerListsEveryTool(t *testing.T) {
	cs := connect(t, collection(t), "")
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(wantTools))
	if !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

func TestAnswersSayHowFreshTheyAre(t *testing.T) {
	cs := connect(t, collection(t), "refresh failed")
	got := call[ValueOut](t, cs, "collection_value", map[string]any{})
	if got.DataAsOf == nil || time.Since(*got.DataAsOf) > time.Minute {
		t.Errorf("data_as_of = %v, want the last run's finish", got.DataAsOf)
	}
	if got.Stale != "refresh failed" || got.Result.Total != 7.18 {
		t.Errorf("answer = %+v", got)
	}
}

func TestNoDatabaseIsAToolError(t *testing.T) {
	acq := func(context.Context) (Store, func(), string, error) {
		return nil, nil, "", errors.New("no local copy, and the download failed")
	}
	ct, sst := mcp.NewInMemoryTransports()
	ctx := t.Context()
	if _, err := NewServer(acq, "pw", time.Now).Connect(ctx, sst, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "collection_value", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("a missing database must be a tool error, not a protocol error: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "download failed") {
		t.Errorf("result = %+v, want an error explaining why", res)
	}
}
```

- [x] **Step 3: Run the tests and see them fail**

Run: `go test ./internal/ask/`
Expected: FAIL to compile with `undefined: tools`, `NewServer`, `Answer`, `ValueIn`, `ValueOut`.

- [x] **Step 4: Implement**

`internal/ask/ask.go`:

```go
// Package ask answers questions about the collection as MCP tools (DD-15).
// Every tool reads through Store, and every answer says how fresh it is.
package ask

import (
	"context"
	"math"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Store is what the tools read. *store.SQLite satisfies it.
type Store interface {
	Listings(ctx context.Context, source string) ([]card.Listing, error)
	History(ctx context.Context, source string) ([]card.Observation, error)
	LastFinished(ctx context.Context) (*time.Time, error)
}

// Acquire hands out an open Store. release must be called when the tool is
// done with it. stale explains why the data may be out of date.
type Acquire func(ctx context.Context) (st Store, release func(), stale string, err error)

// Answer wraps every tool's result with how fresh the data is.
type Answer[T any] struct {
	DataAsOf *time.Time `json:"data_as_of" jsonschema:"when the latest pricing run finished; null if none has"`
	Stale    string     `json:"stale,omitempty" jsonschema:"set when the data could not be refreshed, with the reason"`
	Result   T          `json:"result"`
}

type tools struct {
	source       string
	now          func() time.Time
	queryTimeout time.Duration
}

// NewServer returns the MCP server with every tool registered.
func NewServer(acq Acquire, source string, now func() time.Time) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "pricewatch", Version: "v1"}, nil)
	t := tools{source: source, now: now, queryTimeout: 10 * time.Second}
	add(s, acq, "collection_value", valueDoc, t.collectionValue)
	return s
}

// add registers fn as a tool: it acquires the store, runs fn and wraps the
// result in an Answer. An error from fn becomes a tool error the model can
// read, never a crash.
func add[In, Out any](s *mcp.Server, acq Acquire, name, doc string, fn func(context.Context, Store, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: doc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Answer[Out], error) {
			st, release, stale, err := acq(ctx)
			if err != nil {
				return nil, Answer[Out]{}, err
			}
			defer release()
			out, err := fn(ctx, st, in)
			if err != nil {
				return nil, Answer[Out]{}, err
			}
			asOf, err := st.LastFinished(ctx)
			if err != nil {
				return nil, Answer[Out]{}, err
			}
			return nil, Answer[Out]{DataAsOf: asOf, Stale: stale, Result: out}, nil
		})
}

// Card is one collection key: one exact print, never merged with another
// variant of the same card.
type Card struct {
	Key       string     `json:"collection_key" jsonschema:"identifies this exact print; pass it to price_history"`
	Name      string     `json:"name"`
	Expansion string     `json:"expansion"`
	Number    string     `json:"number"`
	Variant   string     `json:"variant"`
	Quantity  int        `json:"quantity" jsonschema:"copies owned"`
	Status    string     `json:"status" jsonschema:"how the card resolved at the price source: resolved, ambiguous, unmatched or unmapped"`
	Price     *float64   `json:"price" jsonschema:"latest market price in USD; null if never priced"`
	PricedAt  *time.Time `json:"priced_at" jsonschema:"when that price was observed"`
	Value     *float64   `json:"value" jsonschema:"price times quantity"`
}

func newCard(l card.Listing, s site.Series) Card {
	c := Card{Key: l.Key, Name: l.Name, Expansion: l.Expansion, Number: l.Number, Variant: l.VariantLabel,
		Quantity: l.Quantity, Status: string(l.Status)}
	if c.Status == "" {
		c.Status = "unmapped"
	}
	if len(s) > 0 {
		last := s[len(s)-1] // History only returns observations with a market price
		v := cents(*last.Market * float64(l.Quantity))
		c.Price, c.PricedAt, c.Value = last.Market, &last.ObservedAt, &v
	}
	return c
}

// cards reads every collection key with its latest price, plus the full
// history by key for tools that need more than the latest.
func (t tools) cards(ctx context.Context, st Store) ([]Card, map[string]site.Series, error) {
	listings, err := st.Listings(ctx, t.source)
	if err != nil {
		return nil, nil, err
	}
	obs, err := st.History(ctx, t.source)
	if err != nil {
		return nil, nil, err
	}
	hist := site.ByKey(obs)
	out := make([]Card, len(listings))
	for i, l := range listings {
		out[i] = newCard(l, hist[l.Key])
	}
	return out, hist, nil
}

// cents rounds to whole cents, so sums of prices don't show float noise.
func cents(x float64) float64 { return math.Round(x*100) / 100 }
```

`internal/ask/value.go`:

```go
package ask

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
)

const valueDoc = `Total market value of the collection, or of one expansion: each card's latest
market price times the copies owned. Cards never priced are counted as unpriced and left
out of the total, not valued at $0. Includes a per-expansion breakdown, most valuable first.`

type ValueIn struct {
	Expansion string `json:"expansion,omitempty" jsonschema:"only this expansion, by its TCG Collector name, e.g. EX Ruby & Sapphire"`
}

type ValueOut struct {
	Total      float64          `json:"total" jsonschema:"USD, over priced cards only"`
	Priced     int              `json:"priced" jsonschema:"cards (collection keys) with a price"`
	Unpriced   int              `json:"unpriced" jsonschema:"cards never priced, not in the total"`
	Expansions []ExpansionValue `json:"expansions" jsonschema:"most valuable first"`
}

type ExpansionValue struct {
	Expansion string  `json:"expansion"`
	Total     float64 `json:"total"`
	Priced    int     `json:"priced"`
	Unpriced  int     `json:"unpriced"`
}

func (t tools) collectionValue(ctx context.Context, st Store, in ValueIn) (ValueOut, error) {
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return ValueOut{}, err
	}
	var out ValueOut
	byExp := map[string]*ExpansionValue{}
	for _, c := range cards {
		if in.Expansion != "" && !strings.EqualFold(c.Expansion, in.Expansion) {
			continue
		}
		e := byExp[c.Expansion]
		if e == nil {
			e = &ExpansionValue{Expansion: c.Expansion}
			byExp[c.Expansion] = e
		}
		if c.Value == nil {
			out.Unpriced++
			e.Unpriced++
			continue
		}
		out.Total += *c.Value
		out.Priced++
		e.Total += *c.Value
		e.Priced++
	}
	if in.Expansion != "" && len(byExp) == 0 {
		return ValueOut{}, fmt.Errorf("no cards in expansion %q; expansion names are TCG Collector's, e.g. EX Ruby & Sapphire", in.Expansion)
	}
	out.Total = cents(out.Total)
	for _, e := range byExp {
		e.Total = cents(e.Total)
		out.Expansions = append(out.Expansions, *e)
	}
	slices.SortFunc(out.Expansions, func(a, b ExpansionValue) int {
		if c := cmp.Compare(b.Total, a.Total); c != 0 {
			return c
		}
		return cmp.Compare(a.Expansion, b.Expansion)
	})
	return out, nil
}
```

- [x] **Step 5: Run the tests and see them pass**

Run: `go mod tidy && go test ./internal/ask/ && go vet ./...`
Expected: PASS. `go.mod` now lists `github.com/modelcontextprotocol/go-sdk v1.8.0` as a direct requirement.

If the SDK rejects a schema (for example `validating tool output`), read the error, which names the field. The likely cause is a field missing `omitempty` that is legitimately empty. Fix the tag rather than loosening the type.

- [x] **Step 6: Commit**

```bash
git add go.mod go.sum internal/ask
git commit -m "ask: MCP server with freshness-stamped answers and collection_value"
```

---

### Task 6: ask: top_cards, find_cards, price_history

**Files:**
- Modify: `internal/ask/value.go` (add `top_cards`), `internal/ask/ask.go` (register three tools)
- Create: `internal/ask/cards.go`
- Test: `internal/ask/cards_test.go`, `internal/ask/value_test.go`, `internal/ask/server_test.go` (extend `wantTools`)

**Interfaces:**
- Consumes: `tools.cards`, `Card`, `add`, fixture from Task 5; `card.Normalize`.
- Produces: `TopIn`, `FindIn`, `HistoryIn`, `CardsOut{Cards []Card; Truncated bool}`, `HistoryOut{Card Card; Prices []Point}`, `Point{At time.Time; Price float64}`, `func capped(cards []Card, limit int) CardsOut`.

- [x] **Step 1: Write the failing tests**

Append to `internal/ask/value_test.go`:

```go
func TestTopCards(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name          string
		in            TopIn
		want          []string // card names with variant, most valuable unit price first
		wantTruncated bool
		wantErr       bool
	}{
		{name: "default: every priced card", in: TopIn{},
			want: []string{"Mudkip Reverse Holo", "Mudkip Normal", "Tropius Normal"}},
		{name: "limit", in: TopIn{Limit: 1}, want: []string{"Mudkip Reverse Holo"}, wantTruncated: true},
		{name: "one expansion", in: TopIn{Expansion: "Unseen Forces"}, want: []string{"Tropius Normal"}},
		{name: "limit over 100", in: TopIn{Limit: 101}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().topCards(t.Context(), st, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if names := labels(got.Cards); !slices.Equal(names, tt.want) || got.Truncated != tt.wantTruncated {
				t.Errorf("got %v truncated=%v, want %v truncated=%v", names, got.Truncated, tt.want, tt.wantTruncated)
			}
		})
	}
}

func labels(cards []Card) []string {
	var out []string
	for _, c := range cards {
		out = append(out, c.Name+" "+c.Variant)
	}
	return out
}
```

`internal/ask/cards_test.go`:

```go
package ask

import (
	"slices"
	"strings"
	"testing"
)

func TestFindCardsNeverMergesVariants(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name string
		in   FindIn
		want []string
	}{
		{"both prints of a name, any case", FindIn{Name: "MUDKIP"}, []string{"Mudkip Normal", "Mudkip Reverse Holo"}},
		{"part of a name", FindIn{Name: "tro"}, []string{"Tropius Normal"}},
		{"number narrows", FindIn{Name: "mudkip", Number: "59/109"}, []string{"Mudkip Normal", "Mudkip Reverse Holo"}},
		{"wrong number finds nothing", FindIn{Name: "mudkip", Number: "59"}, nil},
		{"expansion narrows", FindIn{Name: "t", Expansion: "unseen forces"}, []string{"Treecko Normal", "Tropius Normal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().findCards(t.Context(), st, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			names := labels(got.Cards)
			slices.Sort(names)
			if !slices.Equal(names, tt.want) {
				t.Errorf("found %v, want %v", names, tt.want)
			}
		})
	}

	got, err := testTools().findCards(t.Context(), st, FindIn{Name: "mudkip"})
	if err != nil {
		t.Fatal(err)
	}
	prices := map[string]float64{}
	for _, c := range got.Cards {
		prices[c.Variant] = *c.Price
	}
	if prices["Normal"] != 1.50 || prices["Reverse Holo"] != 4.00 || got.Cards[0].Key == got.Cards[1].Key {
		t.Errorf("each print must keep its own key and price: %+v", got.Cards)
	}
}

func TestFindCardsNeedsAName(t *testing.T) {
	if _, err := testTools().findCards(t.Context(), collection(t), FindIn{Name: "  "}); err == nil {
		t.Error("a blank name matched every card")
	}
}

func TestUnpricedCardSaysSo(t *testing.T) {
	got, err := testTools().findCards(t.Context(), collection(t), FindIn{Name: "treecko"})
	if err != nil {
		t.Fatal(err)
	}
	if c := got.Cards[0]; c.Price != nil || c.Value != nil || c.Status != "resolved" {
		t.Errorf("treecko = %+v, want resolved with no price", c)
	}
}

func TestPriceHistory(t *testing.T) {
	st := collection(t)
	got, err := testTools().priceHistory(t.Context(), st, HistoryIn{Key: mudkip.Key()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Card.Variant != "Normal" || len(got.Prices) != 2 || got.Prices[0].Price != 1.00 || got.Prices[1].Price != 1.50 {
		t.Errorf("history = %+v, want Normal's two prices, oldest first", got)
	}
	_, err = testTools().priceHistory(t.Context(), st, HistoryIn{Key: "Mudkip"})
	if err == nil || !strings.Contains(err.Error(), "find_cards") {
		t.Errorf("unknown key: err = %v, want a pointer to find_cards", err)
	}
}
```

In `internal/ask/server_test.go`, change `wantTools` to:

```go
var wantTools = []string{"collection_value", "top_cards", "find_cards", "price_history"}
```

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/ask/`
Expected: FAIL to compile with `undefined: TopIn`, `FindIn`, `HistoryIn`.

- [x] **Step 3: Implement**

Append to `internal/ask/value.go`:

```go
const topDoc = `The most valuable cards in the collection by latest market price per copy, most
valuable first. Each row is one exact print (collection_key); Normal and Reverse Holo of the
same card are separate rows. Unpriced cards are left out.`

type TopIn struct {
	Limit     int    `json:"limit,omitempty" jsonschema:"how many cards, 1 to 100; default 20"`
	Expansion string `json:"expansion,omitempty" jsonschema:"only this expansion, by its TCG Collector name"`
}

type CardsOut struct {
	Cards     []Card `json:"cards"`
	Truncated bool   `json:"truncated,omitempty" jsonschema:"more cards matched than were returned"`
}

func (t tools) topCards(ctx context.Context, st Store, in TopIn) (CardsOut, error) {
	limit := in.Limit
	switch {
	case limit == 0:
		limit = 20
	case limit < 0 || limit > 100:
		return CardsOut{}, fmt.Errorf("limit %d: want 1 to 100", limit)
	}
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return CardsOut{}, err
	}
	var out []Card
	for _, c := range cards {
		if c.Price == nil || (in.Expansion != "" && !strings.EqualFold(c.Expansion, in.Expansion)) {
			continue
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Card) int {
		if c := cmp.Compare(*b.Price, *a.Price); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return capped(out, limit), nil
}

// capped returns at most limit cards, and says whether it dropped any.
func capped(cards []Card, limit int) CardsOut {
	if len(cards) > limit {
		return CardsOut{Cards: cards[:limit], Truncated: true}
	}
	return CardsOut{Cards: cards}
}
```

`internal/ask/cards.go`:

```go
package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

const findDoc = `Look up cards in the collection by name, optionally narrowed by exact card number
(as printed, e.g. 59/109) and expansion. Returns every matching print as its own row with its
status, latest price and collection_key. One card number often exists as several prints
(Normal, Reverse Holo, 1st Edition) with very different prices: if more than one row matches,
show them all or ask which print is meant. Never combine their prices.`

const historyDoc = `Every recorded market price for one exact print, oldest first. Takes a
collection_key from find_cards or top_cards, never a card name, so the history is always for
one print.`

// maxFound caps find_cards, so a one-letter search can't flood the context.
const maxFound = 100

type FindIn struct {
	Name      string `json:"name" jsonschema:"part of the card name, any case, e.g. tropius"`
	Number    string `json:"number,omitempty" jsonschema:"the exact card number as printed, e.g. 59/109"`
	Expansion string `json:"expansion,omitempty" jsonschema:"only this expansion, by its TCG Collector name"`
}

type HistoryIn struct {
	Key string `json:"collection_key" jsonschema:"from find_cards or top_cards; names one exact print"`
}

type HistoryOut struct {
	Card   Card    `json:"card"`
	Prices []Point `json:"prices" jsonschema:"oldest first"`
}

type Point struct {
	At    time.Time `json:"at"`
	Price float64   `json:"price"`
}

func (t tools) findCards(ctx context.Context, st Store, in FindIn) (CardsOut, error) {
	name := card.Normalize(in.Name)
	if name == "" {
		return CardsOut{}, errors.New("name is required")
	}
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return CardsOut{}, err
	}
	var out []Card
	for _, c := range cards {
		if !strings.Contains(card.Normalize(c.Name), name) ||
			(in.Number != "" && c.Number != in.Number) ||
			(in.Expansion != "" && !strings.EqualFold(c.Expansion, in.Expansion)) {
			continue
		}
		out = append(out, c)
	}
	return capped(out, maxFound), nil
}

func (t tools) priceHistory(ctx context.Context, st Store, in HistoryIn) (HistoryOut, error) {
	cards, hist, err := t.cards(ctx, st)
	if err != nil {
		return HistoryOut{}, err
	}
	for _, c := range cards {
		if c.Key != in.Key {
			continue
		}
		out := HistoryOut{Card: c}
		for _, o := range hist[c.Key] {
			out.Prices = append(out.Prices, Point{At: o.ObservedAt, Price: *o.Market})
		}
		return out, nil
	}
	return HistoryOut{}, fmt.Errorf("no card with collection_key %q: look it up with find_cards first", in.Key)
}
```

In `NewServer` in `internal/ask/ask.go`, after the `collection_value` line:

```go
	add(s, acq, "top_cards", topDoc, t.topCards)
	add(s, acq, "find_cards", findDoc, t.findCards)
	add(s, acq, "price_history", historyDoc, t.priceHistory)
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/ask/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/ask
git commit -m "ask: top_cards, find_cards and price_history, one row per print"
```

---

### Task 7: ask: movers and pipeline_status

**Files:**
- Create: `internal/ask/movers.go`, `internal/ask/status.go`
- Modify: `internal/ask/ask.go` (grow `Store`, register two tools)
- Test: `internal/ask/movers_test.go`, `internal/ask/status_test.go`, `internal/ask/server_test.go` (extend `wantTools`)

**Interfaces:**
- Consumes: `site.Moves`, `Mover.Key`, `site.ByKey` (Task 2); `site.Groups`, `site.Group` (existing); `(*store.SQLite).MappingCounts`, `Unresolved`, `RunsSince`, `QuotaUsed`, `QuotaAdd` (existing).
- Produces: `MoversIn{Days int}`, `MoversOut{Rising, Falling []Move}`, `Move`, `StatusOut`; `Store` gains four methods.

- [x] **Step 1: Write the failing tests**

`internal/ask/movers_test.go`:

```go
package ask

import (
	"slices"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func TestMovers(t *testing.T) {
	day := 24 * time.Hour
	up := row("S", "Riser", "1/100", "Holo", 1)
	down := row("S", "Faller", "2/100", "Holo", 1)
	recent := row("S", "Recent", "3/100", "Holo", 1)
	st := newStore(t, []card.Row{up, down, recent}, []obs{
		{up.Key(), 10, at.Add(-10 * day)}, {up.Key(), 20, at},            // +100% over 10 days
		{down.Key(), 50, at.Add(-8 * day)}, {down.Key(), 40, at},         // -20% over 8 days
		{recent.Key(), 5, at.Add(-2 * day)}, {recent.Key(), 10, at},      // +100% over 2 days
	})
	tests := []struct {
		name        string
		days        int
		wantRising  []string
		wantFalling []string
		wantErr     bool
	}{
		{name: "default week", wantRising: []string{"Riser"}, wantFalling: []string{"Faller"}},
		{name: "two days sees the recent move too", days: 2, wantRising: []string{"Riser", "Recent"}, wantFalling: []string{"Faller"}},
		{name: "nine days: only the riser has a baseline that old", days: 9, wantRising: []string{"Riser"}},
		{name: "out of range", days: 91, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().movers(t.Context(), st, MoversIn{Days: tt.days})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			// slices.Equal treats nil and empty as equal.
			if r, f := moveNames(got.Rising), moveNames(got.Falling); !slices.Equal(r, tt.wantRising) || !slices.Equal(f, tt.wantFalling) {
				t.Errorf("rising %v falling %v; want %v and %v", r, f, tt.wantRising, tt.wantFalling)
			}
			for _, m := range append(got.Rising, got.Falling...) {
				if m.Key == "" {
					t.Errorf("%s has no collection_key", m.Name)
				}
			}
		})
	}
}

func moveNames(ms []Move) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}
```

Riser and Recent both rise exactly +100% in the two-day case (Recent's baseline sits exactly on the cutoff, which counts). `byMagnitude` breaks the tie by collection key (`region|expansion|number|...`), and Riser's `1/100` sorts before Recent's `3/100`.

`internal/ask/status_test.go`:

```go
package ask

import (
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
)

func TestPipelineStatus(t *testing.T) {
	st := collection(t)
	ctx := t.Context()
	now := time.Now().UTC() // the fixture's runs are stamped with the real clock
	if err := st.PutMapping(ctx, card.Mapping{Key: treecko.Key(), Source: "pw", Status: card.StatusUnmatched,
		Reason: "unknown expansion"}); err != nil {
		t.Fatal(err)
	}
	if err := st.QuotaAdd(ctx, "pw", now.Format(time.DateOnly), 7); err != nil {
		t.Fatal(err)
	}
	tt := testTools()
	tt.now = func() time.Time { return now }

	got, err := tt.pipelineStatus(ctx, st, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Keys != 4 || got.Priced != 3 {
		t.Errorf("keys %d priced %d, want 4 and 3", got.Keys, got.Priced)
	}
	if got.Mappings["resolved"] != 3 || got.Mappings["unmatched"] != 1 {
		t.Errorf("mappings = %v", got.Mappings)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0].Label != "Unknown expansion" || got.Unresolved[0].Count != 1 {
		t.Errorf("unresolved = %+v", got.Unresolved)
	}
	if got.QuotaToday != 7 || got.Runs != 4 || got.Requests != 4 {
		t.Errorf("quota %d, runs %d, requests %d; want 7, 4, 4", got.QuotaToday, got.Runs, got.Requests)
	}
}
```

In `internal/ask/server_test.go`, change `wantTools` to:

```go
var wantTools = []string{"collection_value", "top_cards", "find_cards", "price_history", "movers", "pipeline_status"}
```

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/ask/`
Expected: FAIL to compile with `undefined: MoversIn`, `Move`, and `tt.pipelineStatus`.

- [x] **Step 3: Implement**

Grow `Store` in `internal/ask/ask.go`:

```go
type Store interface {
	Listings(ctx context.Context, source string) ([]card.Listing, error)
	History(ctx context.Context, source string) ([]card.Observation, error)
	LastFinished(ctx context.Context) (*time.Time, error)
	MappingCounts(ctx context.Context, source string) (map[card.Status]int, error)
	Unresolved(ctx context.Context, source string) ([]card.Mapping, error)
	RunsSince(ctx context.Context, since time.Time) ([]card.Run, error)
	QuotaUsed(ctx context.Context, source, day string) (int, error)
}
```

`internal/ask/movers.go`:

```go
package ask

import (
	"context"
	"fmt"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/site"
)

const moversDoc = `Biggest price rises and falls over the last N days (default 7): each card's
latest market price against its last price at least N days old. A card's first price is never
a move. Only cards worth $1 or more that moved at least $0.50 count. Cheap cards are checked
about weekly, so a window under 7 days only sees the cards checked inside it.`

const maxMoves = 10

type MoversIn struct {
	Days int `json:"days,omitempty" jsonschema:"look-back window in days, 1 to 90; default 7"`
}

type MoversOut struct {
	Rising  []Move `json:"rising" jsonschema:"largest rise first, at most 10"`
	Falling []Move `json:"falling" jsonschema:"largest fall first, at most 10"`
}

type Move struct {
	Key       string  `json:"collection_key"`
	Name      string  `json:"name"`
	Expansion string  `json:"expansion"`
	Number    string  `json:"number"`
	Variant   string  `json:"variant"`
	Was       float64 `json:"was" jsonschema:"market price at the start of the window, USD"`
	Now       float64 `json:"now" jsonschema:"latest market price, USD"`
	Percent   float64 `json:"percent"`
}

func (t tools) movers(ctx context.Context, st Store, in MoversIn) (MoversOut, error) {
	days := in.Days
	switch {
	case days == 0:
		days = 7
	case days < 1 || days > 90:
		return MoversOut{}, fmt.Errorf("days %d: want 1 to 90", days)
	}
	listings, err := st.Listings(ctx, t.source)
	if err != nil {
		return MoversOut{}, err
	}
	obs, err := st.History(ctx, t.source)
	if err != nil {
		return MoversOut{}, err
	}
	info := make(map[string]card.Listing, len(listings))
	for _, l := range listings {
		info[l.Key] = l
	}
	var out MoversOut
	for _, m := range site.Moves(site.ByKey(obs), info, t.now(), time.Duration(days)*24*time.Hour) {
		mv := Move{Key: m.Key(), Name: m.Name, Expansion: m.Set, Number: m.Number, Variant: m.Variant,
			Was: m.Was, Now: m.Now, Percent: cents(m.Percent)}
		switch {
		case m.Percent > 0 && len(out.Rising) < maxMoves:
			out.Rising = append(out.Rising, mv)
		case m.Percent < 0 && len(out.Falling) < maxMoves:
			out.Falling = append(out.Falling, mv)
		}
	}
	return out, nil
}
```

`internal/ask/status.go`:

```go
package ask

import (
	"context"
	"time"

	"github.com/jkhaynes/pricewatch/internal/site"
)

const statusDoc = `How the pricing pipeline is doing: how many cards are priced, how cards resolved
at the price source (resolved, ambiguous, unmatched), why unresolved cards failed grouped by
reason, requests spent today against the daily budget, and runs in the last 24 hours.`

type StatusOut struct {
	Keys       int            `json:"keys" jsonschema:"distinct cards (collection keys) in the collection"`
	Priced     int            `json:"priced" jsonschema:"cards with at least one market price"`
	Mappings   map[string]int `json:"mappings" jsonschema:"cards by resolution status; cards missing from every status are not yet resolved"`
	Unresolved []site.Group   `json:"unresolved" jsonschema:"why cards did not resolve, largest group first"`
	QuotaToday int            `json:"quota_used_today" jsonschema:"requests spent today, UTC"`
	Runs       int            `json:"runs_last_24h" jsonschema:"runs that finished in the last 24 hours"`
	Requests   int            `json:"requests_last_24h"`
}

func (t tools) pipelineStatus(ctx context.Context, st Store, _ struct{}) (StatusOut, error) {
	cards, _, err := t.cards(ctx, st)
	if err != nil {
		return StatusOut{}, err
	}
	counts, err := st.MappingCounts(ctx, t.source)
	if err != nil {
		return StatusOut{}, err
	}
	unresolved, err := st.Unresolved(ctx, t.source)
	if err != nil {
		return StatusOut{}, err
	}
	now := t.now().UTC()
	used, err := st.QuotaUsed(ctx, t.source, now.Format(time.DateOnly))
	if err != nil {
		return StatusOut{}, err
	}
	runs, err := st.RunsSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return StatusOut{}, err
	}
	out := StatusOut{Keys: len(cards), Mappings: map[string]int{}, Unresolved: site.Groups(unresolved), QuotaToday: used}
	for _, c := range cards {
		if c.Price != nil {
			out.Priced++
		}
	}
	for status, n := range counts {
		out.Mappings[string(status)] = n
	}
	for _, r := range runs {
		out.Requests += r.Requests
		if r.FinishedAt != nil {
			out.Runs++
		}
	}
	return out, nil
}
```

In `NewServer`, after `price_history`:

```go
	add(s, acq, "movers", moversDoc, t.movers)
	add(s, acq, "pipeline_status", statusDoc, t.pipelineStatus)
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/ask/`
Expected: PASS. 

- [x] **Step 5: Commit**

```bash
git add internal/ask
git commit -m "ask: movers over any window and pipeline_status"
```

---

### Task 8: ask: the read-only query tool

**Files:**
- Create: `internal/ask/query.go`
- Modify: `internal/ask/ask.go` (grow `Store`, register `query` with a doc built from the source)
- Test: `internal/ask/query_test.go`, `internal/ask/server_test.go` (extend `wantTools`)

**Interfaces:**
- Consumes: `(*store.SQLite).Query` (Task 1), `tools.queryTimeout` (Task 5).
- Produces: `QueryIn{SQL string}`, `QueryOut{Columns []string; Rows [][]any; Truncated bool}`, `func queryDoc(source string) string`.

**Go notes:** `context.WithTimeout` derives a child context that is cancelled when the timeout fires or the parent is cancelled, whichever comes first. `defer cancel()` releases its timer. It's the equivalent of a linked `CancellationTokenSource` with `CancelAfter`.

- [x] **Step 1: Write the failing tests**

`internal/ask/query_test.go`:

```go
package ask

import (
	"strings"
	"testing"
	"time"
)

func TestQuery(t *testing.T) {
	st := collection(t)
	tests := []struct {
		name          string
		sql           string
		wantCols      []string
		wantRows      int
		wantTruncated bool
		wantErr       string
	}{
		{name: "select", sql: `SELECT card_name, quantity FROM collection WHERE card_name = 'Mudkip' ORDER BY variant`,
			wantCols: []string{"card_name", "quantity"}, wantRows: 2},
		{name: "capped at 500 rows",
			sql:      `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n LIMIT 600) SELECT x FROM n`,
			wantCols: []string{"x"}, wantRows: 500, wantTruncated: true},
		{name: "bad sql is a readable error", sql: `SELECT nope FROM collection`, wantErr: "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testTools().query(t.Context(), st, QueryIn{SQL: tt.sql})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got.Columns, ",") != strings.Join(tt.wantCols, ",") ||
				len(got.Rows) != tt.wantRows || got.Truncated != tt.wantTruncated {
				t.Errorf("got cols %v, %d rows, truncated %v", got.Columns, len(got.Rows), got.Truncated)
			}
		})
	}
}

func TestQueryTimesOut(t *testing.T) {
	tt := testTools()
	tt.queryTimeout = 50 * time.Millisecond
	start := time.Now()
	_, err := tt.query(t.Context(), collection(t),
		QueryIn{SQL: `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT COUNT(*) FROM n`})
	if err == nil {
		t.Fatal("an endless query returned no error")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v to give up; the timeout was 50ms", time.Since(start))
	}
}

func TestQueryDocNamesTheSource(t *testing.T) {
	if doc := queryDoc("pokewallet"); !strings.Contains(doc, "source = 'pokewallet'") {
		t.Errorf("query doc does not tell the model which source to filter on:\n%s", doc)
	}
}
```

If `TestQueryTimesOut` hangs rather than failing, modernc is not interrupting on context cancellation. **Stop and report this.** Do not work around it: the timeout is a spec requirement.

In `internal/ask/server_test.go`, change `wantTools` to:

```go
var wantTools = []string{"collection_value", "top_cards", "find_cards", "price_history", "movers", "pipeline_status", "query"}
```

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/ask/ -run 'Query|Server'`
Expected: FAIL to compile with `undefined: QueryIn`, `queryDoc`.

- [x] **Step 3: Implement**

Add to `Store` in `internal/ask/ask.go`:

```go
	Query(ctx context.Context, query string, maxRows int) (cols []string, rows [][]any, truncated bool, err error)
```

`internal/ask/query.go`:

```go
package ask

import (
	"context"
	"fmt"
)

const maxQueryRows = 500

// queryDoc is built per source, because every card_map and observations query
// must filter on it.
func queryDoc(source string) string {
	return fmt.Sprintf(`Run one read-only SQLite SELECT against the pricewatch database, for questions the
other tools don't answer. At most 500 rows, 10 second limit. Writes are refused.

Tables:
- collection(collection_key, card_name, card_number, expansion, rarity, variant, language,
  condition, quantity, tcgc_price): one row per line of the TCG Collector export. Several rows
  can share a collection_key (the same print in different conditions); sum quantity over them.
- card_map(collection_key, source, source_card_id, variant, status, reason): how each key
  resolved at a price source. status is resolved, ambiguous or unmatched; reason says why not.
- observations(run_id, card_id, source, market_price, low_price, high_price, observed_at): price
  history in USD. card_id is the collection_key. market_price can be NULL.
- runs(id, started_at, finished_at, ok_count, error_count, requests): pricing runs.
- quota(source, day, used): requests spent per UTC day.

Rules, or the answer will be wrong:
- Always filter card_map and observations on source = '%s'.
- A card's current price is its LATEST observation with a non-NULL market_price, not an average
  or a sum over observations.
- Value is current price times quantity.
- collection_key identifies one exact print. Never add or average prices across different
  collection_keys of the same card: Normal and Reverse Holo prices differ, often by far more
  than any price movement.
- tcgc_price is TCG Collector's price when the export was made, not a current market price.`, source)
}

type QueryIn struct {
	SQL string `json:"sql" jsonschema:"one SQLite SELECT statement"`
}

type QueryOut struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated,omitempty" jsonschema:"more than 500 rows matched; narrow the query"`
}

func (t tools) query(ctx context.Context, st Store, in QueryIn) (QueryOut, error) {
	ctx, cancel := context.WithTimeout(ctx, t.queryTimeout)
	defer cancel()
	cols, rows, truncated, err := st.Query(ctx, in.SQL, maxQueryRows)
	if err != nil {
		return QueryOut{}, err
	}
	return QueryOut{Columns: cols, Rows: rows, Truncated: truncated}, nil
}
```

In `NewServer`, after `pipeline_status`:

```go
	add(s, acq, "query", queryDoc(source), t.query)
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/ask/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/ask
git commit -m "ask: read-only query tool with the schema and its gotchas"
```

---

### Task 9: `pricewatch mcp`, wiring and README

**Files:**
- Create: `cmd/pricewatch/mcp.go`
- Test: `cmd/pricewatch/mcp_test.go`
- Modify: `cmd/pricewatch/main.go`, `README.md`

**Interfaces:**
- Consumes: `dbsync.Cache`, `dbsync.GitHub` (Tasks 3 and 4); `store.OpenReadOnly` (Task 1); `ask.NewServer`, `ask.Store`, `ask.Answer`, `ask.ValueOut` (Tasks 5 to 8); `pokewallet.Name`.
- Produces: `func cmdMCP(ctx context.Context, args []string, out io.Writer, log *slog.Logger) error`, `type mcpOpts`, `func newCache(o mcpOpts, token string) (*dbsync.Cache[*store.SQLite], error)`, `func serveMCP(ctx context.Context, o mcpOpts, token string, t mcp.Transport, log *slog.Logger) error`.

**Go notes:**
- `return cache.Acquire(ctx)` inside a function that returns `(ask.Store, func(), string, error)` compiles even though `Acquire` returns `*store.SQLite`. A multi-value return only needs each value to be *assignable*.
- What Go does not do is covariance in function types: a `func(...) (*store.SQLite, ...)` is not an `ask.Acquire`. So the closure adapter is required. C#'s delegate variance would have hidden this.

- [x] **Step 1: Write the failing tests**

`cmd/pricewatch/mcp_test.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/ask"
	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewCache(t *testing.T) {
	local, err := newCache(mcpOpts{DB: "here.db", TTL: time.Hour}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if local.Path != "here.db" || local.Fetch != nil {
		t.Errorf("--db: path %q, fetch set: %v; want here.db and never download", local.Path, local.Fetch != nil)
	}
	remote, err := newCache(mcpOpts{Repo: "o/r", Branch: "db", TTL: time.Hour}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := os.UserCacheDir()
	if remote.Path != filepath.Join(dir, "pricewatch", "pricewatch.db") || remote.Fetch == nil {
		t.Errorf("default: path %q, fetch set: %v", remote.Path, remote.Fetch != nil)
	}
}

// TestMCPServesALocalDatabase runs the real wiring, read-only store included,
// over an in-memory transport instead of stdio.
func TestMCPServesALocalDatabase(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "pricewatch.db")
	w, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	r := card.Row{Region: "International", Name: "Mudkip", Number: "59/109", Expansion: "EX Ruby & Sapphire",
		Variant: "Normal", Language: "English", Quantity: 2}
	if err := w.ReplaceCollection(ctx, []card.Row{r}); err != nil {
		t.Fatal(err)
	}
	run, err := w.StartRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	price := 1.25
	if err := w.Save(ctx, run, card.Observation{CardID: r.Key(), Source: "pokewallet",
		Price: card.Price{Market: &price}, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := w.FinishRun(ctx, run, 1, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	ct, sst := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		done <- serveMCP(ctx, mcpOpts{DB: path, TTL: time.Hour, Source: "pokewallet"}, "", sst, log)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "collection_value", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var got ask.Answer[ask.ValueOut]
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Result.Total != 2.50 || got.DataAsOf == nil || got.Stale != "" {
		t.Errorf("answer = %+v, want 2.50, fresh", got)
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "query",
		Arguments: map[string]any{"sql": "DELETE FROM collection"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "readonly") {
		t.Errorf("a DELETE through the query tool was not refused: %+v", res)
	}

	cs.Close()
	if err := <-done; err != nil {
		t.Errorf("serveMCP after the client left: %v", err)
	}
}
```

The DELETE assertion looks for SQLite's message, `attempt to write a readonly database`. If the wording differs, match what SQLite actually returns and keep the assertion. The point is that the refusal reaches the model as a tool error.

If `serveMCP` returns a non-nil error when the client disconnects cleanly (the SDK may report the closed connection), assert `errors.Is` against the SDK's sentinel for a closed connection instead. Do not drop the check.

- [x] **Step 2: Run the tests and see them fail**

Run: `go test ./cmd/pricewatch/ -run 'NewCache|MCP'`
Expected: FAIL to compile with `undefined: newCache`, `mcpOpts`, `serveMCP`.

- [x] **Step 3: Implement**

`cmd/pricewatch/mcp.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jkhaynes/pricewatch/internal/ask"
	"github.com/jkhaynes/pricewatch/internal/dbsync"
	"github.com/jkhaynes/pricewatch/internal/source/pokewallet"
	"github.com/jkhaynes/pricewatch/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpOpts struct {
	DB, Repo, Branch, Source string
	TTL                      time.Duration
}

// cmdMCP serves the ask tools over stdio (DD-15). Stdout is the protocol, so
// nothing here writes to out; logs go to stderr.
func cmdMCP(ctx context.Context, args []string, _ io.Writer, log *slog.Logger) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var o mcpOpts
	fs.StringVar(&o.DB, "db", "", "use this local database and never download")
	fs.StringVar(&o.Repo, "repo", "jkhaynes/pricewatch-data", "GitHub repo holding the database")
	fs.StringVar(&o.Branch, "branch", "db", "branch holding pricewatch.db")
	fs.DurationVar(&o.TTL, "ttl", time.Hour, "how long a downloaded copy counts as fresh")
	fs.StringVar(&o.Source, "source", pokewallet.Name, "price source")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return serveMCP(ctx, o, os.Getenv("PRICEWATCH_GITHUB_TOKEN"), &mcp.StdioTransport{}, log)
}

func serveMCP(ctx context.Context, o mcpOpts, token string, t mcp.Transport, log *slog.Logger) (err error) {
	cache, err := newCache(o, token)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cache.Close()) }()
	if err := cache.Refresh(ctx); err != nil {
		log.Warn("database refresh failed; tools will use the cached copy or report why there is none", "err", err)
	}
	acq := func(ctx context.Context) (ask.Store, func(), string, error) { return cache.Acquire(ctx) }
	return ask.NewServer(acq, o.Source, time.Now).Run(ctx, t)
}

// newCache keeps a downloaded copy in the user cache directory, or, with
// --db, uses that file as it is.
func newCache(o mcpOpts, token string) (*dbsync.Cache[*store.SQLite], error) {
	c := &dbsync.Cache[*store.SQLite]{Path: o.DB, TTL: o.TTL, Open: store.OpenReadOnly}
	if o.DB != "" {
		return c, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("find the cache directory: %w", err)
	}
	c.Path = filepath.Join(dir, "pricewatch", "pricewatch.db")
	c.Fetch = dbsync.GitHub(&http.Client{Timeout: 2 * time.Minute}, "https://api.github.com",
		o.Repo, o.Branch, "pricewatch.db", token)
	return c, nil
}
```

`fs.SetOutput(os.Stderr)` matters: by default a `FlagSet` prints usage errors to stderr already, but setting it explicitly documents that nothing in this command may touch stdout.

In `cmd/pricewatch/main.go`, add to `usage`:

```go
  pricewatch mcp    [flags]
```

and to the `switch`:

```go
	case "mcp":
		return cmdMCP(ctx, args[1:], out, log)
```

- [x] **Step 4: Run the tests and see them pass**

Run: `go test ./... && go vet ./... && gofmt -l cmd/ internal/`
Expected: every package PASS, vet clean, gofmt prints nothing.

- [ ] **Step 5: Try it for real**

```bash
go install ./cmd/pricewatch
```

Then register it with Claude Code, using a fine-grained token with **read-only Contents** access on `pricewatch-data` only:

```bash
claude mcp add pricewatch -e PRICEWATCH_GITHUB_TOKEN=<token> -- pricewatch mcp
```

In a new Claude Code session, ask "what's my collection worth?" and "show me every print of Tropius I own". Confirm that `%LocalAppData%\pricewatch\pricewatch.db` now exists. Report what happened. This is a manual check, and the author runs it.

- [x] **Step 6: README**

Add a section to `README.md` after "Status page":

````markdown
## Asking Claude about the collection

`pricewatch mcp` is a local [MCP](https://modelcontextprotocol.io) server (DD-15). Register it
once and Claude can answer questions like "what's my collection worth?", "what moved this
week?" or "which cards do I own two of that are worth over $5?".

It downloads `pricewatch.db` from the `db` branch of `pricewatch-data`, keeps it for an hour
in your user cache directory, and opens it read-only. It needs a fine-grained token with
read-only **Contents** access to that repo:

```
go install ./cmd/pricewatch
claude mcp add pricewatch -e PRICEWATCH_GITHUB_TOKEN=<token> -- pricewatch mcp
```

Flags: `--db <path>` uses a local file and never downloads; `--ttl` changes how long a copy
counts as fresh (default `1h`).

Tools: `collection_value`, `top_cards`, `find_cards`, `price_history`, `movers`,
`pipeline_status`, and `query` for anything else (read-only SQL). Every answer carries
`data_as_of`, and a `stale` warning when the copy could not be refreshed. A card name that
matches several prints comes back as several rows; their prices are never combined.
````

- [x] **Step 7: Commit**

```bash
git add cmd/pricewatch/mcp.go cmd/pricewatch/mcp_test.go cmd/pricewatch/main.go README.md
git commit -m "pricewatch mcp: serve the ask tools over stdio"
```

---

## Spec coverage

| Spec requirement | Task |
|---|---|
| Local stdio server, tools only | 5, 9 |
| Read-only by construction (`mode=ro`, `query_only`) | 1 (plus `immutable=1`, see Task 1's notes), 9 (DELETE refused end to end) |
| Cache in `os.UserCacheDir()/pricewatch/pricewatch.db` | 9 |
| GitHub contents API, raw media type, bearer token, context timeout | 4, 9 (client timeout) |
| mtime is the timestamp; TTL default 1h | 3, 9 |
| Check at startup and on stale tool calls | 3 (`Acquire` refreshes), 9 (`Refresh` at startup) |
| No retry within a TTL after a failure | 3 |
| RWMutex hot swap, download outside the lock | 3 |
| Fallback to cache with `stale`; error with no cache; `--db` never downloads | 3, 5 |
| `data_as_of` on every result | 1 (`LastFinished`), 5 (`Answer`) |
| The seven tools and their inputs | 5, 6, 7, 8 |
| Value = latest price × quantity; unpriced counted | 5 |
| Variants never merged; `price_history` by key only | 6 |
| Movers reuse `site` logic with a window | 2, 7 |
| `query` 500 rows, 10 s, schema and gotchas in its description | 1, 8 |
| Tool failures are tool errors, never crashes | 5 (`TestNoDatabaseIsAToolError`), 9 |
| Stdout is the protocol; logs to stderr | 9 |
| In-memory end-to-end test | 5, 9 |

**Deviation from the spec, deliberate:** `movers` takes `days` (an integer, default 7) instead of a `window` string. Go's `time.ParseDuration` has no `d` unit, and an integer is harder for the model to get wrong than a parsed string.
