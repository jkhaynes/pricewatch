package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/resolve"
	"github.com/jkhaynes/pricewatch/internal/source/pokewallet"
	"github.com/jkhaynes/pricewatch/internal/store"
	"github.com/jkhaynes/pricewatch/internal/tcgcollector"
)

type importOpts struct {
	DB, CSV, Overrides, Source string
	Provider                   providerOpts
}

func cmdImport(ctx context.Context, args []string, out io.Writer, log *slog.Logger) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	var o importOpts
	fs.StringVar(&o.DB, "db", "pricewatch.db", "SQLite database path")
	fs.StringVar(&o.Source, "source", pokewallet.Name, "price source")
	fs.StringVar(&o.Overrides, "expansions", "", "optional CSV of expansion,set_id[,number_prefix] overrides")
	fs.StringVar(&o.Provider.BaseURL, "base-url", "", "override the source's API base URL")
	fs.DurationVar(&o.Provider.Timeout, "timeout", 15*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("import needs exactly one CSV path\n%s", usage)
	}
	o.CSV = fs.Arg(0)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	_, err := importCollection(ctx, o, out, log)
	return err
}

func importCollection(ctx context.Context, o importOpts, out io.Writer, log *slog.Logger) (map[card.Status]int, error) {
	f, err := os.Open(o.CSV)
	if err != nil {
		return nil, fmt.Errorf("open export: %w", err)
	}
	defer f.Close()
	rows, rowErrs, err := tcgcollector.Parse(f)
	if err != nil {
		return nil, err
	}
	for _, e := range rowErrs {
		fmt.Fprintf(out, "skipped row: %v\n", e)
	}

	var overrides []resolve.Override
	if o.Overrides != "" {
		of, err := os.Open(o.Overrides)
		if err != nil {
			return nil, fmt.Errorf("open overrides: %w", err)
		}
		defer of.Close()
		if overrides, err = resolve.LoadOverrides(of); err != nil {
			return nil, err
		}
	}

	st, err := store.Open(ctx, o.DB)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	o.Provider.Quota, o.Provider.Log = st, log
	prov, err := newProvider(o.Source, o.Provider)
	if err != nil {
		return nil, err
	}
	if err := st.ReplaceCollection(ctx, rows); err != nil {
		return nil, err
	}

	res := resolve.New(prov.catalog, prov.name, overrides)
	seen := map[string]bool{}
	var decided int
	for _, row := range rows {
		key := row.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		if m, ok, err := st.Mapping(ctx, prov.name, key); err != nil {
			return nil, err
		} else if ok && m.Status == card.StatusResolved {
			continue // resolve once, reuse forever
		}
		m, err := res.Resolve(ctx, row)
		if err != nil {
			hint := "re-run import to continue"
			if errors.Is(err, card.ErrRateLimited) || errors.Is(err, card.ErrQuotaExhausted) {
				hint = "provider limit reached; re-run import later to continue"
			}
			return nil, fmt.Errorf("resolution stopped after %d keys (progress is saved; %s): %w", decided, hint, err)
		}
		if err := st.PutMapping(ctx, m); err != nil {
			return nil, err
		}
		decided++
	}
	log.Info("import finished", "rows", len(rows), "row_errors", len(rowErrs), "distinct", len(seen), "decided_now", decided)

	counts, err := st.MappingCounts(ctx, prov.name)
	if err != nil {
		return nil, err
	}
	unresolved, err := st.Unresolved(ctx, prov.name)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "%s: rows %d, skipped %d, distinct cards %d: resolved %d, ambiguous %d, unmatched %d\n",
		prov.name, len(rows), len(rowErrs), len(seen),
		counts[card.StatusResolved], counts[card.StatusAmbiguous], counts[card.StatusUnmatched])
	if len(unresolved) > 0 {
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "STATUS\tCARD\tREASON")
		for _, m := range unresolved {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Status, m.Key, m.Reason)
		}
		tw.Flush()
	}
	return counts, nil
}
