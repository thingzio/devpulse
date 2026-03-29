resource "google_sql_database_instance" "default" {
  name             = "${var.prefix}-pg"
  database_version = "POSTGRES_16"
  region           = var.region
  project          = var.project_id

  settings {
    tier              = var.db_tier
    edition           = "ENTERPRISE"
    availability_type = "ZONAL"
    disk_autoresize   = true

    ip_configuration {
      ipv4_enabled                                  = false
      private_network                               = google_compute_network.default.id
      enable_private_path_for_google_cloud_services = true
    }

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      start_time                     = "03:00"
    }

    database_flags {
      name  = "cloudsql.iam_authentication"
      value = "on"
    }
  }

  deletion_protection = true

  depends_on = [google_service_networking_connection.private]
}

resource "google_sql_database" "default" {
  name     = "devpulse"
  instance = google_sql_database_instance.default.name
}

resource "google_sql_user" "service" {
  name     = trimsuffix(google_service_account.run.email, ".gserviceaccount.com")
  instance = google_sql_database_instance.default.name
  type     = "CLOUD_IAM_SERVICE_ACCOUNT"
}

resource "google_sql_user" "import" {
  name     = trimsuffix(google_service_account.import.email, ".gserviceaccount.com")
  instance = google_sql_database_instance.default.name
  type     = "CLOUD_IAM_SERVICE_ACCOUNT"
}
