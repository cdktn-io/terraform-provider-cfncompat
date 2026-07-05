terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation rule-specific intrinsic (with
# Fn::ValueOfAll pre-resolved to its list-of-strings result):
#   "Fn::EachMemberEquals" : [
#     {"Fn::ValueOfAll" : ["AWS::EC2::VPC::Id", "Tags.Department"]}, "IT"
#   ]
output "example" {
  value = provider::cfncompat::condition_each_member_equals(["IT", "IT", "IT"], "IT")
}
