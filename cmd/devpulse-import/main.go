package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thingzio/devpulse/pkg/importer"
	"github.com/thingzio/devpulse/pkg/logging"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

func main() {
	logging.SetupLogger(version)

	slog.Info("starting devpulse-import",
		"commit", commit,
		"date", date,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := importer.Run(ctx)
	stop()

	if err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
