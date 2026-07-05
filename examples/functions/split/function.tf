terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic:
#   "Fn::Split" : [ "|" , "a|b|c" ]
output "example" {
  value = provider::cfncompat::split("|", "a|b|c")
}
