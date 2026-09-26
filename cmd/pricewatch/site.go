package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jkhaynes/pricewatch/internal/priority"
	"github.com/jkhaynes/pricewatch/internal/site"
	"github.com/jkhaynes/pricewatch/internal/source/pokewallet"
	"github.com/jkhaynes/pricewatch/internal/store"
)

type siteOpts struct {
	DB, Source, Out       string
	DailyLimit, RunMinute int
	Now                   func() time.Time // nil: time.Now
}

func cmdSite(ctx context.Context, args []string, out io.Writer, _ *slog.Logger) error {
	fs := flag.NewFlagSet("site", flag.ContinueOnError)
	var o siteOpts
	fs.StringVar(&o.DB, "db", "pricewatch.db", "SQLite database path")
	fs.StringVar(&o.Source, "source", pokewallet.Name, "price source")
	fs.StringVar(&o.Out, "out", "site", "directory to write index.html into")
	fs.IntVar(&o.DailyLimit, "daily-limit", pokewallet.Limits.PerDay, "the source's daily request limit, for the budget section")
	fs.IntVar(&o.RunMinute, "run-minute", 7, "minute past each hour the schedule runs, for the countdown")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return buildSite(ctx, o, out)
}

func buildSite(ctx context.Context, o siteOpts, out io.Writer) (err error) {
	st, err := store.Open(ctx, o.DB)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	d, err := site.Build(ctx, st, site.Options{Source: o.Source, Now: now(), DailyLimit: o.DailyLimit,
		RunMinute: o.RunMinute, Policy: priority.Default})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", o.Out, err)
	}
	path := filepath.Join(o.Out, "index.html")
	if err := writeWith(path, func(w io.Writer) error { return site.Render(w, d) }); err != nil {
		return err
	}
	// The profile card (README embed) is written from the same Data, so the
	// page and the card can never disagree.
	cardPath := filepath.Join(o.Out, "card.svg")
	if err := writeWith(cardPath, func(w io.Writer) error { return site.RenderCard(w, d) }); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s and %s: %d cards priced of %d, %d unresolved\n", path, cardPath, d.Coverage.Priced, d.Coverage.Total, d.UnresolvedTotal)
	return nil
}

// writeWith creates path and hands it to render. The Close error is returned
// too: for a written file it is where a failed flush shows up.
func writeWith(path string, render func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := render(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
