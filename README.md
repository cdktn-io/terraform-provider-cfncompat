# Terraform Provider: cfncompat

A [Terraform](https://www.terraform.io) provider built on the [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework) that provides **CloudFormation compatibility semantics** for [CDK Terrain](https://github.com/open-constructs/cdk-terrain)'s Terraform/OpenTofu synthesis backend.

## Purpose

CDK Terrain synthesizes CloudFormation-authored constructs into Terraform/OpenTofu configurations. While most CloudFormation resource types have direct Terraform equivalents, CloudFormation's **intrinsic functions** (e.g., `Fn::Cidr`, `Fn::Join`, `Fn::Select`, `Fn::FindInMap`, `Fn::Base64`) and **deployment-model capabilities** (e.g., `CustomResource` with pre-signed S3 URLs and Lambda-backed lifecycle polling) have no native Terraform counterpart.

The `cfncompat` provider fills this gap by exposing CloudFormation intrinsic functions as **Terraform provider-defined functions** (`provider::cfncompat::*`) and deployment-model capabilities as **Terraform resources** (`cfncompat_*`), enabling CDK Terrain to faithfully translate CloudFormation templates without semantic loss.

## Status

🚧 **Early development** — all pure-computable CloudFormation intrinsic functions are implemented as provider-defined functions. Deployment-model resources (`cfncompat_custom_resource`) are not implemented yet.

### Provider Functions (implemented)

Every CloudFormation intrinsic function that is a pure computation (no AWS API access, no template/synthesis context) is implemented, matching the AWS-documented semantics exactly. Condition and rule functions use a `condition_` prefix so the generated CDKTN/JSII bindings camelCase to AWS CDK's `Fn.condition*` API (bare `if`/`and`/`or`/`not` are reserved words in JSII target languages). See [`RFCs/004-intrinsic-function-polyfill.md`](RFCs/004-intrinsic-function-polyfill.md) for the full mapping rationale.

| Terraform Form | CloudFormation Equivalent | AWS CDK Binding |
|---|---|---|
| `provider::cfncompat::base64(...)` | `Fn::Base64` | `Fn.base64` |
| `provider::cfncompat::cidr(...)` | `Fn::Cidr` | `Fn.cidr` |
| `provider::cfncompat::find_in_map(...)` | `Fn::FindInMap` | `Fn.findInMap` |
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
| `Fn::GetAZs` | Requires AWS API | Terraform data source territory |
| `Fn::ImportValue` | CFN cross-stack exports | CFN-only for v1 (RFC 002 §G5) |
| `Fn::Transform` | CFN macros | Synth-time error (RFC 002) |
| `Fn::ForEach` | Template transform, not a value function | Synthesis backend |
| `Fn::RefAll`, `Fn::ValueOf`, `Fn::ValueOfAll` | Parameter/AWS context rule functions | Not applicable outside CFN rules |

### Planned Features

| Feature | Type | Terraform Form | CloudFormation Equivalent |
|---|---|---|---|
| `cfncompat_custom_resource` | Resource | `resource "cfncompat_custom_resource"` | `AWS::CloudFormation::CustomResource` |

A placeholder skeleton file exists in `internal/provider/` for the custom resource.

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

# Run unit tests
go test ./...

# Generate documentation
make generate
```

## Provider Source

- **Registry address:** `registry.terraform.io/cdktn-io/cfncompat`
- **Repository:** `github.com/cdktn-io/terraform-provider-cfncompat`

## License

MPL-2.0 — see [LICENSE](LICENSE) for details.
