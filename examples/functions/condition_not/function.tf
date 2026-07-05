terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic (condition pre-resolved to a
# boolean, since provider-defined functions take a single boolean argument
# rather than a single-element condition list):
#   "Fn::Not" : [{"Fn::Equals" : [{"Ref" : "EnvironmentType"}, "prod"]}]
output "example" {
  value = provider::cfncompat::condition_not(false)
}
