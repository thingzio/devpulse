variable "project_id" {
  description = "GCP project ID for the SaaS deployment"
  type        = string
  default     = "thingzio"
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

variable "admin_users" {
  description = "Comma-separated GitHub usernames allowed admin access"
  type        = string
  default     = "mchmarny"
}

variable "import_parallelism" {
  description = "Number of concurrent import job tasks per execution"
  type        = number
  default     = 3
}

variable "import_workers" {
  description = "Number of concurrent goroutine workers per import task"
  type        = number
  default     = 2
}

variable "import_task_timeout" {
  description = "Per-task timeout in minutes for import jobs"
  type        = number
  default     = 55
}

variable "support_email" {
  description = "Support contact form recipient email"
  type        = string
  default     = "devpulse@thingz.io"
}

# --- Shared infrastructure (from thingzio/infra) ---

variable "vpc_id" {
  description = "Shared VPC network ID"
  type        = string
  default     = "projects/thingzio/global/networks/thingzio-vpc"
}

variable "subnet_id" {
  description = "Shared VPC subnet ID"
  type        = string
  default     = "projects/thingzio/regions/us-west1/subnetworks/thingzio-subnet"
}

variable "db_instance_name" {
  description = "Shared Cloud SQL instance name"
  type        = string
  default     = "thingzio-pg"
}

variable "db_connection_name" {
  description = "Shared Cloud SQL connection string (project:region:instance)"
  type        = string
  default     = "thingzio:us-west1:thingzio-pg"
}

variable "db_name" {
  description = "Database name within the shared Cloud SQL instance"
  type        = string
  default     = "thingz"
}
