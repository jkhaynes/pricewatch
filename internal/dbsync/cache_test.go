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
