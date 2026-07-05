terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

# Equivalent to the CloudFormation intrinsic function:
#   { "Fn::Base64" : "AWS CloudFormation" }
output "example" {
  value = provider::cfncompat::base64("AWS CloudFormation")
}
