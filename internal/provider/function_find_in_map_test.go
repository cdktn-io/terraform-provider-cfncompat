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

// findInMapObject is a small test helper that builds a types.Object from a
// set of named attributes, inferring each attribute's type from its value.
func findInMapObject(attrs map[string]attr.Value) types.Object {
	attrTypes := make(map[string]attr.Type, len(attrs))
	for k, v := range attrs {
		attrTypes[k] = v.Type(context.Background())
	}

	return types.ObjectValueMust(attrTypes, attrs)
}

// findInMapMap is a small test helper that builds a types.Map with the given
// (homogeneous) element type.
func findInMapMap(elemType attr.Type, entries map[string]attr.Value) types.Map {
	return types.MapValueMust(elemType, entries)
}

// findInMapStringList is a small test helper that builds a types.List of
// types.String values for use as a mapping leaf value.
func findInMapStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

// findInMapNoDefault returns the (empty) variadic default_value tuple used
// when no default_value argument is supplied.
func findInMapNoDefault() types.Tuple {
	return types.TupleValueMust([]attr.Type{}, []attr.Value{})
}

// findInMapDefault returns a variadic default_value tuple wrapping a single
// dynamic default value, matching how the framework represents a single
// argument passed to a dynamically typed variadic parameter.
func findInMapDefault(v attr.Value) types.Tuple {
	return types.TupleValueMust([]attr.Type{types.DynamicType}, []attr.Value{types.DynamicValue(v)})
}

// findInMapTwoDefaults returns a variadic default_value tuple with two
// elements, used to test the "at most one default_value" arity error.
func findInMapTwoDefaults(v1, v2 attr.Value) types.Tuple {
	return types.TupleValueMust(
		[]attr.Type{types.DynamicType, types.DynamicType},
		[]attr.Value{types.DynamicValue(v1), types.DynamicValue(v2)},
	)
}

func TestFindInMapFunction(t *testing.T) {
	t.Parallel()

	// Official AWS doc example (environment-specific configurations):
	// Mappings.SecurityGroups = { Dev: { SecurityGroupIds: "sg-12345678" },
	// Prod: { SecurityGroupIds: "sg-abcdef01,sg-ghijkl23" } }.
	securityGroups := findInMapObject(map[string]attr.Value{
		"Dev": findInMapObject(map[string]attr.Value{
			"SecurityGroupIds": types.StringValue("sg-12345678"),
		}),
		"Prod": findInMapObject(map[string]attr.Value{
			"SecurityGroupIds": types.StringValue("sg-abcdef01,sg-ghijkl23"),
		}),
	})

	// Official AWS doc example (region-specific values): two mappings,
	// AWSInstanceType2Arch and AWSRegionArch2AMI, composed via nested
	// Fn::FindInMap in the template. Here they are exercised independently,
	// since composing provider function calls is the synthesis backend's job.
	awsInstanceType2Arch := findInMapMap(
		types.ObjectType{AttrTypes: map[string]attr.Type{"Arch": types.StringType}},
		map[string]attr.Value{
			"t3.micro": findInMapObject(map[string]attr.Value{"Arch": types.StringValue("HVM64")}),
			"t4g.nano": findInMapObject(map[string]attr.Value{"Arch": types.StringValue("ARM64")}),
		},
	)

	awsRegionArch2AMI := findInMapMap(
		types.ObjectType{AttrTypes: map[string]attr.Type{"HVM64": types.StringType, "ARM64": types.StringType}},
		map[string]attr.Value{
			"us-east-1": findInMapObject(map[string]attr.Value{
				"HVM64": types.StringValue("{{ami-12345678901234567}}"),
				"ARM64": types.StringValue("{{ami-23456789012345678}}"),
			}),
			"eu-west-1": findInMapObject(map[string]attr.Value{
				"HVM64": types.StringValue("{{ami-34567890123456789}}"),
				"ARM64": types.StringValue("{{ami-45678901234567890}}"),
			}),
		},
	)

	// Official AWS doc example (default value): Mappings.RegionMap =
	// { us-east-1: { InstanceType: t3.large }, eu-west-1: { InstanceType: t3.medium } }.
	regionMap := findInMapMap(
		types.ObjectType{AttrTypes: map[string]attr.Type{"InstanceType": types.StringType}},
		map[string]attr.Value{
			"us-east-1": findInMapObject(map[string]attr.Value{"InstanceType": types.StringValue("t3.large")}),
			"eu-west-1": findInMapObject(map[string]attr.Value{"InstanceType": types.StringValue("t3.medium")}),
		},
	)

	// A mapping whose leaf values are lists of strings, exercising the
	// deviation-documented "value is a string OR list of strings" shape.
	listLeafMapping := findInMapObject(map[string]attr.Value{
		"Prod": findInMapObject(map[string]attr.Value{
			"SecurityGroupIds": findInMapStringList("sg-abcdef01", "sg-ghijkl23"),
		}),
	})

	// A mapping whose top-level entry is not itself an object/map.
	malformedTopEntry := findInMapObject(map[string]attr.Value{
		"BadKey": types.StringValue("not-an-object-or-map"),
	})

	// A mapping whose leaf value is neither a string nor a list of strings.
	nonStringLeafMapping := findInMapObject(map[string]attr.Value{
		"Top": findInMapObject(map[string]attr.Value{
			"Second": types.NumberValue(big.NewFloat(42)),
		}),
	})

	tests := map[string]struct {
		mapping        types.Dynamic
		topLevelKey    types.String
		secondLevelKey types.String
		defaultValue   types.Tuple
		expected       attr.Value
		expectError    string
		expectUnknown  bool
	}{
		"doc example: environment-specific configuration (Dev)": {
			mapping:        types.DynamicValue(securityGroups),
			topLevelKey:    types.StringValue("Dev"),
			secondLevelKey: types.StringValue("SecurityGroupIds"),
			defaultValue:   findInMapNoDefault(),
			expected:       types.StringValue("sg-12345678"),
		},
		"doc example: environment-specific configuration (Prod)": {
			mapping:        types.DynamicValue(securityGroups),
			topLevelKey:    types.StringValue("Prod"),
			secondLevelKey: types.StringValue("SecurityGroupIds"),
			defaultValue:   findInMapNoDefault(),
			expected:       types.StringValue("sg-abcdef01,sg-ghijkl23"),
		},
		"doc example: region-specific values, AWSInstanceType2Arch t3.micro": {
			mapping:        types.DynamicValue(awsInstanceType2Arch),
			topLevelKey:    types.StringValue("t3.micro"),
			secondLevelKey: types.StringValue("Arch"),
			defaultValue:   findInMapNoDefault(),
			expected:       types.StringValue("HVM64"),
		},
		"doc example: region-specific values, AWSInstanceType2Arch t4g.nano": {
			mapping:        types.DynamicValue(awsInstanceType2Arch),
			topLevelKey:    types.StringValue("t4g.nano"),
			secondLevelKey: types.StringValue("Arch"),
			defaultValue:   findInMapNoDefault(),
			expected:       types.StringValue("ARM64"),
		},
		"doc example: region-specific values, AWSRegionArch2AMI us-east-1 HVM64": {
			mapping:        types.DynamicValue(awsRegionArch2AMI),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("HVM64"),
			defaultValue:   findInMapNoDefault(),
			expected:       types.StringValue("{{ami-12345678901234567}}"),
		},
		"doc example: region-specific values, AWSRegionArch2AMI eu-west-1 ARM64": {
			mapping:        types.DynamicValue(awsRegionArch2AMI),
			topLevelKey:    types.StringValue("eu-west-1"),
			secondLevelKey: types.StringValue("ARM64"),
			defaultValue:   findInMapNoDefault(),
			expected:       types.StringValue("{{ami-45678901234567890}}"),
		},
		"doc example: default value, key found (us-east-1)": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapDefault(types.StringValue("t3.micro")),
			expected:       types.StringValue("t3.large"),
		},
		"doc example: default value, top-level key not found falls back to default": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("ap-southeast-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapDefault(types.StringValue("t3.micro")),
			expected:       types.StringValue("t3.micro"),
		},
		"second-level key not found falls back to default": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("NotAKey"),
			defaultValue:   findInMapDefault(types.StringValue("fallback")),
			expected:       types.StringValue("fallback"),
		},
		"list-of-strings leaf value is returned unchanged": {
			mapping:        types.DynamicValue(listLeafMapping),
			topLevelKey:    types.StringValue("Prod"),
			secondLevelKey: types.StringValue("SecurityGroupIds"),
			defaultValue:   findInMapNoDefault(),
			expected:       findInMapStringList("sg-abcdef01", "sg-ghijkl23"),
		},
		"top-level key not found, no default, is an error naming the key and level": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("ap-southeast-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapNoDefault(),
			expectError:    `find_in_map: top-level key "ap-southeast-1" not found in mapping`,
		},
		"second-level key not found, no default, is an error naming the key and level": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("NotAKey"),
			defaultValue:   findInMapNoDefault(),
			expectError:    `find_in_map: second-level key "NotAKey" not found in mapping["us-east-1"]`,
		},
		"more than one default_value argument is an error": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapTwoDefaults(types.StringValue("a"), types.StringValue("b")),
			expectError:    "find_in_map: at most one default_value argument is allowed, got 2",
		},
		"null mapping is an error": {
			mapping:        types.DynamicNull(),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapNoDefault(),
			expectError:    "find_in_map: mapping must not be null",
		},
		"null top_level_key is an error": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringNull(),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapNoDefault(),
			expectError:    "find_in_map: top_level_key must not be null",
		},
		"null second_level_key is an error": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringNull(),
			defaultValue:   findInMapNoDefault(),
			expectError:    "find_in_map: second_level_key must not be null",
		},
		"mapping underlying value is not an object or map is an error": {
			mapping:        types.DynamicValue(types.StringValue("not-a-mapping")),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapNoDefault(),
			expectError:    "find_in_map: mapping: expected an object or map, got basetypes.StringValue",
		},
		"top-level entry is not an object or map is an error": {
			mapping:        types.DynamicValue(malformedTopEntry),
			topLevelKey:    types.StringValue("BadKey"),
			secondLevelKey: types.StringValue("Anything"),
			defaultValue:   findInMapNoDefault(),
			expectError:    `find_in_map: mapping["BadKey"]: expected an object or map, got basetypes.StringValue`,
		},
		"leaf value that is not a string or list of strings is an error": {
			mapping:        types.DynamicValue(nonStringLeafMapping),
			topLevelKey:    types.StringValue("Top"),
			secondLevelKey: types.StringValue("Second"),
			defaultValue:   findInMapNoDefault(),
			expectError:    `find_in_map: mapping["Top"]["Second"]: value must be a string or list of strings, got basetypes.NumberValue`,
		},
		"unknown mapping defers (no error, no result)": {
			mapping:        types.DynamicUnknown(),
			topLevelKey:    types.StringValue("us-east-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapNoDefault(),
			expectUnknown:  true,
		},
		"unknown top_level_key defers (no error, no result)": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringUnknown(),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapNoDefault(),
			expectUnknown:  true,
		},
		"unknown default_value defers when key is missing (no error, no result)": {
			mapping:        types.DynamicValue(regionMap),
			topLevelKey:    types.StringValue("ap-southeast-1"),
			secondLevelKey: types.StringValue("InstanceType"),
			defaultValue:   findInMapDefault(types.StringUnknown()),
			expectUnknown:  true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{
					tc.mapping, tc.topLevelKey, tc.secondLevelKey, tc.defaultValue,
				}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.DynamicUnknown()),
			}

			NewFindInMapFunction().Run(context.Background(), req, &resp)

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

			if tc.expectUnknown {
				got, ok := resp.Result.Value().(types.Dynamic)
				if !ok {
					t.Fatalf("expected result of type types.Dynamic, got %T", resp.Result.Value())
				}
				if !got.IsUnknown() {
					t.Fatalf("expected an unknown (deferred) result, got %#v", got)
				}
				return
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

// TestAccFindInMapFunction is an acceptance test exercising
// provider::cfncompat::find_in_map through a real Terraform CLI run. It is
// gated by TF_ACC=1 and is not run as part of `make test`; it is registered
// here for the batch acceptance run after provider.go wiring.
func TestAccFindInMapFunction(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::find_in_map({
				    us-east-1 = { InstanceType = "t3.large" }
				    eu-west-1 = { InstanceType = "t3.medium" }
				  }, "us-east-1", "InstanceType")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "t3.large"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::find_in_map({
				    us-east-1 = { InstanceType = "t3.large" }
				    eu-west-1 = { InstanceType = "t3.medium" }
				  }, "ap-southeast-1", "InstanceType", "t3.micro")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "t3.micro"),
				),
			},
		},
	})
}
