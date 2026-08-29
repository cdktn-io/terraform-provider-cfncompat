terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

# Region and credentials come from the environment (AWS_REGION / the standard
# AWS credential chain), the same way the custom_resource fixture resolves
# them -- see integ's terratest options.
provider "cfncompat" {}

# --- AWS::* pseudo parameters (RFC 006 section 2) ------------------------

# The bridge's singleton node: one data source per stack, one STS call, all
# AWS::* pseudo parameters. stack_name / notification_arns are inputs that
# are echoed back (the bridge passes Stack.stackName and
# StackProps.notificationArns).
data "cfncompat_pseudo_parameters" "current" {
  stack_name        = "cfncompat-e2e"
  notification_arns = ["arn:aws:sns:us-east-1:123456789012:e2e"]
}

# Without stack_name there is no stack identity to derive, so AWS::StackId is
# null rather than an invented value (RFC 006 section 2.3).
data "cfncompat_pseudo_parameters" "anonymous" {}

# --- Fn::GetAZs (RFC 006 section 3) --------------------------------------

# Fn::GetAZs "" -- the provider's own region.
data "cfncompat_availability_zones" "current" {}

# Fn::GetAZs <region> -- an explicit region, here chained off AWS::Region so
# the two must agree.
data "cfncompat_availability_zones" "explicit" {
  region = data.cfncompat_pseudo_parameters.current.region
}

# Fn::GetAZs "" spelled out: an explicitly empty region argument is
# AWS::Region, exactly as an omitted one is.
data "cfncompat_availability_zones" "empty" {
  region = ""
}

# --- Pseudo-parameter attributes ----------------------------------------

output "account_id" {
  value = data.cfncompat_pseudo_parameters.current.account_id
}

output "partition" {
  value = data.cfncompat_pseudo_parameters.current.partition
}

output "region" {
  value = data.cfncompat_pseudo_parameters.current.region
}

output "url_suffix" {
  value = data.cfncompat_pseudo_parameters.current.url_suffix
}

output "stack_name" {
  value = data.cfncompat_pseudo_parameters.current.stack_name
}

output "stack_id" {
  value = data.cfncompat_pseudo_parameters.current.stack_id
}

output "notification_arns" {
  value = data.cfncompat_pseudo_parameters.current.notification_arns
}

output "id" {
  value = data.cfncompat_pseudo_parameters.current.id
}

# stack_id is null on the anonymous data source; normalize to "" so the
# output is a plain (empty) string terratest can compare against.
output "anonymous_stack_id" {
  value = data.cfncompat_pseudo_parameters.anonymous.stack_id == null ? "" : data.cfncompat_pseudo_parameters.anonymous.stack_id
}

output "anonymous_stack_name" {
  value = data.cfncompat_pseudo_parameters.anonymous.stack_name == null ? "" : data.cfncompat_pseudo_parameters.anonymous.stack_name
}

output "anonymous_notification_arns" {
  value = data.cfncompat_pseudo_parameters.anonymous.notification_arns
}

output "anonymous_account_id" {
  value = data.cfncompat_pseudo_parameters.anonymous.account_id
}

# --- Availability-zone attributes ---------------------------------------

output "az_names" {
  value = data.cfncompat_availability_zones.current.names
}

output "az_all_names" {
  value = data.cfncompat_availability_zones.current.all_names
}

output "az_zone_ids" {
  value = data.cfncompat_availability_zones.current.zone_ids
}

output "az_id" {
  value = data.cfncompat_availability_zones.current.id
}

output "az_explicit_names" {
  value = data.cfncompat_availability_zones.explicit.names
}

output "az_empty_region_names" {
  value = data.cfncompat_availability_zones.empty.names
}

output "az_empty_region" {
  value = data.cfncompat_availability_zones.empty.region
}

output "az_empty_region_id" {
  value = data.cfncompat_availability_zones.empty.id
}

# --- Composed values (what the bridge actually emits) --------------------

# Fn::Select(0, Fn::GetAZs "") -- the environment-agnostic CDK path
# (core/lib/stack.ts availabilityZones).
output "az0" {
  value = provider::cfncompat::select(0, data.cfncompat_availability_zones.current.names)
}

# Guarded: a region with a single Availability Zone has no index 1, and
# Fn::Select is an error out of range -- so the fixture yields "" there
# rather than failing the apply.
output "az1" {
  value = length(data.cfncompat_availability_zones.current.names) > 1 ? provider::cfncompat::select(1, data.cfncompat_availability_zones.current.names) : ""
}

# A service ARN built from AWS::Partition / AWS::Region / AWS::AccountId, the
# single most common pseudo-parameter composition in CDK templates.
output "composed_arn" {
  value = "arn:${data.cfncompat_pseudo_parameters.current.partition}:lambda:${data.cfncompat_pseudo_parameters.current.region}:${data.cfncompat_pseudo_parameters.current.account_id}:function:cfncompat-e2e"
}

# A regional service endpoint built from AWS::URLSuffix.
output "s3_endpoint" {
  value = "s3.${data.cfncompat_pseudo_parameters.current.region}.${data.cfncompat_pseudo_parameters.current.url_suffix}"
}

# The CloudFormation export-name shape, built from AWS::StackName.
output "export_name" {
  value = "${data.cfncompat_pseudo_parameters.current.stack_name}:ExportsOutputRefBucket"
}
