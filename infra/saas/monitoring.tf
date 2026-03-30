resource "google_monitoring_uptime_check_config" "serve" {
  display_name = "${var.prefix}-uptime"
  timeout      = "10s"
  period       = "300s"
  project      = var.project_id

  http_check {
    path         = "/"
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
  filter  = "resource.type=\"cloud_run_revision\" jsonPayload.msg=\"user signed in\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

resource "google_logging_metric" "tos_accepted" {
  name    = "${var.prefix}-tos-accepted"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" jsonPayload.msg=\"tos accepted\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

resource "google_logging_metric" "import_duration" {
  name    = "${var.prefix}-import-duration"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" jsonPayload.msg=\"import worker complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "s"
  }

  value_extractor = "EXTRACT(jsonPayload.duration)"

  bucket_options {
    explicit_buckets {
      bounds = [60, 300, 600, 1800, 3600]
    }
  }
}

resource "google_logging_metric" "import_tenant_count" {
  name    = "${var.prefix}-import-tenant-count"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" jsonPayload.msg=\"import worker complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "1"
  }

  value_extractor = "EXTRACT(jsonPayload.tenants)"

  bucket_options {
    explicit_buckets {
      bounds = [10, 50, 100, 200, 500, 1000]
    }
  }
}

resource "google_logging_metric" "tenant_weekly_events" {
  name    = "${var.prefix}-tenant-weekly-events"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" jsonPayload.msg=\"tenant usage\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "1"

    labels {
      key         = "tenant_id"
      value_type  = "STRING"
      description = "Tenant ID"
    }
  }

  value_extractor = "EXTRACT(jsonPayload.weekly_events)"

  bucket_options {
    explicit_buckets {
      bounds = [100, 500, 1000, 2000, 5000, 10000, 20000]
    }
  }

  label_extractors = {
    "tenant_id" = "EXTRACT(jsonPayload.tenant_id)"
  }
}

resource "google_logging_metric" "event_limit_reached" {
  name    = "${var.prefix}-event-limit-reached"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_job\" jsonPayload.msg=\"weekly event limit reached\""

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

resource "google_logging_metric" "upgrade_requests" {
  name    = "${var.prefix}-upgrade-requests"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" jsonPayload.msg=\"upgrade requested\""

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
