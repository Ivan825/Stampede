# Stampede workers on Google Cloud: one regional managed instance group per
# region, all joining one Stampede server. Workers only dial out (to the
# server's worker port and to the system under test), so no inbound firewall
# rule is created.
#
# The join token is stored in Secret Manager and read at boot by a dedicated
# service account; it never appears in instance metadata.

terraform {
  required_version = ">= 1.6"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 6.0, < 8.0"
    }
  }
}

provider "google" {
  project = var.project_id
}

locals {
  name   = var.name_prefix
  labels = merge({ cloud = "gcp" }, var.worker_labels)

  # Secret Manager read through the metadata server's access token, so the
  # image needs only curl and python3 (both in Debian images).
  fetch_token = <<-EOT
    fetch_token() {
      local at
      at=$(curl -fsS -H 'Metadata-Flavor: Google' \
        'http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token' |
        python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
      curl -fsS -H "Authorization: Bearer $at" \
        'https://secretmanager.googleapis.com/v1/${google_secret_manager_secret.join_token.id}/versions/latest:access' |
        python3 -c 'import base64,json,sys; print(base64.b64decode(json.load(sys.stdin)["payload"]["data"]).decode())'
    }
  EOT

  tls_flag     = var.insecure ? "--insecure" : (var.server_ca_pem != "" ? "--ca /etc/stampede/ca.pem" : (var.mtls ? "--mtls${var.ca_fingerprint != "" ? " --ca-fingerprint ${var.ca_fingerprint}" : ""}" : ""))
  label_flags  = join("", [for k, v in local.labels : " --label ${k}=${v}"])
  max_vus_flag = var.max_vus > 0 ? " --max-vus ${var.max_vus}" : ""
}

resource "google_project_service" "secretmanager" {
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

resource "google_secret_manager_secret" "join_token" {
  secret_id = "${local.name}-join-token"
  replication {
    auto {}
  }
  depends_on = [google_project_service.secretmanager]
}

resource "google_secret_manager_secret_version" "join_token" {
  secret      = google_secret_manager_secret.join_token.id
  secret_data = var.join_token
}

resource "google_service_account" "worker" {
  account_id   = "${local.name}-worker"
  display_name = "Stampede worker"
}

resource "google_secret_manager_secret_iam_member" "worker_reads_token" {
  secret_id = google_secret_manager_secret.join_token.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.worker.email}"
}

resource "google_compute_instance_template" "worker" {
  for_each     = var.workers_per_region
  name_prefix  = "${local.name}-${each.key}-"
  machine_type = var.machine_type
  region       = each.key
  tags         = ["stampede-worker"]
  labels       = local.labels

  disk {
    source_image = var.image
    auto_delete  = true
    boot         = true
    disk_size_gb = 10
  }

  network_interface {
    network    = var.network
    subnetwork = var.subnetworks[each.key]
    # An ephemeral public IP for outbound traffic; drop it when the
    # subnetwork has Cloud NAT.
    dynamic "access_config" {
      for_each = var.public_ip ? [1] : []
      content {}
    }
  }

  service_account {
    email  = google_service_account.worker.email
    scopes = ["cloud-platform"]
  }

  shielded_instance_config {
    enable_secure_boot = true
  }

  metadata = {
    enable-oslogin = "TRUE"
    startup-script = templatefile("${path.module}/../templates/worker-startup.sh.tftpl", {
      stampede_version = var.stampede_version
      server_address   = var.server_address
      region           = each.key
      fetch_token      = local.fetch_token
      ca_pem           = var.server_ca_pem
      tls_flag         = local.tls_flag
      label_flags      = local.label_flags
      max_vus_flag     = local.max_vus_flag
    })
  }

  lifecycle {
    create_before_destroy = true
  }

  depends_on = [google_secret_manager_secret_version.join_token, google_secret_manager_secret_iam_member.worker_reads_token]
}

resource "google_compute_region_instance_group_manager" "worker" {
  for_each           = var.workers_per_region
  name               = "${local.name}-${each.key}"
  region             = each.key
  base_instance_name = "${local.name}-${each.key}"
  target_size        = each.value

  version {
    instance_template = google_compute_instance_template.worker[each.key].self_link_unique
  }

  update_policy {
    type                  = "PROACTIVE"
    minimal_action        = "REPLACE"
    max_surge_fixed       = 3
    max_unavailable_fixed = 0
  }
}
