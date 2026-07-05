terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic (requires the
# AWS::LanguageExtensions transform):
#   { "Fn::Length" : { "Fn::Split": ["|", "a|b|c"] } }
output "example" {
  value = provider::cfncompat::length(provider::cfncompat::split("|", "a|b|c"))
}
