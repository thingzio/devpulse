resource "google_dns_managed_zone" "default" {
  name     = "${var.prefix}-zone"
  dns_name = "${var.domain}."
  project  = var.project_id

  dnssec_config {
    state = "on"
  }

  depends_on = [google_project_service.default]
}
