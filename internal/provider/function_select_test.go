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

// selectStringList is a small test helper that builds a types.List of
// types.String values for use as the "objects" argument in test cases.
func selectStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

// selectStringTuple is a small test helper that builds a types.Tuple of
// types.String values (as HCL list literals with a dynamic parameter are
// typically inferred as tuples) for use as the "objects" argument.
func selectStringTuple(values ...string) types.Tuple {
	elemTypes := make([]attr.Type, 0, len(values))
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elemTypes = append(elemTypes, types.StringType)
		elems = append(elems, types.StringValue(v))
	}

	return types.TupleValueMust(elemTypes, elems)
}

func TestSelectFunction(t *testing.T) {
	t.Parallel()

	nullElementList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("a"),
		types.StringNull(),
		types.StringValue("c"),
	})

	heterogeneousTuple := types.TupleValueMust(
		[]attr.Type{types.StringType, types.NumberType, types.BoolType},
		[]attr.Value{types.StringValue("a"), types.NumberValue(big.NewFloat(42)), types.BoolValue(true)},
	)

	tests := map[string]struct {
		index       types.Int64
		objects     types.Dynamic
		expected    attr.Value
		expectError string
	}{
		// Official AWS doc example: { "Fn::Select" : [ "1", [ "apples", "grapes", "oranges", "mangoes" ] ] } => "grapes".
		"doc example: basic select from list": {
			index:    types.Int64Value(1),
			objects:  types.DynamicValue(selectStringList("apples", "grapes", "oranges", "mangoes")),
			expected: types.StringValue("grapes"),
		},
		"doc example: basic select from tuple": {
			index:    types.Int64Value(1),
			objects:  types.DynamicValue(selectStringTuple("apples", "grapes", "oranges", "mangoes")),
			expected: types.StringValue("grapes"),
		},
		// Official AWS doc example: comma-delimited list parameter resolved to a
		// list of three CIDR blocks, selecting index 0.
		"doc example: select from comma-delimited list values": {
			index: types.Int64Value(0),
			objects: types.DynamicValue(selectStringList(
				"10.0.48.0/24", "10.0.112.0/24", "10.0.176.0/24",
			)),
			expected: types.StringValue("10.0.48.0/24"),
		},
		"select first element (index 0)": {
			index:    types.Int64Value(0),
			objects:  types.DynamicValue(selectStringList("a", "b", "c")),
			expected: types.StringValue("a"),
		},
		"select last element (index N-1)": {
			index:    types.Int64Value(2),
			objects:  types.DynamicValue(selectStringList("a", "b", "c")),
			expected: types.StringValue("c"),
		},
		"single element list, index 0": {
			index:    types.Int64Value(0),
			objects:  types.DynamicValue(selectStringList("only")),
			expected: types.StringValue("only"),
		},
		"heterogeneous tuple selects the value at index unchanged": {
			index:    types.Int64Value(2),
			objects:  types.DynamicValue(heterogeneousTuple),
			expected: types.BoolValue(true),
		},
		"index out of range (too large)": {
			index:       types.Int64Value(3),
			objects:     types.DynamicValue(selectStringList("a", "b", "c")),
			expectError: "select: index 3 out of range for objects of length 3 (must be between 0 and 2)",
		},
		"index out of range (negative)": {
			index:       types.Int64Value(-1),
			objects:     types.DynamicValue(selectStringList("a", "b", "c")),
			expectError: "select: index -1 out of range for objects of length 3 (must be between 0 and 2)",
		},
		"index out of range on empty list": {
			index:       types.Int64Value(0),
			objects:     types.DynamicValue(selectStringList()),
			expectError: "select: index 0 out of range for objects of length 0 (must be between 0 and -1)",
		},
		"null element in objects is an error": {
			index:       types.Int64Value(1),
			objects:     types.DynamicValue(nullElementList),
			expectError: "select: objects[1] must not be null",
		},
		"null element in objects errors even if index points elsewhere": {
			index:       types.Int64Value(0),
			objects:     types.DynamicValue(nullElementList),
			expectError: "select: objects[1] must not be null",
		},
		"null index is an error": {
			index:       types.Int64Null(),
			objects:     types.DynamicValue(selectStringList("a", "b")),
			expectError: "select: index must not be null",
		},
		"null objects (dynamic null) is an error": {
			index:       types.Int64Value(0),
			objects:     types.DynamicNull(),
			expectError: "select: objects must not be null",
		},
		"null objects (typed null list wrapped in dynamic) is an error": {
			index:       types.Int64Value(0),
			objects:     types.DynamicValue(types.ListNull(types.StringType)),
			expectError: "select: objects must not be null",
		},
		"non-list/tuple objects is an error": {
			index:       types.Int64Value(0),
			objects:     types.DynamicValue(types.StringValue("not-a-list")),
			expectError: "select: objects must be a list or tuple, got basetypes.StringValue",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.index, tc.objects}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.DynamicUnknown()),
			}

			NewSelectFunction().Run(context.Background(), req, &resp)

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

			got, ok := resp.Result.Value().(types.Dynamic)
			if !ok {
				t.Fatalf("expected result of type types.Dynamic, got %T", resp.Result.Value())
			}

			if !got.UnderlyingValue().Equal(tc.expected) {
				t.Fatalf("expected %#v, got %#v", tc.expected, got.UnderlyingValue())
			}
		})
	}
}

// TestAccSelectFunction is an acceptance test exercising
// provider::cfncompat::select through a real Terraform CLI run. It is gated
// by TF_ACC=1 and is not run as part of `make test`; it is registered here
// for the batch acceptance run after provider.go wiring.
func TestAccSelectFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::select(1, ["apples", "grapes", "oranges", "mangoes"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "grapes"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::select(0, ["10.0.48.0/24", "10.0.112.0/24", "10.0.176.0/24"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "10.0.48.0/24"),
				),
			},
		},
	})
}
