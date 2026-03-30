# Upgrade Request Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Let users request a plan upgrade from the dashboard, with idempotent backend, log-based metrics, alerting, and dashboard widget.

**Architecture:** Add `upgrade_requested_at` column to tenant table. New `POST /api/upgrade-request` endpoint sets it idempotently. Log-based metric + email notification channel + alert policy in Terraform. Frontend shows "Request Upgrade" button on repo limit error.

**Tech Stack:** Go, PostgreSQL, Terraform (GCP Monitoring), jQuery

---

### Task 1: Add migration for upgrade_requested_at column

**Files:**
- Modify: `pkg/data/postgres/sql/migrations_saas/001_initial.sql`

**Step 1: Add column to tenant table definition**

After line 14 (`tos_accepted_at TIMESTAMPTZ,`), add:

```sql
    upgrade_requested_at TIMESTAMPTZ,
```

**Step 2: Run `make test`**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/data/postgres/sql/migrations_saas/001_initial.sql
git commit -S -m "feat: add upgrade_requested_at column to tenant table"
```

---

### Task 2: Update tenant struct, SQL constants, and scanTenant

**Files:**
- Modify: `pkg/tenant/tenant.go`
- Modify: `pkg/tenant/session.go`

**Step 1: Add field to Tenant struct**

In `pkg/tenant/tenant.go`, add after `ToSAcceptedAt` (line 20):

```go
	UpgradeRequestedAt *time.Time
```

**Step 2: Update all SQL SELECT constants to include upgrade_requested_at**

Update `upsertTenantSQL` RETURNING clause (line 33-34):
```sql
	RETURNING id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	          tos_accepted_at, upgrade_requested_at, created_at, updated_at`
```

Update `getTenantByGitHubIDSQL` (lines 37-38):
```sql
	SELECT id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at, created_at, updated_at
```

Update `getTenantByIDSQL` (lines 42-43):
```sql
	SELECT id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at, created_at, updated_at
```

**Step 3: Update scanTenant**

In `scanTenant` (line 52-54), add `&t.UpgradeRequestedAt` after `&t.ToSAcceptedAt`:

```go
	err := row.Scan(
		&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
		&t.MaxRepos, &t.MaxEventsPerWeek, &t.Plan, &t.ToSAcceptedAt, &t.UpgradeRequestedAt, &t.CreatedAt, &t.UpdatedAt,
	)
```

**Step 4: Update validateSessionSQL in session.go**

In `pkg/tenant/session.go`, update `validateSessionSQL` (lines 20-21):
```sql
	SELECT t.id, t.github_id, t.username, t.email, t.avatar_url,
	       t.max_repos, t.max_events_per_week, t.plan, t.tos_accepted_at, t.upgrade_requested_at, t.created_at, t.updated_at
```

**Step 5: Add RequestUpgrade function and SQL constant**

Add after `updatePlanSQL` (line 48):

```go
const requestUpgradeSQL = `
	UPDATE tenant SET upgrade_requested_at = NOW(), updated_at = NOW()
	WHERE id = $1 AND upgrade_requested_at IS NULL
	RETURNING username, email, plan, max_repos`
```

Add after `UpdatePlan` function (after line 105):

```go
// UpgradeRequest holds details returned when an upgrade is first requested.
type UpgradeRequest struct {
	Username string
	Email    string
	Plan     string
	MaxRepos int
}

// RequestUpgrade idempotently records an upgrade request for a tenant.
// Returns the request details on first call, nil on subsequent calls.
func RequestUpgrade(ctx context.Context, db *sql.DB, tenantID string) (*UpgradeRequest, error) {
	var req UpgradeRequest
	err := db.QueryRowContext(ctx, requestUpgradeSQL, tenantID).Scan(
		&req.Username, &req.Email, &req.Plan, &req.MaxRepos,
	)
	if err == sql.ErrNoRows {
		return nil, nil // already requested
	}
	if err != nil {
		return nil, fmt.Errorf("requesting upgrade: %w", err)
	}
	return &req, nil
}
```

**Step 6: Run `make test`**

Run: `make test`
Expected: PASS

**Step 7: Commit**

```bash
git add pkg/tenant/tenant.go pkg/tenant/session.go
git commit -S -m "feat: add upgrade request to tenant schema and functions"
```

---

### Task 3: Add upgrade request handler and update repo limit error

**Files:**
- Modify: `pkg/server/server.go`
- Modify: `pkg/tenant/installation.go`

**Step 1: Update error format in AddTenantRepos**

In `pkg/tenant/installation.go`, change the error format at line 134 from:

```go
		return fmt.Errorf("%w: %d + %d > %d", ErrRepoLimitExceeded, currentCount, len(repos), maxRepos)
```

to:

```go
		return fmt.Errorf("repo_limit_reached:%d", maxRepos)
```

**Step 2: Add upgrade request handler to server.go**

Add the handler function (after `addRepoHandler`):

```go
func upgradeRequestHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		req, err := tenant.RequestUpgrade(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("upgrade request failed", "tenant_id", tn.ID, "error", err)
			http.Error(w, "error processing request", http.StatusInternalServerError)
			return
		}

		if req != nil {
			slog.Info("upgrade requested",
				"tenant_id", tn.ID,
				"username", req.Username,
				"email", req.Email,
				"plan", req.Plan,
				"max_repos", req.MaxRepos,
			)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
```

**Step 3: Register the route**

In `server.go`, add after the `POST /api/repos` route (line 166):

```go
	mux.Handle("POST /api/upgrade-request", wrap(upgradeRequestHandler(db)))
```

**Step 4: Run `make test`**

Run: `make test`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/server/server.go pkg/tenant/installation.go
git commit -S -m "feat: add upgrade request endpoint and simplify limit error"
```

---

### Task 4: Update frontend to show upgrade button on repo limit

**Files:**
- Modify: `pkg/server/static/js/app.js`

**Step 1: Update the error handler in addTrackedRepo**

Replace the `.fail()` handler (currently around lines 2339-2349) with:

```javascript
    }).fail(function(xhr) {
        var msg = xhr.responseText || 'Failed to add repo';
        var limitMatch = msg.match(/^repo_limit_reached:(\d+)$/);
        if (limitMatch) {
            var limit = limitMatch[1];
            $status.html('Repo limit for current plan reached (' + limit + '). <a href="#" id="request-upgrade-link">Request Upgrade</a>');
            $('#request-upgrade-link').on('click', function(e) {
                e.preventDefault();
                $.post('/api/upgrade-request', function() {
                    $status.text('Upgrade request submitted. We will be in touch.');
                }).fail(function() {
                    $status.text('Error submitting upgrade request. Please try again.');
                });
            });
        } else {
            var urlMatch = msg.match(/(https:\/\/\S+)/);
            if (urlMatch) {
                var url = urlMatch[1];
                var text = msg.replace(url, '');
                $status.html(text + '<a href="' + $('<span>').text(url).html() + '" target="_blank">Install here</a>');
            } else {
                $status.text(msg);
            }
        }
    });
```

**Step 2: Run `make test`**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/server/static/js/app.js
git commit -S -m "feat: show upgrade request button on repo limit reached"
```

---

### Task 5: Add Terraform monitoring resources

**Files:**
- Modify: `infra/saas/monitoring.tf`
- Modify: `infra/saas/dashboard.json`
- Modify: `infra/saas/variables.tf` (if `notification_email` var doesn't exist)

**Step 1: Add notification email variable**

In `infra/saas/variables.tf`, add:

```hcl
variable "notification_email" {
  description = "Email for monitoring alert notifications"
  type        = string
  default     = "devpulse@thingz.io"
}
```

**Step 2: Add notification channel, log metric, and alert policy to monitoring.tf**

Append to `infra/saas/monitoring.tf`:

```hcl
# Notification channel

resource "google_monitoring_notification_channel" "email" {
  display_name = "${var.prefix}-email"
  type         = "email"
  project      = var.project_id

  labels = {
    email_address = var.notification_email
  }
}

# Upgrade request metric

resource "google_logging_metric" "upgrade_requests" {
  name    = "${var.prefix}-upgrade-requests"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" jsonPayload.msg=\"upgrade requested\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "tenant_id"
      value_type  = "STRING"
      description = "Tenant ID"
    }
  }

  label_extractors = {
    "tenant_id" = "EXTRACT(jsonPayload.tenant_id)"
  }
}

# Upgrade request alert

resource "google_monitoring_alert_policy" "upgrade_request" {
  display_name          = "${var.prefix}-upgrade-request"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Upgrade requested"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND metric.type = \"logging.googleapis.com/user/${google_logging_metric.upgrade_requests.name}\""
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

**Step 3: Also add notification channel to existing alert policies**

Update `error_rate` and `import_failure` alert policies to include:

```hcl
  notification_channels = [google_monitoring_notification_channel.email.name]
```

**Step 4: Add upgrade requests widget to dashboard.json**

Add before the closing `]` of the widgets array:

```json
      ,
      {
        "title": "Upgrade Requests",
        "xyChart": {
          "dataSets": [{
            "timeSeriesQuery": {
              "timeSeriesFilter": {
                "filter": "resource.type=\"cloud_run_revision\" metric.type=\"logging.googleapis.com/user/devpulse-saas-upgrade-requests\"",
                "aggregation": {
                  "alignmentPeriod": "86400s",
                  "perSeriesAligner": "ALIGN_SUM"
                }
              }
            },
            "plotType": "STACKED_BAR"
          }]
        }
      }
```

**Step 5: Run `make qualify`**

Run: `make qualify`
Expected: PASS

**Step 6: Commit**

```bash
git add infra/saas/monitoring.tf infra/saas/dashboard.json infra/saas/variables.tf
git commit -S -m "feat: add upgrade request metric, alert, and dashboard widget"
```

---

### Task 6: Verify end-to-end

**Step 1: Run `make qualify`**

Run: `make qualify`
Expected: PASS (all tests, lint, vulncheck, tfsec, e2e)

**Step 2: Manual test (local)**

1. `make up && make server`
2. Sign in, add repos until limit is hit
3. Verify "Repo limit for current plan reached (5). Request Upgrade" message
4. Click "Request Upgrade" — verify "Upgrade request submitted" message
5. Click again — verify same success message (idempotent)
6. Check server logs for `"upgrade requested"` entry (only on first click)
