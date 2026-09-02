# {{resolve:ssm-secure:/app/db/password:4}} -- and aws-cdk-lib's
# SecretValue.ssmSecure("/app/db/password", "4").
#
# Unlike CloudFormation, which never stores a secure string value, Terraform
# writes the decrypted value into state in plaintext. Every read warns about
# that; suppress_state_warning silences the warning once the trade-off has
# been accepted (for example under OpenTofu state encryption).
data "cfncompat_ssm_secure_parameter_value" "db_password" {
  name    = "/app/db/password"
  version = 4
}

# Unpinned: the latest version at read time, which Terraform repeats on every
# plan. CloudFormation only re-resolves when a stack operation touches the
# resource.
data "cfncompat_ssm_secure_parameter_value" "api_key" {
  name                   = "/app/api-key"
  suppress_state_warning = true
}

output "db_password_version" {
  value = data.cfncompat_ssm_secure_parameter_value.db_password.resolved_version
}
