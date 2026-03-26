package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/importer"
	"github.com/thingzio/devpulse/pkg/logging"
	"github.com/thingzio/devpulse/pkg/server"
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

	store, err := openStore()
	if err != nil {
		stop()
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}

	db := store.DB()

	if os.Getenv("PORT") != "" {
		err = server.Run(ctx, db)
	} else {
		err = importer.Run(ctx, db)
	}

	store.Close()
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
