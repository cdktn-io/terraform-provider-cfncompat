terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

variable "environment_type" {
  type    = string
  default = "prod"
}

# Equivalent to the CloudFormation intrinsic:
#   "IsProduction" : {
#     "Fn::Equals": [{"Ref": "EnvironmentType"}, "prod"]
#   }
output "is_production" {
  value = provider::cfncompat::condition_equals(var.environment_type, "prod")
}

# Numbers compare by their canonical decimal representation, so 1, 1.0, and
# the string "1" all compare equal.
output "example_number_equals_string" {
  value = provider::cfncompat::condition_equals(1, "1")
}
