terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic (condition pre-resolved to a
# boolean, since this provider-defined function takes the already-evaluated
# boolean value rather than the name of a condition in the template's
# Conditions section):
#   "InstanceType": {
#     "Fn::If": ["CreateProdResources", "c5.xlarge", "t3.small"]
#   }
output "example" {
  value = provider::cfncompat::condition_if(true, "c5.xlarge", "t3.small")
}
