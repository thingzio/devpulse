locals {
  services = [
    "artifactregistry.googleapis.com",
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudscheduler.googleapis.com",
    "dns.googleapis.com",
    "servicenetworking.googleapis.com",
    "compute.googleapis.com",
    "monitoring.googleapis.com",
    "iam.googleapis.com",
  ]
}

resource "google_project_service" "default" {
  for_each = toset(local.services)
  project  = var.project_id
  service  = each.value
}

data "google_project" "default" {
  project_id = var.project_id
}
