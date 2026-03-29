# Bootstrap Guide

Step-by-step guide to deploy DevPulse from scratch on GCP.

## Prerequisites

- [gcloud CLI](https://cloud.google.com/sdk/docs/install) installed and authenticated
- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.13
- [ko](https://ko.build/install/) for building container images
- GCP organization, project, and billing account
- Domain name (e.g. `devpulse.thingz.io`) with DNS access at your registrar
- GitHub account (for OAuth App and GitHub App registration)
- `GITHUB_TOKEN` with `write:packages` scope for GHCR push

## 1. Setup GCP Project

```shell
export PROJECT_ID="devpulseio"
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
| Homepage URL | `https://devpulse.thingz.io` |
| Authorization callback URL | `https://devpulse.thingz.io/auth/github/callback` |

Save the **Client ID** and generate a **Client Secret**. You'll need these in step 8.

## 4. Register GitHub App

Go to https://github.com/settings/apps/new

| Field | Value |
|-------|-------|
| GitHub App name | `DevPulseThingz` (must be globally unique) |
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

## 5. Push Bootstrap Images

Terraform needs container images to exist before creating Cloud Run resources. Push them manually (one-time):

```shell
# Authenticate to GHCR
echo $GITHUB_TOKEN | docker login ghcr.io -u YOUR_USERNAME --password-stdin

# Build and push both images (repo must include full image name)
KO_DOCKER_REPO=ghcr.io/thingzio/devpulse-site ko build ./cmd/devpulse-site/ --bare --tags latest
KO_DOCKER_REPO=ghcr.io/thingzio/devpulse-import ko build ./cmd/devpulse-import/ --bare --tags latest
```

Then make both packages public on GHCR (required for the AR remote repo to pull):
- https://github.com/orgs/thingzio/packages/container/devpulse-site/settings → Visibility → Public
- https://github.com/orgs/thingzio/packages/container/devpulse-import/settings → Visibility → Public

## 6. Run Terraform

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

This creates: VPC, Cloud SQL, Secret Manager, service accounts, Cloud Run service + job, Cloud Scheduler, Cloud DNS zone, Artifact Registry remote repo, monitoring alerts, Workload Identity Federation.

Note the outputs:
```shell
terraform output
# service_url       = "https://devpulse-saas-serve-xxxxx.run.app"
# db_connection_name = "devpulse-saas:us-west1:devpulse-saas-pg"
# dns_nameservers   = ["ns-cloud-a1.googledomains.com.", ...]
# deployer_sa       = "github-actions-devpulse-saas@devpulseio.iam.gserviceaccount.com"
# wif_provider      = "projects/.../providers/gh-provider-devpulse-saas"
```

## 7. Configure GitHub Actions

In the GitHub repo settings (`Settings → Environments`), create an environment called `saas` with these variables:

| Variable | Value |
|----------|-------|
| `WIF_PROVIDER` | From `terraform output wif_provider` |
| `DEPLOYER_SA` | From `terraform output deployer_sa` |
| `SERVICE_NAME` | `devpulse-saas-serve` |
| `JOB_NAME` | `devpulse-saas-import` |
| `REGION` | `us-west1` |

## 8. Store Secrets

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

# Anthropic API key (optional, enables LLM-generated insights)
echo -n "YOUR_ANTHROPIC_API_KEY" | \
gcloud secrets versions add devpulse-saas-anthropic-api-key \
    --project=$PROJECT_ID \
    --data-file=-
```

## 9. Delegate DNS

At your domain registrar, update the NS records for `devpulse.thingz.io` to point to the Cloud DNS nameservers from the Terraform output.

Verify propagation:
```shell
dig NS devpulse.thingz.io
```

## 10. Create Monitoring Dashboard

```shell
gcloud monitoring dashboards create \
    --project=$PROJECT_ID \
    --config-from-file=infra/saas/dashboard.json
```

View at: https://console.cloud.google.com/monitoring/dashboards?project=$PROJECT_ID

## 11. First Release

Tag a release to trigger the full CI pipeline (build images + deploy to Cloud Run):

```shell
make bump-minor
```

This triggers `release-on-tag.yaml` which:
1. Runs all tests
2. Builds `devpulse-site` and `devpulse-import` container images via goreleaser + ko
3. Pushes images to GHCR
4. Deploys to Cloud Run using the GitHub Actions environment vars from step 7
5. Publishes the GitHub release

## 12. Verify

```shell
# Check Cloud Run service
gcloud run services describe devpulse-saas-serve \
    --region=us-west1 --format="value(status.url)"

# Check import job
gcloud run jobs describe devpulse-saas-import --region=us-west1

# Trigger a manual import
gcloud run jobs execute devpulse-saas-import --region=us-west1

# Check logs
gcloud logging read 'resource.type="cloud_run_revision"' \
    --limit=20 --format='table(timestamp, textPayload)'
```

Open `https://devpulse.thingz.io` — you should see the landing page with "Sign in with GitHub".

## Updating

### Code changes

Push a version tag to trigger the release pipeline:
```shell
make bump-patch  # or bump-minor, bump-major
```

Or deploy a specific tag manually:
```shell
gh workflow run deploy-saas.yaml -f image_tag=v1.2.3
```

### Infrastructure changes

```shell
cd infra/saas
terraform plan -var="project_id=$PROJECT_ID"
terraform apply -var="project_id=$PROJECT_ID"
```

### Database tier upgrade

```shell
terraform apply -var="project_id=$PROJECT_ID" -var="db_tier=db-g1-small"
```

See [INFRASTRUCTURE.md](INFRASTRUCTURE.md) for the full scaling plan.

### Tenant plan management

Tenant plans are managed via SQL. Connect to the database and update directly.

**View current tenants and their limits:**
```sql
SELECT username, plan, max_repos, max_events_per_week, created_at
FROM tenant ORDER BY created_at;
```

**Promote a tenant to pro:**
```sql
UPDATE tenant
SET plan = 'pro', max_repos = 25, max_events_per_week = 20000, updated_at = NOW()
WHERE username = 'their-github-username';
```

**Plan tier defaults:**

| Plan | Repos | Events/Week |
|------|-------|-------------|
| free | 5 | 2,000 |
| pro | 25 | 20,000 |
| enterprise | 100 | 100,000 |

**Check a tenant's current weekly usage:**
```sql
SELECT t.username, t.plan, t.max_repos, t.max_events_per_week,
    COUNT(DISTINCT tr.id) AS active_repos,
    (SELECT COUNT(*) FROM event e
     JOIN tenant_repo tr2 ON tr2.org = e.org AND tr2.repo = e.repo
     WHERE tr2.tenant_id = t.id AND tr2.active = TRUE
       AND e.date >= date_trunc('week', NOW())::text) AS weekly_events
FROM tenant t
LEFT JOIN tenant_repo tr ON tr.tenant_id = t.id AND tr.active = TRUE
GROUP BY t.id
ORDER BY t.username;
```
