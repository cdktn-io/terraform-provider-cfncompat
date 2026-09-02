# parse_dynamic_reference is for references whose text is NOT known at
# synthesis time. A synthesis backend translating aws-cdk-lib sees dynamic
# references as structured tokens and wires their parts straight onto data
# source arguments -- no function involved. This function earns its place when
# only Go logic operating on the plan-time value can split the string: a
# deploy-time variable, another resource's attribute, or an escape hatch such
# as CfnResource.addPropertyOverride.

variable "image_reference" {
  type        = string
  description = "An AMI id, or a CloudFormation dynamic reference that resolves to one."
  default     = "{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64}}"
}

locals {
  is_reference = provider::cfncompat::is_dynamic_reference(var.image_reference)
  reference    = local.is_reference ? provider::cfncompat::parse_dynamic_reference(var.image_reference) : null
}

# value_type is left unset: the input was a {{resolve:ssm}} reference, which is
# the dynamic-reference resolution path.
data "cfncompat_ssm_parameter_value" "image" {
  count = local.is_reference ? 1 : 0

  name    = local.reference.name
  version = local.reference.version == null ? null : tonumber(local.reference.version)
}

output "image_id" {
  value = local.is_reference ? data.cfncompat_ssm_parameter_value.image[0].value : var.image_reference
}

# The object's attribute set is fixed, whatever the reference's service is;
# the segments that service has no notion of are null.
output "parsed_secret_reference" {
  value = provider::cfncompat::parse_dynamic_reference(
    "{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret:SecretString:password}}"
  )
  # => {
  #      service       = "secretsmanager"
  #      name          = "arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret"
  #      version       = null
  #      secret_string = "SecretString"
  #      json_key      = "password"
  #      version_stage = null
  #      version_id    = null
  #    }
}
