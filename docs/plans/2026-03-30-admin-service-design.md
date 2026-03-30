# Admin Service Design

## Problem

No non-interactive way to manage tenants (upgrade plans, etc.). Cloud SQL has no public IP, and `gcloud sql connect` requires interactive password entry.

## Design

IAM-protected Cloud Run service for admin operations. No API keys — GCP IAM gates access.

### Architecture

```
Admin (laptop)
  → gcloud auth print-identity-token
  → curl with Bearer token
  → Cloud Run admin service (no-allow-unauthenticated)
  → IAM check (roles/run.invoker)
  → PostgreSQL via Cloud SQL
```

### Components

1. **`cmd/devpulse-admin/main.go`** — HTTP server with admin endpoints
2. **`infra/saas/cloudrun.tf`** — Cloud Run service (private, IAM-gated)
3. **`.goreleaser.yaml`** — build target + ko image
4. **`tools/upgrade-tenant`** — script that curls the admin service

### Endpoints

| Method | Path | Body | Action |
|--------|------|------|--------|
| POST | /upgrade | `{"username":"x","plan":"pro"}` | Update tenant plan, clear upgrade_requested_at |
| GET | /health | — | Health check |

### Plan Limits

| Plan | Repos | Events/Week |
|------|-------|-------------|
| free | 5 | 2,000 |
| pro | 25 | 20,000 |
| enterprise | 100 | 100,000 |

### IAM

- `admin_invoker_email` Terraform variable (your GCP identity)
- `roles/run.invoker` binding on the admin service
- No public access (`--no-allow-unauthenticated`)

### Terraform Output

- `admin_url` — admin service URL for the script
