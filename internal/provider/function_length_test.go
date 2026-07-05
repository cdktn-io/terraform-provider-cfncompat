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

// lengthStringList is a small test helper that builds a types.List of
// types.String values for use as the "value" argument in test cases.
func lengthStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

// lengthStringTuple is a small test helper that builds a types.Tuple of
// types.String values (as HCL list literals with a dynamic parameter are
// typically inferred as tuples) for use as the "value" argument.
func lengthStringTuple(values ...string) types.Tuple {
	elemTypes := make([]attr.Type, 0, len(values))
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elemTypes = append(elemTypes, types.StringType)
		elems = append(elems, types.StringValue(v))
	}

	return types.TupleValueMust(elemTypes, elems)
}

// lengthStringSet is a small test helper that builds a types.Set of
// types.String values for use as the "value" argument in test cases.
func lengthStringSet(values ...string) types.Set {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.SetValueMust(types.StringType, elems)
}

func TestLengthFunction(t *testing.T) {
	t.Parallel()

	heterogeneousTuple := types.TupleValueMust(
		[]attr.Type{types.StringType, types.NumberType, types.BoolType},
		[]attr.Value{types.StringValue("a"), types.NumberValue(big.NewFloat(42)), types.BoolValue(true)},
	)

	tests := map[string]struct {
		value       types.Dynamic
		expected    *big.Float
		expectError string
	}{
		// Official AWS doc example: Fn::Length applied to the result of
		// Fn::Split("|", "a|b|c"), which produces ["a", "b", "c"]. The
		// function returns 3.
		"doc example: length of Fn::Split result (list)": {
			value:    types.DynamicValue(lengthStringList("a", "b", "c")),
			expected: big.NewFloat(3),
		},
		"doc example: length of Fn::Split result (tuple)": {
			value:    types.DynamicValue(lengthStringTuple("a", "b", "c")),
			expected: big.NewFloat(3),
		},
		// Official AWS doc example: Fn::Length of a Ref to a list parameter
		// type with 3 elements returns 3.
		"doc example: length of Ref to list parameter with 3 elements": {
			value:    types.DynamicValue(lengthStringList("x", "y", "z")),
			expected: big.NewFloat(3),
		},
		// Official AWS doc example: Fn::Length of a literal array
		// [1, {"Ref": "ParameterName"}, 3] (a heterogeneous 3-element array)
		// returns 3.
		"doc example: length of literal heterogeneous array": {
			value:    types.DynamicValue(heterogeneousTuple),
			expected: big.NewFloat(3),
		},
		"empty list has length 0": {
			value:    types.DynamicValue(lengthStringList()),
			expected: big.NewFloat(0),
		},
		"empty tuple has length 0": {
			value:    types.DynamicValue(lengthStringTuple()),
			expected: big.NewFloat(0),
		},
		"single element list has length 1": {
			value:    types.DynamicValue(lengthStringList("only")),
			expected: big.NewFloat(1),
		},
		"set of strings counts elements": {
			value:    types.DynamicValue(lengthStringSet("a", "b", "c", "d")),
			expected: big.NewFloat(4),
		},
		"empty set has length 0": {
			value:    types.DynamicValue(lengthStringSet()),
			expected: big.NewFloat(0),
		},
		"null value (dynamic null) is an error": {
			value:       types.DynamicNull(),
			expectError: "length: value must not be null",
		},
		"null value (typed null list wrapped in dynamic) is an error": {
			value:       types.DynamicValue(types.ListNull(types.StringType)),
			expectError: "length: value must not be null",
		},
		"string value is an error (non-array)": {
			value:       types.DynamicValue(types.StringValue("abc")),
			expectError: "length: value must be a list, tuple, or set, got basetypes.StringValue",
		},
		"number value is an error (non-array)": {
			value:       types.DynamicValue(types.NumberValue(big.NewFloat(42))),
			expectError: "length: value must be a list, tuple, or set, got basetypes.NumberValue",
		},
		"bool value is an error (non-array)": {
			value:       types.DynamicValue(types.BoolValue(true)),
			expectError: "length: value must be a list, tuple, or set, got basetypes.BoolValue",
		},
		"map value is an error (non-array)": {
			value: types.DynamicValue(types.MapValueMust(
				types.StringType,
				map[string]attr.Value{"key": types.StringValue("value")},
			)),
			expectError: "length: value must be a list, tuple, or set, got basetypes.MapValue",
		},
		"object value is an error (non-array)": {
			value: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{"key": types.StringType},
				map[string]attr.Value{"key": types.StringValue("value")},
			)),
			expectError: "length: value must be a list, tuple, or set, got basetypes.ObjectValue",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.value}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.NumberUnknown()),
			}

			NewLengthFunction().Run(context.Background(), req, &resp)

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

			got, ok := resp.Result.Value().(types.Number)
			if !ok {
				t.Fatalf("expected result of type types.Number, got %T", resp.Result.Value())
			}

			if got.ValueBigFloat().Cmp(tc.expected) != 0 {
				t.Fatalf("expected %s, got %s", tc.expected.String(), got.ValueBigFloat().String())
			}
		})
	}
}

// TestAccLengthFunction is an acceptance test exercising
// provider::cfncompat::length through a real Terraform CLI run. It is gated
// by TF_ACC=1 and is not run as part of `make test`; it is registered here
// for the batch acceptance run after provider.go wiring.
func TestAccLengthFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::length(provider::cfncompat::split("|", "a|b|c"))
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "3"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::length(["1", "2", "3"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "3"),
				),
			},
		},
	})
}
