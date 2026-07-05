terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic:
#   { "Fn::Cidr" : [ "192.168.0.0/24", "6", "5"] }
#
# Splits the /24 ip_block into 6 consecutive /27 subnets (cidr_bits = 5,
# so the resulting prefix length is 32 - 5 = 27).
output "example" {
  value = provider::cfncompat::cidr("192.168.0.0/24", 6, 5)
}
