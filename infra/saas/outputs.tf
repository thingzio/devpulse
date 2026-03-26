output "service_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.serve.uri
}

output "db_connection_name" {
  description = "Cloud SQL connection name"
  value       = google_sql_database_instance.default.connection_name
}

output "dns_nameservers" {
  description = "DNS nameservers for domain delegation"
  value       = google_dns_managed_zone.default.name_servers
}

output "deployer_sa" {
  description = "GitHub Actions deployer service account email"
  value       = google_service_account.deployer.email
}

output "wif_provider" {
  description = "Workload Identity Federation provider for GitHub Actions"
  value       = google_iam_workload_identity_pool_provider.github.name
}
