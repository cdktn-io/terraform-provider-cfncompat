# Changelog
All notable changes to this project will be documented in this file.

## Unreleased

FEATURES:

* **New Resource:** `cfncompat_custom_resource` — faithful CloudFormation custom-resource protocol polyfill (Lambda and SNS service tokens, pre-signed S3 ResponseURL, ServiceTimeout, replacement cleanup semantics); covers CDK `AwsCustomResource` and provider-framework handlers unmodified (see `RFCs/005-custom-resource-polyfill.md`)
* **Provider Configuration:** AWS credential-chain configuration via `hashicorp/aws-sdk-go-base/v2` (awscc-parity schema: static credentials, profile, shared config files, `assume_role`, `assume_role_with_web_identity`, endpoints overrides) plus `custom_resource_bucket`; configuration stays optional — provider functions work without any AWS setup

* **New Functions:** all pure-computable CloudFormation intrinsic functions as provider-defined functions: `base64`, `cidr`, `find_in_map`, `join`, `length`, `select`, `split`, `sub`, `to_json_string`, `condition_and`, `condition_contains`, `condition_each_member_equals`, `condition_each_member_in`, `condition_equals`, `condition_if`, `condition_not`, `condition_or` (see `RFCs/004-intrinsic-function-polyfill.md` for naming and exclusion rationale)
