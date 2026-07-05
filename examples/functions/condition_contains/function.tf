terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation rule-specific intrinsic:
#   "Fn::Contains" : [ ["t3.large", "t3.small"], {"Ref" : "InstanceType"} ]
#
# In a CloudFormation template, Fn::Contains is only valid within the
# RuleCondition or Assert fields of the template's Rules section. This
# provider function has no such placement restriction.
output "example" {
  value = provider::cfncompat::condition_contains(["t3.large", "t3.small"], "t3.large")
}
