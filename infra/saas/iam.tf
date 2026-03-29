resource "google_service_account" "run" {
  account_id   = "${var.prefix}-run"
  display_name = "DevPulse SaaS Cloud Run service"
  project      = var.project_id
}

resource "google_service_account" "import" {
  account_id   = "${var.prefix}-import"
  display_name = "DevPulse SaaS import worker"
  project      = var.project_id
}

locals {
  run_roles = [
    "roles/cloudsql.client",
    "roles/cloudsql.instanceUser",
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
  ]

  import_roles = [
    "roles/cloudsql.client",
    "roles/cloudsql.instanceUser",
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
  ]
}

resource "google_project_iam_member" "run" {
  for_each = toset(local.run_roles)
  project  = var.project_id
  role     = each.value
  member   = "serviceAccount:${google_service_account.run.email}"
}

resource "google_project_iam_member" "import" {
  for_each = toset(local.import_roles)
  project  = var.project_id
  role     = each.value
  member   = "serviceAccount:${google_service_account.import.email}"
}

# GitHub Actions federated identity for deployments
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "gh-pool-${var.prefix}"
  display_name              = "GH Actions ${var.prefix}"
  project                   = var.project_id

  depends_on = [google_project_service.default]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "gh-provider-${var.prefix}"
  display_name                       = "GitHub Actions Provider (${var.prefix})"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.actor"      = "assertion.actor"
    "attribute.repository" = "assertion.repository"
  }

  attribute_condition = "assertion.repository == '${var.git_repo}'"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "deployer" {
  account_id   = "github-actions-${var.prefix}"
  display_name = "GitHub Actions deployer (${var.prefix})"
  project      = var.project_id
}

resource "google_service_account_iam_member" "deployer_wif" {
  service_account_id = google_service_account.deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.git_repo}"
}

locals {
  deployer_roles = [
    "roles/run.admin",
    "roles/iam.serviceAccountUser",
    "roles/cloudscheduler.admin",
  ]
}

resource "google_project_iam_member" "deployer" {
  for_each = toset(local.deployer_roles)
  project  = var.project_id
  role     = each.value
  member   = "serviceAccount:${google_service_account.deployer.email}"
}
