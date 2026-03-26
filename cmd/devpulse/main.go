package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/logging"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

func main() {
	logging.SetupLogger()

	slog.Info("starting",
		"version", version,
		"commit", commit,
		"date", date,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	var err error
	if os.Getenv("PORT") != "" {
		err = runServe(ctx)
	} else {
		err = runImport(ctx)
	}

	stop()

	if err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func openStore() (*postgres.Store, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	store, err := postgres.New(dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := postgres.RunSaaSMigrations(store.DB()); err != nil {
		store.Close()
		return nil, fmt.Errorf("running saas migrations: %w", err)
	}
	return store, nil
}
