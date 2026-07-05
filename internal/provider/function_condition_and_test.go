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

// conditionAndBools converts plain Go bools into a slice of types.Bool, for
// building table-driven test cases concisely.
func conditionAndBools(vals ...bool) []types.Bool {
	out := make([]types.Bool, 0, len(vals))
	for _, v := range vals {
		out = append(out, types.BoolValue(v))
	}
	return out
}

// conditionAndTupleArg packages a slice of types.Bool as the single
// attr.Value that represents a variadic parameter's argument data: the
// framework represents a variadic parameter as one argument position
// containing a tuple with one element per call-site argument.
func conditionAndTupleArg(conditions []types.Bool) attr.Value {
	elemTypes := make([]attr.Type, len(conditions))
	elems := make([]attr.Value, len(conditions))

	for i, c := range conditions {
		elemTypes[i] = types.BoolType
		elems[i] = c
	}

	return types.TupleValueMust(elemTypes, elems)
}

func TestConditionAndFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		conditions  []types.Bool
		expected    bool
		expectError string
	}{
		// AWS doc example: MyAndCondition evaluates to true if the referenced
		// security group name is equal to sg-mysggroup AND SomeOtherCondition
		// evaluates to true. Fn::Equals and Condition references are
		// pre-resolved to booleans by the synthesis backend before being
		// passed as arguments to this provider function.
		"doc example: MyAndCondition, both operands true -> true": {
			conditions: conditionAndBools(true, true),
			expected:   true,
		},
		"doc example: MyAndCondition, Equals false, other true -> false": {
			conditions: conditionAndBools(false, true),
			expected:   false,
		},
		"doc example: MyAndCondition, Equals true, other false -> false": {
			conditions: conditionAndBools(true, false),
			expected:   false,
		},
		"doc example: MyAndCondition, both false -> false": {
			conditions: conditionAndBools(false, false),
			expected:   false,
		},
		"minimum boundary (2 conditions), all true -> true": {
			conditions: conditionAndBools(true, true),
			expected:   true,
		},
		"maximum boundary (10 conditions), all true -> true": {
			conditions: conditionAndBools(true, true, true, true, true, true, true, true, true, true),
			expected:   true,
		},
		"maximum boundary (10 conditions), one false -> false": {
			conditions: conditionAndBools(true, true, true, true, true, true, true, true, true, false),
			expected:   false,
		},
		"three conditions, all true -> true": {
			conditions: conditionAndBools(true, true, true),
			expected:   true,
		},
		"three conditions, middle false -> false": {
			conditions: conditionAndBools(true, false, true),
			expected:   false,
		},
		"fewer than 2 conditions is an error": {
			conditions:  conditionAndBools(true),
			expectError: "condition_and: expected between 2 and 10 conditions, got 1",
		},
		"zero conditions is an error": {
			conditions:  conditionAndBools(),
			expectError: "condition_and: expected between 2 and 10 conditions, got 0",
		},
		"more than 10 conditions is an error": {
			conditions:  conditionAndBools(true, true, true, true, true, true, true, true, true, true, true),
			expectError: "condition_and: expected between 2 and 10 conditions, got 11",
		},
		"null condition is an error": {
			conditions:  []types.Bool{types.BoolValue(true), types.BoolNull()},
			expectError: "condition_and: conditions[1] must not be null",
		},
		"null condition at first position is an error": {
			conditions:  []types.Bool{types.BoolNull(), types.BoolValue(true)},
			expectError: "condition_and: conditions[0] must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{conditionAndTupleArg(tc.conditions)}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionAndFunction().Run(context.Background(), req, &resp)

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

// TestConditionAndFunction_unknownCondition verifies that an unknown
// condition value (e.g. known only after apply) short-circuits to an
// unknown, error-free result rather than being evaluated.
func TestConditionAndFunction_unknownCondition(t *testing.T) {
	t.Parallel()

	conditions := []types.Bool{types.BoolValue(true), types.BoolUnknown()}

	req := function.RunRequest{
		Arguments: function.NewArgumentsData([]attr.Value{conditionAndTupleArg(conditions)}),
	}
	resp := function.RunResponse{
		Result: function.NewResultData(types.BoolUnknown()),
	}

	NewConditionAndFunction().Run(context.Background(), req, &resp)

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

// TestAccConditionAndFunction is an acceptance test exercising
// provider::cfncompat::condition_and through a real Terraform CLI run. It is
// gated by TF_ACC=1 and is not run as part of `make test`; it is registered
// here for the batch acceptance run after provider.go wiring.
func TestAccConditionAndFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_and(true, true)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_and(true, false)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_and(true, true, true, true, true, true, true, true, true, true)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
		},
	})
}
