terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic (requires the AWS::LanguageExtensions
# transform in a template; this provider function is a pure serializer):
#   "Fn::ToJsonString" : { "key1": "value1", "key2": "resolvedValue" }
output "example" {
  value = provider::cfncompat::to_json_string({
    key1 = "value1"
    key2 = "resolvedValue"
  })
}
