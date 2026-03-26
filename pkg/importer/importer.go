package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/thingzio/devpulse/pkg/tenant"
)

// Run iterates active tenants and imports data for each.
func Run(ctx context.Context, db *sql.DB) error {
	start := time.Now()

	tenants, err := tenant.GetActiveTenants(db)
	if err != nil {
		return err
	}

	slog.Info("import worker starting", "tenants", len(tenants))

	var totalErrors int
	for _, t := range tenants {
		if err := ctx.Err(); err != nil {
			return err
		}

		if importErr := importTenant(ctx, t); importErr != nil {
			totalErrors++
			slog.Error("tenant import failed",
				"tenant_id", t.ID,
				"username", t.Username,
				"error", importErr,
			)
			continue
		}
	}

	slog.Info("import worker complete",
		"tenants", len(tenants),
		"errors", totalErrors,
		"duration", time.Since(start).String(),
	)

	return nil
}

func importTenant(_ context.Context, t tenant.ActiveTenant) error {
	slog.Info("importing tenant", "tenant_id", t.ID, "username", t.Username)

	// TODO: full implementation
	// 1. Get installations for this tenant
	// 2. Skip suspended installations
	// 3. Mint installation token per installation
	// 4. Get repos for each installation
	// 5. Check staleness per repo
	// 6. Call existing Store.ImportEvents, ImportRepoMeta, etc.
	// 7. Log sync_summary with tenant context

	return fmt.Errorf("import not yet implemented for tenant %s", t.ID)
}
