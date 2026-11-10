variable "project_id" {
  type = string
}

variable "region" {
  type    = string
  default = "us-central1"
}

variable "cluster_name" {
  type    = string
  default = "platform"
}

variable "authorized_cidr" {
  description = "CIDR allowed to reach the GKE control plane (your public IP/32)."
  type        = string
}

variable "github_repo" {
  description = "GitHub repository (owner/name) allowed to use Workload Identity Federation for CI."
  type        = string
  default     = "bhavishyajain545/gcp-gke-platform"
}

variable "alert_email" {
  description = "Email for SLO burn-rate alerts."
  type        = string
}

variable "app_namespace" {
  type    = string
  default = "url-health"
}

variable "app_ksa" {
  description = "Kubernetes service account name used by the app (bound to the GCP SA via Workload Identity)."
  type        = string
  default     = "url-health-api"
}
