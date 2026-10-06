variable "server_address" {
  description = "host:port of the Stampede server's worker port (8081 by default), reachable from every region."
  type        = string
  validation {
    condition     = can(regex("^[^:/]+:[0-9]+$", var.server_address))
    error_message = "Use host:port, for example stampede.example.com:8081."
  }
}

variable "join_token" {
  description = "The server's join token (STAMPEDE_JOIN_TOKEN). Stored as an SSM SecureString in each region."
  type        = string
  sensitive   = true
}

variable "workers_per_region" {
  description = "Number of workers per region, for example { \"us-east-1\" = 2, \"ap-south-1\" = 1 }. Supported regions: us-east-1, eu-west-1, ap-south-1 (see main.tf to add more)."
  type        = map(number)
  validation {
    condition     = alltrue([for r in keys(var.workers_per_region) : contains(["us-east-1", "eu-west-1", "ap-south-1"], r)])
    error_message = "Supported regions are us-east-1, eu-west-1 and ap-south-1; add a provider and module block in main.tf for others."
  }
  validation {
    condition     = alltrue([for n in values(var.workers_per_region) : n >= 0 && floor(n) == n])
    error_message = "Worker counts must be whole numbers >= 0."
  }
}

variable "subnet_ids" {
  description = "Subnets per region. Regions not listed use the default VPC's default subnets."
  type        = map(list(string))
  default     = {}
}

variable "instance_type" {
  description = "EC2 instance type. Load generation is CPU and network bound; compute-optimised types (c7g/c7i) suit it."
  type        = string
  default     = "c7g.large"
}

variable "architecture" {
  description = "CPU architecture of instance_type: arm64 (Graviton) or x86_64."
  type        = string
  default     = "arm64"
  validation {
    condition     = contains(["arm64", "x86_64"], var.architecture)
    error_message = "architecture must be arm64 or x86_64."
  }
}

variable "stampede_version" {
  description = "Stampede release to install, without the leading v (for example 0.1.0)."
  type        = string
}

variable "public_ip" {
  description = "Give workers public IPs for outbound traffic. Set false in subnets with a NAT gateway."
  type        = bool
  default     = true
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
  description = "Extra worker labels (--label KEY=VALUE). cloud=aws is always set."
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

variable "tags" {
  description = "Extra tags for the worker instances."
  type        = map(string)
  default     = {}
}
