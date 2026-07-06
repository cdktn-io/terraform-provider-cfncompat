# Changelog
All notable changes to this project will be documented in this file.

## 0.2.0 (July 7, 2026)

FEATURES:

* **New Resource:** `cfncompat_custom_resource` — faithful CloudFormation custom-resource protocol polyfill (Lambda and SNS service tokens, pre-signed S3 ResponseURL, ServiceTimeout, replacement cleanup semantics); covers CDK `AwsCustomResource` and provider-framework handlers unmodified (see `RFCs/005-custom-resource-polyfill.md`)
* **Provider Configuration:** AWS credential-chain configuration via `hashicorp/aws-sdk-go-base/v2` (awscc-parity schema: static credentials, profile, shared config files, `assume_role`, `assume_role_with_web_identity`, endpoints overrides) plus `custom_resource_bucket`; configuration stays optional — provider functions work without any AWS setup

NOTES:

* On-demand terratest end-to-end suite under `integ/` (`make -C integ help`), runnable via the `workflow_dispatch`-only e2e GitHub workflow; not part of default CI

## 0.1.0 (July 5, 2026)

FEATURES:

* **New Functions:** all pure-computable CloudFormation intrinsic functions as provider-defined functions: `base64`, `cidr`, `find_in_map`, `join`, `length`, `select`, `split`, `sub`, `to_json_string`, `condition_and`, `condition_contains`, `condition_each_member_equals`, `condition_each_member_in`, `condition_equals`, `condition_if`, `condition_not`, `condition_or` (see `RFCs/004-intrinsic-function-polyfill.md` for naming and exclusion rationale)
