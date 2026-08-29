terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {
  region = "us-east-1"
}

# Fn::GetAZs "" — the availability zones of the provider's own region.
data "cfncompat_availability_zones" "current" {}

# Fn::GetAZs "eu-west-1" — an explicit region.
data "cfncompat_availability_zones" "euw1" {
  region = "eu-west-1"
}

locals {
  # Fn::Select(0, Fn::GetAZs "") / Fn::Select(1, Fn::GetAZs "") — the pair
  # aws-cdk-lib returns for an environment-agnostic stack.
  az0 = provider::cfncompat::select(0, data.cfncompat_availability_zones.current.names)
  az1 = provider::cfncompat::select(1, data.cfncompat_availability_zones.current.names)
}

# `names` is the Fn::GetAZs value: available zones restricted to those with a
# default subnet (CloudFormation's documented EC2-VPC behaviour), falling back
# to every available zone. `all_names` is always every available zone, and
# `zone_ids` are the zone ids aligned with it.
output "availability_zone_names" {
  value = data.cfncompat_availability_zones.current.names
}

output "availability_zone_ids" {
  value = data.cfncompat_availability_zones.current.zone_ids
}
