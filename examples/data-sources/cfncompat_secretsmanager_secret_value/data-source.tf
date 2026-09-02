# {{resolve:secretsmanager:prod/db:SecretString:username}} and
# {{resolve:secretsmanager:prod/db:SecretString:password}} -- the shape
# aws-cdk-lib's SecretValue.secretsManager() emits for RDS credentials.
data "cfncompat_secretsmanager_secret_value" "db_username" {
  secret_id = "prod/db"
  json_key  = "username"
}

data "cfncompat_secretsmanager_secret_value" "db_password" {
  secret_id = "prod/db"
  json_key  = "password"
}

# {{resolve:secretsmanager:prod/db}} -- the entire SecretString, with no
# json_key.
data "cfncompat_secretsmanager_secret_value" "whole_secret" {
  secret_id              = "prod/db"
  suppress_state_warning = true
}

# A secret in another AWS account must be given as its full ARN, exactly as
# CloudFormation requires. AWSPREVIOUS reads the version rotation just
# replaced.
data "cfncompat_secretsmanager_secret_value" "cross_account_previous" {
  secret_id     = "arn:aws:secretsmanager:us-west-2:123456789012:secret:shared/api-key-AbCdEf"
  json_key      = "key"
  version_stage = "AWSPREVIOUS"
}

# Unlike ssm/ssm-secure, CloudFormation re-resolves a secretsmanager reference
# only when the consuming resource is independently being updated -- so a
# rotation is invisible to it, while Terraform's read-every-plan surfaces it
# immediately. Pin version_id when a rotation must not churn the plan.
data "cfncompat_secretsmanager_secret_value" "pinned" {
  secret_id  = "prod/db"
  json_key   = "password"
  version_id = "01234567-89ab-cdef-0123-456789abcdef"
}

output "db_secret_version" {
  value = data.cfncompat_secretsmanager_secret_value.db_password.resolved_version_id
}
