package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thingzio/devpulse/pkg/admin"
	"github.com/thingzio/devpulse/pkg/logging"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

func main() {
	logging.SetupLogger()

	slog.Info("starting devpulse-admin",
		"version", version,
		"commit", commit,
		"date", date,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := admin.Run(ctx)
	stop()

	if err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
