resource "google_cloud_run_v2_service" "serve" {
  name                = "${var.prefix}-serve"
  location            = var.region
  project             = var.project_id
  deletion_protection = false # TODO: set to true after initial deploy

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 0
      max_instance_count = 10
    }

    vpc_access {
      network_interfaces {
        network    = google_compute_network.default.id
        subnetwork = google_compute_subnetwork.default.id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/thingzio/devpulse-site:latest"

      ports {
        container_port = 8080
      }

      env {
        name  = "DATABASE_URL"
        value = "host=/cloudsql/${google_sql_database_instance.default.connection_name} dbname=devpulse user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
      }

      env {
        name  = "GITHUB_OAUTH_CLIENT_ID"
        value = var.github_oauth_client_id
      }

      env {
        name = "GITHUB_OAUTH_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.oauth_client_secret.secret_id
            version = "latest"
          }
        }
      }

      env {
        name = "GITHUB_WEBHOOK_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.webhook_secret.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "BASE_URL"
        value = "https://${var.domain}"
      }

      env {
        name  = "GITHUB_APP_ID"
        value = var.github_app_id
      }

      env {
        name  = "GITHUB_APP_KEY_PATH"
        value = "/secrets/github-app-key/key.pem"
      }

      resources {
        limits = {
          cpu    = "1000m"
          memory = "512Mi"
        }
      }

      volume_mounts {
        name       = "github-app-key"
        mount_path = "/secrets/github-app-key"
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      startup_probe {
        http_get {
          path = "/health"
        }
        initial_delay_seconds = 2
        period_seconds        = 3
        failure_threshold     = 5
      }
    }

    volumes {
      name = "github-app-key"
      secret {
        secret = google_secret_manager_secret.github_app_key.secret_id
        items {
          version = "latest"
          path    = "key.pem"
        }
      }
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.default.connection_name]
      }
    }
  }

  depends_on = [google_project_service.default]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.serve.name
  location = var.region
  project  = var.project_id
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_job" "import" {
  name                = "${var.prefix}-import"
  location            = var.region
  project             = var.project_id
  deletion_protection = false # TODO: set to true after initial deploy

  template {
    template {
      service_account = google_service_account.import.email
      timeout         = "3600s"

      vpc_access {
        network_interfaces {
          network    = google_compute_network.default.id
          subnetwork = google_compute_subnetwork.default.id
        }
        egress = "PRIVATE_RANGES_ONLY"
      }

      containers {
        image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/thingzio/devpulse-import:latest"

        env {
          name  = "DATABASE_URL"
          value = "host=/cloudsql/${google_sql_database_instance.default.connection_name} dbname=devpulse user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
        }

        env {
          name  = "GITHUB_APP_ID"
          value = var.github_app_id
        }

        env {
          name  = "GITHUB_APP_KEY_PATH"
          value = "/secrets/github-app-key/key.pem"
        }

        env {
          name = "ANTHROPIC_API_KEY"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.anthropic_api_key.secret_id
              version = "latest"
            }
          }
        }

        env {
          name  = "ANTHROPIC_MODEL"
          value = "claude-haiku-4-5-20251001"
        }

        resources {
          limits = {
            cpu    = "1000m"
            memory = "512Mi"
          }
        }

        volume_mounts {
          name       = "github-app-key"
          mount_path = "/secrets/github-app-key"
        }

        volume_mounts {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
      }

      volumes {
        name = "github-app-key"
        secret {
          secret = google_secret_manager_secret.github_app_key.secret_id
          items {
            version = "latest"
            path    = "key.pem"
          }
        }
      }

      volumes {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [google_sql_database_instance.default.connection_name]
        }
      }
    }
  }

  depends_on = [google_project_service.default]
}

# Admin service (IAM-protected, no public access)

resource "google_cloud_run_v2_service" "admin" {
  name                = "${var.prefix}-admin"
  location            = var.region
  project             = var.project_id
  deletion_protection = false

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }

    vpc_access {
      network_interfaces {
        network    = google_compute_network.default.id
        subnetwork = google_compute_subnetwork.default.id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/thingzio/devpulse-admin:latest"

      ports {
        container_port = 8080
      }

      env {
        name  = "DATABASE_URL"
        value = "host=/cloudsql/${google_sql_database_instance.default.connection_name} dbname=devpulse user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
      }

      resources {
        limits = {
          cpu    = "1000m"
          memory = "512Mi"
        }
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      startup_probe {
        http_get {
          path = "/health"
        }
        initial_delay_seconds = 2
        period_seconds        = 3
        failure_threshold     = 5
      }
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.default.connection_name]
      }
    }
  }

  depends_on = [google_project_service.default]
}

resource "google_cloud_run_v2_service_iam_member" "admin_invoker" {
  for_each = toset(var.admin_invoker_emails)
  name     = google_cloud_run_v2_service.admin.name
  location = var.region
  project  = var.project_id
  role     = "roles/run.invoker"
  member   = "user:${each.value}"
}
