# Terraform Provider: cfncompat

A [Terraform](https://www.terraform.io) provider built on the [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework) that provides **CloudFormation compatibility semantics** for [CDK Terrain](https://github.com/open-constructs/cdk-terrain)'s Terraform/OpenTofu synthesis backend.

## Purpose

CDK Terrain synthesizes CloudFormation-authored constructs into Terraform/OpenTofu configurations. While most CloudFormation resource types have direct Terraform equivalents, CloudFormation's **intrinsic functions** (e.g., `Fn::Cidr`, `Fn::Join`, `Fn::Select`, `Fn::FindInMap`, `Fn::Base64`) and **deployment-model capabilities** (e.g., `CustomResource` with pre-signed S3 URLs and Lambda-backed lifecycle polling) have no native Terraform counterpart.

The `cfncompat` provider fills this gap by exposing CloudFormation intrinsic functions as **Terraform provider-defined functions** (`provider::cfncompat::*`), deployment-model capabilities as **Terraform resources** (`cfncompat_*`), and the values that need an AWS API call at deploy time (seven of the eight `AWS::*` pseudo parameters, `Fn::GetAZs`, and the SSM Parameter Store / Secrets Manager reads behind `AWS::SSM::Parameter::Value<...>` and the `{{resolve:...}}` dynamic references) as **Terraform data sources**, enabling CDK Terrain to faithfully translate CloudFormation templates without semantic loss.

## Status

🚧 **Early development** — all pure-computable CloudFormation intrinsic functions are implemented as provider-defined functions, the CloudFormation custom-resource protocol is implemented as the `cfncompat_custom_resource` resource, and seven of the eight `AWS::*` pseudo parameters, `Fn::GetAZs`, and every CloudFormation dynamic-reference mechanism (`AWS::SSM::Parameter::Value<...>`, `{{resolve:ssm}}`, `{{resolve:ssm-secure}}`, `{{resolve:secretsmanager}}`) are implemented as data sources (the eighth pseudo parameter, `AWS::NoValue`, is bridge-side — see below).

### Resources (implemented)

| Feature | Terraform Form | CloudFormation Equivalent |
|---|---|---|
| Custom resource protocol | `resource "cfncompat_custom_resource"` | `AWS::CloudFormation::CustomResource` / `Custom::*` |

`cfncompat_custom_resource` emulates the CloudFormation engine's side of the [custom resource protocol](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/crpg-ref.html): it delivers Create/Update/Delete events to Lambda or SNS service tokens and polls a pre-signed S3 `ResponseURL` for the handler response — so existing CloudFormation custom-resource handlers (including CDK's provider framework and `AwsCustomResource`) work unmodified. Requires AWS credentials (see provider configuration) and a response bucket (`custom_resource_bucket` provider attribute or per-resource `response_bucket`). See [`RFCs/005-custom-resource-polyfill.md`](RFCs/005-custom-resource-polyfill.md) for the design.

Provider AWS configuration follows the terraform-provider-awscc schema (static credentials, profile, shared config files, `assume_role`, `assume_role_with_web_identity`, endpoint overrides) and is fully optional: provider functions never need it.

### Data Sources (implemented)

| Feature | Terraform Form | CloudFormation Equivalent |
|---|---|---|
| Pseudo parameters | `data "cfncompat_pseudo_parameters"` | `AWS::AccountId`, `AWS::Partition`, `AWS::Region`, `AWS::URLSuffix`, `AWS::StackName`, `AWS::StackId`, `AWS::NotificationARNs` |
| Availability zones | `data "cfncompat_availability_zones"` | `Fn::GetAZs` |
| SSM parameter (scalar) | `data "cfncompat_ssm_parameter_value"` | `{{resolve:ssm:...}}` (`value_type` unset) / `AWS::SSM::Parameter::Value<String>`, `<AWS-specific type>` (`value_type` set) |
| SSM parameter (list) | `data "cfncompat_ssm_parameter_list_value"` | `AWS::SSM::Parameter::Value<List<String>>`, `<CommaDelimitedList>`, `<List<AWS-specific type>>` |
| SSM secure string | `data "cfncompat_ssm_secure_parameter_value"` | `{{resolve:ssm-secure:...}}` |
| Secrets Manager secret | `data "cfncompat_secretsmanager_secret_value"` | `{{resolve:secretsmanager:...}}` |

`cfncompat_pseudo_parameters` is aws-cdk-lib's [`Aws`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html) accessor class as a single data source: one node per stack, one STS `GetCallerIdentity` call, seven of the eight `AWS::*` pseudo parameters (all but `AWS::NoValue`, which is bridge-side). `stack_name` and `notification_arns` are echoed inputs; `stack_id` is a deterministic CloudFormation stack ARN derived from `(partition, region, account_id, stack_name)`, so custom-resource handlers that use it as an ownership key stay correct across applies. `cfncompat_availability_zones` is [`Fn::GetAZs`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-getavailabilityzones.html), including the documented EC2-VPC default-subnet behaviour, and adds an `endpoints.ec2` provider override for LocalStack-style runs; it needs the permissions CloudFormation documents for `Fn::GetAZs` — `ec2:DescribeAvailabilityZones` and `ec2:DescribeAccountAttributes`, plus `ec2:DescribeSubnets` for EC2-VPC. Both need AWS credentials and create nothing. See [`RFCs/006-pseudo-parameter-polyfill.md`](RFCs/006-pseudo-parameter-polyfill.md) for the design.

The four dynamic-reference data sources cover every way CloudFormation reads a value out of Systems Manager Parameter Store or Secrets Manager at deploy time, including the parts nothing in `hashicorp/aws` reproduces: CloudFormation's strict Systems Manager type matching, and its validation of a resolved value against the declared inner type (`AWS::EC2::Image::Id` and the nine other AWS-specific types are checked to *exist* in the account and region, not merely to look right — `validate = false` opts out). Sensitivity is static per data source, so a non-secret value is **not** marked sensitive and does not poison the attributes it flows into, while the two secret-reading data sources always are — and warn, on every read, that Terraform stores the value in state in plaintext where CloudFormation never does. `provider::cfncompat::parse_dynamic_reference` and `is_dynamic_reference` split a `{{resolve:...}}` string whose text is only known at plan time. The semantics were verified against 36 real CloudFormation stacks; see [`RFCs/007-dynamic-reference-polyfill.md`](RFCs/007-dynamic-reference-polyfill.md) for the design and [`RFCs/dynamic-ssm/live-test-results.md`](RFCs/dynamic-ssm/live-test-results.md) for the evidence.

### Provider Functions (implemented)

Every CloudFormation intrinsic function that is a pure computation (no AWS API access, no template/synthesis context) is implemented, matching the AWS-documented semantics exactly. Condition and rule functions use a `condition_` prefix so the generated CDKTN/JSII bindings camelCase to AWS CDK's `Fn.condition*` API (bare `if`/`and`/`or`/`not` are reserved words in JSII target languages). See [`RFCs/004-intrinsic-function-polyfill.md`](RFCs/004-intrinsic-function-polyfill.md) for the full mapping rationale.

| Terraform Form | CloudFormation Equivalent | AWS CDK Binding |
|---|---|---|
| `provider::cfncompat::base64(...)` | `Fn::Base64` | `Fn.base64` |
| `provider::cfncompat::cidr(...)` | `Fn::Cidr` | `Fn.cidr` |
| `provider::cfncompat::find_in_map(...)` | `Fn::FindInMap` | `Fn.findInMap` |
| `provider::cfncompat::is_dynamic_reference(...)` | *(no CFN equivalent)* — is this string one whole `{{resolve:...}}` reference? | — |
| `provider::cfncompat::parse_dynamic_reference(...)` | *(no CFN equivalent)* — splits a `{{resolve:...}}` reference into its parts | — |
| `provider::cfncompat::join(...)` | `Fn::Join` | `Fn.join` |
| `provider::cfncompat::length(...)` | `Fn::Length` (LanguageExtensions) | `Fn.len` |
| `provider::cfncompat::select(...)` | `Fn::Select` | `Fn.select` |
| `provider::cfncompat::split(...)` | `Fn::Split` | `Fn.split` |
| `provider::cfncompat::sub(...)` | `Fn::Sub` | `Fn.sub` |
| `provider::cfncompat::to_json_string(...)` | `Fn::ToJsonString` (LanguageExtensions) | `Fn.toJsonString` |
| `provider::cfncompat::condition_and(...)` | `Fn::And` | `Fn.conditionAnd` |
| `provider::cfncompat::condition_contains(...)` | `Fn::Contains` | `Fn.conditionContains` |
| `provider::cfncompat::condition_each_member_equals(...)` | `Fn::EachMemberEquals` | `Fn.conditionEachMemberEquals` |
| `provider::cfncompat::condition_each_member_in(...)` | `Fn::EachMemberIn` | `Fn.conditionEachMemberIn` |
| `provider::cfncompat::condition_equals(...)` | `Fn::Equals` | `Fn.conditionEquals` |
| `provider::cfncompat::condition_if(...)` | `Fn::If` | `Fn.conditionIf` |
| `provider::cfncompat::condition_not(...)` | `Fn::Not` | `Fn.conditionNot` |
| `provider::cfncompat::condition_or(...)` | `Fn::Or` | `Fn.conditionOr` |

### Excluded intrinsics (resolved at synthesis time, not provider functions)

Terraform provider-defined functions must be pure and offline, so intrinsics needing AWS API access or CloudFormation template/deployment context are owned by the CDK Terrain synthesis backend instead:

| CloudFormation Intrinsic | Why excluded | Where it's handled |
|---|---|---|
| `Ref`, `Fn::GetAtt` | Reference resolution | Synthesis-time reference resolver (RFC 002 keystone seam) |
| `Fn::ImportValue` | CFN cross-stack exports | CFN-only for v1 (RFC 002 §G5) |
| `Fn::Transform` | CFN macros | Synth-time error (RFC 002) |
| `Fn::ForEach` | Template transform, not a value function | Synthesis backend |
| `Fn::RefAll`, `Fn::ValueOf`, `Fn::ValueOfAll` | Parameter/AWS context rule functions | Not applicable outside CFN rules |

`Fn::GetAZs` was previously excluded on the same grounds; it now has a home that can call AWS — the `cfncompat_availability_zones` data source above. `AWS::NoValue` needs no provider surface either: CDK Terrain resolves it to `Token.nullValue()`, which renders the Terraform `null` keyword (CloudFormation removes the property, Terraform treats `null` as unset). That equivalence holds in an attribute position only: **inside a list** the bridge must drop the element or wrap the list in `compact()`, because Terraform keeps a `null` list element where CloudFormation removes it — see [RFC 006 §4](RFCs/006-pseudo-parameter-polyfill.md).

### Planned Features

| Feature | Type | Notes |
|---|---|---|
| `cfncompat_aws_sdk_call` | Resource | Native arbitrary-SDK-call resource (removes `AwsCustomResource`'s runtime-Lambda dependency) — deferred, see RFC 005 |
| `cfncompat_signal` | Resource | `cfn-signal`/`CreationPolicy` analog (SQS long-poll blueprint from RFC 002 §7.1) — deferred |

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.8
- [Go](https://golang.org/doc/install) >= 1.25

## Building the Provider

```shell
git clone https://github.com/cdktn-io/terraform-provider-cfncompat.git
cd terraform-provider-cfncompat
go install
```

## Using the Provider

```hcl
terraform {
  required_providers {
    cfncompat = {
      source = "registry.terraform.io/cdktn-io/cfncompat"
    }
  }
}

provider "cfncompat" {
  # configuration options will be added as needed
}
```

## Developing

```shell
# Build
go build -v ./...

# Run unit tests (no AWS, no Terraform CLI needed)
go test ./...

# Generate documentation
make generate
```

Acceptance tests additionally need `TF_ACC=1` and a real `terraform` binary. Those that call **real AWS** are opt-in on top of it, so a credential-less CI run skips rather than fails them:

```shell
# The read-only data sources — STS GetCallerIdentity / EC2 Describe* / SSM GetParameter calls
TF_ACC=1 CFNCOMPAT_TEST_AWS=1 go test -run 'TestAcc(PseudoParameters|AvailabilityZones|SsmParameter)' ./internal/provider/

# The two secret-reading data sources need a fixture you create and delete yourself
TF_ACC=1 CFNCOMPAT_TEST_AWS=1 CFNCOMPAT_TEST_SSM_SECURE_NAME=/cfncompat/acctest/secure \
  CFNCOMPAT_TEST_SECRET_ID=cfncompat-acctest/db \
  go test -run 'TestAcc(SsmSecureParameterValue|SecretsManagerSecretValue)' ./internal/provider/

# The custom-resource protocol — needs a deployed handler and its response bucket
TF_ACC=1 CFNCOMPAT_TEST_LAMBDA_ARN=arn:aws:lambda:... CFNCOMPAT_TEST_RESPONSE_BUCKET=my-bucket \
  go test -run TestAccCustomResource ./internal/provider/
```

AWS credentials come from the environment as usual (e.g. `aws-vault exec`). The terratest end-to-end suite under `integ/` has its own gate, `CFNCOMPAT_E2E_AWS=1` (`make -C integ help`).

## Provider Source

- **Registry address:** `registry.terraform.io/cdktn-io/cfncompat`
- **Repository:** `github.com/cdktn-io/terraform-provider-cfncompat`

## License

MPL-2.0 — see [LICENSE](LICENSE) for details.
