resource "google_cloud_scheduler_job" "import" {
  name     = "${var.prefix}-import-scheduled"
  schedule = "0 */2 * * *"
  project  = var.project_id
  region   = var.region

  http_target {
    http_method = "POST"
    uri         = "https://${var.region}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${var.project_id}/jobs/${google_cloud_run_v2_job.import.name}:run"

    oauth_token {
      service_account_email = google_service_account.deployer.email
    }
  }

  depends_on = [google_project_service.default]
}
