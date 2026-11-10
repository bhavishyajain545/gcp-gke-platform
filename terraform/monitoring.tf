# Managed Prometheus scrapes the app (PodMonitoring in k8s/base); the metrics
# land in Cloud Monitoring as prometheus.googleapis.com/<name>/<kind>. The SLOs
# below are defined on those metrics, with a multi-window burn-rate alert.

resource "google_monitoring_notification_channel" "email" {
  display_name = "SLO alerts"
  type         = "email"
  labels = {
    email_address = var.alert_email
  }
  depends_on = [google_project_service.apis]
}

resource "google_monitoring_custom_service" "api" {
  service_id   = "url-health-api"
  display_name = "url-health-api"
  depends_on   = [google_project_service.apis]
}

locals {
  prom_filter   = "resource.type=\"prometheus_target\" resource.labels.namespace=\"${var.app_namespace}\""
  latency_hist  = "metric.type=\"prometheus.googleapis.com/http_request_duration_seconds/histogram\" ${local.prom_filter} metric.labels.route=\"/checks\""
  requests_cnt  = "metric.type=\"prometheus.googleapis.com/http_requests_total/counter\" ${local.prom_filter} metric.labels.route=\"/checks\""
  requests_good = "${local.requests_cnt} metric.labels.code!=monitoring.regex.full_match(\"5..\")"
}

# 99% of /checks requests complete in under 300 ms (rolling 28 days).
resource "google_monitoring_slo" "latency" {
  service      = google_monitoring_custom_service.api.service_id
  slo_id       = "checks-latency-p99-300ms"
  display_name = "99% of /checks under 300ms"
  goal         = 0.99

  rolling_period_days = 28

  request_based_sli {
    distribution_cut {
      distribution_filter = local.latency_hist
      range {
        min = 0
        max = 0.3
      }
    }
  }
}

# 99.5% of /checks requests are not 5xx (rolling 28 days).
resource "google_monitoring_slo" "availability" {
  service      = google_monitoring_custom_service.api.service_id
  slo_id       = "checks-availability"
  display_name = "99.5% of /checks non-5xx"
  goal         = 0.995

  rolling_period_days = 28

  request_based_sli {
    good_total_ratio {
      good_service_filter  = local.requests_good
      total_service_filter = local.requests_cnt
    }
  }
}

# Multi-window burn-rate alert: page when the error budget burns 14.4x (1h window)
# or 6x (6h window). Classic SRE workbook thresholds for a 28-day budget.
resource "google_monitoring_alert_policy" "latency_burn" {
  display_name = "url-health-api latency SLO burn rate"
  combiner     = "OR"

  conditions {
    display_name = "fast burn (1h > 14.4x)"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${google_monitoring_slo.latency.name}\", \"3600s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = 14.4
      duration        = "0s"
    }
  }

  conditions {
    display_name = "slow burn (6h > 6x)"
    condition_threshold {
      filter          = "select_slo_burn_rate(\"${google_monitoring_slo.latency.name}\", \"21600s\")"
      comparison      = "COMPARISON_GT"
      threshold_value = 6
      duration        = "0s"
    }
  }

  notification_channels = [google_monitoring_notification_channel.email.id]

  documentation {
    content   = "The /checks latency SLO is burning its error budget. Check pod CPU throttling (HPA), Firestore latency and recent deploys in ArgoCD."
    mime_type = "text/markdown"
  }
}

resource "google_monitoring_dashboard" "api" {
  dashboard_json = jsonencode({
    displayName = "url-health-api"
    mosaicLayout = {
      columns = 12
      tiles = [
        {
          xPos = 0, yPos = 0, width = 6, height = 4
          widget = {
            title = "/checks p99 latency (s)"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  prometheusQuery = "histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{route=\"/checks\"}[5m])))"
                }
                plotType = "LINE"
              }]
            }
          }
        },
        {
          xPos = 6, yPos = 0, width = 6, height = 4
          widget = {
            title = "Requests/s by status code"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  prometheusQuery = "sum by (code) (rate(http_requests_total[5m]))"
                }
                plotType = "STACKED_AREA"
              }]
            }
          }
        },
        {
          xPos = 0, yPos = 4, width = 6, height = 4
          widget = {
            title = "Monitored URLs up (1) / down (0)"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  prometheusQuery = "max by (url) (url_check_up)"
                }
                plotType = "LINE"
              }]
            }
          }
        },
        {
          xPos = 6, yPos = 4, width = 6, height = 4
          widget = {
            title = "Probe latency p95 by URL (s)"
            xyChart = {
              dataSets = [{
                timeSeriesQuery = {
                  prometheusQuery = "histogram_quantile(0.95, sum by (url, le) (rate(url_check_latency_seconds_bucket[5m])))"
                }
                plotType = "LINE"
              }]
            }
          }
        },
      ]
    }
  })
}
