resource "google_monitoring_uptime_check_config" "serve" {
  display_name = "${var.prefix}-uptime"
  timeout      = "10s"
  period       = "600s"
  project      = var.project_id

  http_check {
    path         = "/health"
    port         = 443
    use_ssl      = true
    validate_ssl = true
  }

  monitored_resource {
    type = "uptime_url"
    labels = {
      project_id = var.project_id
      host       = var.domain
    }
  }
}

# Log-based metrics (free tier)

resource "google_logging_metric" "sign_ins" {
  name    = "${var.prefix}-sign-ins"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"user signed in\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

resource "google_logging_metric" "tos_accepted" {
  name    = "${var.prefix}-tos-accepted"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"tos accepted\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

resource "google_logging_metric" "import_duration" {
  name    = "${var.prefix}-import-duration"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"import worker complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "s"
  }

  value_extractor = "EXTRACT(jsonPayload.duration_sec)"

  bucket_options {
    explicit_buckets {
      bounds = [60, 300, 600, 1800, 3600]
    }
  }
}

resource "google_logging_metric" "event_limit_reached" {
  name    = "${var.prefix}-event-limit-reached"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"weekly event limit reached\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "tenant_id"
      value_type  = "STRING"
      description = "Tenant ID"
    }
  }

  label_extractors = {
    "tenant_id" = "EXTRACT(jsonPayload.tenant_id)"
  }
}

# Alert policies

resource "google_monitoring_notification_channel" "email" {
  display_name = "${var.prefix}-email"
  type         = "email"
  project      = var.project_id

  labels = {
    email_address = var.notification_email
  }
}

resource "google_logging_metric" "import_repo_errors" {
  name    = "${var.prefix}-import-repo-errors"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"repo import failed\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "org"
      value_type  = "STRING"
      description = "Organization name"
    }

    labels {
      key         = "repo"
      value_type  = "STRING"
      description = "Repository name"
    }
  }

  label_extractors = {
    "org"  = "EXTRACT(jsonPayload.org)"
    "repo" = "EXTRACT(jsonPayload.repo)"
  }
}

resource "google_logging_metric" "import_repo_skipped_unchanged" {
  name    = "${var.prefix}-import-repo-skipped-unchanged"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"repo unchanged, skipping\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "org"
      value_type  = "STRING"
      description = "Organization name"
    }

    labels {
      key         = "repo"
      value_type  = "STRING"
      description = "Repository name"
    }
  }

  label_extractors = {
    "org"  = "EXTRACT(jsonPayload.org)"
    "repo" = "EXTRACT(jsonPayload.repo)"
  }
}

resource "google_logging_metric" "backfill_rate_limited" {
  name    = "${var.prefix}-backfill-rate-limited"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"backfill rate limited\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "repo"
      value_type  = "STRING"
      description = "Repository (org/repo)"
    }
  }

  label_extractors = {
    "repo" = "EXTRACT(jsonPayload.repo)"
  }
}

resource "google_logging_metric" "backfill_completed" {
  name    = "${var.prefix}-backfill-completed"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"PR backfill complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "1"
  }

  value_extractor = "EXTRACT(jsonPayload.updated)"

  bucket_options {
    explicit_buckets {
      bounds = [1, 10, 50, 100, 200, 500]
    }
  }
}

resource "google_logging_metric" "webhook_installs" {
  name    = "${var.prefix}-webhook-installs"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"installation event\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "action"
      value_type  = "STRING"
      description = "Installation action (created/deleted/suspend)"
    }
  }

  label_extractors = {
    "action" = "EXTRACT(jsonPayload.action)"
  }
}

resource "google_logging_metric" "upgrade_requests" {
  name    = "${var.prefix}-upgrade-requests"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"upgrade requested\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "tenant_id"
      value_type  = "STRING"
      description = "Tenant ID"
    }
  }

  label_extractors = {
    "tenant_id" = "EXTRACT(jsonPayload.tenant_id)"
  }
}

resource "google_monitoring_alert_policy" "upgrade_request" {
  display_name          = "${var.prefix}-upgrade-request"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Upgrade requested"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND metric.type = \"logging.googleapis.com/user/${google_logging_metric.upgrade_requests.name}\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "error_rate" {
  display_name          = "${var.prefix}-error-rate"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Cloud Run 5xx error rate"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${google_cloud_run_v2_service.serve.name}\" AND metric.type = \"run.googleapis.com/request_count\" AND metric.labels.response_code_class = \"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 5
      duration        = "300s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "high_latency" {
  display_name          = "${var.prefix}-high-latency"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Request latency p99 > 1s"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${google_cloud_run_v2_service.serve.name}\" AND metric.type = \"run.googleapis.com/request_latencies\" AND metric.labels.response_code_class = \"2xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 1000
      duration        = "600s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_PERCENTILE_99"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "import_failure" {
  display_name          = "${var.prefix}-import-failure"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Import job failure"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_job\" AND resource.labels.job_name = \"${google_cloud_run_v2_job.import.name}\" AND metric.type = \"run.googleapis.com/job/completed_execution_count\" AND metric.labels.result = \"failed\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "import_repo_errors" {
  display_name          = "${var.prefix}-import-repo-errors"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Import repo errors > 5 per hour"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_job\" AND metric.type = \"logging.googleapis.com/user/devpulse-saas-import-repo-errors\""
      comparison      = "COMPARISON_GT"
      threshold_value = 5
      duration        = "0s"

      aggregations {
        alignment_period     = "3600s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }
}

resource "google_logging_metric" "rate_limit_exceeded" {
  name    = "${var.prefix}-rate-limit-exceeded"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"rate limit exceeded\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "path"
      value_type  = "STRING"
      description = "Request path"
    }
  }

  label_extractors = {
    "path" = "EXTRACT(jsonPayload.path)"
  }
}

resource "google_logging_metric" "insights_generated" {
  name    = "${var.prefix}-insights-generated"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"insights generated\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "org"
      value_type  = "STRING"
      description = "Organization name"
    }

    labels {
      key         = "repo"
      value_type  = "STRING"
      description = "Repository name"
    }
  }

  label_extractors = {
    "org"  = "EXTRACT(jsonPayload.org)"
    "repo" = "EXTRACT(jsonPayload.repo)"
  }
}

resource "google_logging_metric" "import_repo_complete" {
  name    = "${var.prefix}-import-repo-complete"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" resource.labels.job_name=\"${var.prefix}-import\" jsonPayload.msg=\"repo import complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "org"
      value_type  = "STRING"
      description = "Organization name"
    }

    labels {
      key         = "repo"
      value_type  = "STRING"
      description = "Repository name"
    }
  }

  label_extractors = {
    "org"  = "EXTRACT(jsonPayload.org)"
    "repo" = "EXTRACT(jsonPayload.repo)"
  }
}

resource "google_monitoring_dashboard" "ui" {
  project        = var.project_id
  dashboard_json = file("${path.module}/dashboard_ui.json")
}

resource "google_monitoring_dashboard" "import" {
  project        = var.project_id
  dashboard_json = file("${path.module}/dashboard_import.json")
}
