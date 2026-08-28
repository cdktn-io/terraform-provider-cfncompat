# cfncompat e2e tests

On-demand end-to-end tests for the `cfncompat` Terraform provider, written with
[terratest](https://terratest.gruntwork.io/). These are **not** part of the
default `make test` / CI `test.yml` run — they build the provider binary from
source, run real `terraform init`/`apply`/`destroy` cycles, call real AWS APIs
(`TestE2EPseudoParameters`) and deploy real AWS resources
(`TestE2ECustomResource`). Run them on demand, locally
or via the [`e2e.yml`](../.github/workflows/e2e.yml) `workflow_dispatch`
GitHub Actions workflow.

## Layout

```
integ/
  util.go                       # provider build + Terraform CLI filesystem-mirror config helpers
  e2e_functions_test.go         # TestE2EFunctions          -- provider-defined functions, no AWS needed
  e2e_pseudo_parameters_test.go # TestE2EPseudoParameters   -- the two data sources, real AWS, read-only
  e2e_custom_resource_test.go   # TestE2ECustomResource     -- cfncompat_custom_resource, real AWS
  fixtures/
    functions/main.tf           # exercises all 17 provider::cfncompat::* functions
    pseudo_parameters/main.tf   # cfncompat_pseudo_parameters + cfncompat_availability_zones
    custom_resource/main.tf     # S3 + Lambda echo handler + cfncompat_custom_resource
  tf/                            # (gitignored) per-run working dirs, copied from fixtures/
  .build/                        # (gitignored) built provider binary + mirror + CLI config
```

## Why a filesystem mirror instead of `dev_overrides`?

The provider is not published to the Terraform Registry yet, so
`terraform init` cannot resolve `cdktn-io/cfncompat` normally. Terraform CLI's
[`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
mechanism is the usual fix for this, but it makes `terraform init` skip
dependency lock file verification for *every* provider in the config, and
disables use of the lock file entirely — which breaks the mixed-provider
`custom_resource` fixture (`hashicorp/aws`, `hashicorp/archive`) that needs a
normal, lockable init.

Instead, `BuildProvider` builds the provider binary into a
[`filesystem_mirror`](https://developer.hashicorp.com/terraform/cli/config/config-file#filesystem_mirror)
layout under `integ/.build/mirror`, and `WriteCLIConfig` writes a
`TF_CLI_CONFIG_FILE` that resolves *only* `registry.terraform.io/cdktn-io/cfncompat`
from that mirror (`direct { exclude = [...] }` for that address), while every
other provider still resolves normally from the public registry.

## Prerequisites

- Go (matching the root module's `go.mod` version).
- Terraform >= 1.8, on `PATH`. To run against OpenTofu instead, set
  `CFNCOMPAT_E2E_TF_BINARY=tofu` (must also be on `PATH`).
- AWS credentials — required for `TestE2EPseudoParameters` /
  `make pseudo-parameters` (read-only: `sts:GetCallerIdentity`,
  `ec2:DescribeAvailabilityZones`, `ec2:DescribeSubnets`) and for
  `TestE2ECustomResource` / `make custom-resource` (permission to create S3
  buckets, IAM roles, and Lambda functions). `TestE2EFunctions` /
  `make functions` needs no AWS credentials at all.

## Running locally

```sh
# Provider-defined functions only -- no AWS credentials needed.
make functions

# Pseudo parameters + Fn::GetAZs data sources against real AWS. Read-only:
# it creates nothing, it only reads STS and EC2.
aws-vault exec --no-session <profile> -- make pseudo-parameters

# Full custom resource lifecycle (Create + Update) against real AWS.
aws-vault exec --no-session <profile> -- make custom-resource

# All three, custom-resource last.
aws-vault exec --no-session <profile> -- make all
```

Or drive `go test` directly:

```sh
go test -v -count 1 -run '^TestE2EFunctions$' ./...
CFNCOMPAT_E2E_AWS=1 go test -v -count 1 -run '^TestE2EPseudoParameters$' ./...
CFNCOMPAT_E2E_AWS=1 go test -v -count 1 -run '^TestE2ECustomResource$' ./...
```

### Stages and stage-skipping

Each test runs through named stages (`build_provider`, `deploy`, `validate`,
`cleanup`) via terratest's `test_structure.RunTestStage`. Setting a
`SKIP_<stage>=true` environment variable skips that stage — useful for fast
local iteration (e.g. re-running just `validate` against an already-deployed
fixture without rebuilding the provider or re-applying).

The Makefile exposes this as pattern targets on top of `functions`,
`pseudo-parameters` and `custom-resource`:

```sh
make functions-no-cleanup      # deploy + validate, but leave resources up (SKIP_cleanup)
make functions-validate-only   # only re-run validate against a prior run's state
make functions-cleanup-only    # only destroy a prior run's resources
```

(likewise `pseudo-parameters-no-cleanup`, `pseudo-parameters-validate-only`,
`pseudo-parameters-cleanup-only`, and the `custom-resource-*` equivalents).

Run `make help` for the full target list.

`make clean` removes `tf/` (per-run working directories) and `.build/` (the
built provider binary, mirror, and CLI config).

## GitHub Actions

[`../.github/workflows/e2e.yml`](../.github/workflows/e2e.yml) runs these
tests on `workflow_dispatch` only (never on push/PR):

- `functions-e2e` always runs `TestE2EFunctions`.
- `pseudo-parameters-e2e` and `custom-resource-e2e` only run when the `run_aws`
  workflow input is `true`, and need a `AWS_E2E_ROLE_ARN` repository variable
  (an IAM role trusting this repo's GitHub OIDC provider) to assume via
  `aws-actions/configure-aws-credentials`. `pseudo-parameters-e2e` runs first:
  it is read-only and fails fast on a misconfigured role, before
  `custom-resource-e2e` starts creating anything.
