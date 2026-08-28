terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {
  region = "us-east-1"

  # The custom resource below writes its handler response through a
  # pre-signed PUT URL under this bucket.
  custom_resource_bucket = "my-cfncompat-responses"
}

# One data source for every CloudFormation AWS::* pseudo parameter — the
# equivalent of aws-cdk-lib's `Aws` accessor class, resolved with a single
# STS GetCallerIdentity call. stack_name and notification_arns are inputs
# that are echoed back: CDK Terrain passes Stack.stackName and
# StackProps.notificationArns.
data "cfncompat_pseudo_parameters" "current" {
  stack_name        = "MyApp-Prod"
  notification_arns = ["arn:aws:sns:us-east-1:123456789012:deploy-events"]
}

locals {
  # arn:aws:lambda:us-east-1:123456789012:function:x
  function_arn = "arn:${data.cfncompat_pseudo_parameters.current.partition}:lambda:${data.cfncompat_pseudo_parameters.current.region}:${data.cfncompat_pseudo_parameters.current.account_id}:function:x"

  # s3.us-east-1.amazonaws.com (amazonaws.com.cn in the aws-cn partition)
  s3_host = "s3.${data.cfncompat_pseudo_parameters.current.region}.${data.cfncompat_pseudo_parameters.current.url_suffix}"

  # The CloudFormation cross-stack export naming convention.
  export_name = "${data.cfncompat_pseudo_parameters.current.stack_name}:ExportsOutputRefBucket"
}

# AWS::StackId is deterministic — a pure function of partition, region,
# account id and stack name — so custom-resource handlers that use it as an
# ownership key stay correct across applies.
resource "cfncompat_custom_resource" "notifications" {
  service_token = "arn:aws:lambda:us-east-1:123456789012:function:my-handler"
  stack_id      = data.cfncompat_pseudo_parameters.current.stack_id
}
