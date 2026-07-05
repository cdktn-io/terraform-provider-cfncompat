terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic (conditions pre-resolved to
# booleans, since provider-defined functions take boolean arguments rather
# than references to named Conditions section entries):
#   "MyOrCondition" : {
#      "Fn::Or" : [
#         {"Fn::Equals" : ["sg-mysggroup", {"Ref" : "ASecurityGroup"}]},
#         {"Condition" : "SomeOtherCondition"}
#      ]
#   }
output "example" {
  value = provider::cfncompat::condition_or(false, true)
}
