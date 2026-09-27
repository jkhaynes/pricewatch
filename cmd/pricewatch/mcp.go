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
