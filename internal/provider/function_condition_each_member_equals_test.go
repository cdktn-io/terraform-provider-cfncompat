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

// conditionEachMemberEqualsStringList is a small test helper that builds a
// types.List of types.String values for use as the "list_of_strings"
// argument in test cases.
func conditionEachMemberEqualsStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

func TestConditionEachMemberEqualsFunction(t *testing.T) {
	t.Parallel()

	nullElementList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("IT"),
		types.StringNull(),
	})

	tests := map[string]struct {
		listOfStrings types.List
		value         types.String
		expected      bool
		expectError   string
	}{
		// Official AWS doc example:
		//   "Fn::EachMemberEquals" : [
		//     {"Fn::ValueOfAll" : ["AWS::EC2::VPC::Id", "Tags.Department"]}, "IT"
		//   ]
		// Here Fn::ValueOfAll is pre-resolved to its list-of-strings result,
		// since this provider function operates on already-resolved values.
		"doc example: all members equal value": {
			listOfStrings: conditionEachMemberEqualsStringList("IT", "IT", "IT"),
			value:         types.StringValue("IT"),
			expected:      true,
		},
		"doc example variant: one member differs from value": {
			listOfStrings: conditionEachMemberEqualsStringList("IT", "Finance", "IT"),
			value:         types.StringValue("IT"),
			expected:      false,
		},
		"single member equal to value": {
			listOfStrings: conditionEachMemberEqualsStringList("A"),
			value:         types.StringValue("A"),
			expected:      true,
		},
		"single member not equal to value": {
			listOfStrings: conditionEachMemberEqualsStringList("B"),
			value:         types.StringValue("A"),
			expected:      false,
		},
		"empty list is vacuously true": {
			listOfStrings: conditionEachMemberEqualsStringList(),
			value:         types.StringValue("IT"),
			expected:      true,
		},
		"comparison is case-sensitive": {
			listOfStrings: conditionEachMemberEqualsStringList("it", "IT"),
			value:         types.StringValue("IT"),
			expected:      false,
		},
		"value is an empty string and all members are empty": {
			listOfStrings: conditionEachMemberEqualsStringList("", ""),
			value:         types.StringValue(""),
			expected:      true,
		},
		"value is an empty string but a member is not": {
			listOfStrings: conditionEachMemberEqualsStringList("", "x"),
			value:         types.StringValue(""),
			expected:      false,
		},
		"null list_of_strings is an error": {
			listOfStrings: types.ListNull(types.StringType),
			value:         types.StringValue("IT"),
			expectError:   "condition_each_member_equals: list_of_strings must not be null",
		},
		"null value is an error": {
			listOfStrings: conditionEachMemberEqualsStringList("IT"),
			value:         types.StringNull(),
			expectError:   "condition_each_member_equals: value must not be null",
		},
		"null element within list_of_strings is an error": {
			listOfStrings: nullElementList,
			value:         types.StringValue("IT"),
			expectError:   "condition_each_member_equals: list_of_strings[1] must not be null",
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

			NewConditionEachMemberEqualsFunction().Run(context.Background(), req, &resp)

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
				t.Fatalf("expected %v, got %v", tc.expected, got.ValueBool())
			}
		})
	}
}

// TestAccConditionEachMemberEqualsFunction is an acceptance test exercising
// provider::cfncompat::condition_each_member_equals through a real Terraform
// CLI run. It is gated by TF_ACC=1 and is not run as part of `make test`; it
// is registered here for the batch acceptance run after provider.go wiring.
func TestAccConditionEachMemberEqualsFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_each_member_equals(["IT", "IT", "IT"], "IT")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_each_member_equals(["IT", "Finance"], "IT")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_each_member_equals([], "IT")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
		},
	})
}
