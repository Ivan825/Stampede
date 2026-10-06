# Stampede workers in several AWS regions, all joining one Stampede server.
#
# Terraform cannot create providers in a loop, so each supported region has
# its own provider alias and module block below; a region with 0 workers in
# workers_per_region creates nothing. To add a region, copy one provider and
# module pair and add the region to local.supported_regions.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0, < 7.0"
    }
  }
}

locals {
  supported_regions = ["us-east-1", "eu-west-1", "ap-south-1"]

  common = {
    server_address   = var.server_address
    join_token       = var.join_token
    instance_type    = var.instance_type
    architecture     = var.architecture
    stampede_version = var.stampede_version
    public_ip        = var.public_ip
    insecure         = var.insecure
    server_ca_pem    = var.server_ca_pem
    mtls             = var.mtls
    ca_fingerprint   = var.ca_fingerprint
    worker_labels    = var.worker_labels
    max_vus          = var.max_vus
    name_prefix      = var.name_prefix
    tags             = var.tags
  }
}

provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}

provider "aws" {
  alias  = "eu_west_1"
  region = "eu-west-1"
}

provider "aws" {
  alias  = "ap_south_1"
  region = "ap-south-1"
}

module "us_east_1" {
  source    = "./modules/region"
  count     = lookup(var.workers_per_region, "us-east-1", 0) > 0 ? 1 : 0
  providers = { aws = aws.us_east_1 }

  worker_count     = var.workers_per_region["us-east-1"]
  subnet_ids       = lookup(var.subnet_ids, "us-east-1", [])
  server_address   = local.common.server_address
  join_token       = local.common.join_token
  instance_type    = local.common.instance_type
  architecture     = local.common.architecture
  stampede_version = local.common.stampede_version
  public_ip        = local.common.public_ip
  insecure         = local.common.insecure
  server_ca_pem    = local.common.server_ca_pem
  mtls             = local.common.mtls
  ca_fingerprint   = local.common.ca_fingerprint
  worker_labels    = local.common.worker_labels
  max_vus          = local.common.max_vus
  name_prefix      = local.common.name_prefix
  tags             = local.common.tags
}

module "eu_west_1" {
  source    = "./modules/region"
  count     = lookup(var.workers_per_region, "eu-west-1", 0) > 0 ? 1 : 0
  providers = { aws = aws.eu_west_1 }

  worker_count     = var.workers_per_region["eu-west-1"]
  subnet_ids       = lookup(var.subnet_ids, "eu-west-1", [])
  server_address   = local.common.server_address
  join_token       = local.common.join_token
  instance_type    = local.common.instance_type
  architecture     = local.common.architecture
  stampede_version = local.common.stampede_version
  public_ip        = local.common.public_ip
  insecure         = local.common.insecure
  server_ca_pem    = local.common.server_ca_pem
  mtls             = local.common.mtls
  ca_fingerprint   = local.common.ca_fingerprint
  worker_labels    = local.common.worker_labels
  max_vus          = local.common.max_vus
  name_prefix      = local.common.name_prefix
  tags             = local.common.tags
}

module "ap_south_1" {
  source    = "./modules/region"
  count     = lookup(var.workers_per_region, "ap-south-1", 0) > 0 ? 1 : 0
  providers = { aws = aws.ap_south_1 }

  worker_count     = var.workers_per_region["ap-south-1"]
  subnet_ids       = lookup(var.subnet_ids, "ap-south-1", [])
  server_address   = local.common.server_address
  join_token       = local.common.join_token
  instance_type    = local.common.instance_type
  architecture     = local.common.architecture
  stampede_version = local.common.stampede_version
  public_ip        = local.common.public_ip
  insecure         = local.common.insecure
  server_ca_pem    = local.common.server_ca_pem
  mtls             = local.common.mtls
  ca_fingerprint   = local.common.ca_fingerprint
  worker_labels    = local.common.worker_labels
  max_vus          = local.common.max_vus
  name_prefix      = local.common.name_prefix
  tags             = local.common.tags
}
