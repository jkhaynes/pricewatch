package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	// One copy per process: Windows cannot replace a file another process's
	// server has open (2026-09-28, Claude Desktop and Claude Code at once).
	dir, _ := os.UserCacheDir()
	want := filepath.Join(dir, "pricewatch", fmt.Sprintf("pricewatch-%d.db", os.Getpid()))
	if remote.Path != want || remote.Fetch == nil {
		t.Errorf("default: path %q, fetch set: %v; want %q", remote.Path, remote.Fetch != nil, want)
	}
}

// Copies left by servers that exited without cleaning up, and the old shared
// copy, are removed; this process's copy and anything that isn't a database stay.
func TestRemoveOtherCopies(t *testing.T) {
	dir := t.TempDir()
	ours := filepath.Join(dir, "pricewatch-1.db")
	for _, name := range []string{"pricewatch-1.db", "pricewatch-2.db", "pricewatch.db", "download-123.db", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeOtherCopies(ours, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{"notes.txt", "pricewatch-1.db"}; !slices.Equal(left, want) {
		t.Errorf("left %q, want %q", left, want)
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
