// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

// Package integ contains the cfncompat terraform-provider end-to-end
// (terratest) test suite.
//
// The provider under test is not published to the Terraform Registry, so
// tests build it from source and point Terraform at it via a CLI
// filesystem_mirror config (see BuildProvider and WriteCLIConfig) rather
// than relying on `terraform init` to download it.
package integ

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	test_structure "github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/stretchr/testify/require"
)

const (
	// providerSource is the Terraform Registry source address the fixtures
	// declare in required_providers; it must match the address the built
	// binary is served under (registry.terraform.io/<namespace>/<type>).
	providerSource = "registry.terraform.io/cdktn-io/cfncompat"
	// providerVersion is the dev pseudo-version installed into the
	// filesystem mirror. Deliberately plain semver (no pre-release suffix
	// like "-dev"): Terraform's filesystem_mirror version discovery does
	// not reliably resolve pre-release versions when a required_providers
	// block has no explicit version constraint.
	providerVersion = "0.0.1"
	// defaultTFBinary is used when CFNCOMPAT_E2E_TF_BINARY is unset.
	defaultTFBinary = "terraform"
)

// repoRoot returns the absolute path to the repository root, i.e. the
// parent directory of the integ module.
func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err, "failed to get working directory")

	abs, err := filepath.Abs(filepath.Join(wd, ".."))
	require.NoError(t, err, "failed to resolve repo root")

	return abs
}

// buildDir returns integ/.build, creating it if it does not already exist.
func buildDir(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err, "failed to get working directory")

	dir := filepath.Join(wd, ".build")
	require.NoError(t, os.MkdirAll(dir, 0o755), "failed to create .build directory")

	return dir
}

// mirrorRoot returns the filesystem_mirror root directory used to deliver
// the locally-built provider binary to Terraform.
func mirrorRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(buildDir(t), "mirror")
}

// BuildProvider builds the cfncompat provider binary from the repository
// root and installs it into a Terraform CLI filesystem_mirror layout under
// integ/.build/mirror, so `terraform init` can resolve
// registry.terraform.io/cdktn-io/cfncompat without the provider being
// published anywhere. It returns the mirror root path.
//
// Safe to call repeatedly: it always rebuilds, but `go build` is
// content-addressed and cached, so repeat calls are fast.
func BuildProvider(t *testing.T) string {
	t.Helper()

	root := repoRoot(t)
	mirror := mirrorRoot(t)
	platformDir := filepath.Join(
		mirror,
		providerSource,
		providerVersion,
		fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH),
	)
	require.NoError(t, os.MkdirAll(platformDir, 0o755), "failed to create provider mirror directory")

	binPath := filepath.Join(platformDir, "terraform-provider-cfncompat_v"+providerVersion)

	// #nosec G204 -- fixed argument list, no user input.
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build of provider binary failed:\n%s", string(out))

	return mirror
}

// WriteCLIConfig writes a Terraform CLI configuration file
// (integ/.build/dev.tfrc) that resolves registry.terraform.io/cdktn-io/cfncompat
// from the local filesystem mirror (see BuildProvider) while letting every
// other provider (hashicorp/aws, hashicorp/archive, ...) resolve normally
// from the public registry. It returns the path to the written file.
//
// BuildProvider must be called (directly or indirectly) before the
// filesystem mirror this file references is populated.
func WriteCLIConfig(t *testing.T) string {
	t.Helper()

	mirror := mirrorRoot(t)

	contents := fmt.Sprintf(`provider_installation {
  filesystem_mirror {
    path    = %q
    include = ["registry.terraform.io/cdktn-io/cfncompat"]
  }
  direct {
    exclude = ["registry.terraform.io/cdktn-io/cfncompat"]
  }
}
`, mirror)

	path := filepath.Join(buildDir(t), "dev.tfrc")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644), "failed to write Terraform CLI config")

	return path
}

// TerraformBinary returns the Terraform (or OpenTofu) CLI binary to invoke,
// honoring CFNCOMPAT_E2E_TF_BINARY (e.g. set to "tofu" to run against
// OpenTofu instead of Terraform). Defaults to "terraform".
func TerraformBinary() string {
	if bin := os.Getenv("CFNCOMPAT_E2E_TF_BINARY"); bin != "" {
		return bin
	}
	return defaultTFBinary
}

// CopyFixture copies fixtures/<fixtureName> to a per-test working directory
// under integ/tf/<test-name>/<fixtureName>, so parallel test runs (and
// repeated local runs) never collide and Terraform state stays out of the
// checked-in fixtures/ tree. When any SKIP_<stage> environment variable is
// set (the local iterative-development affordance), it instead reuses the
// fixture directory in place so state persists between stage-skipping runs.
func CopyFixture(t *testing.T, fixtureName string) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err, "failed to get working directory")
	tfDir := filepath.Join(wd, "tf")
	require.NoError(t, os.MkdirAll(tfDir, 0o755), "failed to create tf directory")

	return test_structure.CopyTerraformFolderToDest(t, "fixtures", fixtureName, tfDir)
}

// NewTerraformOptions builds the provider, writes the filesystem-mirror CLI
// config, and returns terraform.Options rooted at workingDir wired to
// resolve cfncompat from that mirror while every other provider resolves
// normally from the public registry.
func NewTerraformOptions(t *testing.T, workingDir string, vars map[string]interface{}) *terraform.Options {
	t.Helper()

	BuildProvider(t)
	cliConfigPath := WriteCLIConfig(t)

	envVars := map[string]string{
		"TF_CLI_CONFIG_FILE": cliConfigPath,
	}

	options := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir:    workingDir,
		TerraformBinary: TerraformBinary(),
		Vars:            vars,
		EnvVars:         envVars,
		NoColor:         true,
	})

	return options
}
