# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Purpose

`cfncompat` is a Terraform provider (built on the [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework)) that gives [CDK Terrain](https://github.com/open-constructs/cdk-terrain) a way to translate CloudFormation semantics into Terraform/OpenTofu that has no native equivalent:

- CloudFormation **intrinsic functions** (`Fn::Cidr`, `Fn::Join`, `Fn::Select`, `Fn::FindInMap`, `Fn::Base64`) → exposed as **provider-defined functions** (`provider::cfncompat::*`).
- CloudFormation **deployment-model capabilities** (`CustomResource` with pre-signed S3 URLs and Lambda-backed lifecycle polling) → exposed as **resources** (`cfncompat_*`).

Status: early skeleton. `internal/provider/provider.go` currently registers no resources, data sources, or functions — `Resources()`, `DataSources()`, and `Functions()` all return `nil`.

Repo lives at `github.com/cdktn-io/terraform-provider-cfncompat`. The public Terraform Registry namespace for a provider always matches the GitHub account/org that owns the repo (there's no way to decouple them), so this publishes as `registry.terraform.io/cdktn-io/cfncompat` — the `cdktn` HCP Terraform organization manages that `cdktn-io` public namespace, but does not rename it.

## Architecture

- `main.go` — plugin entrypoint. Serves the provider at address `registry.terraform.io/cdktn-io/cfncompat` via `providerserver.Serve`. Supports `-debug` for delve-based debugging.
- `internal/provider/provider.go` — the `CfncompatProvider` type and its Plugin Framework interface implementations (`Metadata`, `Schema`, `Configure`, `Resources`, `DataSources`, `Functions`, `EphemeralResources`, `Actions`). This is the wiring point: every new resource/function/data source must be registered in its corresponding method here.
- `internal/provider/function_*.go`, `resource_*.go` — one file per function/resource. New ones not yet implemented are stubbed with `//go:build ignore` and their real code commented out as a skeleton (see `function_cidr.go`, `function_join.go`, `resource_custom_resource.go`). To implement one: drop the `//go:build ignore` line, uncomment the body, then register the constructor in `provider.go`.
- `internal/provider/provider_test.go` — defines `testAccProtoV6ProviderFactories`, the shared provider factory acceptance tests use to spin up a `tfprotov6.ProviderServer` in-process.
- `tools/tools.go` (separate module, build-tag `generate`) — pins the generator binaries (`hashicorp/copywrite`, `hashicorp/terraform-plugin-docs/cmd/tfplugindocs`) used by `make generate`. `go:generate` directives here drive: copyright header insertion, `terraform fmt` on `examples/`, and `tfplugindocs` doc generation into `docs/`.
- `examples/provider/provider.tf` — example HCL used both as documentation and as the source `tfplugindocs` formats/embeds.

## Commands

```shell
make build       # go build -v ./...
make install     # build + go install -v ./...
make fmt         # gofmt -s -w -e .
make lint        # golangci-lint run
make generate    # regenerate copyright headers, fmt examples/, regenerate docs/ (cd tools; go generate ./...)
make test        # go test -v -cover -timeout=120s -parallel=10 ./...
make testacc     # TF_ACC=1 go test -v -cover -timeout 120m ./...   (acceptance tests against real Terraform CLI)
```

Run a single test:

```shell
go test -v -run TestName ./internal/provider/
TF_ACC=1 go test -v -run TestAccName ./internal/provider/   # acceptance test
```

`make generate` requires a `terraform` binary on `PATH` (used unwrapped, i.e. `terraform_wrapper: false` in CI) — it's needed to `terraform fmt` the `examples/` directory.

## Conventions

- `golangci-lint` (`.golangci.yml`) denies importing anything from `terraform-plugin-sdk/v2` (including its `acctest`, `resource`, `terraform` subpackages) — this provider is Plugin Framework only, never the older SDKv2.
- CI (`.github/workflows/test.yml`) has a `generate` job that fails the build if `make generate` produces a diff — always run `make generate` and commit the result after adding/changing resources, functions, or `examples/`.
- Acceptance tests are gated by `TF_ACC=1` and run against a matrix of real Terraform CLI versions in CI; unit tests (`make test`) do not require Terraform installed.
