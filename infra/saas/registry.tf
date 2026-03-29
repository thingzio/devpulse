resource "google_artifact_registry_repository" "ghcr" {
  location      = var.region
  repository_id = "${var.prefix}-ghcr"
  description   = "Remote proxy for GitHub Container Registry"
  format        = "DOCKER"
  project       = var.project_id
  mode          = "REMOTE_REPOSITORY"

  remote_repository_config {
    docker_repository {
      custom_repository {
        uri = "https://ghcr.io"
      }
    }
  }

  depends_on = [google_project_service.default]
}
