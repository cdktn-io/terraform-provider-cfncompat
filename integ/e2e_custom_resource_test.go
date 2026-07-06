// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package integ

import (
	"os"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/terraform"
	test_structure "github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2ECustomResource exercises cfncompat_custom_resource end to end
// against real AWS: it deploys an S3 bucket (response transport) and a
// Lambda-backed CloudFormation-style echo handler, then applies a
// cfncompat_custom_resource that invokes it, asserting the Create AND
// Update lifecycle paths. Requires AWS credentials; skipped unless
// CFNCOMPAT_E2E_AWS=1.
func TestE2ECustomResource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_E2E_AWS") != "1" {
		t.Skip("set CFNCOMPAT_E2E_AWS=1 to run TestE2ECustomResource (it deploys real AWS resources and needs credentials)")
	}

	t.Parallel()

	workingDir := CopyFixture(t, "custom_resource")

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	nameSuffix := strings.ToLower(random.UniqueId())

	defer test_structure.RunTestStage(t, "cleanup", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.Destroy(t, options)
	})

	test_structure.RunTestStage(t, "build_provider", func() {
		options := NewTerraformOptions(t, workingDir, map[string]interface{}{
			"name_suffix": nameSuffix,
		})
		options.EnvVars["AWS_REGION"] = region
		test_structure.SaveTerraformOptions(t, workingDir, options)
	})

	test_structure.RunTestStage(t, "deploy", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.InitAndApply(t, options)
	})

	test_structure.RunTestStage(t, "validate", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)

		physicalID := terraform.Output(t, options, "physical_resource_id")
		assert.NotEmpty(t, physicalID, "physical_resource_id should not be empty")
		assert.Equal(t, "hello", terraform.Output(t, options, "echo"))

		// Update sub-stage: re-apply with a changed greeting and confirm the
		// custom resource's Update event path round-trips the new value.
		options.Vars["greeting"] = "updated"
		terraform.Apply(t, options)

		require.Equal(t, "updated", terraform.Output(t, options, "echo"))
	})
}
