# Bootstrap Guide

Step-by-step guide to deploy DevPulse from scratch on GCP.

## Prerequisites

- [gcloud CLI](https://cloud.google.com/sdk/docs/install) installed and authenticated
- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.13
- GCP organization or billing account
- Domain name (e.g. `devpulse.thingz.io`) with DNS access at your registrar
- GitHub account (for OAuth App and GitHub App registration)

## 1. Create GCP Project

```shell
export PROJECT_ID="devpulse-saas"
export BILLING_ACCOUNT="XXXXXX-XXXXXX-XXXXXX"

gcloud projects create $PROJECT_ID
gcloud billing projects link $PROJECT_ID --billing-account=$BILLING_ACCOUNT
gcloud config set project $PROJECT_ID
```

## 2. Create Terraform State Bucket

```shell
gcloud storage buckets create gs://devpulse-saas-tf-state \
    --project=$PROJECT_ID \
    --location=us \
    --uniform-bucket-level-access
```

## 3. Register GitHub OAuth App

Go to https://github.com/settings/applications/new

| Field | Value |
|-------|-------|
| Application name | DevPulse |
| Homepage URL | `https://devpulse.thingz.io` |
| Authorization callback URL | `https://devpulse.thingz.io/auth/github/callback` |

Save the **Client ID** and generate a **Client Secret**. You'll need these in step 6.

## 4. Register GitHub App

Go to https://github.com/settings/apps/new

| Field | Value |
|-------|-------|
| GitHub App name | DevPulse |
| Homepage URL | `https://devpulse.thingz.io` |
| Webhook URL | `https://devpulse.thingz.io/webhook/github` |
| Webhook secret | Generate a random string (e.g. `openssl rand -hex 32`) |

Permissions (Repository):
- **Metadata**: Read-only
- **Contents**: Read-only

Subscribe to events:
- Installation
- Installation repositories

After creating:
- Note the **App ID**
- Generate and download a **private key** (.pem file)
- Note the **webhook secret** you chose

## 5. Run Terraform

```shell
cd infra/saas

terraform init

terraform plan \
    -var="project_id=$PROJECT_ID" \
    -var="region=us-west1" \
    -var="domain=devpulse.thingz.io"

terraform apply \
    -var="project_id=$PROJECT_ID" \
    -var="region=us-west1" \
    -var="domain=devpulse.thingz.io"
```

This creates: VPC, Cloud SQL, Secret Manager, service accounts, Cloud Run service + job, Cloud Scheduler, Cloud DNS zone, monitoring alerts, Workload Identity Federation for GitHub Actions.

Note the outputs:
```shell
terraform output
# service_url       = "https://devpulse-saas-serve-xxxxx.run.app"
# db_connection_name = "devpulse-saas:us-west1:devpulse-saas-pg"
# dns_nameservers   = ["ns-cloud-a1.googledomains.com.", ...]
# deployer_sa       = "github-actions-devpulse-saas@devpulse-saas.iam.gserviceaccount.com"
# wif_provider      = "projects/.../providers/github-actions-provider-devpulse-saas"
```

## 6. Store Secrets

```shell
# GitHub App private key
gcloud secrets versions add devpulse-saas-github-app-key \
    --project=$PROJECT_ID \
    --data-file=devpulse.pem

# GitHub OAuth client secret
echo -n "YOUR_OAUTH_CLIENT_SECRET" | \
gcloud secrets versions add devpulse-saas-oauth-client-secret \
    --project=$PROJECT_ID \
    --data-file=-

# GitHub webhook secret
echo -n "YOUR_WEBHOOK_SECRET" | \
gcloud secrets versions add devpulse-saas-webhook-secret \
    --project=$PROJECT_ID \
    --data-file=-
```

## 7. Delegate DNS

At your domain registrar, update the NS records for `devpulse.thingz.io` to point to the Cloud DNS nameservers from the Terraform output.

Verify propagation:
```shell
dig NS devpulse.thingz.io
```

## 8. Build and Push First Image

Push triggers automatically on version tags via GitHub Actions. For the first deployment, push manually:

```shell
# Authenticate to GHCR
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin

# Build and push
docker build -t ghcr.io/thingzio/devpulse:latest .
docker push ghcr.io/thingzio/devpulse:latest
```

Or tag a release to trigger the CI pipeline:
```shell
make bump-patch
```

## 9. Configure GitHub Actions

In the GitHub repo settings, create an environment called `saas` with these variables:

| Variable | Value |
|----------|-------|
| `WIF_PROVIDER` | From `terraform output wif_provider` |
| `DEPLOYER_SA` | From `terraform output deployer_sa` |
| `SERVICE_NAME` | `devpulse-saas-serve` |
| `JOB_NAME` | `devpulse-saas-import` |
| `REGION` | `us-west1` |

## 10. Create Monitoring Dashboard

```shell
gcloud monitoring dashboards create \
    --project=$PROJECT_ID \
    --config-from-file=infra/saas/dashboard.json
```

This creates a Cloud Monitoring dashboard with 12 widgets: service request count, latency percentiles, instance count, CPU/memory utilization, billable time, sign-ins, import tenant count, import duration, Cloud SQL CPU/memory/connections.

View at: https://console.cloud.google.com/monitoring/dashboards?project=$PROJECT_ID

## 11. Verify

```shell
# Check Cloud Run service is running
gcloud run services describe devpulse-saas-serve \
    --region=us-west1 --format="value(status.url)"

# Check import job
gcloud run jobs describe devpulse-saas-import \
    --region=us-west1

# Trigger a manual import
gcloud run jobs execute devpulse-saas-import --region=us-west1

# Check logs
gcloud logging read 'resource.type="cloud_run_revision"' \
    --limit=20 --format='table(timestamp, textPayload)'
```

Open `https://devpulse.thingz.io` in your browser. You should see the landing page with "Sign in with GitHub".

## Updating

### Code changes

Push a version tag to trigger the release pipeline:
```shell
make bump-patch  # or bump-minor, bump-major
```

Or deploy manually:
```shell
# Via GitHub Actions
gh workflow run deploy-saas.yaml -f image_tag=v1.2.3

# Via gcloud
gcloud run services update devpulse-saas-serve \
    --region=us-west1 \
    --image=ghcr.io/thingzio/devpulse:v1.2.3
gcloud run jobs update devpulse-saas-import \
    --region=us-west1 \
    --image=ghcr.io/thingzio/devpulse:v1.2.3
```

### Infrastructure changes

```shell
cd infra/saas
terraform plan -var="project_id=$PROJECT_ID"
terraform apply -var="project_id=$PROJECT_ID"
```

### Database tier upgrade

Change the `db_tier` variable:
```shell
terraform apply -var="project_id=$PROJECT_ID" -var="db_tier=db-g1-small"
```

See [INFRASTRUCTURE.md](INFRASTRUCTURE.md) for the full scaling plan.
