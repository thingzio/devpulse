# Copyright 2026 Thingz LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

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
      image = var.bootstrap_image

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

      # The container applies schema migrations during startup, before it
      # begins answering /health. A DDL migration against a large table can
      # take tens of seconds on this instance size, so the probe budget has to
      # exceed the slowest expected migration or Cloud Run kills the container
      # mid-startup and the revision never goes ready.
      # 2s + (30 x 3s) = ~92s.
      startup_probe {
        http_get {
          path = "/health"
        }
        initial_delay_seconds = 2
        period_seconds        = 3
        failure_threshold     = 30
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

  # The release workflow deploys pinned version tags (devpulse-site:vX.Y.Z),
  # while this config names :latest as the bootstrap value. Without this,
  # every plan reports the deployed version as drift and a subsequent apply
  # would roll the service back to whatever :latest currently points at --
  # discarding the released version and any traffic pin along with it.
  # The deploy pipeline owns the image tag; Terraform owns everything else.
  lifecycle {
    ignore_changes = [template[0].containers[0].image]
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
        image = var.bootstrap_image

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

  # See the note on google_cloud_run_v2_service.serve -- the release workflow
  # owns the image tag for the job as well.
  lifecycle {
    ignore_changes = [template[0].template[0].containers[0].image]
  }

  depends_on = [google_project_service.default]
}

