terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {}

# Equivalent to the CloudFormation rule-specific intrinsic (with
# Fn::ValueOfAll/Fn::RefAll pre-resolved to concrete string lists):
#   "Fn::EachMemberIn" : [
#     {"Fn::ValueOfAll" : ["AWS::EC2::Subnet::Id", "VpcId"]}, {"Fn::RefAll" : "AWS::EC2::VPC::Id"}
#   ]
output "example" {
  value = provider::cfncompat::condition_each_member_in(
    ["vpc-1111", "vpc-2222"],
    ["vpc-1111", "vpc-2222", "vpc-3333"],
  )
}
