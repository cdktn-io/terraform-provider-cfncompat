// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package integ

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	test_structure "github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	// accountIDPattern is the AWS account-id shape returned by STS
	// GetCallerIdentity: exactly 12 digits.
	accountIDPattern = regexp.MustCompile(`^[0-9]{12}$`)
	// stackIDPattern is the AWS::StackId ARN shape cfncompat synthesizes:
	// arn:<partition>:cloudformation:<region>:<account>:stack/<name>/<uuid>
	// (RFC 006 section 2.3 -- the shape is kept for protocol fidelity because
	// RFC 005 passes it verbatim as the event's StackId).
	// The UUID component is pinned to a version 5 (name-based) UUID -- the
	// version nibble is 5 and the variant nibble is one of [89ab] -- which
	// is what makes stack_id a pure function of its inputs rather than a
	// random value.
	stackIDPattern = regexp.MustCompile(
		`^arn:[a-z0-9-]+:cloudformation:[a-z0-9-]+:[0-9]{12}:stack/cfncompat-e2e/` +
			`[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// TestE2EPseudoParameters exercises the two RFC 006 data sources end to end
// against real AWS: cfncompat_pseudo_parameters (all AWS::* pseudo
// parameters, one STS GetCallerIdentity) and cfncompat_availability_zones
// (Fn::GetAZs, EC2 DescribeAvailabilityZones + DescribeSubnets). The fixture
// is read-only -- it declares no resources and creates nothing -- but the
// data sources still need credentials, so this is skipped unless
// CFNCOMPAT_E2E_AWS=1.
func TestE2EPseudoParameters(t *testing.T) {
	if os.Getenv("CFNCOMPAT_E2E_AWS") != "1" {
		t.Skip("set CFNCOMPAT_E2E_AWS=1 to run TestE2EPseudoParameters (it calls real AWS STS/EC2 APIs and needs credentials)")
	}

	t.Parallel()

	workingDir := CopyFixture(t, "pseudo_parameters")

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// The fixture creates nothing, so cleanup is only about dropping the
	// (data-source-only) state -- kept for stage-skipping parity with the
	// other e2e tests.
	defer test_structure.RunTestStage(t, "cleanup", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.Destroy(t, options)
	})

	test_structure.RunTestStage(t, "build_provider", func() {
		options := NewTerraformOptions(t, workingDir, nil)
		options.EnvVars["AWS_REGION"] = region
		test_structure.SaveTerraformOptions(t, workingDir, options)
	})

	test_structure.RunTestStage(t, "deploy", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)
		terraform.InitAndApply(t, options)
	})

	test_structure.RunTestStage(t, "validate", func() {
		options := test_structure.LoadTerraformOptions(t, workingDir)

		validatePseudoParameterOutputs(t, options, region)
		validateAvailabilityZoneOutputs(t, options, region)
		validateComposedOutputs(t, options)
		validateStackIDDeterminism(t, options)
	})
}

// validatePseudoParameterOutputs asserts the AWS::* pseudo-parameter
// attribute contract from RFC 006 section 2.2.
func validatePseudoParameterOutputs(t *testing.T, options *terraform.Options, region string) {
	t.Helper()

	// AWS::AccountId -- STS GetCallerIdentity.
	accountID := terraform.Output(t, options, "account_id")
	assert.Regexp(t, accountIDPattern, accountID, "account_id should be a 12-digit AWS account id")

	// AWS::Partition -- from the caller ARN; the e2e role is a commercial
	// partition role.
	partition := terraform.Output(t, options, "partition")
	assert.Equal(t, "aws", partition)

	// AWS::Region -- the resolved provider region. The child terraform run
	// was forced to exactly this region via AWS_REGION, so it is the answer
	// the data source must give (re-deriving it from this process's own
	// environment would not be).
	gotRegion := terraform.Output(t, options, "region")
	assert.Equal(t, region, gotRegion, "region should be the region the fixture was run in")

	// AWS::URLSuffix -- partition -> DNS suffix table (RFC 006 section 2.4).
	assert.Equal(t, "amazonaws.com", terraform.Output(t, options, "url_suffix"))

	// AWS::StackName / AWS::NotificationARNs -- echoed inputs.
	assert.Equal(t, "cfncompat-e2e", terraform.Output(t, options, "stack_name"))
	assert.Equal(t,
		[]string{"arn:aws:sns:us-east-1:123456789012:e2e"},
		terraform.OutputList(t, options, "notification_arns"),
		"notification_arns should be echoed back verbatim")

	// AWS::StackId -- the deterministic CloudFormation stack ARN.
	stackID := terraform.Output(t, options, "stack_id")
	assert.Regexp(t, stackIDPattern, stackID, "stack_id should be a CloudFormation stack ARN")
	assert.Contains(t, stackID, accountID, "stack_id should embed the caller's account id")
	assert.Contains(t, stackID, gotRegion, "stack_id should embed the resolved region")

	// id -- <partition>:<account_id>:<region>.
	assert.Equal(t, partition+":"+accountID+":"+gotRegion, terraform.Output(t, options, "id"))

	// The anonymous (no stack_name) data source: no stack identity to derive,
	// so stack_id and stack_name are null -- normalized to "" by the fixture.
	assert.Empty(t, terraform.Output(t, options, "anonymous_stack_id"),
		"stack_id should be null without stack_name")
	assert.Empty(t, terraform.Output(t, options, "anonymous_stack_name"),
		"stack_name should stay unset when not configured")
	assert.Empty(t, terraform.OutputList(t, options, "anonymous_notification_arns"),
		"notification_arns should default to an empty list")
	// Same environment, same answer: the account is not a function of the
	// stack inputs.
	assert.Equal(t, accountID, terraform.Output(t, options, "anonymous_account_id"))
}

// validateAvailabilityZoneOutputs asserts the Fn::GetAZs attribute contract
// from RFC 006 section 3.
func validateAvailabilityZoneOutputs(t *testing.T, options *terraform.Options, region string) {
	t.Helper()

	names := terraform.OutputList(t, options, "az_names")
	require.NotEmpty(t, names, "names (Fn::GetAZs) should not be empty")
	for _, name := range names {
		assert.True(t, strings.HasPrefix(name, region),
			"availability zone %q should be prefixed with the region %q", name, region)
	}
	assert.IsIncreasing(t, names, "names should be alphabetically ordered")

	allNames := terraform.OutputList(t, options, "az_all_names")
	require.NotEmpty(t, allNames, "all_names should not be empty")
	assert.Subset(t, allNames, names, "names should be a subset of all_names")
	assert.IsIncreasing(t, allNames, "all_names should be alphabetically ordered")

	zoneIDs := terraform.OutputList(t, options, "az_zone_ids")
	assert.Len(t, zoneIDs, len(allNames), "zone_ids should align 1:1 with all_names")
	for _, zoneID := range zoneIDs {
		assert.NotEmpty(t, zoneID, "zone id should not be empty")
	}

	// id is the region.
	assert.Equal(t, region, terraform.Output(t, options, "az_id"))

	// Fn::GetAZs "" and Fn::GetAZs AWS::Region must agree.
	assert.Equal(t, names, terraform.OutputList(t, options, "az_explicit_names"),
		"an explicit region equal to AWS::Region should yield the same zones")

	// An explicitly empty region is AWS::Region too -- the Fn::GetAZs
	// reference makes "" and an omitted argument the same thing.
	assert.Equal(t, names, terraform.OutputList(t, options, "az_empty_region_names"),
		`region = "" should yield the same zones as an omitted region`)
	assert.Equal(t, region, terraform.Output(t, options, "az_empty_region"),
		`region = "" should echo back the resolved region`)
	assert.Equal(t, region, terraform.Output(t, options, "az_empty_region_id"),
		`id should be the resolved region for region = ""`)
}

// validateComposedOutputs asserts the values the bridge actually emits:
// Fn::Select over Fn::GetAZs, and ARN/endpoint string composition over the
// pseudo parameters.
func validateComposedOutputs(t *testing.T, options *terraform.Options) {
	t.Helper()

	names := terraform.OutputList(t, options, "az_names")
	require.NotEmpty(t, names)

	assert.Equal(t, names[0], terraform.Output(t, options, "az0"),
		"select(0, names) should be the first availability zone")
	// The fixture guards select(1, ...) with a length check, so a
	// single-zone region yields "" rather than failing the plan.
	if len(names) > 1 {
		assert.Equal(t, names[1], terraform.Output(t, options, "az1"),
			"select(1, names) should be the second availability zone")
	} else {
		assert.Empty(t, terraform.Output(t, options, "az1"),
			"az1 should be empty in a region with a single availability zone")
	}

	composedARN := terraform.Output(t, options, "composed_arn")
	assert.True(t, strings.HasPrefix(composedARN, "arn:aws:lambda:"),
		"composed_arn %q should start with arn:aws:lambda:", composedARN)
	assert.True(t, strings.HasSuffix(composedARN, ":function:cfncompat-e2e"),
		"composed_arn %q should end with the function name", composedARN)

	s3Endpoint := terraform.Output(t, options, "s3_endpoint")
	assert.True(t, strings.HasPrefix(s3Endpoint, "s3."),
		"s3_endpoint %q should start with s3.", s3Endpoint)
	assert.True(t, strings.HasSuffix(s3Endpoint, ".amazonaws.com"),
		"s3_endpoint %q should end with the partition url_suffix", s3Endpoint)

	assert.Equal(t, "cfncompat-e2e:ExportsOutputRefBucket", terraform.Output(t, options, "export_name"))
}

// validateStackIDDeterminism re-applies the fixture and asserts AWS::StackId
// is unchanged. CDK custom-resource handlers use stack_id as an ownership key
// (the S3 notifications handler prefixes every notification Id with it), so a
// stack_id that drifts between applies would orphan or delete the wrong
// resources -- RFC 006 section 2.3 makes it a pure function of
// (partition, region, account_id, stack_name).
func validateStackIDDeterminism(t *testing.T, options *terraform.Options) {
	t.Helper()

	before := terraform.Output(t, options, "stack_id")
	require.NotEmpty(t, before)

	terraform.Apply(t, options)

	after := terraform.Output(t, options, "stack_id")
	assert.Equal(t, before, after, "stack_id must be stable across applies")

	// The re-read data sources must also leave the plan empty: exit code 0
	// means "no changes", 2 means "changes present".
	assert.Equal(t, 0, terraform.PlanExitCode(t, options),
		"a plan after apply should report no changes")
}
