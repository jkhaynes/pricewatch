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
