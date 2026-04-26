resource "google_cloud_run_v2_service" "serve" {
  name                = "${var.prefix}-serve"
  location            = var.region
  project             = var.project_id
  deletion_protection = true

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }

    vpc_access {
      network_interfaces {
        network    = local.vpc_id
        subnetwork = local.subnet_id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/devpulse-site:latest"

      ports {
        container_port = 8080
      }

      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = "latest"
          }
        }
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

      env {
        name  = "IMPORT_JOB_NAME"
        value = "projects/${var.project_id}/locations/${var.region}/jobs/${google_cloud_run_v2_job.import.name}"
      }

      env {
        name = "SEND_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.send_api_key.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "SUPPORT_EMAIL"
        value = var.support_email
      }

      env {
        name  = "DEVPULSE_ADMIN_USERS"
        value = var.admin_users
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
        value = "claude-sonnet-4-6"
      }

      env {
        name = "DIGEST_HMAC_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.digest_hmac_secret.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "SAMPLE_REPOS"
        value = var.sample_repos
      }

      resources {
        limits = {
          cpu    = "1000m"
          memory = "512Mi"
        }
        cpu_idle          = true
        startup_cpu_boost = true
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
        instances = [local.db_connection]
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
  deletion_protection = true

  template {
    task_count  = var.import_parallelism
    parallelism = var.import_parallelism

    template {
      service_account = google_service_account.import.email
      timeout         = "5400s"

      vpc_access {
        network_interfaces {
          network    = local.vpc_id
          subnetwork = local.subnet_id
        }
        egress = "PRIVATE_RANGES_ONLY"
      }

      containers {
        image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/devpulse-import:latest"

        env {
          name = "DATABASE_URL"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.database_url.secret_id
              version = "latest"
            }
          }
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

        env {
          name  = "IMPORT_WORKERS"
          value = tostring(var.import_workers)
        }

        env {
          name  = "IMPORT_TASK_TIMEOUT"
          value = tostring(var.import_task_timeout)
        }

        env {
          name = "SEND_API_KEY"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.send_api_key.secret_id
              version = "latest"
            }
          }
        }

        env {
          name  = "BASE_URL"
          value = "https://${var.domain}"
        }

        env {
          name = "DIGEST_HMAC_SECRET"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.digest_hmac_secret.secret_id
              version = "latest"
            }
          }
        }

        env {
          name  = "DEVPULSE_ADMIN_USERS"
          value = var.admin_users
        }

        env {
          name  = "DIGEST_ADMIN_ONLY"
          value = tostring(var.digest_admin_only)
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
          instances = [local.db_connection]
        }
      }
    }
  }

  depends_on = [google_project_service.default]
}

