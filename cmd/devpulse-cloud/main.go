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
	urfave "github.com/urfave/cli/v3"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

func main() {
	level := "info"
	if os.Getenv("DEVPULSE_DEBUG") == "true" || os.Getenv("DEVPULSE_DEBUG") == "1" {
		level = "debug"
	}
	logging.SetDefaultJSONLogger(level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	app := &urfave.Command{
		Name:            "devpulse-cloud",
		Version:         fmt.Sprintf("%s (%s - %s)", version, commit, date),
		Usage:           "DevPulse multi-tenant SaaS service",
		HideHelpCommand: true,
		Flags: []urfave.Flag{
			&urfave.StringFlag{
				Name:     "db",
				Usage:    "PostgreSQL connection URI (postgres://...)",
				Sources:  urfave.EnvVars("DATABASE_URL"),
				Required: true,
			},
		},
		Commands: []*urfave.Command{
			serveCmd,
			importWorkerCmd,
		},
		Metadata: map[string]any{},
	}

	if err := app.Run(ctx, os.Args); err != nil {
		stop()
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
	stop()
}

func openSaaSStore(dsn string) (*postgres.Store, error) {
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
