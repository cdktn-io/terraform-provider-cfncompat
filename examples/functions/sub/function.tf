terraform {
  required_providers {
    cfncompat = {
      source = "cdktn-io/cfncompat"
    }
  }
}

# Equivalent to the CloudFormation intrinsic function:
#   { "Fn::Sub" : [ "www.${Domain}", { "Domain" : "mydomain.com" } ] }
#
# Note: in this HCL example the literal "${" must be written as "$${" so that
# Terraform does not interpret it as its own template interpolation syntax
# before passing the string argument to the function.
output "example" {
  value = provider::cfncompat::sub("www.$${Domain}", { "Domain" = "mydomain.com" })
}
