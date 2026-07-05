terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation intrinsic (with a Mappings section
# declaring RegionMap), plus the standard Fn::FindInMap DefaultValue
# behavior:
#   { "Fn::FindInMap": [ "RegionMap", { "Ref": "AWS::Region" }, "InstanceType", { "DefaultValue": "t3.micro" } ] }
#
# Unlike template-form CloudFormation, this provider function takes the
# mapping itself as the first argument, rather than the logical name of a
# mapping declared in a Mappings section.
output "example" {
  value = provider::cfncompat::find_in_map(
    {
      us-east-1 = { InstanceType = "t3.large" }
      eu-west-1 = { InstanceType = "t3.medium" }
    },
    "us-east-1",
    "InstanceType",
    "t3.micro",
  )
}
