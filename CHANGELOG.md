# Changelog
All notable changes to this project will be documented in this file.

## Unreleased (0.4.0)

FEATURES:

* **New Data Source:** `cfncompat_ssm_parameter_value` — a scalar Systems Manager Parameter Store read with the two resolution paths CloudFormation actually has: with `value_type` unset it is `{{resolve:ssm:name[:version]}}` (accepts `String` and `StringList`, returns the raw untrimmed stored string), and with `value_type` set it is `AWS::SSM::Parameter::Value<T>` (strict `String`-only type match plus validation of the resolved value against the CloudFormation inner type). `value` is deliberately **not** sensitive, unlike `hashicorp/aws`'s `aws_ssm_parameter`, matching CloudFormation's own `DescribeStacks` `ResolvedValue`
* **New Data Source:** `cfncompat_ssm_parameter_list_value` — `AWS::SSM::Parameter::Value<List<String>>` / `<CommaDelimitedList>` / `<List<AWS-specific type>>` as a real `list(string)`, split on commas with each member whitespace-trimmed as CloudFormation's typed list resolution does; `raw_value` keeps the stored string verbatim. Accepts only `StringList` parameters, mirroring CloudFormation's strict declared-type check
* **New Data Source:** `cfncompat_ssm_secure_parameter_value` — `{{resolve:ssm-secure:...}}`, with `value` marked sensitive and a per-read warning that Terraform stores the decrypted value in state in plaintext where CloudFormation stores only the literal reference (`suppress_state_warning` silences it)
* **New Data Source:** `cfncompat_secretsmanager_secret_value` — `{{resolve:secretsmanager:secret-id:SecretString:json-key:version-stage:version-id}}`, whole `SecretString` or one JSON key, with the same sensitivity and state warning
* **New Functions:** `provider::cfncompat::parse_dynamic_reference` splits a single `{{resolve:...}}` string into a fixed-shape object (`service`, `name`, `version`, `secret_string`, `json_key`, `version_stage`, `version_id`), ARN-shaped Secrets Manager ids included, for the case where the reference text is not known until plan time; `provider::cfncompat::is_dynamic_reference` is its total predicate
* **Validation:** the ten CloudFormation AWS-specific parameter types (`AWS::EC2::Image::Id`, `Instance::Id`, `Subnet::Id`, `VPC::Id`, `Volume::Id`, `SecurityGroup::Id`/`GroupName`, `KeyPair::KeyName`, `AvailabilityZone::Name`, `AWS::Route53::HostedZone::Id`) and their nine `List<...>` forms are checked both syntactically and — as CloudFormation does — for **existence** in the account and region, batched into one API call per list; `validate = false` keeps only the syntactic check
* **Provider Configuration:** new `endpoints.ssm`, `endpoints.secretsmanager` and `endpoints.route53` overrides, alongside the existing `lambda`/`sns`/`s3`/`sts`/`ec2` ones

DEPENDENCIES:

* New direct dependencies `aws-sdk-go-v2/service/ssm`, `aws-sdk-go-v2/service/secretsmanager` and `aws-sdk-go-v2/service/route53`. Measured binary impact on darwin/arm64: +7.14 MiB (ssm), +3.00 MiB (route53, for the single `AWS::Route53::HostedZone::Id` existence check), +1.07 MiB (secretsmanager); the six new EC2 operations are free because `service/ec2` was already linked for `Fn::GetAZs`. Total +14.95%

NOTES:

* `RFCs/007-dynamic-reference-polyfill.md` records the design; `RFCs/dynamic-ssm/live-test-results.md` records the 36 real CloudFormation stacks its semantics were verified against, and `RFCs/dynamic-ssm/design-ssm-cfn-mechanics.md` / `design-ssm-tf-typing.md` the supporting research
* Two behaviours diverge from CloudFormation deliberately, and are documented as such. `allowed_pattern`/`allowed_values` apply to the **resolved value**, whereas CloudFormation's `AllowedPattern`/`AllowedValues` on an SSM-typed template `Parameter` validate the parameter *name* — a synthesis backend must not map `CfnParameter` constraints onto them. `label` is a cfncompat extension: CloudFormation supports Systems Manager labels nowhere
* `cfncompat_secretsmanager_secret_value` re-reads on every plan, so a secret rotation produces a diff; CloudFormation re-resolves a `secretsmanager` reference only when the consuming resource is independently being updated. Pin `version_id`, or use `lifecycle { ignore_changes }` on the consumer. The SSM paths have no such divergence — CloudFormation re-resolves those on every stack operation
* Acceptance tests for the two secret-reading data sources are gated on `CFNCOMPAT_TEST_SSM_SECURE_NAME` / `CFNCOMPAT_TEST_SECRET_ID` on top of `CFNCOMPAT_TEST_AWS=1`, since they need a fixture the operator creates and deletes

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
