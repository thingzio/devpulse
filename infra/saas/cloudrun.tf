resource "google_cloud_run_v2_service" "serve" {
  name     = "${var.prefix}-serve"
  location = var.region
  project  = var.project_id

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 0
      max_instance_count = 10
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/thingzio/devpulse-site:latest"

      ports {
        container_port = 8080
      }

      env {
        name  = "PORT"
        value = "8080"
      }

      env {
        name  = "DATABASE_URL"
        value = "host=/cloudsql/${google_sql_database_instance.default.connection_name} dbname=devpulse user=${google_service_account.run.email} sslmode=disable"
      }

      env {
        name = "GITHUB_OAUTH_CLIENT_ID"
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

      resources {
        limits = {
          cpu    = "1000m"
          memory = "512Mi"
        }
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

resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.serve.name
  location = var.region
  project  = var.project_id
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_job" "import" {
  name     = "${var.prefix}-import"
  location = var.region
  project  = var.project_id

  template {
    template {
      service_account = google_service_account.import.email
      timeout         = "3600s"

      containers {
        image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/thingzio/devpulse-import:latest"

        env {
          name  = "DATABASE_URL"
          value = "host=/cloudsql/${google_sql_database_instance.default.connection_name} dbname=devpulse user=${google_service_account.import.email} sslmode=disable"
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
