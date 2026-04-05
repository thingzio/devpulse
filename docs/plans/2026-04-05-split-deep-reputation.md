# Split Deep Reputation into Separate Cloud Run Job

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move deep reputation scoring out of the concurrent import job into a dedicated single-task Cloud Run job, eliminating rate limit contention between workers sharing the same GitHub installation token.

**Architecture:** Add `IMPORT_MODE` env var to the existing `devpulse-import` binary. The import job (3 tasks) runs with `IMPORT_MODE=import` skipping deep rep. A new `devpulse-saas-deeprep` job (1 task) runs at :30 with `IMPORT_MODE=reputation`, iterating all eligible repos sequentially. Both jobs use the same container image.

**Tech Stack:** Go, Terraform (GCP Cloud Run jobs, Cloud Scheduler, Monitoring), GitHub Actions

---

### Task 1: Add IMPORT_MODE branching to importer

**Files:**
- Modify: `pkg/importer/importer.go`

**Step 1: Add mode constants and Run() branching**

At the top of `importer.go`, after the imports, add:

```go
// ImportMode controls which phases the import worker runs.
const (
	ModeAll        = "all"        // default: all phases including deep reputation
	ModeImport     = "import"     // fast phases only, skip deep reputation
	ModeReputation = "reputation" // deep reputation only, sequential
)
```

Modify `Run()` to read the mode and branch:

```go
func Run(ctx context.Context) error {
	mode := os.Getenv("IMPORT_MODE")
	if mode == "" {
		mode = ModeAll
	}

	slog.Info("import worker starting", "mode", mode)

	switch mode {
	case ModeReputation:
		return RunDeepReputation(ctx)
	case ModeImport, ModeAll:
		return runImport(ctx, mode)
	default:
		return fmt.Errorf("unknown IMPORT_MODE: %s", mode)
	}
}
```

Rename the existing `Run` body into `runImport(ctx context.Context, mode string) error`. Pass `mode` through to `importRepo`.

**Step 2: Skip deep reputation in import mode**

In `importRepo`, change the deep reputation gate from:

```go
if token != "" && limits.DeepReputation {
```

to:

```go
if token != "" && limits.DeepReputation && mode != ModeImport {
```

Pass `mode` as a parameter: `func importRepo(ctx context.Context, store data.Store, token, org, repo string, llmCfg *data.LLMConfig, planName, mode string) error`

Update `importClaim` to pass `mode` through.

**Step 3: Verify compilation**

Run: `go build ./...`
Expected: clean build

**Step 4: Run tests**

Run: `make test`
Expected: all pass (no behavioral change for `ModeAll`)

**Step 5: Commit**

```
feat: add IMPORT_MODE branching to importer (import/reputation/all)
```

---

### Task 2: Implement RunDeepReputation

**Files:**
- Create: `pkg/importer/reputation.go`

**Step 1: Write RunDeepReputation**

Deep reputation scores are per-user, not per-repo. A contributor appearing in both
a Pro repo and a Free repo gets scored once. The SQL query (`selectLowestReputationUsernamesSQL`)
uses `COALESCE($1, e.org)` — passing nil for org/repo returns all stale users globally.

Token rotation: collect tokens from ALL active installations across all tenants, build a
`ghutil.TokenPool`, and pass `pool.Token` as the `tokenFn`. Each GitHub API call round-robins
across tokens, spreading load across multiple 5000 req/hr budgets. With 3-4 tokens the combined
budget (~15-20k req/hr) far exceeds what 100 users need (~1000 calls), so no single token
reaches its limit.

```go
package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// RunDeepReputation scores all stale contributors globally using round-robin
// token rotation across all active installations. Single task, no parallelism.
func RunDeepReputation(ctx context.Context) error {
	store, err := postgres.NewFromEnv(postgres.ImportPoolConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			slog.Error("closing store", "error", closeErr)
		}
	}()

	db := store.DB()
	start := time.Now()

	ghAppConfig, ghAppErr := tenant.LoadGitHubAppConfig()
	if ghAppErr != nil {
		slog.Warn("github app config not available", "error", ghAppErr)
	}

	executionID := os.Getenv("CLOUD_RUN_EXECUTION")
	if executionID == "" {
		executionID = fmt.Sprintf("local-%d", time.Now().Unix())
	}

	slog.Info("deep reputation worker starting", "execution", executionID)

	pool, err := collectTokenPool(ctx, db, ghAppConfig)
	if err != nil {
		return fmt.Errorf("collecting token pool: %w", err)
	}

	slog.Info("token pool ready", "tokens", pool.Size())

	// Score all stale users globally (nil org/repo = all users via COALESCE).
	res, err := store.ImportDeepReputation(ctx, pool.Token, deepReputationDefaultLimit, 0, nil, nil)
	if err != nil && ghutil.WaitForRateReset(ctx, err) {
		res, err = store.ImportDeepReputation(ctx, pool.Token, deepReputationDefaultLimit, 0, nil, nil)
	}
	if err != nil {
		return fmt.Errorf("deep reputation scoring: %w", err)
	}

	slog.Info("deep reputation worker complete",
		"scored", res.Scored,
		"errors", res.Errors,
		"duration", time.Since(start).String())

	return nil
}

// collectTokenPool mints installation tokens from all active tenants and
// returns a round-robin TokenPool. Falls back to GITHUB_TOKEN env var.
func collectTokenPool(ctx context.Context, db *sql.DB, ghAppConfig *tenant.GitHubAppConfig) (*ghutil.TokenPool, error) {
	// 1. Explicit token (dev/testing)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return ghutil.NewTokenPool(token), nil
	}

	if ghAppConfig == nil {
		return nil, fmt.Errorf("github app config not available")
	}

	// 2. Mint tokens from all active installations across all tenants.
	tenants, err := tenant.GetActiveTenants(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("getting active tenants: %w", err)
	}

	seen := make(map[int64]bool) // dedup by installation ID
	var tokens []string

	for _, tn := range tenants {
		installs, err := tenant.GetActiveInstallations(ctx, db, tn.ID)
		if err != nil || len(installs) == 0 {
			continue
		}
		for _, inst := range installs {
			if seen[inst.ID] {
				continue
			}
			seen[inst.ID] = true

			tok, err := tenant.MintInstallationToken(ctx, ghAppConfig, inst.ID)
			if err != nil {
				slog.Debug("minting token failed",
					"tenant_id", tn.ID,
					"installation_id", inst.ID,
					"error", err)
				continue
			}
			slog.Info("minted installation token",
				"tenant_id", tn.ID,
				"installation_id", inst.ID,
				"login", inst.Login,
				"expires_at", tok.ExpiresAt)
			tokens = append(tokens, tok.Token)
		}
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf("no active installations found")
	}

	rand.Shuffle(len(tokens), func(i, j int) { tokens[i], tokens[j] = tokens[j], tokens[i] })

	return ghutil.NewTokenPool(tokens...), nil
}
```

**Step 2: Verify compilation**

Run: `go build ./...`
Expected: clean build

**Step 3: Run tests**

Run: `make test`
Expected: all pass

**Step 4: Commit**

```
feat: add RunDeepReputation for sequential deep rep scoring
```

---

### Task 3: Add deeprep Cloud Run job to Terraform

**Files:**
- Modify: `infra/saas/cloudrun.tf`
- Modify: `infra/saas/variables.tf`

**Step 1: Add deeprep timeout variable to variables.tf**

After the `import_parallelism` variable:

```hcl
variable "deeprep_timeout" {
  description = "Timeout in seconds for the deep reputation job"
  type        = number
  default     = 7200
}
```

**Step 2: Add deeprep job to cloudrun.tf**

After the `google_cloud_run_v2_job.import` resource (after line 226), add:

```hcl
resource "google_cloud_run_v2_job" "deeprep" {
  name                = "${var.prefix}-deeprep"
  location            = var.region
  project             = var.project_id
  deletion_protection = false

  template {
    task_count  = 1
    parallelism = 1

    template {
      service_account = google_service_account.import.email
      timeout         = "${var.deeprep_timeout}s"

      vpc_access {
        network_interfaces {
          network    = google_compute_network.default.id
          subnetwork = google_compute_subnetwork.default.id
        }
        egress = "PRIVATE_RANGES_ONLY"
      }

      containers {
        image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/thingzio/devpulse-import:latest"

        env {
          name  = "DATABASE_URL"
          value = "host=/cloudsql/${google_sql_database_instance.default.connection_name} dbname=devpulse user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
        }

        env {
          name  = "GITHUB_APP_ID"
          value = var.github_app_id
        }

        env {
          name  = "GITHUB_APP_KEY_PATH"
          value = "/secrets/github-app-key/key.pem"
        }

        env {
          name  = "IMPORT_MODE"
          value = "reputation"
        }

        resources {
          limits = {
            cpu    = "1000m"
            memory = "512Mi"
          }
        }

        volume_mounts {
          name       = "github-app-key"
          mount_path = "/secrets/github-app-key"
        }

        volume_mounts {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
      }

      volumes {
        name = "github-app-key"
        secret {
          secret = google_secret_manager_secret.github_app_key.secret_id
          items {
            version = "latest"
            path    = "key.pem"
          }
        }
      }

      volumes {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [google_sql_database_instance.default.connection_name]
        }
      }
    }
  }

  depends_on = [google_project_service.default]
}
```

**Step 3: Add IMPORT_MODE=import to existing import job**

In the `google_cloud_run_v2_job.import` resource, add a new env block after the ANTHROPIC_MODEL env (after line 185):

```hcl
        env {
          name  = "IMPORT_MODE"
          value = "import"
        }
```

**Step 4: Commit**

```
feat: add deeprep Cloud Run job with IMPORT_MODE env vars
```

---

### Task 4: Add deeprep scheduler and deployer IAM

**Files:**
- Modify: `infra/saas/scheduler.tf`
- Modify: `infra/saas/iam.tf`

**Step 1: Add deeprep scheduler to scheduler.tf**

After the existing `google_cloud_scheduler_job.import` resource:

```hcl
resource "google_cloud_scheduler_job" "deeprep" {
  name     = "${var.prefix}-deeprep-hourly"
  schedule = "30 * * * *"
  project  = var.project_id
  region   = var.region

  http_target {
    http_method = "POST"
    uri         = "https://${var.region}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${var.project_id}/jobs/${google_cloud_run_v2_job.deeprep.name}:run"

    oauth_token {
      service_account_email = google_service_account.deployer.email
    }
  }

  depends_on = [google_project_service.default]
}
```

**Step 2: Add deployer SA permission for deeprep import SA**

The deeprep job reuses `google_service_account.import`, which already has `deployer_import_sa` binding in iam.tf. No IAM change needed — the deployer can already act as the import SA.

**Step 3: Commit**

```
feat: add deeprep hourly scheduler at :30
```

---

### Task 5: Add monitoring for deeprep job

**Files:**
- Modify: `infra/saas/monitoring.tf`

**Step 1: Add log-based metrics**

After the existing `google_logging_metric.import_repo_errors` resource:

```hcl
resource "google_logging_metric" "deeprep_duration" {
  name    = "${var.prefix}-deeprep-duration"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-deeprep\" jsonPayload.msg=\"deep reputation worker complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "s"
  }

  value_extractor = "EXTRACT(jsonPayload.duration)"

  bucket_options {
    explicit_buckets {
      bounds = [60, 300, 600, 1800, 3600, 7200]
    }
  }
}

resource "google_logging_metric" "deeprep_errors" {
  name    = "${var.prefix}-deeprep-errors"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-deeprep\" jsonPayload.msg=\"deep reputation failed\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "username"
      value_type  = "STRING"
      description = "GitHub username"
    }
  }

  label_extractors = {
    "username" = "EXTRACT(jsonPayload.username)"
  }
}
```

**Step 2: Add alert policy for deeprep job failure**

After the existing `google_monitoring_alert_policy.import_repo_errors` resource:

```hcl
resource "google_monitoring_alert_policy" "deeprep_failure" {
  display_name          = "${var.prefix}-deeprep-failure"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Deep reputation job failure"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_job\" AND resource.labels.job_name = \"${google_cloud_run_v2_job.deeprep.name}\" AND metric.type = \"run.googleapis.com/job/completed_execution_count\" AND metric.labels.result = \"failed\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }
}
```

**Step 3: Commit**

```
feat: add monitoring metrics and failure alert for deeprep job
```

---

### Task 6: Add deeprep metrics to admin metrics review

**Files:**
- Modify: `pkg/admin/metrics.go`

**Step 1: Add deeprep field to metricsConfig**

In the `metricsConfig` struct, add after the `job` field:

```go
deeprep  string
```

In `newMetricsConfig()`, add after the `job` assignment:

```go
deeprep:  prefix + "-deeprep",
```

**Step 2: Add two new queries to collectAllMetrics**

After the "Import: Repo Errors" query (line ~219), add:

```go
{
    label:  "Deep Reputation: Job Execution Results",
    filter: fmt.Sprintf(`resource.type="cloud_run_job" AND resource.labels.job_name="%s" AND metric.type="run.googleapis.com/job/completed_execution_count"`, cfg.deeprep),
    params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.result",
},
{
    label:  "Deep Reputation: Errors (by username)",
    filter: fmt.Sprintf(`metric.type="logging.googleapis.com/user/%s-deeprep-errors"`, "devpulse-saas"),
    params: dailyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.username",
},
```

**Step 3: Run tests**

Run: `make test`
Expected: all pass

**Step 4: Commit**

```
feat: add deeprep job metrics to admin metrics review
```

---

### Task 7: Update deploy workflow

**Files:**
- Modify: `.github/workflows/deploy-cloud-run.yaml`

**Step 1: Add deeprep job update**

After line 45 (`--image="${AR}/thingzio/devpulse-import:${TAG}"`), add:

```yaml
          gcloud run jobs update ${{ vars.DEEPREP_JOB_NAME }} \
            --region=${{ vars.REGION }} \
            --image="${AR}/thingzio/devpulse-import:${TAG}"
```

Note: Requires adding `DEEPREP_JOB_NAME=devpulse-saas-deeprep` to the GitHub environment variables for the `saas` environment.

**Step 2: Commit**

```
feat: deploy deeprep job in cloud run deploy workflow
```

---

### Task 8: Update documentation

**Files:**
- Modify: `docs/INFRASTRUCTURE.md`
- Modify: `.claude/CLAUDE.md`

**Step 1: Update INFRASTRUCTURE.md**

Add to the monitoring alerts table:
- `deeprep-failure`: any failed deep rep job execution

Add to the jobs section:
- `devpulse-saas-deeprep`: single-task job for sequential deep reputation scoring, runs at :30

**Step 2: Update CLAUDE.md**

In the Architecture section, update the import worker description to note the mode split.

In the Environment Variables section, add:
- `IMPORT_MODE` — `all` (default), `import` (skip deep rep), `reputation` (deep rep only)

**Step 3: Run qualify**

Run: `make qualify`
Expected: all checks pass

**Step 4: Commit**

```
docs: update infrastructure and project docs for deeprep job split
```

---

### Task 9: Final verification and tag

**Step 1: Run full qualification**

Run: `make qualify`
Expected: all checks pass

**Step 2: Verify terraform plan**

Run (from infra/saas): `terraform plan`
Expected: shows new resources (deeprep job, scheduler, metrics, alert) and modified import job (new env var)

**Step 3: Tag release**

Run: `make bump-minor`
Expected: tags v0.18.0 (minor bump — new feature, architectural change)

---

## Post-Deploy Checklist

1. Add `DEEPREP_JOB_NAME=devpulse-saas-deeprep` to GitHub environment `saas`
2. Run `terraform apply` in `infra/saas/`
3. Verify import job runs at :00 with `IMPORT_MODE=import` (no deep rep in logs)
4. Verify deeprep job runs at :30 with sequential scoring (one repo at a time)
5. Run `tools/metrics-review` — confirm new "Deep Reputation" sections appear
6. Monitor first few cycles for rate limit behavior improvement
