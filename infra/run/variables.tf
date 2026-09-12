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
}

variable "git_repo" {
  description = "GitHub repository for federated identity"
  type        = string
}

variable "github_oauth_client_id" {
  description = "GitHub OAuth App client ID (public, not a secret)"
  type        = string
}

variable "github_app_id" {
  description = "GitHub App ID for installation token minting"
  type        = string
}

variable "notification_email" {
  description = "Email for monitoring alert notifications"
  type        = string
}

variable "admin_users" {
  description = "Comma-separated GitHub usernames allowed admin access"
  type        = string
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
}

variable "digest_admin_only" {
  description = "Restrict digest emails to admin users only (true/false)"
  type        = bool
  default     = true
}

variable "sample_repos" {
  description = "Comma-separated org/repo pairs seeded for new users as sample data"
  type        = string
  default     = "containerd/containerd,etcd-io/etcd,grafana/grafana,prometheus/prometheus"
}

# --- Shared infrastructure (from thingzio/infra) ---

variable "vpc_id" {
  description = "Shared VPC network ID"
  type        = string
}

variable "subnet_id" {
  description = "Shared VPC subnet ID"
  type        = string
}

variable "db_instance_name" {
  description = "Shared Cloud SQL instance name"
  type        = string
}

variable "db_connection_name" {
  description = "Shared Cloud SQL connection string (project:region:instance)"
  type        = string
}

variable "db_name" {
  description = "Database name within the shared Cloud SQL instance"
  type        = string
}
