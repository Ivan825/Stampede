output "autoscaling_group" {
  description = "Auto Scaling group of the workers."
  value       = aws_autoscaling_group.worker.name
}

output "join_token_parameter" {
  description = "SSM parameter holding the join token."
  value       = aws_ssm_parameter.join_token.name
}
