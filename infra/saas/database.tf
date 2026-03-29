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
      ssl_mode                                      = "ENCRYPTED_ONLY" #tfsec:ignore:google-sql-encrypt-in-transit-data
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

    database_flags {
      name  = "log_temp_files"
      value = "0"
    }

    database_flags {
      name  = "log_connections"
      value = "on"
    }

    database_flags {
      name  = "log_disconnections"
      value = "on"
    }

    database_flags {
      name  = "log_lock_waits"
      value = "on"
    }

    database_flags {
      name  = "log_checkpoints"
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

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  name     = "devpulse"
  instance = google_sql_database_instance.default.name
  password = random_password.db_password.result
}
