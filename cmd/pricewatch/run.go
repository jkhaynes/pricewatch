package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/jkhaynes/pricewatch/internal/pipeline"
	"github.com/jkhaynes/pricewatch/internal/source/pokewallet"
	"github.com/jkhaynes/pricewatch/internal/store"
)

type runOpts struct {
	DB, Source      string
	Budget, Workers int
	Provider        providerOpts
}

func cmdRun(ctx context.Context, args []string, out io.Writer, log *slog.Logger) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var o runOpts
	fs.StringVar(&o.DB, "db", "pricewatch.db", "SQLite database path")
	fs.StringVar(&o.Source, "source", pokewallet.Name, "price source")
	fs.IntVar(&o.Budget, "budget", 100, "max requests (source cards) this run")
	fs.IntVar(&o.Workers, "workers", 2, "concurrent workers")
	fs.StringVar(&o.Provider.BaseURL, "base-url", "", "override the source's API base URL")
	fs.DurationVar(&o.Provider.Timeout, "timeout", 15*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
		case <-ctx.Done():
			return
		}
		log.Warn("stopping: finishing in-flight cards. Press Ctrl-C again to abandon them")
		close(stop)
		select {
		case <-sigs:
			log.Warn("abandoning in-flight cards")
			cancel()
		case <-ctx.Done():
		}
	}()

	_, err := priceRun(ctx, stop, o, out, log)
	return err
}

func priceRun(ctx context.Context, stop <-chan struct{}, o runOpts, out io.Writer, log *slog.Logger) (pipeline.Summary, error) {
	st, err := store.Open(ctx, o.DB)
	if err != nil {
		return pipeline.Summary{}, err
	}
	defer st.Close()
	o.Provider.Quota, o.Provider.Log = st, log
	prov, err := newProvider(o.Source, o.Provider)
	if err != nil {
		return pipeline.Summary{}, err
	}

	r := &pipeline.Runner{Store: st, Source: prov.prices, SourceName: prov.name,
		Budget: o.Budget, Workers: o.Workers, Log: log}
	sum, err := r.Run(ctx, stop)
	if err != nil {
		return sum, err
	}

	fmt.Fprintf(out, "run %d (%s): %d requests, %d cards: ok %d, failed %d, abandoned %d, deferred %d\n",
		sum.RunID, prov.name, sum.Requests, sum.Keys, sum.OK, sum.Failed, sum.Abandoned, sum.Deferred)
	if sum.StoppedBy != nil {
		fmt.Fprintf(out, "stopped early: %v\n", sum.StoppedBy)
	}
	if len(sum.NewlyUnresolved) > 0 {
		fmt.Fprintln(out, "\nremoved from rotation (variant could not be priced unambiguously):")
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		for _, m := range sum.NewlyUnresolved {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Status, m.Key, m.Reason)
		}
		tw.Flush()
	}
	if len(sum.Changes) > 0 {
		fmt.Fprintln(out, "\nchanged since each card's previous observation:")
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(tw, "BEFORE\tNOW\tDELTA\t%\tCARD\t")
		for _, c := range sum.Changes {
			fmt.Fprintf(tw, "%.2f\t%.2f\t%+.2f\t%+.1f\t%s\t\n",
				*c.Previous.Market, *c.Current.Market, c.Delta(), c.Percent(), c.CardID)
		}
		tw.Flush()
	}
	return sum, nil
}
