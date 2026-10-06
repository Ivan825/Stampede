# Stampede workers in one AWS region: an Auto Scaling group of Amazon Linux
# 2023 instances that install a Stampede release and run `stampede worker`.
# The join token is an SSM SecureString read at boot by the instance role;
# it never appears in user data.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

data "aws_region" "current" {}

locals {
  region = data.aws_region.current.region
  name   = "${var.name_prefix}-${local.region}"
  labels = merge({ cloud = "aws" }, var.worker_labels)

  fetch_token = <<-EOT
    fetch_token() {
      aws ssm get-parameter --region ${local.region} --name '${aws_ssm_parameter.join_token.name}' \
        --with-decryption --query Parameter.Value --output text
    }
  EOT

  tls_flag     = var.insecure ? "--insecure" : (var.server_ca_pem != "" ? "--ca /etc/stampede/ca.pem" : (var.mtls ? "--mtls${var.ca_fingerprint != "" ? " --ca-fingerprint ${var.ca_fingerprint}" : ""}" : ""))
  label_flags  = join("", [for k, v in local.labels : " --label ${k}=${v}"])
  max_vus_flag = var.max_vus > 0 ? " --max-vus ${var.max_vus}" : ""
}

data "aws_ssm_parameter" "ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-${var.architecture}"
}

data "aws_vpc" "default" {
  count   = length(var.subnet_ids) == 0 ? 1 : 0
  default = true
}

data "aws_subnets" "default" {
  count = length(var.subnet_ids) == 0 ? 1 : 0
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default[0].id]
  }
  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

data "aws_subnet" "first" {
  id = local.subnet_ids[0]
}

locals {
  subnet_ids = length(var.subnet_ids) > 0 ? var.subnet_ids : data.aws_subnets.default[0].ids
}

resource "aws_ssm_parameter" "join_token" {
  name  = "/${var.name_prefix}/join-token"
  type  = "SecureString"
  value = var.join_token
}

data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "read_token" {
  statement {
    actions   = ["ssm:GetParameter"]
    resources = [aws_ssm_parameter.join_token.arn]
  }
}

resource "aws_iam_role" "worker" {
  name               = "${local.name}-worker"
  assume_role_policy = data.aws_iam_policy_document.assume.json
}

resource "aws_iam_role_policy" "read_token" {
  name   = "read-join-token"
  role   = aws_iam_role.worker.id
  policy = data.aws_iam_policy_document.read_token.json
}

resource "aws_iam_instance_profile" "worker" {
  name = "${local.name}-worker"
  role = aws_iam_role.worker.name
}

# Workers only make outbound connections.
resource "aws_security_group" "worker" {
  name        = "${local.name}-worker"
  description = "Stampede workers: outbound only"
  vpc_id      = data.aws_subnet.first.vpc_id

  egress {
    description = "server worker port and the system under test"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_launch_template" "worker" {
  name_prefix   = "${local.name}-worker-"
  image_id      = data.aws_ssm_parameter.ami.insecure_value
  instance_type = var.instance_type

  iam_instance_profile {
    arn = aws_iam_instance_profile.worker.arn
  }

  network_interfaces {
    associate_public_ip_address = var.public_ip
    security_groups             = [aws_security_group.worker.id]
  }

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }

  user_data = base64encode(templatefile("${path.module}/../../../templates/worker-startup.sh.tftpl", {
    stampede_version = var.stampede_version
    server_address   = var.server_address
    region           = local.region
    fetch_token      = local.fetch_token
    ca_pem           = var.server_ca_pem
    tls_flag         = local.tls_flag
    label_flags      = local.label_flags
    max_vus_flag     = local.max_vus_flag
  }))

  tag_specifications {
    resource_type = "instance"
    tags = merge(var.tags, {
      Name             = "${local.name}-worker"
      "stampede:role"  = "worker"
      "stampede:cloud" = "aws"
    })
  }
}

resource "aws_autoscaling_group" "worker" {
  name                = "${local.name}-workers"
  min_size            = var.worker_count
  max_size            = var.worker_count
  desired_capacity    = var.worker_count
  vpc_zone_identifier = local.subnet_ids
  health_check_type   = "EC2"

  launch_template {
    id      = aws_launch_template.worker.id
    version = aws_launch_template.worker.latest_version
  }

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 50
    }
  }

  tag {
    key                 = "stampede:role"
    value               = "worker"
    propagate_at_launch = false
  }
}
