// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// conditionEqualsStringList is a small test helper that builds a types.List
// of types.String values for use as a non-primitive "value" argument.
func conditionEqualsStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

func TestConditionEqualsFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value1      types.Dynamic
		value2      types.Dynamic
		expected    bool
		expectError string
	}{
		// Official AWS doc example: IsProduction evaluates to true if the
		// value for the EnvironmentType parameter equals "prod". The Ref is
		// pre-resolved by the synthesis backend to a plain string before
		// being passed to this provider function.
		"doc example: IsProduction true when EnvironmentType equals prod": {
			value1:   types.DynamicValue(types.StringValue("prod")),
			value2:   types.DynamicValue(types.StringValue("prod")),
			expected: true,
		},
		"doc example: IsProduction false when EnvironmentType is dev": {
			value1:   types.DynamicValue(types.StringValue("dev")),
			value2:   types.DynamicValue(types.StringValue("prod")),
			expected: false,
		},
		// Official AWS doc example (MyAndCondition / MyOrCondition): Equals
		// [sg-mysggroup, Ref ASecurityGroup].
		"doc example: MyAndCondition security group name equal": {
			value1:   types.DynamicValue(types.StringValue("sg-mysggroup")),
			value2:   types.DynamicValue(types.StringValue("sg-mysggroup")),
			expected: true,
		},
		"doc example: MyAndCondition security group name not equal": {
			value1:   types.DynamicValue(types.StringValue("sg-mysggroup")),
			value2:   types.DynamicValue(types.StringValue("sg-otherggroup")),
			expected: false,
		},
		"empty strings are equal": {
			value1:   types.DynamicValue(types.StringValue("")),
			value2:   types.DynamicValue(types.StringValue("")),
			expected: true,
		},
		"strings are case sensitive": {
			value1:   types.DynamicValue(types.StringValue("Prod")),
			value2:   types.DynamicValue(types.StringValue("prod")),
			expected: false,
		},
		"numbers compare by canonical decimal: 1 equals 1": {
			value1:   types.DynamicValue(types.NumberValue(big.NewFloat(1))),
			value2:   types.DynamicValue(types.NumberValue(big.NewFloat(1))),
			expected: true,
		},
		"numbers compare by canonical decimal: 1 equals 1.0": {
			value1:   types.DynamicValue(types.NumberValue(conditionEqualsMustParseBigFloat(t, "1"))),
			value2:   types.DynamicValue(types.NumberValue(conditionEqualsMustParseBigFloat(t, "1.0"))),
			expected: true,
		},
		"numbers compare by canonical decimal: -0 equals 0": {
			value1:   types.DynamicValue(types.NumberValue(conditionEqualsMustParseBigFloat(t, "-0"))),
			value2:   types.DynamicValue(types.NumberValue(conditionEqualsMustParseBigFloat(t, "0"))),
			expected: true,
		},
		"numbers compare by canonical decimal: 1.5 not equal 1.05": {
			value1:   types.DynamicValue(types.NumberValue(big.NewFloat(1.5))),
			value2:   types.DynamicValue(types.NumberValue(big.NewFloat(1.05))),
			expected: false,
		},
		"string \"1\" equals number 1": {
			value1:   types.DynamicValue(types.StringValue("1")),
			value2:   types.DynamicValue(types.NumberValue(big.NewFloat(1))),
			expected: true,
		},
		"string \"1.0\" equals number 1 (string is not renormalized, but canonical number is)": {
			// The string side is compared as-is ("1.0"), while the number
			// side renders to its canonical form ("1"). These are NOT
			// equal, because only the number argument is canonicalized -
			// the string argument is never reinterpreted as a number.
			value1:   types.DynamicValue(types.StringValue("1.0")),
			value2:   types.DynamicValue(types.NumberValue(big.NewFloat(1))),
			expected: false,
		},
		"bool true equals bool true": {
			value1:   types.DynamicValue(types.BoolValue(true)),
			value2:   types.DynamicValue(types.BoolValue(true)),
			expected: true,
		},
		"bool true not equal bool false": {
			value1:   types.DynamicValue(types.BoolValue(true)),
			value2:   types.DynamicValue(types.BoolValue(false)),
			expected: false,
		},
		"bool true equals string \"true\"": {
			value1:   types.DynamicValue(types.BoolValue(true)),
			value2:   types.DynamicValue(types.StringValue("true")),
			expected: true,
		},
		"bool false equals string \"false\"": {
			value1:   types.DynamicValue(types.BoolValue(false)),
			value2:   types.DynamicValue(types.StringValue("false")),
			expected: true,
		},
		"null value_1 is an error": {
			value1:      types.DynamicNull(),
			value2:      types.DynamicValue(types.StringValue("prod")),
			expectError: "condition_equals: value_1 must not be null",
		},
		"null value_2 is an error": {
			value1:      types.DynamicValue(types.StringValue("prod")),
			value2:      types.DynamicNull(),
			expectError: "condition_equals: value_2 must not be null",
		},
		"null underlying value_1 is an error": {
			value1:      types.DynamicValue(types.StringNull()),
			value2:      types.DynamicValue(types.StringValue("prod")),
			expectError: "condition_equals: value_1 must not be null",
		},
		"null underlying value_2 is an error": {
			value1:      types.DynamicValue(types.StringValue("prod")),
			value2:      types.DynamicValue(types.NumberNull()),
			expectError: "condition_equals: value_2 must not be null",
		},
		"list value_1 (non-primitive) is an error": {
			value1:      types.DynamicValue(conditionEqualsStringList("a", "b")),
			value2:      types.DynamicValue(types.StringValue("a")),
			expectError: "condition_equals: value_1 must be a string, number, or boolean; got list",
		},
		"list value_2 (non-primitive) is an error": {
			value1:      types.DynamicValue(types.StringValue("a")),
			value2:      types.DynamicValue(conditionEqualsStringList("a", "b")),
			expectError: "condition_equals: value_2 must be a string, number, or boolean; got list",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.value1, tc.value2}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionEqualsFunction().Run(context.Background(), req, &resp)

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

// conditionEqualsMustParseBigFloat parses a decimal literal into a *big.Float with enough
// precision to exercise condition_equals' canonical-number comparison, or
// fails the test on error.
func conditionEqualsMustParseBigFloat(t *testing.T, s string) *big.Float {
	t.Helper()

	bf, _, err := big.ParseFloat(s, 10, 200, big.ToNearestEven)
	if err != nil {
		t.Fatalf("failed to parse %q as big.Float: %s", s, err)
	}

	return bf
}

// TestConditionEqualsFunction_unknownValues verifies that either argument
// being unknown (e.g. known only after apply) short-circuits to an unknown,
// error-free result rather than being evaluated.
func TestConditionEqualsFunction_unknownValues(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value1 types.Dynamic
		value2 types.Dynamic
	}{
		"value_1 unknown": {
			value1: types.DynamicUnknown(),
			value2: types.DynamicValue(types.StringValue("prod")),
		},
		"value_2 unknown": {
			value1: types.DynamicValue(types.StringValue("prod")),
			value2: types.DynamicUnknown(),
		},
		"both unknown": {
			value1: types.DynamicUnknown(),
			value2: types.DynamicUnknown(),
		},
		"underlying value_1 unknown": {
			value1: types.DynamicValue(types.StringUnknown()),
			value2: types.DynamicValue(types.StringValue("prod")),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.value1, tc.value2}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionEqualsFunction().Run(context.Background(), req, &resp)

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

// TestAccConditionEqualsFunction is an acceptance test exercising
// provider::cfncompat::condition_equals through a real Terraform CLI run. It
// is gated by TF_ACC=1 and is not run as part of `make test`; it is
// registered here for the batch acceptance run after provider.go wiring.
func TestAccConditionEqualsFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_equals("prod", "prod")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_equals("dev", "prod")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_equals(1, "1")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
		},
	})
}
