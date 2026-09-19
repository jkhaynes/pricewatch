package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
)

const usage = `usage:
  pricewatch import [flags] <export.csv>
  pricewatch run    [flags]
  pricewatch site   [flags]`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(context.Background(), os.Args[1:], os.Stdout, log); err != nil {
		log.Error("pricewatch failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer, log *slog.Logger) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "import":
		return cmdImport(ctx, args[1:], out, log)
	case "run":
		return cmdRun(ctx, args[1:], out, log)
	case "site":
		return cmdSite(ctx, args[1:], out, log)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}
