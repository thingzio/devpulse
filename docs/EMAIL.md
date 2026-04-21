# Weekly Digest Email

DevPulse sends a weekly digest email to opted-in tenants summarizing their portfolio health: KPI snapshot (stars, forks, issues, contributors, merge time) plus top week-over-week signals.

## Prerequisites

- [Resend](https://resend.com) API key with a verified sending domain for `devpulse.thingz.io`
- Migration `021_tenant_weekly_digest.sql` applied (runs automatically on import startup via `RunSaaSMigrations`)
- Tenant must have `weekly_digest = TRUE` (default) and a non-empty email in `devpulse_tenant`

## Environment Variables

| Variable | Required | Where | Description |
|----------|----------|-------|-------------|
| `SEND_API_KEY` | Yes | import + site | Resend API key |
| `BASE_URL` | Yes | import + site | Public base URL (e.g. `https://devpulse.thingz.io`) |
| `DIGEST_HMAC_SECRET` | Yes | import + site | Secret for signing unsubscribe tokens |
| `DEVPULSE_ADMIN_USERS` | Yes | import + site | Comma-separated admin usernames |
| `DIGEST_ADMIN_ONLY` | No | import | `true` (default) = admin only, `false` = all users |

All variables are managed via Terraform in `infra/saas/`.

## Rollout Steps

### Step 1: Deploy (admin-only by default)

```bash
terraform apply
```

Create the HMAC secret value:

```bash
echo -n "$(openssl rand -hex 32)" | \
  gcloud secrets versions add devpulse-saas-digest-hmac-secret --data-file=-
```

Release the code. `DIGEST_ADMIN_ONLY` defaults to `true`, so only admin users receive real digest emails.

### Step 2: Enable for all users

```bash
terraform apply -var='digest_admin_only=false'
```

### Step 3: Stop sending (revert to admin-only)

```bash
terraform apply -var='digest_admin_only=true'
```

## How It Works

- The digest runs at the end of each import job execution (first task index only)
- Each tenant is assigned a fixed day-of-week via `FNV32a(tenantID) % 7`
- Only tenants whose assigned day matches today receive an email
- When `DIGEST_ADMIN_ONLY=true`, only the first admin user receives the email (day check skipped)
- Emails include an HMAC-signed one-click unsubscribe link (no login required)
- Users can also toggle the digest from Settings in the dashboard

## Unsubscribe

Two mechanisms:

1. **One-click link** in every email footer, signed with `DIGEST_HMAC_SECRET` (stateless, no auth needed)
2. **Settings toggle** in the dashboard under Notifications
