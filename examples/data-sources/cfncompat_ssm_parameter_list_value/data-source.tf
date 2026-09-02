# AWS::SSM::Parameter::Value<List<AWS::EC2::Subnet::Id>> -- a StringList
# parameter resolved as a real list(string), with every element checked to be
# an existing subnet in one batched ec2:DescribeSubnets call, as CloudFormation
# validates every element of a typed list.
data "cfncompat_ssm_parameter_list_value" "private_subnets" {
  name       = "/network/private-subnet-ids"
  value_type = "List<AWS::EC2::Subnet::Id>"
}

# The Fn::Split(",", {{resolve:ssm:...}}) shape aws-cdk-lib emits in
# StringListParameter.fromStringListParameterName, as one node. The parameter
# must be a StringList: CloudFormation compares the declared SSM type against
# the template's shape and ignores the content, so a String parameter holding
# commas is rejected here too.
data "cfncompat_ssm_parameter_list_value" "allowed_origins" {
  name       = "/app/allowed-origins"
  value_type = "CommaDelimitedList"
}

# `values` are split on commas and whitespace-trimmed, as CloudFormation's
# typed List<...> resolution does ("a,b, c ,d" -> a, b, c, d).
output "subnet_ids" {
  value = data.cfncompat_ssm_parameter_list_value.private_subnets.values
}

# `raw_value` is the stored string verbatim, whitespace included -- precisely
# what a whole-value {{resolve:ssm:...}} reference expands to, which does not
# trim.
output "subnet_ids_raw" {
  value = data.cfncompat_ssm_parameter_list_value.private_subnets.raw_value
}
