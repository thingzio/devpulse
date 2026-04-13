output "service_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.serve.uri
}

output "import_job_name" {
  description = "Cloud Run import job name (for manual triggers)"
  value       = google_cloud_run_v2_job.import.name
}

output "service_name" {
  description = "Cloud Run service name"
  value       = google_cloud_run_v2_service.serve.name
}

output "notification_email" {
  description = "Alert notification email"
  value       = var.notification_email
}

output "admin_url" {
  description = "Admin service URL (IAM-protected)"
  value       = google_cloud_run_v2_service.admin.uri
}

output "project_id" {
  description = "GCP project ID"
  value       = var.project_id
}

output "ar_repo" {
  description = "Artifact Registry repository ID"
  value       = google_artifact_registry_repository.images.repository_id
}

output "deployer_sa" {
  description = "GitHub Actions deployer service account email"
  value       = google_service_account.deployer.email
}

output "wif_provider" {
  description = "Workload Identity Federation provider for GitHub Actions"
  value       = google_iam_workload_identity_pool_provider.github.name
}
