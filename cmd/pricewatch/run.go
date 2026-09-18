package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
)

func cmdRun(ctx context.Context, args []string, out io.Writer, log *slog.Logger) error {
	return errors.New("run: not implemented yet")
}
