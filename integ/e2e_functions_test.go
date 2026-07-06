// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package integ

import (
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	test_structure "github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2EFunctions applies fixtures/functions/main.tf, which exercises all
// 17 provider::cfncompat::* provider-defined functions, against a locally
// built cfncompat provider binary delivered via a Terraform filesystem
// mirror (see BuildProvider/WriteCLIConfig in util.go). It requires no AWS
// credentials: none of the functions under test make AWS API calls.
func TestE2EFunctions(t *testing.T) {
	t.Parallel()

	workingDir := CopyFixture(t, "functions")

	defer test_structure.RunTestStage(t, "cleanup", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.Destroy(t, options)
	})

	test_structure.RunTestStage(t, "build_provider", func() {
		options := NewTerraformOptions(t, workingDir, nil)
		test_structure.SaveTerraformOptions(t, workingDir, options)
	})

	test_structure.RunTestStage(t, "deploy", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.InitAndApply(t, options)
	})

	test_structure.RunTestStage(t, "validate", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		validateFunctionOutputs(t, options)
	})
}

func validateFunctionOutputs(t *testing.T, options *terraform.Options) {
	t.Helper()

	assert.Equal(t, "QVdTIENsb3VkRm9ybWF0aW9u", terraform.Output(t, options, "base64"))

	cidrs := terraform.OutputList(t, options, "cidr")
	require.Len(t, cidrs, 6)
	assert.Equal(t, "192.168.0.0/27", cidrs[0])
	assert.Equal(t, "192.168.0.160/27", cidrs[5])

	assert.Equal(t, "v", terraform.Output(t, options, "find_in_map"))
	assert.Equal(t, "fallback", terraform.Output(t, options, "find_in_map_default"))

	assert.Equal(t, "a:b:c", terraform.Output(t, options, "join"))

	assert.Equal(t, "3", terraform.Output(t, options, "length"))

	assert.Equal(t, "grapes", terraform.Output(t, options, "select"))

	assert.Equal(t, []string{"a", "b", "c"}, terraform.OutputList(t, options, "split"))

	assert.Equal(t, "www.example.com", terraform.Output(t, options, "sub"))

	assert.Equal(t, `{"k":"v"}`, terraform.Output(t, options, "to_json_string"))

	assert.Equal(t, "true", terraform.Output(t, options, "condition_and"))
	assert.Equal(t, "true", terraform.Output(t, options, "condition_or"))
	assert.Equal(t, "true", terraform.Output(t, options, "condition_not"))
	assert.Equal(t, "true", terraform.Output(t, options, "condition_equals"))
	assert.Equal(t, "a", terraform.Output(t, options, "condition_if"))
	assert.Equal(t, "true", terraform.Output(t, options, "condition_contains"))
	assert.Equal(t, "true", terraform.Output(t, options, "condition_each_member_equals"))
	assert.Equal(t, "true", terraform.Output(t, options, "condition_each_member_in"))
}
