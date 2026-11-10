resource "google_firestore_database" "default" {
  name            = "(default)"
  location_id     = "nam5"
  type            = "FIRESTORE_NATIVE"
  deletion_policy = "DELETE"
  depends_on      = [google_project_service.apis]
}
