# Terraform Provider: cfncompat

A [Terraform](https://www.terraform.io) provider built on the [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework) that provides **CloudFormation compatibility semantics** for [CDK Terrain](https://github.com/open-constructs/cdk-terrain)'s Terraform/OpenTofu synthesis backend.

## Purpose

CDK Terrain synthesizes CloudFormation-authored constructs into Terraform/OpenTofu configurations. While most CloudFormation resource types have direct Terraform equivalents, CloudFormation's **intrinsic functions** (e.g., `Fn::Cidr`, `Fn::Join`, `Fn::Select`, `Fn::FindInMap`, `Fn::Base64`) and **deployment-model capabilities** (e.g., `CustomResource` with pre-signed S3 URLs and Lambda-backed lifecycle polling) have no native Terraform counterpart.

The `cfncompat` provider fills this gap by exposing CloudFormation intrinsic functions as **Terraform provider-defined functions** (`provider::cfncompat::*`) and deployment-model capabilities as **Terraform resources** (`cfncompat_*`), enabling CDK Terrain to faithfully translate CloudFormation templates without semantic loss.

## Status

🚧 **Early development** — this provider is a minimal skeleton. No resources, data sources, or functions are implemented yet.

### Planned Features

| Feature | Type | Terraform Form | CloudFormation Equivalent |
|---|---|---|---|
| `cfncompat::cidr` | Provider Function | `provider::cfncompat::cidr(...)` | `Fn::Cidr` |
| `cfncompat::join` | Provider Function | `provider::cfncompat::join(...)` | `Fn::Join` |
| `cfncompat::select` | Provider Function | `provider::cfncompat::select(...)` | `Fn::Select` |
| `cfncompat::find_in_map` | Provider Function | `provider::cfncompat::find_in_map(...)` | `Fn::FindInMap` |
| `cfncompat::base64` | Provider Function | `provider::cfncompat::base64(...)` | `Fn::Base64` |
| ... | Provider Function | ... | every cfn function |
| `cfncompat_custom_resource` | Resource | `resource "cfncompat_custom_resource"` | `AWS::CloudFormation::CustomResource` |

Placeholder skeleton files exist in `internal/provider/` for the highest-priority functions (`cidr`, `join`) and the custom resource.

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
