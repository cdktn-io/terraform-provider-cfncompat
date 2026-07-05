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

// conditionContainsStringList is a small test helper that builds a
// types.List of types.String values for use as the "list_of_strings"
// argument in test cases.
func conditionContainsStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

func TestConditionContainsFunction(t *testing.T) {
	t.Parallel()

	nullElementList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("t3.large"),
		types.StringNull(),
	})

	tests := map[string]struct {
		listOfStrings types.List
		value         types.String
		expected      bool
		expectError   string
	}{
		// Official AWS doc example (Rule functions reference, Fn::Contains):
		// "Fn::Contains" : [ ["t3.large", "t3.small"], {"Ref" : "InstanceType"} ]
		// evaluates to true if the InstanceType parameter value is contained
		// in the list (t3.large or t3.small).
		"doc example: value present in list (t3.large)": {
			listOfStrings: conditionContainsStringList("t3.large", "t3.small"),
			value:         types.StringValue("t3.large"),
			expected:      true,
		},
		"doc example: value present in list (t3.small)": {
			listOfStrings: conditionContainsStringList("t3.large", "t3.small"),
			value:         types.StringValue("t3.small"),
			expected:      true,
		},
		"doc example: value not present in list": {
			listOfStrings: conditionContainsStringList("t3.large", "t3.small"),
			value:         types.StringValue("t3.micro"),
			expected:      false,
		},
		// Official AWS doc example (rules-section-structure.html, Conditionally
		// verify a parameter value): "Fn::Contains": [ ["t3.medium"], {"Ref": "InstanceType"} ]
		"doc example: single-element list match": {
			listOfStrings: conditionContainsStringList("t3.medium"),
			value:         types.StringValue("t3.medium"),
			expected:      true,
		},
		"doc example: single-element list, no match": {
			listOfStrings: conditionContainsStringList("t3.large"),
			value:         types.StringValue("t3.medium"),
			expected:      false,
		},
		"empty list never contains value": {
			listOfStrings: conditionContainsStringList(),
			value:         types.StringValue("anything"),
			expected:      false,
		},
		"empty string value can match an empty string element": {
			listOfStrings: conditionContainsStringList("a", "", "b"),
			value:         types.StringValue(""),
			expected:      true,
		},
		"empty string value does not match when absent": {
			listOfStrings: conditionContainsStringList("a", "b"),
			value:         types.StringValue(""),
			expected:      false,
		},
		"comparison is case sensitive": {
			listOfStrings: conditionContainsStringList("A", "B"),
			value:         types.StringValue("a"),
			expected:      false,
		},
		"duplicate matching entries still return true": {
			listOfStrings: conditionContainsStringList("x", "x", "y"),
			value:         types.StringValue("x"),
			expected:      true,
		},
		"null list_of_strings is an error": {
			listOfStrings: types.ListNull(types.StringType),
			value:         types.StringValue("t3.large"),
			expectError:   "condition_contains: list_of_strings must not be null",
		},
		"null value is an error": {
			listOfStrings: conditionContainsStringList("t3.large"),
			value:         types.StringNull(),
			expectError:   "condition_contains: value must not be null",
		},
		"null element in list_of_strings is an error": {
			listOfStrings: nullElementList,
			value:         types.StringValue("t3.large"),
			expectError:   "condition_contains: list_of_strings[1] must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.listOfStrings, tc.value}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionContainsFunction().Run(context.Background(), req, &resp)

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

			got, ok := resp.Result.Value().(types.Bool)
			if !ok {
				t.Fatalf("expected result of type types.Bool, got %T", resp.Result.Value())
			}

			if got.ValueBool() != tc.expected {
				t.Fatalf("expected %t, got %t", tc.expected, got.ValueBool())
			}
		})
	}
}

// TestAccConditionContainsFunction is an acceptance test exercising
// provider::cfncompat::condition_contains through a real Terraform CLI run.
// It is gated by TF_ACC=1 and is not run as part of `make test`; it is
// registered here for the batch acceptance run after provider.go wiring.
func TestAccConditionContainsFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_contains(["t3.large", "t3.small"], "t3.large")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_contains(["t3.large", "t3.small"], "t3.micro")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
		},
	})
}
