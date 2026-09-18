package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/source"
)

// fakePokeWallet serves a tiny catalog. Prices and a 429 switch can change between runs.
type fakePokeWallet struct {
	mu          sync.Mutex
	normal, rev float64
	limited     bool // every /cards request returns 429
	cardHits    int
}

func (f *fakePokeWallet) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-API-Key") != "test-key" {
		http.Error(w, `{"error":"Missing API key"}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/sets":
		io.WriteString(w, `{"data":[{"name":"Ruby and Sapphire","set_code":"RS","set_id":"1393","language":"eng"}]}`)
	case r.URL.Path == "/sets/1393":
		io.WriteString(w, `{"cards":[
			{"id":"pk_59","card_info":{"name":"Mudkip - 59/109","card_number":"59/109"}},
			{"id":"pk_60","card_info":{"name":"Numel","card_number":"60/109"}}],
			"pagination":{"page":1,"total_pages":1}}`)
	case strings.HasPrefix(r.URL.Path, "/cards/"):
		f.cardHits++
		if f.limited {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":"Rate limit exceeded","message":"Hourly limit exceeded"}`)
			return
		}
		switch r.URL.Path {
		case "/cards/pk_59":
			json.NewEncoder(w).Encode(map[string]any{"tcgplayer": map[string]any{"prices": []map[string]any{
				{"sub_type_name": "Normal", "market_price": f.normal},
				{"sub_type_name": "Reverse Holofoil", "market_price": f.rev},
			}}})
		case "/cards/pk_60": // Normal only: a Reverse Holo row must fail, not borrow this price
			io.WriteString(w, `{"tcgplayer":{"prices":[{"sub_type_name":"Normal","market_price":0.1}]}}`)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePokeWallet) set(fn func(*fakePokeWallet)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type env struct {
	fake *fakePokeWallet
	prov providerOpts
	db   string
	csv  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("POKEWALLET_API_KEY", "test-key")
	fake := &fakePokeWallet{normal: 8.22, rev: 50.47}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "export.csv")
	body := "TCG region,Card name,Card number,Card number sorting order,Expansion,Rarity,Card variant,Card language,Card condition,Quantity,Price,Total price,Note\n" +
		"International,Mudkip,59/109,59,EX Ruby & Sapphire,Common,Normal,English,Mint,1,$8.00,$8.00,\n" +
		"International,Mudkip,59/109,59,EX Ruby & Sapphire,Common,Normal,English,Near Mint,1,$6.00,$6.00,\n" + // same key, second copy
		"International,Mudkip,59/109,59,EX Ruby & Sapphire,Common,Reverse Holo,English,Mint,1,$50.00,$50.00,\n" +
		"International,Numel,60/109,60,EX Ruby & Sapphire,Common,Reverse Holo,English,Mint,1,,,\n" +
		"International,Mudkip,59/109,59,EX Ruby & Sapphire,Common,Jumbo Size,English,Mint,1,,,\n" // excluded in phase 1
	if err := os.WriteFile(csvPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return &env{
		fake: fake,
		prov: providerOpts{BaseURL: srv.URL, Timeout: time.Second, Limits: &source.Limits{}},
		db:   filepath.Join(dir, "pw.db"),
		csv:  csvPath,
	}
}

func (e *env) writeOverrides(t *testing.T) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(e.csv), "expansions.csv")
	if err := os.WriteFile(p, []byte("expansion,set_id\nEX Ruby & Sapphire,1393\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) importWith(t *testing.T, overrides string) (map[card.Status]int, string) {
	t.Helper()
	var out bytes.Buffer
	counts, err := importCollection(t.Context(), importOpts{DB: e.db, CSV: e.csv, Overrides: overrides,
		Source: "pokewallet", Provider: e.prov}, &out, quiet)
	if err != nil {
		t.Fatal(err)
	}
	return counts, out.String()
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestImportReportsThenOverrideResolves(t *testing.T) {
	e := newEnv(t)

	// PokéWallet calls it "Ruby and Sapphire"; TCG Collector says "EX Ruby & Sapphire".
	counts, out := e.importWith(t, "")
	if counts[card.StatusResolved] != 0 || counts[card.StatusUnmatched] != 4 {
		t.Errorf("first import counts = %v\n%s", counts, out)
	}
	if !strings.Contains(out, `candidates: 1393`) || !strings.Contains(out, "unsupported variant") {
		t.Errorf("report must explain and suggest:\n%s", out)
	}

	// The suggested override line fixes it; only previously failed keys are retried.
	counts, out = e.importWith(t, e.writeOverrides(t))
	if counts[card.StatusResolved] != 3 || counts[card.StatusUnmatched] != 1 {
		t.Errorf("second import counts = %v\n%s", counts, out)
	}
}

func TestUnknownSourceIsAnError(t *testing.T) {
	e := newEnv(t)
	_, err := importCollection(t.Context(), importOpts{DB: e.db, CSV: e.csv, Source: "nope", Provider: e.prov}, io.Discard, quiet)
	if err == nil || !strings.Contains(err.Error(), "pokewallet") {
		t.Fatalf("err = %v, want an error listing known sources", err)
	}
}
