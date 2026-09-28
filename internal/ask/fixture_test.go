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
		v, _, err := card.ParseVariant(r.Variant)
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
