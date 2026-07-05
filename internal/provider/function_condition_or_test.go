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

// conditionOrTuple builds the single tftypes.Tuple-backed attr.Value that the
// framework packs a variadic BoolParameter's arguments into: a types.Tuple
// with one types.BoolType element per argument.
func conditionOrTuple(conditions ...types.Bool) attr.Value {
	elementTypes := make([]attr.Type, len(conditions))
	elements := make([]attr.Value, len(conditions))

	for i, c := range conditions {
		elementTypes[i] = types.BoolType
		elements[i] = c
	}

	return types.TupleValueMust(elementTypes, elements)
}

func TestConditionOrFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		conditions  []types.Bool
		expected    bool
		expectError string
	}{
		// Official AWS doc example: MyOrCondition evaluates to true if the
		// referenced security group name is equal to sg-mysggroup or if
		// SomeOtherCondition evaluates to true. Fn::Equals/Condition are
		// pre-resolved to booleans by the synthesis backend before being
		// passed to this provider function.
		"doc example: first condition true, second false": {
			conditions: []types.Bool{types.BoolValue(true), types.BoolValue(false)},
			expected:   true,
		},
		"doc example: first condition false, second true": {
			conditions: []types.Bool{types.BoolValue(false), types.BoolValue(true)},
			expected:   true,
		},
		"doc example: both conditions false": {
			conditions: []types.Bool{types.BoolValue(false), types.BoolValue(false)},
			expected:   false,
		},
		"two true conditions returns true": {
			conditions: []types.Bool{types.BoolValue(true), types.BoolValue(true)},
			expected:   true,
		},
		"three conditions, one true, returns true": {
			conditions: []types.Bool{types.BoolValue(false), types.BoolValue(false), types.BoolValue(true)},
			expected:   true,
		},
		"three conditions, all false, returns false": {
			conditions: []types.Bool{types.BoolValue(false), types.BoolValue(false), types.BoolValue(false)},
			expected:   false,
		},
		"maximum of 10 conditions, all false, returns false": {
			conditions: []types.Bool{
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), types.BoolValue(false),
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), types.BoolValue(false),
				types.BoolValue(false), types.BoolValue(false),
			},
			expected: false,
		},
		"maximum of 10 conditions, last true, returns true": {
			conditions: []types.Bool{
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), types.BoolValue(false),
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), types.BoolValue(false),
				types.BoolValue(false), types.BoolValue(true),
			},
			expected: true,
		},
		"fewer than 2 conditions is an error": {
			conditions:  []types.Bool{types.BoolValue(true)},
			expectError: "condition_or: expected between 2 and 10 conditions, got 1",
		},
		"zero conditions is an error": {
			conditions:  []types.Bool{},
			expectError: "condition_or: expected between 2 and 10 conditions, got 0",
		},
		"more than 10 conditions is an error": {
			conditions: []types.Bool{
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), types.BoolValue(false),
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), types.BoolValue(false),
				types.BoolValue(false), types.BoolValue(false), types.BoolValue(true),
			},
			expectError: "condition_or: expected between 2 and 10 conditions, got 11",
		},
		"null condition is an error": {
			conditions:  []types.Bool{types.BoolValue(true), types.BoolNull()},
			expectError: "condition_or: conditions[1] must not be null",
		},
		"null condition before a true condition is still an error (evaluated left to right)": {
			conditions:  []types.Bool{types.BoolNull(), types.BoolValue(true)},
			expectError: "condition_or: conditions[0] must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{conditionOrTuple(tc.conditions...)}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionOrFunction().Run(context.Background(), req, &resp)

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

// TestConditionOrFunction_unknownCondition verifies that an unknown condition
// value (e.g. known only after apply) short-circuits to an unknown,
// error-free result rather than being evaluated.
func TestConditionOrFunction_unknownCondition(t *testing.T) {
	t.Parallel()

	req := function.RunRequest{
		Arguments: function.NewArgumentsData([]attr.Value{
			conditionOrTuple(types.BoolValue(false), types.BoolUnknown()),
		}),
	}
	resp := function.RunResponse{
		Result: function.NewResultData(types.BoolUnknown()),
	}

	NewConditionOrFunction().Run(context.Background(), req, &resp)

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
}

// TestAccConditionOrFunction is an acceptance test exercising
// provider::cfncompat::condition_or through a real Terraform CLI run. It is
// gated by TF_ACC=1 and is not run as part of `make test`; it is registered
// here for the batch acceptance run after provider.go wiring.
func TestAccConditionOrFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_or(true, false)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_or(false, false, false)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
		},
	})
}
