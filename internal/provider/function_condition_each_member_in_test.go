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

// conditionEachMemberInStringList is a small test helper that builds a
// types.List of types.String values for use as arguments in test cases.
func conditionEachMemberInStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

func TestConditionEachMemberInFunction(t *testing.T) {
	t.Parallel()

	nullElementList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("a"),
		types.StringNull(),
	})

	tests := map[string]struct {
		stringsToCheck types.List
		stringsToMatch types.List
		expected       bool
		expectError    string
	}{
		// Official AWS doc example: "Fn::EachMemberIn" : [
		//   {"Fn::ValueOfAll" : ["AWS::EC2::Subnet::Id", "VpcId"]}, {"Fn::RefAll" : "AWS::EC2::VPC::Id"}
		// ] checks whether every subnet's VpcId is one of the account/Region's
		// VPC IDs. Fn::ValueOfAll/Fn::RefAll must be pre-resolved to concrete
		// string lists by the synthesis backend before calling this function.
		"doc example: every subnet VpcId is a valid VPC ID": {
			stringsToCheck: conditionEachMemberInStringList("vpc-1111", "vpc-2222"),
			stringsToMatch: conditionEachMemberInStringList("vpc-1111", "vpc-2222", "vpc-3333"),
			expected:       true,
		},
		"doc example variant: a subnet VpcId is not a valid VPC ID": {
			stringsToCheck: conditionEachMemberInStringList("vpc-1111", "vpc-9999"),
			stringsToMatch: conditionEachMemberInStringList("vpc-1111", "vpc-2222", "vpc-3333"),
			expected:       false,
		},
		"all members of strings_to_check are present in strings_to_match": {
			stringsToCheck: conditionEachMemberInStringList("A", "B"),
			stringsToMatch: conditionEachMemberInStringList("A", "B", "C"),
			expected:       true,
		},
		"one member of strings_to_check is missing from strings_to_match": {
			stringsToCheck: conditionEachMemberInStringList("A", "D"),
			stringsToMatch: conditionEachMemberInStringList("A", "B", "C"),
			expected:       false,
		},
		"exact match of both lists": {
			stringsToCheck: conditionEachMemberInStringList("A", "B", "C"),
			stringsToMatch: conditionEachMemberInStringList("A", "B", "C"),
			expected:       true,
		},
		"duplicate members in strings_to_check are all satisfied": {
			stringsToCheck: conditionEachMemberInStringList("A", "A", "B"),
			stringsToMatch: conditionEachMemberInStringList("A", "B"),
			expected:       true,
		},
		"strings_to_match has extra members not in strings_to_check": {
			stringsToCheck: conditionEachMemberInStringList("A"),
			stringsToMatch: conditionEachMemberInStringList("A", "B", "C"),
			expected:       true,
		},
		"empty strings_to_check is vacuously true": {
			stringsToCheck: conditionEachMemberInStringList(),
			stringsToMatch: conditionEachMemberInStringList("A", "B"),
			expected:       true,
		},
		"empty strings_to_check and empty strings_to_match is vacuously true": {
			stringsToCheck: conditionEachMemberInStringList(),
			stringsToMatch: conditionEachMemberInStringList(),
			expected:       true,
		},
		"empty strings_to_match with non-empty strings_to_check is false": {
			stringsToCheck: conditionEachMemberInStringList("A"),
			stringsToMatch: conditionEachMemberInStringList(),
			expected:       false,
		},
		"comparison is case-sensitive": {
			stringsToCheck: conditionEachMemberInStringList("a"),
			stringsToMatch: conditionEachMemberInStringList("A"),
			expected:       false,
		},
		"null strings_to_check is an error": {
			stringsToCheck: types.ListNull(types.StringType),
			stringsToMatch: conditionEachMemberInStringList("A"),
			expectError:    "condition_each_member_in: strings_to_check must not be null",
		},
		"null strings_to_match is an error": {
			stringsToCheck: conditionEachMemberInStringList("A"),
			stringsToMatch: types.ListNull(types.StringType),
			expectError:    "condition_each_member_in: strings_to_match must not be null",
		},
		"null element in strings_to_check is an error": {
			stringsToCheck: nullElementList,
			stringsToMatch: conditionEachMemberInStringList("A"),
			expectError:    "condition_each_member_in: strings_to_check[1] must not be null",
		},
		"null element in strings_to_match is an error": {
			stringsToCheck: conditionEachMemberInStringList("A"),
			stringsToMatch: nullElementList,
			expectError:    "condition_each_member_in: strings_to_match[1] must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.stringsToCheck, tc.stringsToMatch}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionEachMemberInFunction().Run(context.Background(), req, &resp)

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

// TestConditionEachMemberInFunction_unknownValues verifies that unknown list
// arguments (and unknown elements within otherwise-known lists) short-circuit
// to an unknown, error-free result rather than being evaluated.
func TestConditionEachMemberInFunction_unknownValues(t *testing.T) {
	t.Parallel()

	unknownElementList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("A"),
		types.StringUnknown(),
	})

	tests := map[string]struct {
		stringsToCheck types.List
		stringsToMatch types.List
	}{
		"unknown strings_to_check list": {
			stringsToCheck: types.ListUnknown(types.StringType),
			stringsToMatch: conditionEachMemberInStringList("A"),
		},
		"unknown strings_to_match list": {
			stringsToCheck: conditionEachMemberInStringList("A"),
			stringsToMatch: types.ListUnknown(types.StringType),
		},
		"unknown element in strings_to_check": {
			stringsToCheck: unknownElementList,
			stringsToMatch: conditionEachMemberInStringList("A"),
		},
		"unknown element in strings_to_match": {
			stringsToCheck: conditionEachMemberInStringList("A"),
			stringsToMatch: unknownElementList,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.stringsToCheck, tc.stringsToMatch}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionEachMemberInFunction().Run(context.Background(), req, &resp)

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Text)
			}

			got, ok := resp.Result.Value().(types.Bool)
			if !ok {
				t.Fatalf("expected result of type types.Bool, got %T", resp.Result.Value())
			}

			if !got.IsUnknown() {
				t.Fatalf("expected unknown result, got %v", got)
			}
		})
	}
}

// TestAccConditionEachMemberInFunction is an acceptance test exercising
// provider::cfncompat::condition_each_member_in through a real Terraform CLI
// run. It is gated by TF_ACC=1 and is not run as part of `make test`; it is
// registered here for the batch acceptance run after provider.go wiring.
func TestAccConditionEachMemberInFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_each_member_in(["vpc-1111", "vpc-2222"], ["vpc-1111", "vpc-2222", "vpc-3333"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_each_member_in(["vpc-1111", "vpc-9999"], ["vpc-1111", "vpc-2222", "vpc-3333"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_each_member_in([], ["A", "B"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
		},
	})
}
