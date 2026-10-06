variable "server_address" {
  description = "host:port of the Stampede server's worker port."
  type        = string
}

variable "join_token" {
  description = "The server's join token."
  type        = string
  sensitive   = true
}

variable "worker_count" {
  description = "Number of workers in this region."
  type        = number
}

variable "instance_type" {
  description = "EC2 instance type; must match architecture."
  type        = string
}

variable "architecture" {
  description = "arm64 or x86_64."
  type        = string
}

variable "stampede_version" {
  description = "Stampede release, without the leading v."
  type        = string
}

variable "subnet_ids" {
  description = "Subnets for the workers; empty uses the default VPC's default subnets."
  type        = list(string)
  default     = []
}

variable "public_ip" {
  description = "Give workers public IPs (needed without a NAT gateway)."
  type        = bool
  default     = true
}

variable "insecure" {
  description = "Connect without TLS."
  type        = bool
  default     = false
}

variable "server_ca_pem" {
  description = "PEM of the CA for the server certificate."
  type        = string
  default     = ""
}

variable "worker_labels" {
  description = "Extra worker labels."
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
}

variable "tags" {
  description = "Tags for the instances."
  type        = map(string)
  default     = {}
}
