output "autoscaling_groups" {
  description = "Worker Auto Scaling group per region."
  value = merge(
    { for m in module.us_east_1 : "us-east-1" => m.autoscaling_group },
    { for m in module.eu_west_1 : "eu-west-1" => m.autoscaling_group },
    { for m in module.ap_south_1 : "ap-south-1" => m.autoscaling_group },
  )
}
