terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {
  region                 = "us-east-1"
  custom_resource_bucket = "my-cfncompat-responses"
}

# Emulates a CloudFormation custom resource backed by a Lambda function,
# equivalent to declaring an "AWS::CloudFormation::CustomResource" (or a
# CDK AwsCustomResource / provider-framework-based custom resource) whose
# ServiceToken points at the same function.
resource "cfncompat_custom_resource" "example" {
  service_token = "arn:aws:lambda:us-east-1:123456789012:function:my-custom-resource-handler"

  resource_properties = {
    Message = "hello from cfncompat"
    Count   = 3
  }

  logical_resource_id = "MyCustomResource"
  service_timeout     = 300
}

output "example_physical_resource_id" {
  value = cfncompat_custom_resource.example.physical_resource_id
}

output "example_data" {
  value = cfncompat_custom_resource.example.data
}
