// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// joinStringList is a small test helper that builds a types.List of
// types.String values for use as the "values" argument in test cases.
func joinStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

func TestJoinFunction(t *testing.T) {
	t.Parallel()

	nullElementList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("a"),
		types.StringNull(),
		types.StringValue("c"),
	})

	tests := map[string]struct {
		delimiter   types.String
		values      types.List
		expected    string
		expectError string
	}{
		// Official AWS doc example 1: "Fn::Join" : [ ":", [ "a", "b", "c" ] ] => "a:b:c".
		"doc example: join a simple string array": {
			delimiter: types.StringValue(":"),
			values:    joinStringList("a", "b", "c"),
			expected:  "a:b:c",
		},
		// Official AWS doc example 2 (ARN construction), with Ref-resolved literals
		// substituted for AWS::Partition / AWS::AccountId since this provider
		// function operates on already-resolved string values.
		"doc example: join using ref-resolved values with empty delimiter": {
			delimiter: types.StringValue(""),
			values:    joinStringList("arn:", "aws", ":s3:::elasticbeanstalk-*-", "123456789012"),
			expected:  "arn:aws:s3:::elasticbeanstalk-*-123456789012",
		},
		"empty delimiter concatenates with no separator": {
			delimiter: types.StringValue(""),
			values:    joinStringList("a", "b", "c"),
			expected:  "abc",
		},
		"empty list returns empty string": {
			delimiter: types.StringValue(","),
			values:    joinStringList(),
			expected:  "",
		},
		"single element list returns the element unchanged": {
			delimiter: types.StringValue(","),
			values:    joinStringList("only"),
			expected:  "only",
		},
		"delimiter does not terminate the final value": {
			delimiter: types.StringValue("-"),
			values:    joinStringList("x", "y"),
			expected:  "x-y",
		},
		"multi-character delimiter": {
			delimiter: types.StringValue(" -- "),
			values:    joinStringList("a", "b"),
			expected:  "a -- b",
		},
		"values containing empty strings": {
			delimiter: types.StringValue(":"),
			values:    joinStringList("", "b", ""),
			expected:  ":b:",
		},
		"null element in values is an error": {
			delimiter:   types.StringValue(":"),
			values:      nullElementList,
			expectError: "join: values[1] must not be null",
		},
		"null delimiter is an error": {
			delimiter:   types.StringNull(),
			values:      joinStringList("a", "b"),
			expectError: "join: delimiter must not be null",
		},
		"null values list is an error": {
			delimiter:   types.StringValue(":"),
			values:      types.ListNull(types.StringType),
			expectError: "join: values must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.delimiter, tc.values}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.StringUnknown()),
			}

			NewJoinFunction().Run(context.Background(), req, &resp)

			if tc.expectError != "" {
				if resp.Error == nil {
					t.Fatalf("expected error %q, got nil", tc.expectError)
				}
				if resp.Error.Text != tc.expectError {
					t.Fatalf("expected error %q, got %q", tc.expectError, resp.Error.Text)
				}
				return
			}

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Text)
			}

			got, ok := resp.Result.Value().(types.String)
			if !ok {
				t.Fatalf("expected result of type types.String, got %T", resp.Result.Value())
			}

			if got.ValueString() != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got.ValueString())
			}
		})
	}
}

// TestAccJoinFunction is an acceptance test exercising provider::cfncompat::join
// through a real Terraform CLI run. It is gated by TF_ACC=1 and is not run as
// part of `make test`; it is registered here for the batch acceptance run
// after provider.go wiring.
func TestAccJoinFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::join(":", ["a", "b", "c"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "a:b:c"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::join("", [])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", ""),
				),
			},
		},
	})
}
