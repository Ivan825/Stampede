variable "project_id" {
  description = "Google Cloud project to create the workers in."
  type        = string
}

variable "server_address" {
  description = "host:port of the Stampede server's worker port (8081 by default), reachable from every region."
  type        = string
  validation {
    condition     = can(regex("^[^:/]+:[0-9]+$", var.server_address))
    error_message = "Use host:port, for example stampede.example.com:8081."
  }
}

variable "join_token" {
  description = "The server's join token (STAMPEDE_JOIN_TOKEN). Stored in Secret Manager."
  type        = string
  sensitive   = true
}

variable "workers_per_region" {
  description = "Number of workers in each region, for example { \"europe-west1\" = 2, \"asia-south1\" = 1 }. Each worker reports its region to the server."
  type        = map(number)
  validation {
    condition     = alltrue([for n in values(var.workers_per_region) : n >= 0 && floor(n) == n])
    error_message = "Worker counts must be whole numbers >= 0."
  }
}

variable "subnetworks" {
  description = "Subnetwork self link or name per region, matching the keys of workers_per_region. With the default network use the region's auto subnetwork name, \"default\"."
  type        = map(string)
}

variable "network" {
  description = "VPC network."
  type        = string
  default     = "default"
}

variable "public_ip" {
  description = "Give each worker an ephemeral public IP for outbound traffic. Set false when the subnetworks have Cloud NAT."
  type        = bool
  default     = true
}

variable "machine_type" {
  description = "Machine type. Load generation is CPU and network bound; c3/c4 or n2 types with 2-8 vCPUs suit most tests."
  type        = string
  default     = "e2-standard-2"
}

variable "image" {
  description = "Boot image. The startup script needs bash, curl, python3 and systemd (Debian and Ubuntu images have them)."
  type        = string
  default     = "debian-cloud/debian-12"
}

variable "stampede_version" {
  description = "Stampede release to install, without the leading v (for example 0.1.0)."
  type        = string
}

variable "insecure" {
  description = "Connect to the server without TLS. Only for trusted private networks: the join token travels in clear text."
  type        = bool
  default     = false
}

variable "server_ca_pem" {
  description = "PEM of the CA that signed the server's worker-port certificate, when it is not publicly trusted."
  type        = string
  default     = ""
}

variable "mtls" {
  description = "Enroll with the server's built-in CA and connect with the worker's own certificate (--mtls). Needs a server started with --worker-mtls, the default in Docker Compose and the Helm chart. Ignored when insecure or server_ca_pem is set."
  type        = bool
  default     = true
}

variable "ca_fingerprint" {
  description = "With mtls, the server CA's fingerprint (sha256:...) from the server log. Pins the CA from the first enrollment."
  type        = string
  default     = ""
}

variable "worker_labels" {
  description = "Extra worker labels (--label KEY=VALUE). cloud=gcp is always set. Also applied as GCE labels, so keep to lowercase letters, digits, - and _."
  type        = map(string)
  default     = {}
}

variable "max_vus" {
  description = "Most virtual users one worker accepts (0 = no limit)."
  type        = number
  default     = 0
}

variable "name_prefix" {
  description = "Prefix for resource names."
  type        = string
  default     = "stampede"
}
