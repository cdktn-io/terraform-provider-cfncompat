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

// toJsonStringObject is a small test helper that builds a types.Object from
// string-keyed values, all sharing the given element type.
func toJsonStringObject(t *testing.T, elemTypes map[string]attr.Type, elems map[string]attr.Value) types.Object {
	t.Helper()

	obj, diags := types.ObjectValue(elemTypes, elems)
	if diags.HasError() {
		t.Fatalf("failed to build test object: %v", diags)
	}

	return obj
}

func TestToJsonStringFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value       types.Dynamic
		expected    string
		expectError string
	}{
		// Official AWS doc example: converting an object to a JSON string.
		// { "key1": "value1", "key2": <Ref resolving to "resolvedValue"> }
		// => "{\"key1\":\"value1\",\"key2\":\"resolvedValue\"}"
		"doc example: convert an object to a JSON string": {
			value: types.DynamicValue(toJsonStringObject(t,
				map[string]attr.Type{"key1": types.StringType, "key2": types.StringType},
				map[string]attr.Value{"key1": types.StringValue("value1"), "key2": types.StringValue("resolvedValue")},
			)),
			expected: `{"key1":"value1","key2":"resolvedValue"}`,
		},
		// Official AWS doc example: converting an array (containing a single
		// object) to a JSON string. Note: the AWS documentation page's shown
		// expected output ("[{\"key1\":\"value1\"},{\"key2\":\"resolvedValue\"}]")
		// is inconsistent with its own input (a one-element array containing a
		// single two-key object) -- splitting the object into two array
		// elements would not be a faithful JSON serialization of the given
		// input. We treat this as a documentation error and instead assert
		// the value that a correct JSON serialization of the stated input
		// actually produces, consistent with the first (object) example.
		"doc example: convert an array to a JSON string": {
			value: types.DynamicValue(types.TupleValueMust(
				[]attr.Type{types.ObjectType{AttrTypes: map[string]attr.Type{"key1": types.StringType, "key2": types.StringType}}},
				[]attr.Value{toJsonStringObject(t,
					map[string]attr.Type{"key1": types.StringType, "key2": types.StringType},
					map[string]attr.Value{"key1": types.StringValue("value1"), "key2": types.StringValue("resolvedValue")},
				)},
			)),
			expected: `[{"key1":"value1","key2":"resolvedValue"}]`,
		},
		"string primitive": {
			value:    types.DynamicValue(types.StringValue("hello")),
			expected: `"hello"`,
		},
		"string requiring escaping": {
			value:    types.DynamicValue(types.StringValue("line1\nline2\t\"quoted\"")),
			expected: `"line1\nline2\t\"quoted\""`,
		},
		"bool true": {
			value:    types.DynamicValue(types.BoolValue(true)),
			expected: `true`,
		},
		"bool false": {
			value:    types.DynamicValue(types.BoolValue(false)),
			expected: `false`,
		},
		"integer number has no trailing decimal": {
			value:    types.DynamicValue(types.NumberValue(big.NewFloat(42))),
			expected: `42`,
		},
		"negative integer number": {
			value:    types.DynamicValue(types.NumberValue(big.NewFloat(-7))),
			expected: `-7`,
		},
		"decimal number preserves fractional digits without trailing zeros": {
			value:    types.DynamicValue(types.NumberValue(big.NewFloat(3.14))),
			expected: `3.14`,
		},
		"zero": {
			value:    types.DynamicValue(types.NumberValue(big.NewFloat(0))),
			expected: `0`,
		},
		"large integer renders without scientific notation": {
			value:    types.DynamicValue(types.NumberValue(big.NewFloat(123456789))),
			expected: `123456789`,
		},
		"int64 value": {
			value:    types.DynamicValue(types.Int64Value(9000)),
			expected: `9000`,
		},
		"null value serializes to JSON null": {
			value:    types.DynamicNull(),
			expected: `null`,
		},
		"null string underlying value serializes to JSON null": {
			value:    types.DynamicValue(types.StringNull()),
			expected: `null`,
		},
		"empty list serializes to empty JSON array": {
			value:    types.DynamicValue(types.ListValueMust(types.StringType, []attr.Value{})),
			expected: `[]`,
		},
		"list of strings": {
			value: types.DynamicValue(types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("a"), types.StringValue("b"), types.StringValue("c"),
			})),
			expected: `["a","b","c"]`,
		},
		"heterogeneous tuple": {
			value: types.DynamicValue(types.TupleValueMust(
				[]attr.Type{types.StringType, types.NumberType, types.BoolType},
				[]attr.Value{types.StringValue("a"), types.NumberValue(big.NewFloat(1)), types.BoolValue(true)},
			)),
			expected: `["a",1,true]`,
		},
		"empty object serializes to empty JSON object": {
			value:    types.DynamicValue(toJsonStringObject(t, map[string]attr.Type{}, map[string]attr.Value{})),
			expected: `{}`,
		},
		"object keys are sorted deterministically regardless of input order": {
			value: types.DynamicValue(toJsonStringObject(t,
				map[string]attr.Type{"zebra": types.StringType, "apple": types.StringType, "mango": types.StringType},
				map[string]attr.Value{"zebra": types.StringValue("z"), "apple": types.StringValue("a"), "mango": types.StringValue("m")},
			)),
			expected: `{"apple":"a","mango":"m","zebra":"z"}`,
		},
		"map value serializes like an object with sorted keys": {
			value: types.DynamicValue(types.MapValueMust(types.StringType, map[string]attr.Value{
				"b": types.StringValue("2"), "a": types.StringValue("1"),
			})),
			expected: `{"a":"1","b":"2"}`,
		},
		"nested map/list/object structure": {
			value: types.DynamicValue(toJsonStringObject(t,
				map[string]attr.Type{
					"name": types.StringType,
					"tags": types.ListType{ElemType: types.StringType},
					"nested": types.ObjectType{AttrTypes: map[string]attr.Type{
						"count":  types.NumberType,
						"active": types.BoolType,
					}},
				},
				map[string]attr.Value{
					"name": types.StringValue("widget"),
					"tags": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("x"), types.StringValue("y")}),
					"nested": toJsonStringObject(t,
						map[string]attr.Type{"count": types.NumberType, "active": types.BoolType},
						map[string]attr.Value{"count": types.NumberValue(big.NewFloat(2)), "active": types.BoolValue(false)},
					),
				},
			)),
			expected: `{"name":"widget","nested":{"active":false,"count":2},"tags":["x","y"]}`,
		},
		"null nested in list serializes as JSON null entry": {
			value: types.DynamicValue(types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("a"), types.StringNull(), types.StringValue("c"),
			})),
			expected: `["a",null,"c"]`,
		},
		"null nested in object attribute serializes as JSON null": {
			value: types.DynamicValue(toJsonStringObject(t,
				map[string]attr.Type{"key1": types.StringType},
				map[string]attr.Value{"key1": types.StringNull()},
			)),
			expected: `{"key1":null}`,
		},
		"nested dynamic values are unwrapped": {
			value: types.DynamicValue(toJsonStringObject(t,
				map[string]attr.Type{"key1": types.DynamicType},
				map[string]attr.Value{"key1": types.DynamicValue(types.StringValue("value1"))},
			)),
			expected: `{"key1":"value1"}`,
		},
		"string with unicode characters is not escaped to \\u sequences": {
			value:    types.DynamicValue(types.StringValue("héllo wörld")),
			expected: `"héllo wörld"`,
		},
		"string containing HTML-significant characters is not HTML-escaped": {
			value:    types.DynamicValue(types.StringValue("<a>&</a>")),
			expected: `"<a>&</a>"`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.value}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.StringUnknown()),
			}

			NewToJsonStringFunction().Run(context.Background(), req, &resp)

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

// TestToJsonStringFunctionUnknown verifies that an unknown top-level value
// (or an unknown value nested within a known structure) causes the function
// to leave its result unset (propagating unknown), rather than erroring or
// producing a partial result.
func TestToJsonStringFunctionUnknown(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value types.Dynamic
	}{
		"top-level unknown dynamic": {
			value: types.DynamicUnknown(),
		},
		"known dynamic wrapping an unknown string": {
			value: types.DynamicValue(types.StringUnknown()),
		},
		"unknown nested inside a list": {
			value: types.DynamicValue(types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("a"), types.StringUnknown(),
			})),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.value}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.StringUnknown()),
			}

			NewToJsonStringFunction().Run(context.Background(), req, &resp)

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Text)
			}

			got, ok := resp.Result.Value().(types.String)
			if !ok {
				t.Fatalf("expected result of type types.String, got %T", resp.Result.Value())
			}

			if !got.IsUnknown() {
				t.Fatalf("expected unknown result, got %q", got.ValueString())
			}
		})
	}
}

// TestAccToJsonStringFunction is an acceptance test exercising
// provider::cfncompat::to_json_string through a real Terraform CLI run. It is
// gated by TF_ACC=1 and is not run as part of `make test`; it is registered
// here for the batch acceptance run after provider.go wiring.
func TestAccToJsonStringFunction(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::to_json_string({
					key1 = "value1"
					key2 = "resolvedValue"
				  })
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", `{"key1":"value1","key2":"resolvedValue"}`),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::to_json_string(["a", "b", "c"])
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", `["a","b","c"]`),
				),
			},
		},
	})
}
