variable "project_id" {
  description = "GCP project ID for the SaaS deployment"
  type        = string
  default     = "devpulseio"
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
  default     = "Ov23li1tN3Czc3uhlc0j"
}

variable "github_app_id" {
  description = "GitHub App ID for installation token minting"
  type        = string
  default     = "3216423"
}

variable "notification_email" {
  description = "Email for monitoring alert notifications"
  type        = string
  default     = "devpulse@thingz.io"
}

variable "admin_invoker_emails" {
  description = "GCP identities allowed to invoke the admin service"
  type        = list(string)
  default     = ["mchmarny@gmail.com"]
}

variable "db_tier" {
  description = "Cloud SQL machine tier"
  type        = string
  default     = "db-f1-micro"
}

variable "import_parallelism" {
  description = "Number of concurrent import job tasks per execution"
  type        = number
  default     = 3
}

variable "stripe_pro_monthly_price_id" {
  description = "Stripe PRO monthly price ID"
  type        = string
  default     = ""
}

variable "stripe_pro_annual_price_id" {
  description = "Stripe PRO annual price ID"
  type        = string
  default     = ""
}
