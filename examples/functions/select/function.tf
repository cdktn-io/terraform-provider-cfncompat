terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic:
#   { "Fn::Select" : [ "1", [ "apples", "grapes", "oranges", "mangoes" ] ] }
output "example" {
  value = provider::cfncompat::select(1, ["apples", "grapes", "oranges", "mangoes"])
}
