# Changelog
All notable changes to this project will be documented in this file.

## 0.3.0 (August 29, 2026)

FEATURES:

* **New Data Source:** `cfncompat_pseudo_parameters` — every CloudFormation `AWS::*` pseudo parameter from one data source and one STS `GetCallerIdentity` call (`account_id`, `partition` and `url_suffix` both derived from the region, plus echoed `stack_name`/`notification_arns`), including a deterministic, stateless `AWS::StackId` derived from `(partition, region, account_id, stack_name)` so custom-resource handlers can keep using it as an ownership key (see `RFCs/006-pseudo-parameter-polyfill.md`)
* **New Data Source:** `cfncompat_availability_zones` — `Fn::GetAZs`, including CloudFormation's documented EC2-VPC default-subnet behaviour (`names`), plus the full `all_names`/`zone_ids` zone listing; an optional `region` argument mirrors `Fn::GetAZs`'s region parameter, where unset means `AWS::Region`. Needs the permissions CloudFormation documents for `Fn::GetAZs`: `ec2:DescribeAvailabilityZones` and `ec2:DescribeAccountAttributes` (the `supported-platforms` check that decides whether the default-subnet restriction applies), plus `ec2:DescribeSubnets` for EC2-VPC accounts
* **Provider Configuration:** new `endpoints.ec2` override, alongside the existing `lambda`/`sns`/`s3`/`sts` ones, so the availability-zone data source can be pointed at LocalStack

DEPENDENCIES:

* aws-sdk-go-v2 1.42.1 → 1.45.1, smithy-go 1.27.3 → 1.28.1; new direct dependencies `aws-sdk-go-v2/service/ec2` and `aws-sdk-go-v2/service/sts`

NOTES:

* `cfncompat_custom_resource` warns when `stack_id` is left at the shared `cfncompat/no-stack-id` default; wire it to `data.cfncompat_pseudo_parameters.<name>.stack_id`. The warning is planned to become an error in v1.0.

* New `pseudo-parameters-e2e` job in the `workflow_dispatch`-only e2e GitHub workflow (`make -C integ pseudo-parameters`), gated on the same `run_aws` input and OIDC role as the custom-resource job; the fixture is read-only and creates no AWS resources
* Acceptance tests that call real AWS are now opt-in on top of `TF_ACC=1` via a new `CFNCOMPAT_TEST_AWS=1` gate (`TestAccPseudoParametersDataSource`, `TestAccAvailabilityZonesDataSource`), so the credential-less CI acceptance matrix skips rather than fails them; `TestAccCustomResource` keeps its own `CFNCOMPAT_TEST_LAMBDA_ARN`/`CFNCOMPAT_TEST_RESPONSE_BUCKET` gate

## 0.2.0 (July 7, 2026)

FEATURES:

* **New Resource:** `cfncompat_custom_resource` — faithful CloudFormation custom-resource protocol polyfill (Lambda and SNS service tokens, pre-signed S3 ResponseURL, ServiceTimeout, replacement cleanup semantics); covers CDK `AwsCustomResource` and provider-framework handlers unmodified (see `RFCs/005-custom-resource-polyfill.md`)
* **Provider Configuration:** AWS credential-chain configuration via `hashicorp/aws-sdk-go-base/v2` (awscc-parity schema: static credentials, profile, shared config files, `assume_role`, `assume_role_with_web_identity`, endpoints overrides) plus `custom_resource_bucket`; configuration stays optional — provider functions work without any AWS setup

NOTES:

* On-demand terratest end-to-end suite under `integ/` (`make -C integ help`), runnable via the `workflow_dispatch`-only e2e GitHub workflow; not part of default CI

## 0.1.0 (July 5, 2026)

FEATURES:

* **New Functions:** all pure-computable CloudFormation intrinsic functions as provider-defined functions: `base64`, `cidr`, `find_in_map`, `join`, `length`, `select`, `split`, `sub`, `to_json_string`, `condition_and`, `condition_contains`, `condition_each_member_equals`, `condition_each_member_in`, `condition_equals`, `condition_if`, `condition_not`, `condition_or` (see `RFCs/004-intrinsic-function-polyfill.md` for naming and exclusion rationale)
