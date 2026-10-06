output "instance_groups" {
  description = "Regional managed instance group per region."
  value       = { for r, g in google_compute_region_instance_group_manager.worker : r => g.instance_group }
}

output "service_account" {
  description = "Service account the workers run as."
  value       = google_service_account.worker.email
}

output "join_token_secret" {
  description = "Secret Manager secret holding the join token."
  value       = google_secret_manager_secret.join_token.id
}
