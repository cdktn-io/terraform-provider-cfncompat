provider "cfncompat" {
  # All configuration below is optional. The provider-defined functions
  # (provider::cfncompat::*) work with zero configuration at all.
  #
  # Only the cfncompat_custom_resource resource needs AWS credentials and a
  # region, resolved the same way as the official hashicorp/aws provider:
  # explicit attributes here, then environment variables, then shared
  # config/credentials files, then EC2/ECS instance metadata.
  #
  # region  = "us-east-1"
  # profile = "my-profile"

  # Default S3 bucket used for cfncompat_custom_resource response transport
  # when a resource does not set its own response_bucket.
  # custom_resource_bucket = "my-cfncompat-responses"
}
