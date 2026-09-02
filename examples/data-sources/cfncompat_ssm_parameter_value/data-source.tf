# Mode (b), value_type SET -- typed template-parameter semantics.
# AWS::SSM::Parameter::Value<AWS::EC2::Image::Id> is the shape every
# MachineImage.latestAmazonLinux* in aws-cdk-lib emits. The synthesis backend
# elides the CfnParameter entirely and emits this data source keyed on the
# parameter's Default. The parameter's SSM type must be String, and the
# resolved value is checked to be a real AMI in this account and region --
# exactly the two checks CloudFormation performs.
data "cfncompat_ssm_parameter_value" "amazon_linux_ami" {
  name       = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
  value_type = "AWS::EC2::Image::Id"
}

# Mode (a), value_type UNSET -- dynamic-reference semantics, i.e.
# {{resolve:ssm:golden-ami:2}}. A String or a StringList parameter is accepted
# and `value` is the raw stored string. `version` pins the read the way the
# reference's version segment does.
data "cfncompat_ssm_parameter_value" "golden_ami" {
  name    = "golden-ami"
  version = 2
}

# `validate = false` keeps the syntactic check but makes no ec2:DescribeVpcs
# call -- useful when the plan runs without EC2 read permissions.
data "cfncompat_ssm_parameter_value" "vpc" {
  name       = "/network/vpc-id"
  value_type = "AWS::EC2::VPC::Id"
  validate   = false
}

# allowed_pattern and allowed_values are a cfncompat EXTENSION, not a
# CloudFormation polyfill: CloudFormation's AllowedPattern on an SSM-typed
# Parameter validates the parameter NAME, never the resolved value. A
# synthesis backend must not map CfnParameter constraints onto them.
data "cfncompat_ssm_parameter_value" "environment" {
  name            = "/app/environment"
  allowed_values  = ["dev", "staging", "prod"]
  allowed_pattern = "[a-z]+"
}

output "ami_id" {
  value = data.cfncompat_ssm_parameter_value.amazon_linux_ami.value
}

# Terraform re-reads on every plan, which is faithful: CloudFormation
# re-resolves SSM parameters and {{resolve:ssm}} references on every stack
# operation too. Feed resolved_version back into `version` to pin.
output "ami_parameter_version" {
  value = data.cfncompat_ssm_parameter_value.amazon_linux_ami.resolved_version
}
