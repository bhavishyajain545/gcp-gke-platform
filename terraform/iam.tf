# ---------------------------------------------------------------------------
# App identity: KSA url-health/url-health-api -> GSA url-health-api (Workload
# Identity). The pod gets Firestore access with no key file.
# ---------------------------------------------------------------------------
resource "google_service_account" "app" {
  account_id   = "url-health-api"
  display_name = "url-health-api (GKE workload identity)"
  depends_on   = [google_project_service.apis]
}

resource "google_project_iam_member" "app_datastore" {
  project = var.project_id
  role    = "roles/datastore.user"
  member  = google_service_account.app.member
}

resource "google_service_account_iam_member" "app_workload_identity" {
  service_account_id = google_service_account.app.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[${var.app_namespace}/${var.app_ksa}]"
  depends_on         = [google_container_cluster.platform]
}

# Autopilot nodes pull images with the Compute Engine default service account.
# New organizations disable automatic role grants for default SAs, so give it
# read-only access to the registry explicitly (nothing else).
data "google_project" "this" {}

resource "google_artifact_registry_repository_iam_member" "nodes_pull" {
  location   = google_artifact_registry_repository.apps.location
  repository = google_artifact_registry_repository.apps.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${data.google_project.this.number}-compute@developer.gserviceaccount.com"
}

# ---------------------------------------------------------------------------
# CI identity for GitHub Actions via Workload Identity Federation: GitHub's
# OIDC token is exchanged for a short-lived GCP token, scoped to one repo.
# ---------------------------------------------------------------------------
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"
  depends_on                = [google_project_service.apis]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-oidc"
  display_name                       = "GitHub OIDC"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
    "attribute.ref"        = "assertion.ref"
  }
  attribute_condition = "assertion.repository == \"${var.github_repo}\""

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "ci" {
  account_id   = "sa-github-ci"
  display_name = "GitHub Actions CI (push images)"
  depends_on   = [google_project_service.apis]
}

resource "google_project_iam_member" "ci_roles" {
  for_each = toset([
    "roles/artifactregistry.writer",
  ])
  project = var.project_id
  role    = each.value
  member  = google_service_account.ci.member
}

resource "google_service_account_iam_member" "ci_wif" {
  service_account_id = google_service_account.ci.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repo}"
}

# ---------------------------------------------------------------------------
# Bootstrap build identity for `gcloud builds submit` (first image before CI exists).
# ---------------------------------------------------------------------------
resource "google_service_account" "build" {
  account_id   = "sa-build"
  display_name = "Cloud Build (bootstrap image build)"
  depends_on   = [google_project_service.apis]
}

resource "google_project_iam_member" "build_roles" {
  for_each = toset([
    "roles/logging.logWriter",
    "roles/artifactregistry.writer",
    "roles/storage.objectViewer",
  ])
  project = var.project_id
  role    = each.value
  member  = google_service_account.build.member
}
