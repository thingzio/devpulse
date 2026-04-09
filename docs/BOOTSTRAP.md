# Bootstrap Guide

Step-by-step guide to deploy DevPulse from scratch on GCP. Steps are ordered to minimize friction — each step depends only on previous steps.

## Prerequisites

- [gcloud CLI](https://cloud.google.com/sdk/docs/install) installed and authenticated
- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.13
- [ko](https://ko.build/install/) for building container images
- [gh CLI](https://cli.github.com/) for GitHub Actions environment setup
- GCP project with billing enabled
- Domain name with DNS access at your registrar
- GitHub account with org admin access
- `GITHUB_TOKEN` with `write:packages` scope

## 1. Set Environment

```shell
export PROJECT_ID="devpulseio"
export REGION="us-west1"
export DOMAIN="devpulse.thingz.io"
```

## 2. Create Terraform State Bucket

```shell
gcloud storage buckets create gs://devpulse-state \
    --project=$PROJECT_ID \
    --location=us \
    --uniform-bucket-level-access
```

## 3. Register GitHub OAuth App

Go to https://github.com/settings/applications/new

| Field | Value |
|-------|-------|
| Application name | DevPulse |
| Homepage URL | `https://$DOMAIN` |
| Authorization callback URL | `https://$DOMAIN/auth/github/callback` |

Save the **Client ID** (short, like `Ov23li...`) and generate a **Client Secret** (40-char hex).

```shell
export GITHUB_OAUTH_CLIENT_ID="your-client-id"
```

## 4. Register GitHub App

Go to https://github.com/settings/apps/new

| Field | Value |
|-------|-------|
| GitHub App name | Must be globally unique (e.g. `DevPulseThingz`) |
| Homepage URL | `https://$DOMAIN` |
| Webhook URL | `https://$DOMAIN/webhook/github` |
| Webhook secret | `openssl rand -hex 32` |

Permissions:
- **Repository**: Metadata (Read-only), Contents (Read-only), Issues (Read-only), Pull requests (Read-only)
- **Organization**: Packages (Read-only) — required for container version imports

Subscribe to events: **Installation**, **Installation repositories**

After creating: note the **App ID**, download the **private key** (.pem), note the **webhook secret**.

```shell
export GITHUB_APP_ID="your-app-id"
```

## 5. Push Bootstrap Images

Terraform needs images to exist before creating Cloud Run resources. Push manually (one-time):

```shell
# Authenticate to Artifact Registry
gcloud auth configure-docker us-west1-docker.pkg.dev --quiet

# Set the AR registry path
AR_REGISTRY=us-west1-docker.pkg.dev/devpulseio/devpulse-saas-images

# Build and push
KO_DOCKER_REPO=${AR_REGISTRY}/devpulse-site ko build ./cmd/devpulse-site/ --bare --tags latest
KO_DOCKER_REPO=${AR_REGISTRY}/devpulse-import ko build ./cmd/devpulse-import/ --bare --tags latest
KO_DOCKER_REPO=${AR_REGISTRY}/devpulse-admin ko build ./cmd/devpulse-admin/ --bare --tags latest
```

## 6. Store Secrets

Store secret values **before** Terraform apply (Cloud Run fails if secrets have no versions):

```shell
# GitHub OAuth client secret
echo -n "YOUR_OAUTH_CLIENT_SECRET" | \
gcloud secrets versions add devpulse-saas-oauth-client-secret \
    --project=$PROJECT_ID --data-file=-

# GitHub webhook secret
echo -n "YOUR_WEBHOOK_SECRET" | \
gcloud secrets versions add devpulse-saas-webhook-secret \
    --project=$PROJECT_ID --data-file=-

# GitHub App private key
gcloud secrets versions add devpulse-saas-github-app-key \
    --project=$PROJECT_ID --data-file=path/to/devpulse.pem

# Anthropic API key (optional, enables LLM insights)
echo -n "YOUR_ANTHROPIC_API_KEY" | \
gcloud secrets versions add devpulse-saas-anthropic-api-key \
    --project=$PROJECT_ID --data-file=-
```

> **Note:** Secret Manager resources are created by Terraform, but you must add versions (values) manually. Terraform creates empty secret containers — Cloud Run fails if it references a secret with no versions.

## 7. Run Terraform

```shell
cd infra/saas
terraform init

terraform apply
```

This creates: VPC + subnet, Cloud SQL (password-based user), Secret Manager, service accounts, Artifact Registry standard repo, Cloud Run service + job (import), Cloud Scheduler (hourly trigger), Cloud DNS zone, WIF for GitHub Actions, monitoring alerts + log metrics.

> **First apply note:** Set `deletion_protection = false` in `cloudrun.tf` for both service and job during initial setup. Set back to `true` after successful deploy.

Note the outputs:
```shell
terraform output
```

## 8. Configure GitHub Actions

Populate the `saas` environment variables from Terraform outputs:

```shell
cd ../..  # back to repo root
./tools/setup-gh-env
```

This creates 9 variables in the GitHub `saas` environment:
`WIF_PROVIDER`, `DEPLOYER_SA`, `SERVICE_NAME`, `JOB_NAME`, `REGION`, `PROJECT_ID`, `AR_REPO`, `ADMIN_SERVICE_NAME`

## 9. Delegate DNS

At your domain registrar, update NS records for your domain to Cloud DNS nameservers from Terraform output:

```shell
terraform -chdir=infra/saas output dns_nameservers
dig NS $DOMAIN
```

## 10. Create Monitoring Dashboard

```shell
gcloud monitoring dashboards create \
    --project=$PROJECT_ID \
    --config-from-file=infra/saas/dashboard.json
```

## 11. First Release

```shell
make bump-minor
```

This triggers the release pipeline:
1. Tests (unit, lint, tfsec, e2e)
2. Builds `devpulse-site`, `devpulse-import`, and `devpulse-admin` images via goreleaser + ko
3. Pushes to GHCR
4. Deploys to Cloud Run via AR remote repo proxy
5. Publishes GitHub release

## 12. Verify

```shell
# Check service
gcloud run services describe devpulse-saas-serve \
    --region=$REGION --format='value(status.url)'

# Open in browser
open https://$DOMAIN

# After signing in, install the GitHub App on your org:
# https://github.com/apps/DevPulseThingz
# This is required before adding repos — the app must be installed
# on the org to grant API access for imports.

# Trigger manual import (after signing in, installing app, and adding repos)
gcloud run jobs execute devpulse-saas-import --region=$REGION --project=$PROJECT_ID
# The import job runs hourly via Cloud Scheduler (includes all phases)

# Check logs
gcloud logging read 'resource.type="cloud_run_revision"' \
    --project=$PROJECT_ID --limit=20 \
    --format='table(timestamp, jsonPayload.msg)'
```

## Post-Deploy

### Add Anthropic API key (optional, enables LLM insights)

```shell
echo -n "YOUR_ANTHROPIC_API_KEY" | \
gcloud secrets versions add devpulse-saas-anthropic-api-key \
    --project=$PROJECT_ID --data-file=-

gcloud run jobs update devpulse-saas-import --region=$REGION \
    --set-secrets=ANTHROPIC_API_KEY=devpulse-saas-anthropic-api-key:latest \
    --set-env-vars=ANTHROPIC_MODEL=claude-haiku-4-5-20251001
```

### Enable deletion protection

After verifying everything works:
```shell
# Edit infra/saas/cloudrun.tf — set deletion_protection = true on all resources (serve, import, admin)
cd infra/saas && terraform apply
```

### Rotate secrets

If any secrets were exposed during setup, rotate them:
1. Regenerate in GitHub (OAuth App settings / GitHub App settings)
2. Update in Secret Manager: `gcloud secrets versions add <secret-name> --data-file=-`
3. Redeploy: `make bump-patch`

See [ADMIN.md](ADMIN.md) for ongoing operations (releases, deploys, monitoring, tenant management).
