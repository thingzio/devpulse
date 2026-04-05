resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "${var.prefix}-images"
  description   = "Container images for DevPulse services"
  format        = "DOCKER"
  project       = var.project_id
  mode          = "STANDARD_REPOSITORY"

  cleanup_policies {
    id     = "keep-tagged"
    action = "KEEP"
    condition {
      tag_state = "TAGGED"
    }
  }

  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"
    condition {
      tag_state  = "UNTAGGED"
      older_than = "604800s" # 7 days
    }
  }

  depends_on = [google_project_service.default]
}
