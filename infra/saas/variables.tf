variable "project_id" {
  description = "GCP project ID for the SaaS deployment"
  type        = string
}

variable "region" {
  description = "GCP region for Cloud Run and Cloud SQL"
  type        = string
  default     = "us-west1"
}

variable "prefix" {
  description = "Unique deployment identifier"
  type        = string
  default     = "devpulse-saas"
}

variable "domain" {
  description = "Public domain for the SaaS service"
  type        = string
  default     = "devpulse.thingz.io"
}

variable "git_repo" {
  description = "GitHub repository for federated identity"
  type        = string
  default     = "thingzio/devpulse"
}

variable "github_oauth_client_id" {
  description = "GitHub OAuth App client ID (public, not a secret)"
  type        = string
}

variable "db_tier" {
  description = "Cloud SQL machine tier"
  type        = string
  default     = "db-f1-micro"
}
