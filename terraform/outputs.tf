output "cluster_name" {
  value = google_container_cluster.platform.name
}

output "get_credentials" {
  value = "gcloud container clusters get-credentials ${google_container_cluster.platform.name} --region ${var.region} --project ${var.project_id}"
}

output "artifact_registry" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.apps.repository_id}"
}

output "app_service_account" {
  value = google_service_account.app.email
}

output "ci_service_account" {
  value = google_service_account.ci.email
}

output "workload_identity_provider" {
  description = "Set as GitHub Actions secret GCP_WORKLOAD_IDENTITY_PROVIDER."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "dashboard_url" {
  value = "https://console.cloud.google.com/monitoring/dashboards/builder/${element(split("/", google_monitoring_dashboard.api.id), length(split("/", google_monitoring_dashboard.api.id)) - 1)}?project=${var.project_id}"
}

output "slo_url" {
  value = "https://console.cloud.google.com/monitoring/services/${google_monitoring_custom_service.api.service_id}?project=${var.project_id}"
}
